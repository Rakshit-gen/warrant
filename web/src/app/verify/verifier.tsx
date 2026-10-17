"use client";

import { useState } from "react";

type Check = { name: string; ok: boolean; detail?: string };
type Ruling = { claim: string; liable: string; refund: boolean; verified: boolean; why: string[] };
type Answer = { report?: { ok: boolean; checks: Check[] }; ruling?: Ruling; error?: string };

declare global {
  interface Window {
    Go: new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): Promise<void> };
    warrantVerify?: (pack: string, claim: string) => string;
  }
}

const claims = [
  { id: "unauthorized", label: "I did not authorize this" },
  { id: "not_delivered", label: "Nothing was delivered" },
  { id: "wrong_item", label: "The wrong thing came" },
];

const parties: Record<string, string> = {
  platform: "the platform that ran the authorization",
  user: "the user",
  merchant: "the merchant, through its refund process",
  agent_developer: "the agent's developer",
  none: "nobody; no money moved",
};

let loading: Promise<void> | null = null;

// Loads Go's wasm glue and the verifier once, on first use.
function loadVerifier(): Promise<void> {
  if (window.warrantVerify) return Promise.resolve();
  loading ??= new Promise<void>((resolve, reject) => {
    const s = document.createElement("script");
    s.src = "/wasm_exec.js";
    s.onerror = () => reject(new Error("could not load wasm_exec.js"));
    s.onload = async () => {
      try {
        const go = new window.Go();
        const { instance } = await WebAssembly.instantiateStreaming(fetch("/verify.wasm"), go.importObject);
        go.run(instance);
        resolve();
      } catch (e) {
        reject(e);
      }
    };
    document.head.appendChild(s);
  }).catch((e) => {
    loading = null;
    throw e;
  });
  return loading;
}

export function Verifier() {
  const [text, setText] = useState("");
  const [claim, setClaim] = useState("unauthorized");
  const [answer, setAnswer] = useState<Answer | null>(null);
  const [busy, setBusy] = useState(false);

  async function run(pack: string, c: string) {
    setBusy(true);
    try {
      await loadVerifier();
      setAnswer(JSON.parse(window.warrantVerify!(pack, c)));
    } catch (e) {
      setAnswer({ error: String(e) });
    } finally {
      setBusy(false);
    }
  }

  async function sample() {
    const t = await (await fetch("/sample-pack.json")).text();
    setText(t);
    run(t, claim);
  }

  return (
    <div className="mt-8 grid gap-6 lg:grid-cols-[1fr_1fr]">
      <div>
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
                run(t, claim);
              }}
            />
          </label>
        </div>
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          spellCheck={false}
          placeholder="or paste a pack here"
          aria-label="Evidence pack JSON"
          className="mono mt-4 h-[26rem] w-full resize-y border border-rule-strong bg-sheet p-3 text-[0.76rem] leading-relaxed text-ink outline-none focus:border-ink"
        />
        <fieldset className="mt-3">
          <legend className="text-[0.86rem] text-ink-faint">The user&apos;s claim</legend>
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
        <button
          type="button"
          disabled={!text || busy}
          onClick={() => run(text, claim)}
          className="mt-4 border border-ink px-4 py-2 text-[0.92rem] font-bold hover:bg-sheet disabled:opacity-40"
        >
          {busy ? "Checking" : "Check"}
        </button>
      </div>
      <Report answer={answer} />
    </div>
  );
}

function Report({ answer }: { answer: Answer | null }) {
  if (!answer) {
    return (
      <div className="border border-dashed border-rule-strong px-5 py-6 text-[0.92rem] text-ink-faint">
        The checks show up here.
      </div>
    );
  }
  if (answer.error || !answer.report || !answer.ruling) {
    return <div className="border border-denied bg-denied-tint px-5 py-4 text-[0.92rem] text-denied">{answer.error}</div>;
  }
  const { report, ruling } = answer;
  const failed = report.checks.filter((c) => !c.ok).length;
  return (
    <div className="border border-rule bg-sheet">
      <div className={`border-b border-rule px-5 py-4 ${report.ok ? "bg-paid-tint" : "bg-denied-tint"}`}>
        <p className={`text-[1.15rem] font-bold ${report.ok ? "text-paid" : "text-denied"}`}>
          {report.ok ? `All ${report.checks.length} checks pass` : `${failed} of ${report.checks.length} checks fail`}
        </p>
        <p className="mt-2 text-[0.95rem] text-ink">
          Carried by {parties[ruling.liable] ?? ruling.liable}
          {ruling.refund ? ". The user is refunded." : "."}
        </p>
        <ul className="mt-1.5 space-y-1">
          {ruling.why.map((w, i) => (
            <li key={i} className="text-[0.86rem] leading-snug text-ink-soft">
              {w}
            </li>
          ))}
        </ul>
      </div>
      <ul>
        {report.checks.map((c, i) => (
          <li key={i} className="grid grid-cols-[3rem_1fr] gap-2 border-b border-rule px-5 py-2 text-[0.86rem] last:border-b-0">
            <span className={`mono text-[0.78rem] ${c.ok ? "text-paid" : "font-bold text-denied"}`}>{c.ok ? "ok" : "FAIL"}</span>
            <span className="text-ink">
              {c.name}
              {c.detail && <span className="block text-ink-soft">{c.detail}</span>}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
