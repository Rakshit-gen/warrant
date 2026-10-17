# Design

This is how warrant works and why it is built the way it is. The README
covers what it is for and what it measured.

## The problem in one paragraph

UPI already has two ways to let someone else pay from your account without
a PIN each time. Reserve Pay (single block, multiple debits) blocks an
amount and lets a merchant debit it in parts. UPI Circle lets a primary user
delegate spending to a secondary one, up to ₹5,000 a day for the first 24
hours. NPCI's agent pilots and its proposed agent protocol reuse these
rails, so an AI agent ends up holding a mandate. Mandates check how much
is spent and nothing else: not what was bought, not who the payee really
is, not whether the user asked for this errand. They also leave no record
that a bank, a merchant and a platform would all accept when a payment is
disputed. NPCI held its protocol back in September 2026 over exactly these
questions: is the agent who it says it is, is the payment inside what the
user allowed, and who pays when it goes wrong.

warrant is a service that sits between the agent and the bank rail and
answers those three questions for every debit, in a form that someone else
can check later without trusting warrant.

## Parties and keys

| party | key | signs |
|---|---|---|
| user's device | Ed25519, registered with the user | the scope (mandate), intent receipts, step-up answers, revocations |
| agent | Ed25519, registered with the agent and its developer | debit requests |
| warrantd | Ed25519, warrantd.key | log checkpoints |

The device key stands in for the UPI app's secure storage and the user's
PIN. Nothing the agent sends can widen what the user signed: the agent has
no key that the scope, intent or step-up checks accept.

Every signed thing is an envelope: the payload's exact bytes in base64, the
signature over those bytes, and the signer's public key. An earlier version
carried the payload as raw JSON, and it broke twice: Go's json.RawMessage
compacts whitespace when re-encoded, and Postgres jsonb reorders keys.
Carrying bytes means there is no canonical form to agree on.

## A debit, start to finish

1. The agent posts a signed `debit` request: mandate, nonce, timestamp,
   idempotency key, merchant, payee VPA, amount, cart. It may attach an
   intent receipt the user's device signed for this errand.
2. warrantd checks the agent is registered and the signature is its key's.
   Failures here are rejections (401); they never reach the policy and
   never go in the log, because nobody authenticated said anything.
3. If the agent already sent this idempotency key with the same bytes, the
   earlier answer comes back and nothing else happens. Same key, different
   bytes is a 409.
4. In one transaction, holding the mandate row's `FOR UPDATE` lock:
   - gather the policy input: the signed scope, the mandate's status,
     spend settled and in flight, today's spend and count, the agent's
     status, the merchant's registered VPA, the last hour of payments and
     the mandate's payment history;
   - run the policy;
   - insert the request (the nonce has a unique index, so a replayed nonce
     under a new idempotency key fails here and comes back as 409);
   - write `request` and `decision` records to the log, the decision with
     its full input and the policy version;
   - on approve, insert the debit as `pending`; on step-up, open a step-up
     that expires in 10 minutes;
   - write an outbox row.
5. After commit, an approved debit goes to the bank (see below).

The lock is per mandate. Two agents on two mandates never wait for each
other; two requests on one mandate are serialized, which is what makes the
cap hold. Counting in-flight debits as spent means a slow bank cannot be
used to overspend.

## The policy

`policy.Evaluate` is a pure function of its input and options. That is the
property the whole evidence story leans on: a verifier holding the logged
input can run the same function and must get the same decision.

Hard checks deny. They are things the user plainly said or the rail
requires: the agent, merchant, categories and limits on the scope; the
mandate's validity window and status; the cart adding up to the amount; the
payee VPA matching the merchant on Circle; the Circle cooling limit; a
timestamp within 5 minutes; an intent that is the user's and is for this
request.

Soft checks ask the user. They are things that are often fine and
sometimes an attack: a high-risk category (gift cards and other things that
turn into cash), an amount over the user's step-up line, a request outside
the signed intent, a split (several payments to one merchant in 15
minutes, each under the line, together over it), an amount over 3 times
the mandate's median, and 4 payments already in the last 10 minutes.

A signed intent that covers the request turns off the step-up line, split
and outlier checks, because the user has already said yes to that errand.
It does not turn off high-risk items or the burst check.

`Options.CapsOnly` keeps only what a plain mandate checks (agent,
validity, limits). The evaluation runs both to show the difference.

The tunables are constants, not configuration. Changing one changes what
past evidence means, so it should be a new policy version, which the
decision records.

## Paying exactly once

The bank call is the one step that cannot be inside the transaction, and
a lost reply is the normal case at UPI scale, not an edge case.

- The bank's transaction ID is the debit ID. Sending the same debit twice
  is a no-op at the bank (the simulator enforces this the way issuers do).
- Before calling, the debit is marked `unknown` and its attempt count goes
  up. A crash mid-call leaves it `unknown`, never `pending`, so nobody
  sends it again blindly.
- The reconciler runs every second. For `pending` debits (approved, never
  sent: a crash between commit and call) it sends. For `unknown` debits it
  asks the bank for the status first. Success or decline settles it. "Never
  seen" sends again under the same ID, up to Reserve Pay's 3 attempts in
  24 hours, then fails it.
- Settling is a conditional update (`WHERE status IN ('pending','unknown')`)
  that writes the `debit` log record in the same transaction, so a debit
  settles once and its record exists if and only if it settled.

The benchmark races requests on a few mandates through a bank that drops
requests and loses replies, then compares warrant's debits with the bank's
audit: same count, same amount per block, no block over its cap.

## The log

Every record (mandate, request, decision, step-up, debit, revocation,
suspension) is appended to one table in the same transaction as the change
it describes. A sequencer loop gives unsequenced rows their leaf index
every 200ms and signs a checkpoint: tree size, RFC 6962 Merkle root,
time. Sequencing out of the request path keeps the write path to a single
insert; the cost is a short delay before a record has a proof.

The tree keeps every leaf hash in memory. A million records is 32 MB and a
root over them takes a few hundred milliseconds. Past that it should keep
a compact range instead of recomputing.

## Evidence packs

For a debit, warrantd collects every record the ruling depends on: the
agent's registration, the user's, the mandate (scope), the request, the
decision, any intent and step-up answer, any revocation or suspension,
and the debit. Each comes with an inclusion proof against one signed
checkpoint.

`evidence.Verify` checks, with no network and no database:

- the checkpoint is signed by the pack's server key;
- every record's inclusion proof leads to that root;
- the scope is signed by the user's registered device key, the request by
  the agent's key, any intent and step-up by the device;
- the request's digest is the one the decision names;
- running the policy on the logged input gives the logged decision;
- the logged input agrees with the signed scope and request;
- a step-up answer, if any, names this request's digest;
- the debit's status and amount.

The same function runs in the CLI (`warrant verify`) and in the browser,
compiled to WebAssembly. One implementation means the website cannot
disagree with the tool.

## Liability

NPCI has not published who bears an agent's mistake. `evidence.Rule`
writes one answer down so there is something concrete to argue with. The
rule is: whoever had the last chance to stop the payment, and the
information to know they should, carries it. The order is in the docs and
in `evidence/liability.go`; every branch has a test.

The interesting cases are the platform ones. If the pack does not verify,
or the logged decision is not what the policy gives, or money moved
against a refusal, an unanswered step-up or a revocation, warrant itself is
at fault. A system that can blame itself from its own log is easier to
trust than one that cannot.

## Outbox, events, metrics

Decisions, debits and mandate changes write outbox rows in their
transaction. With `-kafka`, a relay publishes them with franz-go and marks
them sent; consumers must handle duplicates, keyed by mandate. Without
Kafka the outbox only grows, which is fine for a demo and wrong for
production. `/v1/events` streams the same events over SSE for the user's
app. `/metrics` is hand-written Prometheus text for a handful of counters
and one histogram, to avoid pulling in the client library for that.

## The evaluation

`eval/corpus.go` generates sessions from a seed: one user, one agent, one
mandate, a few weeks of ordinary orders, then whatever the family does.
Families are ordinary use (5), attacks (12) and evasions (3), the last
being attacks built to get past these checks. Every payment is labeled
wanted or not.

The harness runs warrantd and the bank simulator in-process against a
fresh Postgres schema, on a fake clock that both read, so weeks pass in
seconds. It replays the corpus twice, caps-only and full policy. The
simulated user approves a step-up when the payment was wanted and declines
it when it was not. That they always notice is the most generous
assumption in the evaluation.

The corpus is synthetic. It shows what the checks do on these patterns,
not how often the patterns happen.

## What is not built

- A real bank. The issuer interface (block, debit, status, revoke) is
  shaped after Reserve Pay and Circle, not taken from a published API.
- Merchant-signed carts. warrant trusts the categories in the cart, which
  is why the mislabeled-item evasion works.
- Witnesses for the log. Checkpoints are signed, but one operator could
  show different logs to different people. Gossiping checkpoints to a
  second party would close that.
- Key rotation for warrantd.
- Rate limiting and multi-tenant admin.
