"use client";

import { useState } from "react";
import { parties } from "@/lib/wasm";
import { repoUrl } from "@/lib/site";

// evidence.Rule as a walk: the same questions in the same order as
// evidence/liability.go. The first answer that settles it decides; the
// questions after it no longer matter.

type Q = {
  id: string;
  ask: string;
  options: { id: string; label: string }[];
  // The party it settles on for an answer, or nothing to keep going.
  settles: (a: Answers) => { liable: string; refund: boolean; why: string } | null;
};
type Answers = Record<string, string>;

const questions: Q[] = [
  {
    id: "verifies",
    ask: "Does the evidence pack verify?",
    options: [
      { id: "yes", label: "Every check passes" },
      { id: "no", label: "Something fails" },
    ],
    settles: (a) =>
      a.verifies === "no" ? { liable: "platform", refund: true, why: "The platform cannot show the payment was authorized." } : null,
  },
  {
    id: "moved",
    ask: "Did money move?",
    options: [
      { id: "yes", label: "The debit succeeded" },
      { id: "no", label: "It failed" },
    ],
    settles: (a) => (a.moved === "no" ? { liable: "none", refund: false, why: "There is nothing to give back." } : null),
  },
  {
    id: "matches",
    ask: "Rerun on the logged input, does the policy give the logged decision?",
    options: [
      { id: "yes", label: "Same decision" },
      { id: "no", label: "Different" },
    ],
    settles: (a) =>
      a.matches === "no" ? { liable: "platform", refund: true, why: "The recorded decision is not what the policy gives." } : null,
  },
  {
    id: "said",
    ask: "What did the policy say?",
    options: [
      { id: "approve", label: "Approve" },
      { id: "step_up", label: "Ask the user" },
      { id: "deny", label: "Deny" },
    ],
    settles: (a) =>
      a.said === "deny" ? { liable: "platform", refund: true, why: "The platform paid a request its own policy denied." } : null,
  },
  {
    id: "approved",
    ask: "Did the user's phone sign a yes for this exact request?",
    options: [
      { id: "yes", label: "Signed yes" },
      { id: "no", label: "No answer, or no" },
    ],
    settles: (a) =>
      a.said === "step_up" && a.approved === "no"
        ? { liable: "platform", refund: true, why: "The platform paid without the user's signed approval." }
        : null,
  },
  {
    id: "revoked",
    ask: "Had the user revoked the mandate before the decision?",
    options: [
      { id: "no", label: "No" },
      { id: "yes", label: "Yes" },
    ],
    settles: (a) =>
      a.revoked === "yes" ? { liable: "platform", refund: true, why: "The user revoked the mandate before this request was decided." } : null,
  },
  {
    id: "leaked",
    ask: "Had the developer reported the agent's key leaked before this request was signed?",
    options: [
      { id: "no", label: "No" },
      { id: "yes", label: "Yes" },
    ],
    settles: (a) =>
      a.leaked === "yes"
        ? { liable: "agent_developer", refund: true, why: "The request was signed after the developer said the key leaked." }
        : null,
  },
  {
    id: "claim",
    ask: "What is the user saying?",
    options: [
      { id: "unauthorized", label: "I did not want this" },
      { id: "not_delivered", label: "Nothing came" },
      { id: "wrong_item", label: "The wrong thing came" },
    ],
    settles: (a) =>
      a.claim === "unauthorized"
        ? {
            liable: "user",
            refund: false,
            why:
              a.said === "step_up"
                ? "The user's phone signed a yes for this exact request."
                : "The payment was inside the scope the user signed, or an errand their phone vouched for, and tripped no check.",
          }
        : { liable: "merchant", refund: true, why: "The payment was authorized; the goods are the merchant's to make right." },
  },
];

const start: Answers = { verifies: "yes", moved: "yes", matches: "yes", said: "step_up", approved: "yes", revoked: "no", leaked: "no", claim: "unauthorized" };

const cases: { label: string; a: Answers }[] = [
  { label: "The demo's dispute", a: start },
  { label: "A stolen key", a: { ...start, said: "approve", leaked: "yes" } },
  { label: "Edited evidence", a: { ...start, verifies: "no" } },
  { label: "Paid after revoking", a: { ...start, said: "approve", revoked: "yes" } },
  { label: "Groceries never came", a: { ...start, said: "approve", claim: "not_delivered" } },
];

export function Liability() {
  const [a, setA] = useState<Answers>(start);
  let ruling: ReturnType<Q["settles"]> = null;
  let decidedAt = -1;
  questions.forEach((q, i) => {
    if (ruling || (q.id === "approved" && a.said !== "step_up")) return;
    ruling = q.settles(a);
    if (ruling) decidedAt = i;
  });
  const r = ruling as ReturnType<Q["settles"]>;

  return (
    <div>
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="text-[0.8rem] text-ink-faint">Try</span>
        {cases.map((c) => (
          <button
            key={c.label}
            type="button"
            onClick={() => setA(c.a)}
            className={`border px-2.5 py-1 text-[0.84rem] ${
              JSON.stringify(c.a) === JSON.stringify(a) ? "border-ink bg-ink text-sheet" : "border-rule-strong bg-sheet text-ink-soft hover:border-ink"
            }`}
          >
            {c.label}
          </button>
        ))}
      </div>
      <div className="mt-5 grid gap-6 lg:grid-cols-[1.4fr_1fr]">
        <ol className="border border-rule bg-sheet">
          {questions.map((q, i) => {
            const skipped = q.id === "approved" && a.said !== "step_up";
            const moot = decidedAt >= 0 && i > decidedAt;
            return (
              <li
                key={q.id}
                className={`grid grid-cols-[1.8rem_1fr] gap-x-2 border-b border-rule px-4 py-3 last:border-b-0 ${
                  moot || skipped ? "opacity-35" : ""
                } ${i === decidedAt ? "bg-ground" : ""}`}
              >
                <span className="mono pt-0.5 text-[0.78rem] text-ink-faint">{i + 1}.</span>
                <div>
                  <p className="text-[0.92rem] text-ink">
                    {q.ask}
                    {skipped && <span className="text-ink-faint"> Only when it asked.</span>}
                  </p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {q.options.map((o) => (
                      <button
                        key={o.id}
                        type="button"
                        aria-pressed={a[q.id] === o.id}
                        disabled={skipped}
                        onClick={() => setA({ ...a, [q.id]: o.id })}
                        className={`border px-2 py-0.5 text-[0.82rem] ${
                          a[q.id] === o.id ? "border-ink bg-ink text-sheet" : "border-rule-strong text-ink-soft hover:border-ink"
                        }`}
                      >
                        {o.label}
                      </button>
                    ))}
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
        <div className="self-start border-2 border-ink bg-sheet lg:sticky lg:top-4">
          {r && (
            <div className="px-5 py-5" aria-live="polite">
              <p className="text-[0.8rem] text-ink-faint">Settled at question {decidedAt + 1}. Carried by</p>
              <p className="mt-1 text-[1.5rem] leading-tight font-bold text-ink">{parties[r.liable]}</p>
              <p className="mt-3 leading-relaxed text-ink-soft">{r.why}</p>
              <p className={`mt-3 text-[0.92rem] font-bold ${r.refund ? "text-paid" : "text-ink-soft"}`}>
                {r.refund ? "The user gets the money back." : r.liable === "none" ? "No refund needed." : "No refund."}
              </p>
            </div>
          )}
          <p className="border-t border-rule px-5 py-3 text-[0.78rem] leading-snug text-ink-faint">
            NPCI has not said who bears an agent&apos;s mistake. This is warrant&apos;s proposal, in{" "}
            <a href={`${repoUrl}/blob/main/evidence/liability.go`} className="link">
              evidence/liability.go
            </a>
            , with a test for every branch.
          </p>
        </div>
      </div>
    </div>
  );
}
