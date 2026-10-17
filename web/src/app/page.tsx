import Link from "next/link";
import { Ledger } from "@/components/ledger";
import { SlippedBars } from "@/components/bars";
import { CopyCommand } from "@/components/copy-command";
import { familyLabel, mode, pct, rupees, story } from "@/lib/data";
import { installCmd } from "@/lib/site";

export default function Home() {
  const caps = mode("caps_only");
  const full = mode("warrant");
  const attacks = (m: typeof full) => m?.families.filter((f) => f.kind === "attack") ?? [];
  const evasions = full?.families.filter((f) => f.kind === "evasion") ?? [];
  const b = full?.totals.benign;
  const a = full?.totals.attack;
  const ca = caps?.totals.attack;

  return (
    <div className="mx-auto max-w-6xl px-5">
      <section className="grid gap-10 pt-16 pb-14 lg:grid-cols-[1.25fr_1fr] lg:gap-16 lg:pt-24">
        <div>
          <h1 className="text-[2.4rem] leading-[1.08] font-bold tracking-tight sm:text-[3.1rem]">
            An AI agent just paid with your UPI. Was it allowed to?
          </h1>
          <div className="prose-w mt-6 max-w-xl text-[1.06rem]">
            <p>
              This year Razorpay and NPCI let people pay through AI assistants in pilots. In September NPCI put
              its agent payment protocol on hold until there are safeguards. The open questions are the ones in
              this page: how to know which agent is asking, how to keep it to what the user meant, and who pays
              when it gets it wrong.
            </p>
            <p>
              warrant is a service that sits between agents and UPI Reserve Pay or Circle. The user&apos;s phone
              signs what an agent may do. Every payment the agent asks for is signed, checked against that, and
              sent to the bank exactly once. Every decision goes into a tamper-evident log, so a disputed payment
              comes with evidence anyone can check, and a rule that says who carries it.
            </p>
          </div>
          <div className="mt-8 flex flex-wrap gap-x-6 gap-y-3 text-[0.98rem]">
            <Link href="/results" className="bg-ink px-4 py-2.5 font-bold text-sheet hover:bg-ink-soft">
              See what it catches
            </Link>
            <Link href="/verify" className="link self-center">
              Check an evidence pack in your browser
            </Link>
          </div>
        </div>
        <aside className="self-end border border-rule bg-sheet">
          <p className="border-b border-rule px-5 py-3 text-[0.86rem] text-ink-soft">
            On the same {caps ? `${caps.families.reduce((n, f) => n + f.unwanted, 0)}` : ""} unwanted payments from
            the attack corpus
          </p>
          {a && ca ? (
            <dl className="grid grid-cols-2">
              <Stat label="paid under mandate caps alone" value={pct(ca.unwanted_paid, ca.unwanted)} sub={rupees(ca.money_lost) + " lost"} />
              <Stat label="paid under warrant" value={pct(a.unwanted_paid, a.unwanted)} sub={rupees(a.money_lost) + " lost"} accent />
              <Stat
                label="payments users wanted that warrant asked about"
                value={pct(b!.wanted_asked, b!.wanted)}
                sub={`${b!.wanted_asked} of ${b!.wanted}`}
              />
              <Stat label="payments users wanted that warrant refused" value={pct(b!.wanted_refused, b!.wanted)} sub={`${b!.wanted_refused} of ${b!.wanted}`} />
            </dl>
          ) : (
            <p className="px-5 py-6 text-[0.92rem] text-ink-soft">
              The evaluation has not been run in this build. <code className="c">make eval</code> writes it.
            </p>
          )}
          <p className="border-t border-rule px-5 py-3 text-[0.8rem] leading-snug text-ink-faint">
            A synthetic corpus, not real traffic. <Link href="/results" className="link">How it was made</Link>.
          </p>
        </aside>
      </section>

      <div className="perf" />

      <section className="py-14">
        <div className="grid gap-10 lg:grid-cols-[1fr_2fr]">
          <div className="prose-w">
            <h2 className="text-[1.6rem] leading-tight font-bold text-ink">One mandate, one afternoon</h2>
            <p className="mt-4">
              Asha lets ShopBot buy her groceries. These rows are the output of <code>warrant demo</code>, run
              against warrantd and Postgres, with the reasons exactly as warrant gave them.
            </p>
            <p>
              The disputed order at the end comes with an evidence pack.{" "}
              <Link href="/verify">Open it in the verifier</Link> and the same checks run on your machine.
            </p>
          </div>
          <Ledger steps={story} />
        </div>
      </section>

      <div className="perf" />

      <section className="py-14">
        <h2 className="max-w-3xl text-[1.6rem] leading-tight font-bold">
          Caps limit how much an agent spends, and nothing else.
        </h2>
        <div className="prose-w mt-4 max-w-3xl">
          <p>
            Reserve Pay and Circle limits stop an agent from spending more than the user set aside. Most of what
            goes wrong with an agent stays under that number: a gift card slipped into a grocery order, a bill paid
            to a lookalike VPA, one purchase cut into pieces small enough not to ask about. Each row is an attack
            family from the corpus, run once with only the mandate&apos;s caps and once with warrant.
          </p>
        </div>
        <div className="mt-8">
          {full && caps ? (
            <SlippedBars caps={attacks(caps)} full={attacks(full)} />
          ) : (
            <p className="text-ink-soft">No evaluation results in this build.</p>
          )}
        </div>
      </section>

      <div className="perf" />

      <section className="py-14">
        <h2 className="text-[1.6rem] leading-tight font-bold">How it works</h2>
        <div className="mt-8 grid gap-x-12 gap-y-10 md:grid-cols-2">
          <Part n={1} title="The user signs the scope, once">
            The user&apos;s UPI app holds a device key. It signs a scope: the agent, the merchants, the categories,
            the limits, a line above which warrant must ask, and when it ends. That signature is the strong,
            user-present moment. The bank blocks the funds as a Reserve Pay mandate or records a Circle
            delegation.
          </Part>
          <Part n={2} title="Every request is signed and checked">
            Agents register a key. A payment request is signed by it, with a nonce, a time and an idempotency key,
            so it cannot be forged or replayed. The policy is a pure function of the signed scope, the request and
            the mandate&apos;s history: hard checks refuse, soft checks ask the user, whose phone signs the answer.
            For an errand, the phone can sign an intent up front, which narrows what the agent may do.
          </Part>
          <Part n={3} title="Paid exactly once">
            Limits are checked under a row lock on the mandate, so racing requests cannot overspend. A debit is
            marked unknown before the bank is called and uses its own ID as the bank transaction ID. When a reply
            is lost, the reconciler asks the bank what happened instead of paying again.
          </Part>
          <Part n={4} title="Evidence anyone can check">
            Every registration, mandate, request, decision, answer and debit is a leaf in a Merkle log with signed
            checkpoints. A dispute gets a pack of the records behind one payment and their proofs. Verifying it
            needs nothing from warrant: the checker validates signatures and proofs, then reruns the policy on the
            recorded input. A published rule says who carries the payment.
          </Part>
        </div>
      </section>

      <div className="perf" />

      <section className="grid gap-10 py-14 lg:grid-cols-[1fr_1fr]">
        <div className="prose-w">
          <h2 className="text-[1.6rem] leading-tight font-bold text-ink">What it does not catch</h2>
          <p className="mt-4">
            The corpus has attacks built to get past warrant, and they do. It trusts the category the cart says an
            item is in. A stolen key used at the user&apos;s own pace looks like the user. A split spread past the
            15 minute window is not a split to it. The fix for the first is a cart signed by the merchant; for
            the second, the liability rule: once the developer reports the key leaked, those payments are theirs.
          </p>
        </div>
        <div className="border border-rule bg-sheet">
          {evasions.map((f) => (
            <div key={f.family} className="flex items-baseline justify-between gap-4 border-b border-rule px-5 py-3 last:border-b-0">
              <div>
                <p className="text-[0.95rem]">{familyLabel(f.family)}</p>
                <p className="text-[0.8rem] text-ink-faint">{f.what}</p>
              </div>
              <p className="mono shrink-0 text-[0.84rem] text-denied">
                {f.unwanted_paid}/{f.unwanted} paid
              </p>
            </div>
          ))}
          {evasions.length === 0 && <p className="px-5 py-4 text-ink-soft">No evaluation results in this build.</p>}
        </div>
      </section>

      <div className="perf" />

      <section className="py-14">
        <h2 className="text-[1.6rem] leading-tight font-bold">Run it</h2>
        <p className="mt-3 max-w-2xl text-ink-soft">
          Three binaries for macOS and Linux: <code className="c">warrantd</code>, the API;{" "}
          <code className="c">banksim</code>, an issuer that misbehaves on request; and{" "}
          <code className="c">warrant</code>, for keys, packs, the demo, the evaluation and the benchmark.
        </p>
        <div className="mt-6 max-w-3xl">
          <CopyCommand cmd={installCmd} />
        </div>
        <p className="mt-4 text-[0.92rem] text-ink-soft">
          Then <Link href="/docs" className="link">the docs</Link> walk through a first run.
        </p>
      </section>
    </div>
  );
}

function Stat({ label, value, sub, accent }: { label: string; value: string; sub: string; accent?: boolean }) {
  return (
    <div className="border-rule px-5 py-4 odd:border-r [&:nth-child(-n+2)]:border-b">
      <dd className={`num text-[2rem] leading-none font-bold ${accent ? "text-denied" : "text-ink"}`}>{value}</dd>
      <dt className="mt-2 text-[0.82rem] leading-snug text-ink-soft">{label}</dt>
      <p className="mono mt-1 text-[0.78rem] text-ink-faint">{sub}</p>
    </div>
  );
}

function Part({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[2.2rem_1fr]">
      <span className="mono pt-1 text-[0.9rem] text-denied">{n}.</span>
      <div>
        <h3 className="text-[1.12rem] font-bold">{title}</h3>
        <p className="mt-2 leading-relaxed text-ink-soft">{children}</p>
      </div>
    </div>
  );
}
