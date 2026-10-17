import type { Metadata } from "next";
import Link from "next/link";
import { CopyCommand } from "@/components/copy-command";
import { installCmd, repoUrl } from "@/lib/site";

export const metadata: Metadata = { title: "Docs" };

const sections = [
  { id: "install", label: "Install" },
  { id: "first-run", label: "A first run" },
  { id: "documents", label: "Signed documents" },
  { id: "api", label: "API" },
  { id: "policy", label: "Policy checks" },
  { id: "liability", label: "Who carries a dispute" },
  { id: "limits", label: "Limits" },
];

const api: [string, string, string][] = [
  ["POST", "/v1/users", "register a user and their device key (admin)"],
  ["POST", "/v1/agents", "register an agent, its developer and its key (admin)"],
  ["POST", "/v1/agents/{id}/suspend", "take an agent off; compromised_since marks when its key leaked (admin)"],
  ["POST", "/v1/merchants", "register a merchant and its VPA (admin)"],
  ["GET", "/v1/merchants, /v1/agents", "what is registered"],
  ["POST", "/v1/mandates", "a scope signed by the user's device; blocks funds at the bank"],
  ["GET", "/v1/mandates/{id}", "the mandate, what is spent and what is in flight"],
  ["POST", "/v1/mandates/{id}/revoke", "a revocation signed by the user's device"],
  ["GET", "/v1/users/{id}/mandates", "a user's mandates"],
  ["GET", "/v1/mandates/{id}/requests", "every request made on a mandate, with its decision"],
  ["POST", "/v1/debits", "an agent's signed request, with an optional signed intent; 200 paid, 202 asked, 403 refused"],
  ["GET", "/v1/requests/{id}", "where a request stands now"],
  ["GET", "/v1/users/{id}/stepups", "questions waiting for the user"],
  ["POST", "/v1/stepups/{id}", "the user's signed answer"],
  ["GET", "/v1/debits/{id}/evidence", "the evidence pack for a settled debit"],
  ["POST", "/v1/disputes", "a claim on a debit; returns the ruling and the checks"],
  ["GET", "/v1/log/checkpoint", "the latest signed checkpoint of the log"],
  ["GET", "/v1/events", "server-sent events: decisions, debits, mandates"],
  ["GET", "/metrics", "Prometheus text format"],
  ["GET", "/healthz", "200 when the database answers"],
];

const hard: [string, string][] = [
  ["agent_unknown, agent_suspended", "the agent is not registered, or was taken off"],
  ["agent_not_on_mandate", "the mandate is for another agent"],
  ["stale_request", "the request's time is more than 5 minutes from the server's"],
  ["mandate_inactive, mandate_expired, mandate_not_yet_valid", "the mandate does not stand right now"],
  ["merchant_not_on_mandate", "the merchant is not one the user named"],
  ["payee_not_merchant", "on Circle, the VPA paid is not the merchant's registered one"],
  ["over_max_debit, over_remaining, over_per_day", "the mandate's limits"],
  ["circle_cooling_period", "Circle allows ₹5,000 a day in the first 24 hours after linking"],
  ["amount_not_cart_total", "the amount is not what the cart adds up to"],
  ["category_not_on_mandate", "an item is outside the categories the user allowed"],
  ["intent_invalid", "an intent receipt that is not the user's, or not for this request"],
];

const soft: [string, string][] = [
  ["over_step_up_line", "over the amount the user said to ask about, unless a signed intent covers it"],
  ["high_risk_item", "gift cards, wallet top-ups, prepaid, crypto, transfers, lottery, unless the user allowed them"],
  ["outside_intent", "the request goes beyond the intent the user's device signed"],
  ["split_payment", "payments to one merchant within 15 minutes, each under the line, adding up past it"],
  ["unusual_amount", "more than 3 times the median of the mandate's past payments, once it has 3"],
  ["burst", "4 payments already on the mandate in the last 10 minutes"],
];

export default function Docs() {
  return (
    <div className="mx-auto grid max-w-6xl gap-10 px-5 pt-14 lg:grid-cols-[12rem_1fr]">
      <nav className="hidden lg:block">
        <ul className="sticky top-8 space-y-2 text-[0.9rem] text-ink-soft">
          {sections.map((s) => (
            <li key={s.id}>
              <a href={`#${s.id}`} className="hover:text-ink">
                {s.label}
              </a>
            </li>
          ))}
        </ul>
      </nav>
      <article className="prose-w min-w-0 max-w-3xl">
        <h1 className="text-[2.2rem] leading-tight font-bold tracking-tight text-ink">Docs</h1>

        <H id="install">Install</H>
        <p>macOS or Linux, amd64 or arm64. The script checks the release checksum and puts the binaries in ~/.local/bin.</p>
        <div className="mt-4">
          <CopyCommand cmd={installCmd} />
        </div>
        <p className="mt-4">
          Or from source with Go 1.26: <code>go install github.com/Rakshit-gen/warrant/cmd/...@latest</code>.
          warrantd needs Postgres 14 or newer.
        </p>

        <H id="first-run">A first run</H>
        <p>
          From a clone, <code>scripts/devdb.sh start</code> runs a private Postgres in ./.pgdata on port 54329 and
          prints its URL. Then:
        </p>
        <Pre>{`warrant demo -db postgres://warrant@localhost:54329/warrant?sslmode=disable
warrant verify pack.json`}</Pre>
        <p>
          The demo runs warrantd inside the process with a simulated bank and walks through the story on the{" "}
          <Link href="/">front page</Link>: orders, an injected gift card, a lookalike store, a step-up, a lost bank
          reply, a revocation and a dispute. It writes the dispute&apos;s evidence pack, which{" "}
          <code>warrant verify</code> checks with no network and no database.
        </p>
        <p>To run the parts separately:</p>
        <Pre>{`banksim -addr :8091 -lost-reply 0.1 -latency 40ms
warrantd -db $WARRANT_DB -bank http://localhost:8091 -admin-token secret
warrant demo -api http://localhost:8090 -token secret`}</Pre>
        <p>
          warrantd creates its signing key in warrantd.key on first start. <code>-kafka host:9092</code> relays
          the outbox to Kafka topics <code>warrant.mandates</code>, <code>warrant.decisions</code> and{" "}
          <code>warrant.debits</code>. <code>-caps-only</code> turns off every check beyond the mandate&apos;s
          limits, for comparison.
        </p>

        <H id="documents">Signed documents</H>
        <p>
          Everything the user and the agent say is an envelope: <code>payload</code> is the document&apos;s JSON
          bytes in base64, <code>sig</code> an Ed25519 signature over exactly those bytes, <code>key</code> the
          signer&apos;s public key. Carrying the bytes means there is no canonical JSON to agree on; the verifier
          hashes what the signer signed.
        </p>
        <ul className="mt-3 list-disc space-y-1.5 pl-5 text-ink-soft">
          <li>
            <code>scope</code>, signed by the user&apos;s device: agent, kind (<code>reserve_pay</code> or{" "}
            <code>circle</code>), merchants, categories, high-risk categories allowed, per-debit limit, cap,
            payments per day, step-up line, purpose, validity.
          </li>
          <li>
            <code>debit</code>, signed by the agent: mandate, nonce, time, idempotency key, merchant, payee VPA,
            amount, cart, intent ID.
          </li>
          <li>
            <code>intent</code>, signed by the user&apos;s device for one errand: what it is for, merchants,
            categories, a maximum, a short expiry.
          </li>
          <li>
            <code>stepup</code> and <code>revoke</code>, signed by the user&apos;s device. A step-up answer names
            the SHA-256 of the exact request it approves.
          </li>
        </ul>
        <p className="mt-3">
          The Go client in <code>client/</code> does the signing; see <code>server/server_test.go</code> for a
          complete flow.
        </p>

        <H id="api">API</H>
        <div className="mt-4 overflow-x-auto border border-rule bg-sheet">
          <table className="w-full text-[0.85rem]">
            <tbody>
              {api.map(([m, p, d]) => (
                <tr key={m + p} className="border-b border-rule align-top last:border-b-0">
                  <td className="mono px-4 py-2 text-ink-faint">{m}</td>
                  <td className="mono px-2 py-2 whitespace-nowrap text-ink">{p}</td>
                  <td className="px-4 py-2 text-ink-soft">{d}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <H id="policy">Policy checks</H>
        <p>
          The policy is a pure function of the signed scope, the signed request, the intent if any, and the
          mandate&apos;s state and recent history, all of which go into the log with the decision. Any hard
          reason refuses. Otherwise any soft reason asks the user, who has 10 minutes to answer.
        </p>
        <Codes title="Hard: refused" rows={hard} tone="text-denied" />
        <Codes title="Soft: the user is asked" rows={soft} tone="text-asked" />

        <H id="liability">Who carries a dispute</H>
        <p>
          NPCI has not said who bears an agent&apos;s mistake. warrant writes one answer down so it can be argued
          with: whoever had the last chance to stop a payment, and the evidence to know they should, carries it.
          In order:
        </p>
        <ol className="mt-3 list-decimal space-y-1.5 pl-5 text-ink-soft">
          <li>If the pack does not verify, the platform: it cannot show the payment was authorized.</li>
          <li>If no money moved, nobody.</li>
          <li>
            If the recorded decision is not what the policy gives, or it paid a refusal, or paid a step-up without
            a signed yes, or paid after a revocation, the platform.
          </li>
          <li>If the request was signed after the developer said the agent&apos;s key leaked, the developer.</li>
          <li>If the claim is about delivery or the wrong item, the merchant, through its refunds.</li>
          <li>
            Otherwise the user: they signed a yes for this exact request, or an intent that covered it, or a scope
            it was inside without tripping a check.
          </li>
        </ol>

        <H id="limits">Limits</H>
        <ul className="mt-3 list-disc space-y-1.5 pl-5 text-ink-soft">
          <li>
            The bank is simulated. The issuer interface (block, debit, status, revoke) is shaped after Reserve Pay
            and Circle, not taken from a published API.
          </li>
          <li>
            warrant trusts the categories in the cart. A merchant-signed cart would close that; it is not built.
          </li>
          <li>
            The log is one operator&apos;s. Checkpoints are signed, but nobody else witnesses them yet, so the
            operator could show different logs to different people.
          </li>
          <li>Device keys stand in for the UPI app&apos;s secure storage and the user&apos;s UPI PIN.</li>
          <li>
            The evaluation corpus is synthetic. See <Link href="/results">results</Link>.
          </li>
        </ul>
        <p className="mt-6">
          The design is in <a href={`${repoUrl}/blob/main/DESIGN.md`}>DESIGN.md</a>.
        </p>
      </article>
    </div>
  );
}

function H({ id, children }: { id: string; children: React.ReactNode }) {
  return (
    <h2 id={id} className="mt-12 scroll-mt-6 text-[1.4rem] font-bold text-ink">
      {children}
    </h2>
  );
}

function Pre({ children }: { children: string }) {
  return <pre className="mono mt-4 overflow-x-auto border border-rule bg-sheet px-5 py-4 text-[0.82rem] leading-relaxed text-ink">{children}</pre>;
}

function Codes({ title, rows, tone }: { title: string; rows: [string, string][]; tone: string }) {
  return (
    <div className="mt-5 border border-rule bg-sheet">
      <p className="border-b border-rule px-4 py-2 text-[0.85rem] font-bold text-ink">{title}</p>
      {rows.map(([c, d]) => (
        <div key={c} className="grid gap-1 border-b border-rule px-4 py-2 text-[0.85rem] last:border-b-0 sm:grid-cols-[16rem_1fr] sm:gap-4">
          <span className={`mono text-[0.78rem] ${tone}`}>{c}</span>
          <span className="text-ink-soft">{d}</span>
        </div>
      ))}
    </div>
  );
}
