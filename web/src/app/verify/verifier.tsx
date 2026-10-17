"use client";

import { useState } from "react";
import { loadWarrant, parties, verify, type Verified } from "@/lib/wasm";

const claims = [
  { id: "unauthorized", label: "I did not authorize this" },
  { id: "not_delivered", label: "Nothing was delivered" },
  { id: "wrong_item", label: "The wrong thing came" },
];

// ---- Tampering: edits someone might make to a pack after the fact ----

type Entry = { index: number; kind: string; body: string; proof: string[] };
type Pack = { checkpoint: { payload: string; sig: string; key: string }; entries: Entry[] };

const dec = (b64: string) => new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
const enc = (s: string) => {
  let bin = "";
  for (const b of new TextEncoder().encode(s)) bin += String.fromCharCode(b);
  return btoa(bin);
};
// Edit the JSON inside a log record, and optionally the signed document
// inside that.
function editRecord(p: Pack, kind: string, f: (rec: Record<string, unknown>) => void) {
  const e = p.entries.find((x) => x.kind === kind);
  if (!e) return false;
  const rec = JSON.parse(dec(e.body));
  f(rec);
  e.body = enc(JSON.stringify(rec));
  return true;
}
function editSigned(p: Pack, kind: string, f: (doc: Record<string, unknown>) => void) {
  return editRecord(p, kind, (rec) => {
    const env = rec.signed as { payload: string };
    const doc = JSON.parse(dec(env.payload));
    f(doc);
    env.payload = enc(JSON.stringify(doc));
  });
}

const tampers: { id: string; label: string; what: string; apply: (p: Pack) => boolean }[] = [
  {
    id: "amount",
    label: "Raise the amount the agent signed",
    what: "Adds ₹1,000 to the debit request inside the log.",
    apply: (p) => editSigned(p, "request", (d) => (d.amount = Number(d.amount) + 1000_00)),
  },
  {
    id: "answer",
    label: "Turn Asha's yes into a no",
    what: "Flips approve in her step-up answer.",
    apply: (p) => editSigned(p, "stepup", (d) => (d.approve = !d.approve)),
  },
  {
    id: "drop",
    label: "Lose her answer",
    what: "Removes the step-up record from the pack.",
    apply: (p) => {
      const n = p.entries.length;
      p.entries = p.entries.filter((e) => e.kind !== "stepup");
      return p.entries.length < n;
    },
  },
  {
    id: "decision",
    label: "Rewrite the decision as approve",
    what: "Changes the logged outcome so it looks like no question was needed.",
    apply: (p) =>
      editRecord(p, "decision", (r) => {
        r.decision = { outcome: "approve", reasons: [] };
      }),
  },
  {
    id: "debit",
    label: "Change what the bank debited",
    what: "Edits the amount on the debit record.",
    apply: (p) => editRecord(p, "debit", (r) => (r.amount = Number(r.amount) + 500_00)),
  },
  {
    id: "proof",
    label: "Alter one proof hash",
    what: "Changes a character in one inclusion proof.",
    apply: (p) => {
      const e = p.entries.find((x) => x.proof.length > 0);
      if (!e) return false;
      const h = e.proof[0];
      e.proof[0] = (h[0] === "0" ? "1" : "0") + h.slice(1);
      return true;
    },
  },
  {
    id: "checkpoint",
    label: "Grow the signed checkpoint",
    what: "Claims the log had one more record when warrant signed it.",
    apply: (p) => {
      const cp = JSON.parse(dec(p.checkpoint.payload));
      cp.size += 1;
      p.checkpoint.payload = enc(JSON.stringify(cp));
      return true;
    },
  },
];

export function Verifier() {
  const [text, setText] = useState("");
  const [original, setOriginal] = useState("");
  const [baseline, setBaseline] = useState<Verified | null>(null);
  const [claim, setClaim] = useState("unauthorized");
  const [answer, setAnswer] = useState<Verified | null>(null);
  const [busy, setBusy] = useState(false);
  const [edits, setEdits] = useState<string[]>([]);

  async function run(pack: string, c: string, fresh = false) {
    setBusy(true);
    try {
      await loadWarrant();
      const v = verify(pack, c);
      setAnswer(v);
      if (fresh) {
        setOriginal(pack);
        setBaseline(v);
        setEdits([]);
      }
    } catch (e) {
      setAnswer({ error: String(e) });
    } finally {
      setBusy(false);
    }
  }

  async function sample() {
    const t = await (await fetch("/sample-pack.json")).text();
    setText(t);
    run(t, claim, true);
  }

  function tamper(t: (typeof tampers)[number]) {
    let p: Pack;
    try {
      p = JSON.parse(text);
    } catch {
      return;
    }
    if (!t.apply(p)) return;
    const next = JSON.stringify(p, null, 2);
    setText(next);
    setEdits((e) => [...e, t.id]);
    run(next, claim);
  }

  const loaded = !!original;
  return (
    <div className="mt-8 grid gap-6 lg:grid-cols-[1fr_1fr]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-[0.92rem]">
          <button type="button" onClick={sample} className="bg-ink px-4 py-2 font-bold text-sheet hover:bg-ink-soft">
            Load the sample pack
          </button>
          <label className="link cursor-pointer">
            or open a file
            <input
              type="file"
              accept="application/json,.json"
              className="sr-only"
              onChange={async (e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                const t = await f.text();
                setText(t);
                run(t, claim, true);
              }}
            />
          </label>
        </div>

        {loaded && (
          <div className="mt-5 border border-rule bg-sheet">
            <div className="flex items-baseline justify-between gap-3 border-b border-rule px-4 py-2.5">
              <p className="font-bold">Tamper with it</p>
              {edits.length > 0 && (
                <button
                  type="button"
                  onClick={() => {
                    setText(original);
                    setEdits([]);
                    setAnswer(baseline);
                  }}
                  className="text-[0.84rem] text-ink-soft underline decoration-rule-strong underline-offset-2 hover:text-ink"
                >
                  Back to the original
                </button>
              )}
            </div>
            <ul>
              {tampers.map((t) => {
                const done = edits.includes(t.id);
                return (
                  <li key={t.id} className="border-b border-rule last:border-b-0">
                    <button
                      type="button"
                      disabled={done || busy}
                      onClick={() => tamper(t)}
                      className="grid w-full grid-cols-[1fr_auto] items-baseline gap-3 px-4 py-2 text-left hover:bg-ground disabled:hover:bg-transparent"
                    >
                      <span>
                        <span className={`block text-[0.9rem] ${done ? "text-denied line-through" : "text-ink"}`}>{t.label}</span>
                        <span className="block text-[0.78rem] text-ink-faint">{t.what}</span>
                      </span>
                      <span className="text-[0.78rem] text-ink-faint">{done ? "done" : "apply"}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        )}

        <details className="mt-5 border border-rule bg-sheet" open={!loaded}>
          <summary className="cursor-pointer px-4 py-2.5 text-[0.86rem] text-ink-soft hover:text-ink">
            {loaded ? "The pack, as JSON. Edit it by hand too." : "Or paste a pack"}
          </summary>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            placeholder="paste a pack here"
            aria-label="Evidence pack JSON"
            className="mono block h-[22rem] w-full resize-y border-t border-rule bg-sheet p-3 text-[0.74rem] leading-relaxed text-ink outline-none"
          />
          <div className="border-t border-rule px-4 py-2.5">
            <button
              type="button"
              disabled={!text || busy}
              onClick={() => run(text, claim, !loaded)}
              className="border border-ink px-3.5 py-1.5 text-[0.88rem] font-bold hover:bg-ground disabled:opacity-40"
            >
              {busy ? "Checking" : "Check"}
            </button>
          </div>
        </details>

        <fieldset className="mt-5">
          <legend className="text-[0.86rem] text-ink-faint">Asha&apos;s claim</legend>
          <div className="mt-1.5 flex flex-wrap gap-x-5 gap-y-1.5 text-[0.92rem]">
            {claims.map((c) => (
              <label key={c.id} className="flex cursor-pointer items-center gap-2">
                <input
                  type="radio"
                  name="claim"
                  checked={claim === c.id}
                  onChange={() => {
                    setClaim(c.id);
                    if (text) run(text, c.id);
                  }}
                  className="accent-[var(--ink)]"
                />
                {c.label}
              </label>
            ))}
          </div>
        </fieldset>
      </div>
      <Report answer={answer} baseline={edits.length ? baseline : null} />
    </div>
  );
}

function Report({ answer, baseline }: { answer: Verified | null; baseline: Verified | null }) {
  if (!answer) {
    return (
      <div className="self-start border border-dashed border-rule-strong px-5 py-6 text-[0.92rem] text-ink-faint">
        Load the sample pack. The checks and the ruling show up here, and change as you tamper with it.
      </div>
    );
  }
  if (answer.error || !answer.report || !answer.ruling) {
    return <div className="self-start border border-denied bg-denied-tint px-5 py-4 text-[0.92rem] text-denied">{answer.error}</div>;
  }
  const { report, ruling } = answer;
  const failed = report.checks.filter((c) => !c.ok).length;
  const was = new Map(baseline?.report?.checks.map((c) => [c.name, c.ok]));
  const movedFrom = baseline?.ruling && baseline.ruling.liable !== ruling.liable ? baseline.ruling.liable : null;
  return (
    <div className="self-start border border-rule bg-sheet lg:sticky lg:top-4" aria-live="polite">
      <div className={`border-b border-rule px-5 py-4 ${report.ok ? "bg-paid-tint" : "bg-denied-tint"}`}>
        <p className={`text-[1.15rem] font-bold ${report.ok ? "text-paid" : "text-denied"}`}>
          {report.ok ? `All ${report.checks.length} checks pass` : `${failed} of ${report.checks.length} checks fail`}
        </p>
        <p className="mt-2 text-[0.95rem] text-ink">
          Carried by <span className="font-bold">{parties[ruling.liable] ?? ruling.liable}</span>
          {ruling.refund ? ". The user is refunded." : "."}
        </p>
        {movedFrom && (
          <p className="mt-1 text-[0.86rem] text-denied">Before your edits it was {parties[movedFrom] ?? movedFrom}.</p>
        )}
        <ul className="mt-1.5 space-y-1">
          {ruling.why.map((w, i) => (
            <li key={i} className="text-[0.86rem] leading-snug text-ink-soft">
              {w}
            </li>
          ))}
        </ul>
      </div>
      <ul className="max-h-[34rem] overflow-y-auto">
        {report.checks.map((c, i) => {
          const flipped = baseline && was.get(c.name) === true && !c.ok;
          return (
            <li
              key={i}
              className={`grid grid-cols-[3rem_1fr] gap-2 border-b border-rule px-5 py-2 text-[0.86rem] last:border-b-0 ${flipped ? "bg-denied-tint" : ""}`}
            >
              <span className={`mono text-[0.78rem] ${c.ok ? "text-paid" : "font-bold text-denied"}`}>{c.ok ? "ok" : "FAIL"}</span>
              <span className="text-ink">
                {c.name}
                {flipped && <span className="ml-1.5 text-[0.76rem] text-denied">was ok</span>}
                {c.detail && <span className="block text-ink-soft">{c.detail}</span>}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
