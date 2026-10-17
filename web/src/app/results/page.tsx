import type { Metadata } from "next";
import { benchResult, evalResult, familyLabel, mode, pct, rupees, type Tally } from "@/lib/data";

export const metadata: Metadata = { title: "Results" };

export default function Results() {
  const caps = mode("caps_only");
  const full = mode("warrant");
  const pairs = (kind: Tally["kind"]) =>
    (full?.families ?? []).map((f, i) => ({ f, c: caps!.families[i] })).filter((p) => p.f.kind === kind);

  return (
    <div className="mx-auto max-w-6xl px-5 pt-14">
      <h1 className="text-[2.2rem] leading-tight font-bold tracking-tight">Results</h1>
      <div className="prose-w mt-4 max-w-3xl">
        <p>
          Two runs. The evaluation replays a corpus of agent shopping sessions through warrantd, Postgres and the
          bank simulator, once with only the mandate&apos;s caps checked and once with the full policy. The
          benchmark measures how fast decisions come back with many agents paying at once, and then races
          approvals on a few mandates through a bank that drops and loses replies, and compares warrant&apos;s
          ledger with the bank&apos;s.
        </p>
      </div>

      <h2 id="eval" className="mt-14 text-[1.5rem] font-bold">Evaluation</h2>
      {evalResult && caps && full ? (
        <>
          <div className="prose-w mt-3 max-w-3xl">
            <p>
              {evalResult.per_family} sessions per family, seed {evalResult.seed}. Each session is one user, one
              agent and one mandate over a few weeks: some ordinary orders, then whatever the family does. When
              warrant asks, the simulated user approves what they wanted and declines what they did not. That
              they always notice is an assumption, and the most generous one in here.
            </p>
            <p>
              <strong className="text-ink">The corpus is synthetic.</strong> The attack families are patterns from
              public writing on agent payment risk, generated with random carts, amounts and timings. The numbers
              say what the checks do on these patterns. They are not a fraud rate.
            </p>
          </div>

          <h3 className="mt-10 text-[1.15rem] font-bold">Attacks</h3>
          <p className="mt-1 text-[0.92rem] text-ink-soft">Payments the user did not want: how many went through without anyone asking.</p>
          <Table
            head={["family", "attempts", "caps only: paid", "warrant: paid", "warrant: asked user", "warrant: refused", "lost, caps / warrant", "why warrant stopped it"]}
            rows={pairs("attack").map(({ f, c }) => [
              <Fam key="f" t={f} />,
              f.unwanted,
              frac(c.unwanted_paid, c.unwanted),
              <span key="w" className={f.unwanted_paid ? "text-denied" : ""}>{frac(f.unwanted_paid, f.unwanted)}</span>,
              f.unwanted_asked,
              f.unwanted_stopped,
              `${rupees(c.money_lost)} / ${rupees(f.money_lost)}`,
              <Why key="r" r={f.reasons} />,
            ])}
          />

          <h3 className="mt-10 text-[1.15rem] font-bold">Built to get past it</h3>
          <p className="mt-1 text-[0.92rem] text-ink-soft">Attacks shaped to look like normal use. These are expected misses.</p>
          <Table
            head={["family", "attempts", "caps only: paid", "warrant: paid", "warrant: asked user", "lost, caps / warrant"]}
            rows={pairs("evasion").map(({ f, c }) => [
              <Fam key="f" t={f} />,
              f.unwanted,
              frac(c.unwanted_paid, c.unwanted),
              <span key="w" className="text-denied">{frac(f.unwanted_paid, f.unwanted)}</span>,
              f.unwanted_asked,
              `${rupees(c.money_lost)} / ${rupees(f.money_lost)}`,
            ])}
          />

          <h3 className="mt-10 text-[1.15rem] font-bold">Ordinary use</h3>
          <p className="mt-1 text-[0.92rem] text-ink-soft">
            Payments the user wanted. Every question warrant asks here is friction; every refusal is a mistake.
          </p>
          <Table
            head={["family", "payments", "paid without asking", "asked the user", "refused"]}
            rows={pairs("benign").map(({ f }) => [
              <Fam key="f" t={f} />,
              f.wanted,
              frac(f.wanted_paid, f.wanted),
              <span key="a" className={f.wanted_asked ? "text-asked" : ""}>{frac(f.wanted_asked, f.wanted)}</span>,
              <span key="r" className={f.wanted_refused ? "text-denied" : ""}>{frac(f.wanted_refused, f.wanted)}</span>,
            ])}
          />
          <p className="mt-3 max-w-3xl text-[0.9rem] text-ink-soft">
            Every question on ordinary use in this run is the monthly stock-up: one order per session over the
            step-up line, which the user set. Caps alone ask nothing, by construction.
          </p>
        </>
      ) : (
        <NotRun cmd="make eval" />
      )}

      <h2 id="bench" className="mt-16 text-[1.5rem] font-bold">Benchmark</h2>
      {benchResult ? (
        <>
          <p className="mt-2 text-[0.92rem] text-ink-soft">
            {benchResult.machine}, Postgres {benchResult.postgres}, warrantd and the agents in one process over
            loopback HTTP. One laptop, not a server; the shape matters more than the numbers.
          </p>
          <div className="mt-6 grid gap-6 lg:grid-cols-2">
            <Panel title="Decisions under load">
              <Row k="requests" v={`${benchResult.load.requests.toLocaleString("en-IN")} from ${benchResult.load.workers} agents on ${benchResult.load.mandates} mandates`} />
              <Row k="throughput" v={`${Math.round(benchResult.load.per_second).toLocaleString("en-IN")} decisions/s`} />
              <Row k="all requests" v={lat(benchResult.load.latency_ms)} />
              <Row k={`approvals, bank ${benchResult.load.bank_latency} included`} v={lat(benchResult.load.approve_latency_ms)} />
              <Row k="outcomes" v={Object.entries(benchResult.load.by_outcome).map(([o, n]) => `${o} ${n}`).join(", ")} />
            </Panel>
            <Panel title="The books, after a faulty bank">
              <Row k="racing requests" v={`${benchResult.books.requests} on ${benchResult.books.mandates} mandates`} />
              <Row k="bank faults" v={benchResult.books.faults} />
              <Row k="approved" v={`${benchResult.books.approved} (the caps allow exactly ${benchResult.books.approved_want})`} />
              <Row k="replies lost" v={`${benchResult.books.left_unknown}, resent ${benchResult.books.resent}, all settled in ${benchResult.books.settle_seconds.toFixed(1)}s`} />
              <Row k="debits, bank / warrant" v={`${benchResult.books.bank_debits} / ${benchResult.books.warrant_debits}`} />
              <Row k="blocks that disagree" v={String(benchResult.books.mismatched_blocks)} />
              <Row k="blocks overspent" v={String(benchResult.books.overspent_blocks)} />
            </Panel>
          </div>
        </>
      ) : (
        <NotRun cmd="make bench" />
      )}

      <h2 className="mt-16 text-[1.5rem] font-bold">Reproduce</h2>
      <pre className="mono mt-4 overflow-x-auto border border-rule bg-sheet px-5 py-4 text-[0.84rem] leading-relaxed">
        {`scripts/devdb.sh start   # a private Postgres in ./.pgdata on port 54329
make eval                 # results/eval.json
make bench                # results/bench.json`}
      </pre>
    </div>
  );
}

const frac = (n: number, d: number) => `${n}/${d} (${pct(n, d)})`;
const lat = (m: Record<string, number>) => `p50 ${m.p50?.toFixed(1)}ms, p99 ${m.p99?.toFixed(1)}ms, max ${m.max?.toFixed(0)}ms`;

function Fam({ t }: { t: Tally }) {
  return (
    <div className="min-w-[12rem]">
      <p className="text-ink">{familyLabel(t.family)}</p>
      <p className="text-[0.78rem] leading-snug text-ink-faint">{t.what}</p>
    </div>
  );
}

function Why({ r }: { r: Record<string, number> }) {
  const top = Object.entries(r).sort((a, b) => b[1] - a[1]).slice(0, 3);
  return (
    <span className="mono text-[0.76rem] text-ink-soft">
      {top.map(([k, n]) => `${k} ${n}`).join(", ") || "rejected before policy"}
    </span>
  );
}

function Table({ head, rows }: { head: string[]; rows: React.ReactNode[][] }) {
  return (
    <div className="mt-4 overflow-x-auto border border-rule bg-sheet">
      <table className="w-full text-left text-[0.88rem]">
        <thead>
          <tr className="border-b border-rule text-[0.78rem] text-ink-faint">
            {head.map((h) => (
              <th key={h} className="px-4 py-2.5 font-normal">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-b border-rule align-top last:border-b-0">
              {r.map((c, j) => (
                <td key={j} className={`px-4 py-3 ${j > 0 ? "num whitespace-nowrap" : ""}`}>
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-rule bg-sheet">
      <h3 className="border-b border-rule px-5 py-3 font-bold">{title}</h3>
      <dl>{children}</dl>
    </div>
  );
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="grid grid-cols-[11rem_1fr] gap-3 border-b border-rule px-5 py-2.5 text-[0.88rem] last:border-b-0">
      <dt className="text-ink-faint">{k}</dt>
      <dd className="num text-ink">{v}</dd>
    </div>
  );
}

function NotRun({ cmd }: { cmd: string }) {
  return (
    <p className="mt-3 text-ink-soft">
      Not run in this build. <code className="c">{cmd}</code> writes it.
    </p>
  );
}
