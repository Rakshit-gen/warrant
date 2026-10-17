"use client";

import { useEffect, useMemo, useState } from "react";
import {
  ago,
  allCategories,
  amount,
  cartTotal,
  mandateMerchants,
  merchants,
  presets,
  swappedVPA,
  toInput,
  type Item,
  type Past,
  type Preset,
  type Setup,
} from "@/lib/playground";
import { decide, loadWarrant, type Decision } from "@/lib/wasm";
import { reasonText } from "@/lib/reasons";
import { rupees } from "@/lib/data";

const groups: Preset["group"][] = ["Ordinary", "Attacks", "Gets past it"];
const r = (n: number) => rupees(n * 100);

export function Playground() {
  const [ready, setReady] = useState(false);
  const [loadErr, setLoadErr] = useState("");
  const [active, setActive] = useState<string | null>(presets[0].id);
  const [s, setS] = useState<Setup>(presets[0].setup);
  const [answer, setAnswer] = useState<{ key: string; approve: boolean } | null>(null);

  useEffect(() => {
    loadWarrant()
      .then(() => setReady(true))
      .catch((e) => setLoadErr(String(e)));
    // Links from the results page land here as #try-<family>.
    const fromHash = () => {
      const id = location.hash.match(/^#try-(.+)$/)?.[1];
      const p = presets.find((x) => x.id === id);
      if (p) pick(p);
    };
    const t = setTimeout(fromHash, 0);
    addEventListener("hashchange", fromHash);
    return () => {
      clearTimeout(t);
      removeEventListener("hashchange", fromHash);
    };
  }, []);

  function pick(p: Preset) {
    setActive(p.id);
    setS(p.setup);
  }
  function edit(patch: Partial<Setup>) {
    setActive(null);
    setS((x) => ({ ...x, ...patch }));
  }

  const input = useMemo(() => toInput(s), [s]);
  const key = useMemo(() => JSON.stringify(input) + s.tamper, [input, s.tamper]);
  const decided = useMemo(() => (ready ? decide(input) : null), [ready, input]);
  const preset = presets.find((p) => p.id === active) ?? null;
  const said = answer?.key === key ? answer.approve : null;

  return (
    <section id="playground" className="scroll-mt-4 py-14">
      <div className="grid gap-6 lg:grid-cols-[1fr_1fr] lg:items-end">
        <div>
          <h2 className="text-[1.6rem] leading-tight font-bold">Try to get a payment past it</h2>
          <p className="mt-3 max-w-xl leading-relaxed text-ink-soft">
            Pick an attack or build your own: change the mandate, the cart, the payee, the payments before it. Each
            change runs warrant&apos;s policy, the Go code warrantd runs, compiled to WebAssembly in this tab. The
            same request also goes through a policy that only checks the mandate&apos;s caps.
          </p>
        </div>
        <p className="text-[0.86rem] text-ink-faint lg:text-right">
          {loadErr ? (
            <span className="text-denied">The policy engine did not load: {loadErr}</span>
          ) : ready ? (
            "Policy loaded. Nothing leaves your browser."
          ) : (
            "Loading the policy engine…"
          )}
        </p>
      </div>

      <div className="mt-7 space-y-3">
        {groups.map((g) => (
          <div key={g} className="flex flex-wrap items-baseline gap-x-2 gap-y-2">
            <span className="w-28 shrink-0 text-[0.8rem] text-ink-faint">{g}</span>
            {presets
              .filter((p) => p.group === g)
              .map((p) => (
                <button
                  key={p.id}
                  type="button"
                  aria-pressed={active === p.id}
                  onClick={() => pick(p)}
                  className={`border px-2.5 py-1 text-[0.86rem] transition-colors ${
                    active === p.id
                      ? "border-ink bg-ink text-sheet"
                      : "border-rule-strong bg-sheet text-ink-soft hover:border-ink hover:text-ink"
                  }`}
                >
                  {p.label}
                </button>
              ))}
          </div>
        ))}
      </div>

      <div className="mt-5 border-l-[3px] border-ink py-1 pl-4">
        {preset ? (
          <p className="text-[1rem] text-ink">
            {preset.story}{" "}
            <span className={preset.wanted ? "text-paid" : "text-denied"}>
              {preset.wanted ? "Asha wants this payment." : "Asha does not want this payment."}
            </span>
          </p>
        ) : (
          <p className="text-[1rem] text-ink-soft">Your own request. Nobody has said whether Asha wants it.</p>
        )}
      </div>

      <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="space-y-6">
          <MandateForm s={s} edit={edit} />
          <RequestForm s={s} edit={edit} />
        </div>
        <div className="space-y-4 lg:sticky lg:top-4 lg:self-start">
          <Lane
            title="Mandate caps alone"
            sub="what Reserve Pay and Circle check today"
            d={decided?.caps_only}
            s={s}
            preset={preset}
            said={null}
          />
          <Lane
            title="warrant"
            sub="the full policy"
            d={decided?.warrant}
            s={s}
            preset={preset}
            said={said}
            onAnswer={(approve) => setAnswer({ key, approve })}
          />
          {decided?.scope_error && (
            <p className="border border-asked bg-asked-tint px-4 py-2.5 text-[0.84rem] text-ink">
              The bank would not accept this mandate as signed: {decided.scope_error}
            </p>
          )}
          <details className="border border-rule bg-sheet">
            <summary className="cursor-pointer px-4 py-2.5 text-[0.86rem] text-ink-soft hover:text-ink">
              The input policy.Evaluate saw
            </summary>
            <pre className="mono max-h-80 overflow-auto border-t border-rule px-4 py-3 text-[0.72rem] leading-relaxed text-ink-soft">
              {JSON.stringify(input, null, 2)}
            </pre>
          </details>
        </div>
      </div>
    </section>
  );
}

// ---- The two lanes ------------------------------------------------------

const stages = ["Agent's key", "Fresh nonce", "Policy", "Asha's phone", "Bank", "Log"];
type Node = "pass" | "stop" | "wait" | "skip" | "idle";

function plan(d: Decision | undefined, s: Setup, said: boolean | null, caps: boolean) {
  const nodes: Node[] = Array(6).fill("idle");
  if (!d) return { nodes, verdict: null as null | { tone: string; text: string }, paid: false };
  const upTo = (n: number) => {
    for (let i = 0; i < n; i++) nodes[i] = "pass";
  };
  if (s.tamper !== "none") {
    const at = s.tamper === "forged" ? 0 : 1;
    upTo(at);
    nodes[at] = "stop";
    return {
      nodes,
      paid: false,
      verdict: {
        tone: "text-rejected",
        text: s.tamper === "forged" ? "Rejected: not signed by ShopBot's key" : "Rejected: this nonce was used before",
      },
    };
  }
  upTo(2);
  if (d.outcome === "deny") {
    nodes[2] = "stop";
    return { nodes, paid: false, verdict: { tone: "text-denied", text: "Refused" } };
  }
  nodes[2] = "pass";
  if (d.outcome === "step_up") {
    if (said === null) {
      nodes[3] = "wait";
      return { nodes, paid: false, verdict: { tone: "text-asked", text: "Waiting for Asha" } };
    }
    if (!said) {
      nodes[3] = "stop";
      return { nodes, paid: false, verdict: { tone: "text-denied", text: "Asha declined" } };
    }
    nodes[3] = "pass";
  } else {
    nodes[3] = "skip";
  }
  nodes[4] = "pass";
  nodes[5] = caps ? "skip" : "pass";
  return { nodes, paid: true, verdict: { tone: "text-paid", text: `Paid ${r(amount(s))}` } };
}

function Lane({
  title,
  sub,
  d,
  s,
  preset,
  said,
  onAnswer,
}: {
  title: string;
  sub: string;
  d: Decision | undefined;
  s: Setup;
  preset: Preset | null;
  said: boolean | null;
  onAnswer?: (approve: boolean) => void;
}) {
  const [sends, setSends] = useState(0);
  const caps = !onAnswer;
  const p = plan(d, s, said, caps);
  // A new decision, or a replay, sends the request down the track again.
  // After Asha answers, it carries on from her phone instead.
  const sig = JSON.stringify([p.nodes, p.verdict, d?.reasons]);
  return (
    <div className={`border bg-sheet ${caps ? "border-rule" : "border-ink"}`}>
      <div className="flex items-baseline justify-between gap-3 border-b border-rule px-4 py-2.5">
        <p className="font-bold">
          {title} <span className="text-[0.78rem] font-normal text-ink-faint">{sub}</span>
        </p>
        <button
          type="button"
          onClick={() => setSends((n) => n + 1)}
          disabled={!d}
          className="shrink-0 text-[0.8rem] text-ink-soft underline decoration-rule-strong underline-offset-2 hover:text-ink disabled:opacity-40"
        >
          Send again
        </button>
      </div>
      <Run key={sig + sends} {...p} from={said !== null ? 4 : 0} d={d} s={s} preset={preset} caps={caps} onAnswer={onAnswer} />
    </div>
  );
}

function Run({
  nodes,
  verdict,
  paid,
  from,
  d,
  s,
  preset,
  caps,
  onAnswer,
}: ReturnType<typeof plan> & {
  from: number;
  d: Decision | undefined;
  s: Setup;
  preset: Preset | null;
  caps: boolean;
  onAnswer?: (approve: boolean) => void;
}) {
  // How far along the track the request has got: nodes before t are shown.
  const end = nodes.findLastIndex((n) => n !== "idle") + 1;
  const [t, setT] = useState(Math.min(from, end));
  useEffect(() => {
    if (!end) return;
    const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const timer = setInterval(
      () =>
        setT((x) => {
          if (reduced || x >= end) {
            clearInterval(timer);
            return end;
          }
          return x + 1;
        }),
      reduced ? 0 : 340,
    );
    return () => clearInterval(timer);
  }, [end]);

  const done = end > 0 && t >= end;
  const shown = nodes.map((n, i) => (i < t ? n : "idle"));
  const at = Math.max(0, Math.min(t, end) - 1);
  const waiting = done && nodes[3] === "wait";
  const lost = preset && !preset.wanted && paid;
  const blocked = preset && preset.wanted && !paid && !waiting;

  return (
    <>
      <Track nodes={shown} at={t > 0 ? at : -1} moving={!done} />
      <div className="min-h-[5.5rem] px-4 pt-1 pb-4" aria-live="polite">
        {!d ? (
          <p className="text-[1.1rem] text-ink-faint">Loading…</p>
        ) : !done ? (
          <p className="text-[1.1rem] text-ink-faint">Checking: {stages[at]}…</p>
        ) : (
          <>
            <p className={`stamp text-[1.45rem] leading-tight font-bold ${verdict?.tone}`}>{verdict?.text}</p>
            {lost && (
              <p className="mt-1 text-[0.88rem] text-denied">
                Asha did not want this. {r(amount(s))} gone{caps ? ", and no record of why it was allowed." : "."}
              </p>
            )}
            {blocked && <p className="mt-1 text-[0.88rem] text-asked">Asha wanted this one. A false alarm.</p>}
            {preset && !preset.wanted && !paid && !waiting && (
              <p className="mt-1 text-[0.88rem] text-paid">Stopped before any money moved.</p>
            )}
            {s.tamper === "none" && d.reasons.length > 0 && (
              <ul className="mt-3 space-y-1.5">
                {d.reasons.map((x, i) => (
                  <li
                    key={x.code}
                    className="reveal grid grid-cols-[0.6rem_1fr] gap-2 text-[0.86rem] leading-snug"
                    style={{ animationDelay: `${80 + i * 70}ms` }}
                  >
                    <span aria-hidden className={`mt-1.5 h-1.5 w-1.5 ${x.hard ? "bg-denied" : "bg-asked"}`} />
                    <span>
                      <span className="text-ink">{reasonText[x.code] ?? x.code}</span>{" "}
                      <span className="mono text-[0.74rem] text-ink-faint">
                        {x.code}: {x.detail}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {caps && d.outcome === "approve" && s.tamper === "none" && (
              <p className="mt-2 text-[0.84rem] text-ink-faint">Inside the limits, so it goes through.</p>
            )}
            {waiting && onAnswer && <Phone s={s} d={d} onAnswer={onAnswer} />}
          </>
        )}
      </div>
    </>
  );
}

function Track({ nodes, at, moving }: { nodes: Node[]; at: number; moving: boolean }) {
  const look: Record<Node, string> = {
    pass: "border-ink bg-ink",
    stop: "border-denied bg-denied",
    wait: "border-asked bg-asked waiting",
    skip: "border-rule-strong border-dashed bg-sheet",
    idle: "border-rule bg-sheet",
  };
  const lit = (i: number) => nodes[i] === "pass" || nodes[i] === "stop" || nodes[i] === "wait" || (nodes[i] === "skip" && nodes[i + 1] === "pass");
  return (
    <div className="relative px-4 pt-4 pb-3">
      <ol className="grid grid-cols-6">
        {stages.map((name, i) => (
          <li key={name} className="relative flex flex-col items-center text-center">
            {i > 0 && (
              <span aria-hidden className="absolute top-[0.4rem] right-1/2 h-[2px] w-full bg-rule">
                <span className={`block h-full bg-ink transition-[width] duration-300 ease-out ${lit(i) ? "w-full" : "w-0"}`} />
              </span>
            )}
            <span
              className={`relative z-10 h-[0.9rem] w-[0.9rem] border-2 transition-colors duration-200 ${look[nodes[i]]} ${
                nodes[i] !== "idle" && nodes[i] !== "skip" ? "land" : ""
              }`}
            />
            <span
              className={`mt-1.5 text-[0.7rem] leading-tight transition-colors ${
                nodes[i] === "stop"
                  ? "font-bold text-denied"
                  : nodes[i] === "wait"
                    ? "font-bold text-asked"
                    : i === at && moving
                      ? "font-bold text-ink"
                      : "text-ink-faint"
              }`}
            >
              {name}
              {nodes[i] === "skip" && <span className="block">{i === 5 ? "no record" : "not needed"}</span>}
            </span>
          </li>
        ))}
      </ol>
      {at >= 0 && (
        <span
          aria-hidden
          className={`absolute top-[0.75rem] z-20 h-[1.6rem] w-[1.6rem] -translate-x-1/2 border-2 border-ink transition-[left] duration-300 ease-out ${
            moving ? "opacity-100" : "opacity-0"
          }`}
          style={{ left: `calc(1rem + (100% - 2rem) * ${(at + 0.5) / 6})` }}
        />
      )}
    </div>
  );
}

function Phone({ s, d, onAnswer }: { s: Setup; d: Decision; onAnswer: (approve: boolean) => void }) {
  return (
    <div className="mt-4 max-w-sm border-2 border-ink bg-ground p-4">
      <p className="text-[0.74rem] text-ink-faint">Asha&apos;s phone · warrant</p>
      <p className="mt-1.5 text-[1rem] leading-snug font-bold text-ink">
        ShopBot wants to pay {r(amount(s))} to {merchants[s.merchant]?.name ?? s.merchant}.
      </p>
      <ul className="mt-1.5 space-y-0.5">
        {d.reasons.map((x) => (
          <li key={x.code} className="text-[0.84rem] leading-snug text-ink-soft">
            {reasonText[x.code] ?? x.code}
          </li>
        ))}
      </ul>
      <div className="mt-3 grid grid-cols-2 gap-2">
        <button
          type="button"
          onClick={() => onAnswer(false)}
          className="border border-ink bg-sheet py-2 text-[0.9rem] font-bold hover:bg-denied-tint"
        >
          Decline
        </button>
        <button type="button" onClick={() => onAnswer(true)} className="bg-ink py-2 text-[0.9rem] font-bold text-sheet hover:bg-ink-soft">
          Approve
        </button>
      </div>
      <p className="mt-2.5 text-[0.74rem] leading-snug text-ink-faint">
        Her answer is signed by her phone&apos;s key, names this exact request, and goes in the log.
      </p>
    </div>
  );
}

// ---- The forms ------------------------------------------------------------

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="border border-rule bg-sheet">
      <p className="border-b border-rule px-4 py-2.5 font-bold">{title}</p>
      <div className="space-y-4 px-4 py-4">{children}</div>
    </div>
  );
}

function Num({ label, value, onChange }: { label: string; value: number; onChange: (n: number) => void }) {
  return (
    <label className="block">
      <span className="text-[0.78rem] text-ink-faint">{label}</span>
      <span className="mt-1 flex items-center border border-rule-strong bg-ground focus-within:border-ink">
        <span className="pl-2 text-[0.86rem] text-ink-faint">₹</span>
        <input
          type="number"
          min={0}
          value={value}
          onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
          className="num w-full min-w-0 bg-transparent px-1.5 py-1.5 text-[0.9rem] text-ink outline-none"
        />
      </span>
    </label>
  );
}

function MandateForm({ s, edit }: { s: Setup; edit: (p: Partial<Setup>) => void }) {
  const kinds = [
    { id: "reserve_pay", label: "Groceries at Zepto", sub: "Reserve Pay block" },
    { id: "circle", label: "Phone and power bills", sub: "UPI Circle" },
  ] as const;
  return (
    <Panel title="The mandate Asha's phone signed">
      <div className="grid grid-cols-2 border border-rule-strong">
        {kinds.map((k) => (
          <button
            key={k.id}
            type="button"
            aria-pressed={s.kind === k.id}
            onClick={() => {
              const base = presets.find((p) => p.id === (k.id === "circle" ? "bill_autopay" : "routine_groceries"))!;
              edit(base.setup);
            }}
            className={`px-3 py-2 text-left transition-colors ${s.kind === k.id ? "bg-ink text-sheet" : "text-ink-soft hover:bg-ground"}`}
          >
            <span className="block text-[0.9rem] font-bold">{k.label}</span>
            <span className={`block text-[0.74rem] ${s.kind === k.id ? "text-sheet/70" : "text-ink-faint"}`}>{k.sub}</span>
          </button>
        ))}
      </div>
      <div>
        <p className="text-[0.78rem] text-ink-faint">
          At {mandateMerchants(s).map((m) => merchants[m].name).join(" and ")}, on
        </p>
        <div className="mt-1.5 flex flex-wrap gap-1.5">
          {allCategories
            .filter((c) => c !== "gift_card")
            .map((c) => {
              const on = s.categories.includes(c);
              return (
                <button
                  key={c}
                  type="button"
                  aria-pressed={on}
                  onClick={() => edit({ categories: on ? s.categories.filter((x) => x !== c) : [...s.categories, c] })}
                  className={`border px-2 py-0.5 text-[0.82rem] ${on ? "border-ink text-ink" : "border-rule text-ink-faint line-through"}`}
                >
                  {c}
                </button>
              );
            })}
          {s.categories.length === 0 && <span className="text-[0.82rem] text-asked">any category</span>}
        </div>
        <label className="mt-2.5 flex items-center gap-2 text-[0.86rem] text-ink-soft">
          <input
            type="checkbox"
            checked={s.allowGiftCards}
            onChange={(e) => edit({ allowGiftCards: e.target.checked })}
            className="accent-[var(--ink)]"
          />
          Gift cards are fine without asking me
        </label>
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Num label="per payment" value={s.maxDebit} onChange={(n) => edit({ maxDebit: n })} />
        <Num label="in total" value={s.cap} onChange={(n) => edit({ cap: n })} />
        <Num label="ask me above" value={s.stepUp} onChange={(n) => edit({ stepUp: n })} />
        <Num label="already spent" value={s.spent} onChange={(n) => edit({ spent: n })} />
      </div>
      <label className="flex items-center gap-2 text-[0.86rem] text-ink-soft">
        <input type="checkbox" checked={s.revoked} onChange={(e) => edit({ revoked: e.target.checked })} className="accent-[var(--ink)]" />
        Asha has revoked it
      </label>
    </Panel>
  );
}

function RequestForm({ s, edit }: { s: Setup; edit: (p: Partial<Setup>) => void }) {
  const setItem = (i: number, p: Partial<Item>) => edit({ cart: s.cart.map((it, j) => (j === i ? { ...it, ...p } : it)) });
  const setPast = (i: number, p: Partial<Past>) => edit({ recent: s.recent.map((x, j) => (j === i ? { ...x, ...p } : x)) });
  const sel = "border border-rule-strong bg-ground px-1.5 py-1 text-[0.86rem] text-ink outline-none focus:border-ink";
  const field = "num border border-rule-strong bg-ground px-1.5 py-1 text-[0.86rem] text-ink outline-none focus:border-ink";
  const options = s.kind === "circle" ? ["airtel", "bescom", "zepto"] : ["zepto", "zeptoo", "blinkit"];
  return (
    <Panel title="What ShopBot signs and sends">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block">
          <span className="text-[0.78rem] text-ink-faint">pay</span>
          <select
            value={s.merchant}
            onChange={(e) => edit({ merchant: e.target.value, payee: merchants[e.target.value].vpa })}
            className={`${sel} mt-1 w-full`}
          >
            {options.map((m) => (
              <option key={m} value={m}>
                {merchants[m].name}
                {merchants[m].note ? ` (${merchants[m].note})` : ""}
              </option>
            ))}
          </select>
        </label>
        {s.kind === "circle" && (
          <label className="block">
            <span className="text-[0.78rem] text-ink-faint">to VPA</span>
            <select value={s.payee} onChange={(e) => edit({ payee: e.target.value })} className={`${sel} mono mt-1 w-full`}>
              <option value={merchants[s.merchant]?.vpa}>{merchants[s.merchant]?.vpa} (registered)</option>
              <option value={swappedVPA}>{swappedVPA}</option>
            </select>
          </label>
        )}
      </div>

      <div>
        <p className="text-[0.78rem] text-ink-faint">cart</p>
        <div className="mt-1 border border-rule">
          {s.cart.map((it, i) => (
            <div key={i} className="grid grid-cols-[1fr_6.5rem_4.6rem_2.6rem_1.6rem] items-center gap-1.5 border-b border-rule px-2 py-1.5 last:border-b-0">
              <input
                value={it.name}
                aria-label="item"
                onChange={(e) => setItem(i, { name: e.target.value })}
                className="min-w-0 bg-transparent text-[0.86rem] text-ink outline-none"
              />
              <select value={it.category} aria-label="category" onChange={(e) => setItem(i, { category: e.target.value })} className={sel}>
                {allCategories.map((c) => (
                  <option key={c}>{c}</option>
                ))}
              </select>
              <input
                type="number"
                min={0}
                aria-label="price in rupees"
                value={it.price}
                onChange={(e) => setItem(i, { price: Math.max(0, Number(e.target.value) || 0) })}
                className={`${field} w-full min-w-0`}
              />
              <input
                type="number"
                min={1}
                aria-label="quantity"
                value={it.qty}
                onChange={(e) => setItem(i, { qty: Math.max(1, Number(e.target.value) || 1) })}
                className={`${field} w-full min-w-0`}
              />
              <button
                type="button"
                aria-label={`remove ${it.name}`}
                onClick={() => edit({ cart: s.cart.filter((_, j) => j !== i) })}
                className="text-[1rem] text-ink-faint hover:text-denied"
              >
                ×
              </button>
            </div>
          ))}
          <div className="flex items-center justify-between px-2 py-1.5">
            <button
              type="button"
              onClick={() => edit({ cart: [...s.cart, { name: "Gift card", category: "gift_card", price: 2000, qty: 1 }] })}
              className="text-[0.82rem] text-ink-soft underline decoration-rule-strong underline-offset-2 hover:text-ink"
            >
              add an item
            </button>
            <span className="num text-[0.84rem] text-ink-soft">cart {r(cartTotal(s))}</span>
          </div>
        </div>
        <div className="mt-3 grid grid-cols-2 items-end gap-3">
          <Num label="fee added outside the cart" value={s.fee} onChange={(n) => edit({ fee: n })} />
          <p className="pb-1.5 text-right text-[0.9rem]">
            <span className="text-ink-faint">signed amount </span>
            <span className="num font-bold text-ink">{r(amount(s))}</span>
          </p>
        </div>
      </div>

      <div>
        <label className="flex items-center gap-2 text-[0.86rem] text-ink-soft">
          <input
            type="checkbox"
            checked={!!s.intent}
            onChange={(e) =>
              edit({ intent: e.target.checked ? { text: "this week's groceries", max: 2000, categories: ["grocery", "snacks", "household"] } : null })
            }
            className="accent-[var(--ink)]"
          />
          Asha&apos;s phone signed an errand receipt for this
        </label>
        {s.intent && (
          <div className="mt-2 grid grid-cols-[1fr_8rem] gap-3">
            <label className="block">
              <span className="text-[0.78rem] text-ink-faint">for</span>
              <input
                value={s.intent.text}
                onChange={(e) => edit({ intent: { ...s.intent!, text: e.target.value } })}
                className="mt-1 w-full border border-rule-strong bg-ground px-1.5 py-1.5 text-[0.86rem] text-ink outline-none focus:border-ink"
              />
            </label>
            <Num label="up to" value={s.intent.max} onChange={(n) => edit({ intent: { ...s.intent!, max: n } })} />
          </div>
        )}
      </div>

      <div>
        <p className="text-[0.78rem] text-ink-faint">earlier payments on this mandate</p>
        <div className="mt-1 border border-rule">
          {s.recent.map((p, i) => (
            <div key={i} className="grid grid-cols-[6rem_1fr_6rem_1.6rem] items-center gap-1.5 border-b border-rule px-2 py-1.5 last:border-b-0">
              <input
                type="number"
                min={0}
                aria-label="minutes ago"
                value={p.mins}
                onChange={(e) => setPast(i, { mins: Math.max(0, Number(e.target.value) || 0) })}
                className={`${field} w-full min-w-0`}
              />
              <span className="text-[0.8rem] text-ink-faint">
                min, {ago(p.mins)} at {merchants[p.merchant]?.name}
              </span>
              <input
                type="number"
                min={0}
                aria-label="amount in rupees"
                value={p.amount}
                onChange={(e) => setPast(i, { amount: Math.max(0, Number(e.target.value) || 0) })}
                className={`${field} w-full min-w-0`}
              />
              <button
                type="button"
                aria-label="remove payment"
                onClick={() => edit({ recent: s.recent.filter((_, j) => j !== i) })}
                className="text-[1rem] text-ink-faint hover:text-denied"
              >
                ×
              </button>
            </div>
          ))}
          <div className="px-2 py-1.5">
            <button
              type="button"
              onClick={() => edit({ recent: [{ mins: 4, merchant: s.merchant, amount: 700 }, ...s.recent] })}
              className="text-[0.82rem] text-ink-soft underline decoration-rule-strong underline-offset-2 hover:text-ink"
            >
              add a payment a few minutes ago
            </button>
          </div>
        </div>
      </div>

      <label className="block">
        <span className="text-[0.78rem] text-ink-faint">signature</span>
        <select value={s.tamper} onChange={(e) => edit({ tamper: e.target.value as Setup["tamper"] })} className={`${sel} mt-1 w-full`}>
          <option value="none">signed by ShopBot&apos;s registered key, fresh nonce</option>
          <option value="replay">a copy of an earlier request, same nonce</option>
          <option value="forged">signed by some other key</option>
        </select>
      </label>
    </Panel>
  );
}
