import type { StoryStep } from "@/lib/data";
import { rupees } from "@/lib/data";

const look = {
  approve: { label: "Paid", bar: "bg-paid", text: "text-paid" },
  step_up: { label: "Asked the user", bar: "bg-asked", text: "text-asked" },
  deny: { label: "Refused", bar: "bg-denied", text: "text-denied" },
  none: { label: "", bar: "bg-rule-strong", text: "text-ink-faint" },
} as const;

// The demo's story as ledger rows: what happened, what warrant said, why.
export function Ledger({ steps }: { steps: StoryStep[] }) {
  return (
    <ol className="border border-rule bg-sheet">
      {steps.map((s, i) => {
        const l = look[s.outcome ?? "none"];
        return (
          <li key={i} className="relative grid grid-cols-[2.2rem_1fr] gap-x-3 border-b border-rule px-4 py-4 last:border-b-0 sm:grid-cols-[2.6rem_1fr_11rem] sm:px-5">
            <span aria-hidden className={`absolute inset-y-0 left-0 w-[3px] ${l.bar}`} />
            <span className="mono pt-0.5 text-[0.8rem] text-ink-faint">{String(i + 1).padStart(2, "0")}</span>
            <div className="min-w-0">
              <p className="text-[0.98rem] leading-snug text-ink">{s.text}</p>
              {s.reasons && s.reasons.length > 0 && (
                <ul className="mt-2 space-y-1">
                  {s.reasons.map((r) => (
                    <li key={r.code} className="text-[0.86rem] leading-snug text-ink-soft">
                      <span className={`mono text-[0.8rem] ${r.hard ? "text-denied" : "text-asked"}`}>{r.code}</span>{" "}
                      {r.detail}
                    </li>
                  ))}
                </ul>
              )}
              {s.notes?.map((n, j) => (
                <p key={j} className="mt-1.5 text-[0.86rem] leading-snug text-ink-soft">
                  {n}
                </p>
              ))}
            </div>
            <div className="col-start-2 mt-2 sm:col-start-3 sm:mt-0 sm:text-right">
              {s.outcome && <p className={`text-[0.9rem] font-bold ${l.text}`}>{l.label}</p>}
              {s.debit && (
                <p className="mono mt-0.5 text-[0.82rem] text-ink-soft">
                  {rupees(s.debit.amount)} {s.debit.status}
                </p>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
