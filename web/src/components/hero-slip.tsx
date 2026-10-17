"use client";

import { useEffect, useMemo, useState } from "react";
import { amount, merchants, presets, toInput } from "@/lib/playground";
import { decide, loadWarrant, type Decided } from "@/lib/wasm";
import { reasonText } from "@/lib/reasons";
import { rupees } from "@/lib/data";

// The hero: one request ShopBot signed, as a slip of paper, stamped by a
// plain mandate and by warrant. The stamps come from the real policy
// running in the page, and the slip cycles through the attacks.

const ids = ["gift_card_injection", "payee_swap", "amount_inflation", "split_payment", "routine_groceries"];
const shown = ids.map((id) => presets.find((p) => p.id === id)!);
const EVERY = 4800;

export function HeroSlip() {
  const [i, setI] = useState(0);
  const [held, setHeld] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    loadWarrant().then(() => setReady(true), () => {});
  }, []);
  useEffect(() => {
    if (held || !ready || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const t = setInterval(() => setI((x) => (x + 1) % shown.length), EVERY);
    return () => clearInterval(t);
  }, [held, ready]);

  const p = shown[i];
  const s = p.setup;
  const d = useMemo<Decided | null>(() => (ready ? decide(toInput(s)) : null), [ready, s]);
  const m = merchants[s.merchant];

  return (
    <div onMouseEnter={() => setHeld(true)} onMouseLeave={() => setHeld(false)} className="relative">
      <div role="tablist" aria-label="Requests" className="mb-3 flex flex-wrap gap-1.5">
        {shown.map((x, j) => (
          <button
            key={x.id}
            role="tab"
            aria-selected={i === j}
            onClick={() => {
              setI(j);
              setHeld(true);
            }}
            className={`relative overflow-hidden border px-2 py-0.5 text-[0.8rem] ${
              i === j ? "border-ink text-ink" : "border-rule text-ink-faint hover:border-ink hover:text-ink"
            }`}
          >
            {x.label}
            {i === j && ready && !held && (
              <span key={i} aria-hidden className="countdown absolute bottom-0 left-0 h-[2px] bg-ink" style={{ animationDuration: `${EVERY}ms` }} />
            )}
          </button>
        ))}
      </div>

      <div key={p.id} className="slip relative border border-rule-strong bg-sheet shadow-[6px_6px_0_var(--rule)]">
        <div className="flex items-baseline justify-between border-b border-dashed border-rule-strong px-5 py-3">
          <p className="mono text-[0.74rem] text-ink-faint">DEBIT REQUEST · signed by shopbot</p>
          <p className="mono text-[0.74rem] text-ink-faint">10:00 IST</p>
        </div>
        <div className="px-5 pt-3 pb-2">
          <p className="text-[0.95rem] leading-snug text-ink">{p.story}</p>
          <p className="mono mt-3 text-[0.78rem] text-ink-soft">
            pay {m.name} · {s.kind === "circle" ? s.payee : m.vpa}
            {s.kind === "circle" && s.payee !== m.vpa && <span className="text-denied"> (not {m.name}&apos;s VPA)</span>}
          </p>
        </div>
        <ul className="mono px-5 text-[0.8rem]">
          {s.cart.map((it, j) => (
            <li key={j} className="flex justify-between gap-4 border-b border-dotted border-rule py-1">
              <span className={it.category === "gift_card" ? "text-denied" : "text-ink"}>
                {it.qty > 1 ? `${it.qty} × ` : ""}
                {it.name} <span className="text-ink-faint">{it.category}</span>
              </span>
              <span className="text-ink">{rupees(it.price * it.qty * 100)}</span>
            </li>
          ))}
          {s.fee > 0 && (
            <li className="flex justify-between gap-4 border-b border-dotted border-rule py-1 text-denied">
              <span>not in the cart</span>
              <span>{rupees(s.fee * 100)}</span>
            </li>
          )}
          {s.recent
            .filter((x) => x.mins < 60)
            .map((x, j) => (
              <li key={`r${j}`} className="flex justify-between gap-4 border-b border-dotted border-rule py-1 text-ink-faint">
                <span>paid {x.mins} min ago, same store</span>
                <span>{rupees(x.amount * 100)}</span>
              </li>
            ))}
        </ul>
        <div className="flex items-baseline justify-between px-5 pt-2 pb-4">
          <span className="text-[0.86rem] text-ink-soft">signed amount</span>
          <span className="num text-[1.3rem] font-bold text-ink">{rupees(amount(s) * 100)}</span>
        </div>

        <div className="grid grid-cols-2 border-t border-dashed border-rule-strong">
          <Stamp who="Mandate caps alone" d={d?.caps_only} wanted={p.wanted} delay={350} />
          <Stamp who="warrant" d={d?.warrant} wanted={p.wanted} delay={900} tilt />
        </div>
      </div>
      <p className="mt-3 text-[0.78rem] text-ink-faint">
        Stamped by warrant&apos;s policy, running in this page.{" "}
        <a href="#playground" className="link">
          Build your own request
        </a>
        .
      </p>
    </div>
  );
}

function Stamp({
  who,
  d,
  wanted,
  delay,
  tilt,
}: {
  who: string;
  d: Decided["warrant"] | undefined;
  wanted: boolean;
  delay: number;
  tilt?: boolean;
}) {
  const word = { approve: "PAID", step_up: "ASKED ASHA", deny: "REFUSED" } as const;
  const tone = !d
    ? "border-rule text-ink-faint"
    : d.outcome === "approve"
      ? wanted
        ? "border-paid text-paid"
        : "border-denied text-denied"
      : d.outcome === "step_up"
        ? "border-asked text-asked"
        : "border-paid text-paid";
  const note = !d
    ? ""
    : d.outcome === "approve"
      ? wanted
        ? "as Asha wanted"
        : "Asha did not want this"
      : reasonText[d.reasons[0]?.code] ?? "";
  return (
    <div className={`px-5 py-4 ${tilt ? "border-l border-dashed border-rule-strong" : ""}`}>
      <p className="text-[0.74rem] text-ink-faint">{who}</p>
      {d ? (
        <>
          <p
            className={`stamp-big mono mt-2 inline-block border-[3px] px-2 py-0.5 text-[1.05rem] font-bold tracking-wider ${tone}`}
            style={{ animationDelay: `${delay}ms`, ["--tilt" as string]: tilt ? "-5deg" : "3deg" }}
          >
            {word[d.outcome]}
          </p>
          <p className="reveal mt-2 text-[0.8rem] leading-snug text-ink-soft" style={{ animationDelay: `${delay + 200}ms` }}>
            {note}
          </p>
        </>
      ) : (
        <p className="mt-2 text-[0.84rem] text-ink-faint">…</p>
      )}
    </div>
  );
}
