# warrant

Proof that an AI agent's UPI payment was allowed, checked before the money
moves and verifiable by anyone after.

An agent holding a UPI mandate can spend up to the mandate's cap on
anything, at any payee that fits, whether or not the user asked. warrant
sits between the agent and the bank rail. Every debit request is signed by
the agent, checked against a scope the user's device signed, decided by a
deterministic policy, paid exactly once, and written to a Merkle log. When
a payment is disputed, warrant hands over an evidence pack that a bank, a
merchant or the user can check offline, and a ruling on who carries it.

Go, Postgres, Kafka (optional). Site: the verifier runs the same Go code
in the browser as WebAssembly.

## Why this problem

UPI let AI agents pay in 2026. Razorpay and NPCI ran pilots that let
ChatGPT and Claude users order from Zomato, Swiggy and Zepto and pay with
UPI inside the chat, announced in February 2026. Those payments ride on
existing rails: Reserve Pay, which blocks an amount and lets it be debited
in parts, and UPI Circle, which delegates spending with limits.

NPCI then built a protocol to let agents pay without a human approving
each payment, and was expected to launch it at Global Fintech Fest on
10 September 2026. It was put on hold to build safeguards
and wait for RBI clearance (Business Standard, 11 September). The open
questions in the coverage were the same three every time:

- **Is this agent who it says it is?** A mandate is bound to a payer, not
  to a piece of software.
- **Is this payment inside what the user allowed?** A mandate checks
  amounts. It cannot tell groceries from a gift card a prompt injection
  added, or Zepto from a lookalike.
- **Who pays when it goes wrong?** No allocation between the user, the
  bank, the merchant and the agent's developer has been published.

Every payment company building on agent payments needs answers to these
before the protocol ships. warrant is one concrete answer to all three,
built so the answer can be checked.

## What it does

- **Signed scope.** The user's device signs what the agent may do:
  merchants, categories, per-payment and total limits, a step-up line,
  validity. The agent signs each request. Nothing the agent sends can
  widen what the user signed.
- **Deterministic policy.** Hard checks refuse (merchant or category not
  allowed, payee VPA not the merchant's, cart not matching the amount,
  limits, stale or replayed requests). Soft checks ask the user's device
  (high-risk items like gift cards, amounts over the line, split
  payments, bursts, unusual amounts, going beyond a signed errand).
- **Exactly-once debits.** Per-mandate row locks so concurrent requests
  cannot overspend; a write-ahead `unknown` state and a reconciler that
  asks the bank before ever sending again, under the same transaction ID.
- **Evidence.** Every mandate, request, decision, answer and debit goes
  into an RFC 6962 Merkle log with signed checkpoints. An evidence pack
  re-runs the policy on the logged input and checks every signature and
  inclusion proof, with no network.
- **Liability.** A written rule for who carries a disputed payment,
  including warrant itself when its own log does not support what it did.

## Run it

```sh
curl -fsSL https://raw.githubusercontent.com/Rakshit-gen/warrant/main/scripts/install.sh | sh
```

That installs `warrant`, `warrantd` and `banksim` into ~/.local/bin
(macOS and Linux, amd64 and arm64), after checking the release checksums.
From source: `go install github.com/Rakshit-gen/warrant/cmd/...@latest`.

warrantd needs Postgres 14 or newer. From a clone, `scripts/devdb.sh
start` runs a private one in ./.pgdata on port 54329.

```sh
make demo      # the story: orders, an injection, a lookalike, a step-up, a lost reply, a revoke, a dispute
warrant verify web/public/sample-pack.json
make test      # unit tests, and the server tests against the dev Postgres
make eval      # results/eval.json
make bench     # results/bench.json
```

Running the pieces separately:

```sh
banksim -addr :8091 -lost-reply 0.1 -latency 40ms
warrantd -db "$WARRANT_DB" -bank http://localhost:8091 -admin-token secret
warrant demo -api http://localhost:8090 -token secret
```

The Go client in `client/` signs requests for you; `server/server_test.go`
walks the whole API.

## Layout

```
sign/       Ed25519 envelopes over exact payload bytes
wire/       the signed documents: scope, debit request, intent, step-up, revocation
policy/     the decision, a pure function
ledger/     RFC 6962 Merkle tree and proofs
evidence/   pack verification and the liability rule
server/     warrantd: API, row locks, bank calls, reconciler, log sequencer, outbox, SSE, metrics
bank/       the simulated issuer: blocks, debits, status, faults
client/     Go SDK for agents and user devices
eval/       the synthetic corpus and the replay harness
cmd/        warrant (CLI), warrantd, banksim, verify-wasm
web/        the site (Next.js static export)
```

## Limits

- The bank is simulated. Its interface is shaped after Reserve Pay and
  Circle as publicly described, not taken from a published issuer API.
- warrant trusts the categories in the cart. A merchant could mislabel a
  gift card as groceries; merchant-signed carts would close that.
- One operator runs the log. Checkpoints are signed but not witnessed.
- The evaluation corpus is synthetic. See above.
- Not affiliated with Razorpay, NPCI or any bank.

[DESIGN.md](DESIGN.md) has the details.

## License

MIT
