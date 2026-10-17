import type { Tally } from "@/lib/data";
import { familyLabel, pct, rupees } from "@/lib/data";

// For each attack family: of the payments the user did not want, the
// share that went through without anyone asking, under a plain mandate
// and under warrant.
export function SlippedBars({ caps, full }: { caps: Tally[]; full: Tally[] }) {
  return (
    <div className="border border-rule bg-sheet">
      <div className="hidden grid-cols-[13rem_1fr_7rem] gap-4 border-b border-rule px-5 py-2.5 text-[0.8rem] text-ink-faint sm:grid">
        <span>attack</span>
        <span>unwanted payments that went through</span>
        <span className="text-right">money lost</span>
      </div>
      {full.map((f, i) => {
        const c = caps[i];
        return (
          <div key={f.family} className="grid gap-x-4 gap-y-1.5 border-b border-rule px-5 py-3 last:border-b-0 sm:grid-cols-[13rem_1fr_7rem]">
            <div>
              <p className="text-[0.95rem] text-ink">{familyLabel(f.family)}</p>
              <p className="text-[0.8rem] leading-snug text-ink-faint">{f.what}</p>
            </div>
            <div className="space-y-1.5 self-center">
              <Bar label="mandate caps" n={c.unwanted_paid} d={c.unwanted} tone="bg-ink-faint" />
              <Bar label="warrant" n={f.unwanted_paid} d={f.unwanted} tone="bg-denied" />
            </div>
            <div className="mono self-center text-[0.82rem] text-ink-soft sm:text-right">
              <p>{rupees(c.money_lost)}</p>
              <p className="text-ink">{rupees(f.money_lost)}</p>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function Bar({ label, n, d, tone }: { label: string; n: number; d: number; tone: string }) {
  const w = d ? (100 * n) / d : 0;
  return (
    <div className="grid grid-cols-[6.5rem_1fr_4.5rem] items-center gap-2">
      <span className="text-[0.78rem] text-ink-faint">{label}</span>
      <span className="h-2.5 bg-ground">
        <span className={`block h-full ${tone}`} style={{ width: `${Math.max(w, n ? 1.5 : 0)}%` }} />
      </span>
      <span className="mono text-right text-[0.78rem] text-ink-soft">
        {n}/{d} <span className="text-ink-faint">{pct(n, d)}</span>
      </span>
    </div>
  );
}
