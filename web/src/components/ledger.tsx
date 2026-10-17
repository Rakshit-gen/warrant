"use client";

import { useEffect, useRef, useState } from "react";
import type { StoryStep } from "@/lib/data";
import { rupees } from "@/lib/data";
import { reasonText } from "@/lib/reasons";

const look = {
  approve: { label: "Paid", bar: "bg-paid", text: "text-paid" },
  step_up: { label: "Asked Asha", bar: "bg-asked", text: "text-asked" },
  deny: { label: "Refused", bar: "bg-denied", text: "text-denied" },
  none: { label: "", bar: "bg-rule-strong", text: "text-ink-faint" },
} as const;

// The demo's story as ledger rows, played one entry at a time. Rows after
// the current one are greyed out until the story reaches them.
export function Ledger({ steps }: { steps: StoryStep[] }) {
  const [at, setAt] = useState(steps.length - 1);
  const [playing, setPlaying] = useState(false);
  const last = steps.length - 1;
  const box = useRef<HTMLDivElement>(null);

  // Play from the start the first time the story scrolls into view.
  useEffect(() => {
    const el = box.current;
    if (!el || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const io = new IntersectionObserver(
      ([e]) => {
        if (!e.isIntersecting) return;
        io.disconnect();
        setAt(0);
        setPlaying(true);
      },
      { threshold: 0.35 },
    );
    io.observe(el);
    return () => io.disconnect();
  }, []);

  useEffect(() => {
    if (!playing) return;
    const t = setInterval(() => {
      setAt((a) => {
        if (a >= last) {
          setPlaying(false);
          return a;
        }
        return a + 1;
      });
    }, 1800);
    return () => clearInterval(t);
  }, [playing, last]);

  const go = (n: number) => {
    setPlaying(false);
    setAt(Math.max(0, Math.min(last, n)));
  };

  return (
    <div
      ref={box}
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "ArrowDown" || e.key === "ArrowRight") {
          e.preventDefault();
          go(at + 1);
        }
        if (e.key === "ArrowUp" || e.key === "ArrowLeft") {
          e.preventDefault();
          go(at - 1);
        }
      }}
      className="outline-none"
    >
      <div className="flex flex-wrap items-center gap-2 border border-b-0 border-rule bg-sheet px-4 py-2.5">
        <button
          type="button"
          onClick={() => {
            if (playing) return setPlaying(false);
            setAt((a) => (a >= last ? 0 : a));
            setPlaying(true);
          }}
          className="bg-ink px-3 py-1 text-[0.84rem] font-bold text-sheet hover:bg-ink-soft"
        >
          {playing ? "Pause" : at >= last ? "Replay the story" : "Play"}
        </button>
        <button type="button" onClick={() => go(at - 1)} disabled={at === 0} className="border border-rule-strong px-2.5 py-1 text-[0.84rem] disabled:opacity-40">
          Back
        </button>
        <button type="button" onClick={() => go(at + 1)} disabled={at === last} className="border border-rule-strong px-2.5 py-1 text-[0.84rem] disabled:opacity-40">
          Next
        </button>
        <span className="num ml-auto text-[0.82rem] text-ink-faint">
          entry {at + 1} of {steps.length}
        </span>
      </div>
      <div className="h-[3px] bg-rule">
        <div className="h-full bg-ink transition-[width] duration-300" style={{ width: `${(100 * (at + 1)) / steps.length}%` }} />
      </div>
      <ol className="border border-t-0 border-rule bg-sheet">
        {steps.map((s, i) => {
          const l = look[s.outcome ?? "none"];
          const future = i > at;
          return (
            <li
              key={i}
              onClick={() => go(i)}
              className={`relative grid cursor-pointer grid-cols-[2.2rem_1fr] gap-x-3 border-b border-rule px-4 py-4 transition-opacity duration-300 last:border-b-0 sm:grid-cols-[2.6rem_1fr_11rem] sm:px-5 ${
                future ? "opacity-30" : ""
              } ${i === at ? "bg-ground" : ""}`}
            >
              <span aria-hidden className={`absolute inset-y-0 left-0 w-[3px] ${future ? "bg-rule" : l.bar}`} />
              <span className="mono pt-0.5 text-[0.8rem] text-ink-faint">{String(i + 1).padStart(2, "0")}</span>
              <div className="min-w-0">
                <p className="text-[0.98rem] leading-snug text-ink">{s.text}</p>
                {s.reasons && s.reasons.length > 0 && (
                  <ul className="mt-2 space-y-1">
                    {s.reasons.map((r) => (
                      <li key={r.code} className="text-[0.86rem] leading-snug text-ink-soft">
                        <span className={`mono text-[0.78rem] ${r.hard ? "text-denied" : "text-asked"}`}>{r.code}</span>{" "}
                        {reasonText[r.code] ?? r.detail}
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
                {s.outcome && !future && <p className={`stamp text-[0.9rem] font-bold ${l.text}`}>{l.label}</p>}
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
      <p className="mt-2 text-[0.78rem] text-ink-faint">Click a row, or use the arrow keys, to step through.</p>
    </div>
  );
}
