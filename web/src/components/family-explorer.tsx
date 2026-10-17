"use client";

import { useState } from "react";
import Link from "next/link";
import { familyLabel, pct, rupees, type Tally } from "@/lib/data";
import { presets } from "@/lib/playground";
import { reasonText } from "@/lib/reasons";

const kinds: { id: Tally["kind"]; label: string }[] = [
  { id: "attack", label: "Attacks" },
  { id: "evasion", label: "Built to get past it" },
  { id: "benign", label: "Ordinary use" },
];

export function FamilyExplorer({ caps, full }: { caps: Tally[]; full: Tally[] }) {
  const [kind, setKind] = useState<Tally["kind"]>("attack");
  const list = full.filter((f) => f.kind === kind);
  const [pick, setPick] = useState<string | undefined>(list[0]?.family);
  const f = full.find((x) => x.family === pick && x.kind === kind) ?? list[0];
  const c = caps.find((x) => x.family === f?.family);
  if (!f || !c) return null;
  const tryIt = presets.some((p) => p.id === f.family);

  return (
    <div className="border border-rule bg-sheet">
      <div role="tablist" className="flex flex-wrap border-b border-rule">
        {kinds.map((k) => (
          <button
            key={k.id}
            role="tab"
            aria-selected={kind === k.id}
            onClick={() => {
              setKind(k.id);
              setPick(full.find((x) => x.kind === k.id)?.family);
            }}
            className={`border-r border-rule px-4 py-2.5 text-[0.9rem] ${kind === k.id ? "bg-ink font-bold text-sheet" : "text-ink-soft hover:bg-ground"}`}
          >
            {k.label}
          </button>
        ))}
      </div>
      <div className="grid lg:grid-cols-[17rem_1fr]">
        <ul className="border-b border-rule lg:border-r lg:border-b-0">
          {list.map((x) => {
            const bad = kind === "benign" ? x.wanted_asked + x.wanted_refused : x.unwanted_paid;
            const of = kind === "benign" ? x.wanted : x.unwanted;
            return (
              <li key={x.family}>
                <button
                  type="button"
                  onClick={() => setPick(x.family)}
                  className={`flex w-full items-baseline justify-between gap-3 border-b border-rule px-4 py-2 text-left text-[0.9rem] last:border-b-0 ${
                    x.family === f.family ? "bg-ground font-bold text-ink" : "text-ink-soft hover:bg-ground"
                  }`}
                >
                  <span>{familyLabel(x.family)}</span>
                  <span className={`mono shrink-0 text-[0.76rem] ${bad ? (kind === "benign" ? "text-asked" : "text-denied") : "text-paid"}`}>
                    {pct(bad, of)}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
        <div className="px-5 py-5">
          <p className="text-[1.25rem] font-bold">{familyLabel(f.family)}</p>
          <p className="mt-1 text-ink-soft">{f.what}</p>
          {kind === "benign" ? (
            <div className="mt-5 space-y-3">
              <Split
                label="mandate caps alone"
                parts={[
                  { n: c.wanted_paid, tone: "bg-paid", name: "paid" },
                  { n: c.wanted_asked, tone: "bg-asked", name: "asked" },
                  { n: c.wanted_refused, tone: "bg-denied", name: "refused" },
                ]}
              />
              <Split
                label="warrant"
                parts={[
                  { n: f.wanted_paid, tone: "bg-paid", name: "paid" },
                  { n: f.wanted_asked, tone: "bg-asked", name: "asked" },
                  { n: f.wanted_refused, tone: "bg-denied", name: "refused" },
                ]}
              />
              <p className="text-[0.86rem] text-ink-faint">
                Payments the user wanted. Every question is friction and every refusal is a mistake.
              </p>
            </div>
          ) : (
            <div className="mt-5 space-y-3">
              <Split
                label="mandate caps alone"
                parts={[
                  { n: c.unwanted_paid, tone: "bg-denied", name: "paid" },
                  { n: c.unwanted_asked, tone: "bg-asked", name: "asked" },
                  { n: c.unwanted_stopped, tone: "bg-ink", name: "stopped" },
                ]}
              />
              <Split
                label="warrant"
                parts={[
                  { n: f.unwanted_paid, tone: "bg-denied", name: "paid" },
                  { n: f.unwanted_asked, tone: "bg-asked", name: "asked, user declined" },
                  { n: f.unwanted_stopped, tone: "bg-ink", name: "stopped" },
                ]}
              />
              <div className="grid grid-cols-2 gap-4 pt-2 sm:grid-cols-3">
                <Fig label="money tried" v={rupees(f.money_tried)} />
                <Fig label="lost, caps alone" v={rupees(c.money_lost)} />
                <Fig label="lost, warrant" v={rupees(f.money_lost)} />
              </div>
            </div>
          )}
          {Object.keys(f.reasons).length > 0 && (
            <div className="mt-6">
              <p className="text-[0.8rem] text-ink-faint">What warrant said about these payments</p>
              <ul className="mt-1.5 space-y-1">
                {Object.entries(f.reasons)
                  .sort((x, y) => y[1] - x[1])
                  .map(([code, n]) => (
                    <li key={code} className="grid grid-cols-[3rem_1fr] gap-2 text-[0.88rem]">
                      <span className="num text-right text-ink">{n}×</span>
                      <span className="text-ink-soft">
                        {reasonText[code] ?? code} <span className="mono text-[0.74rem] text-ink-faint">{code}</span>
                      </span>
                    </li>
                  ))}
              </ul>
            </div>
          )}
          {tryIt && (
            <Link href={`/#try-${f.family}`} className="mt-6 inline-block bg-ink px-3.5 py-2 text-[0.88rem] font-bold text-sheet hover:bg-ink-soft">
              Try one in the playground
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}

function Split({ label, parts }: { label: string; parts: { n: number; tone: string; name: string }[] }) {
  const total = parts.reduce((n, p) => n + p.n, 0);
  return (
    <div>
      <div className="flex items-baseline justify-between text-[0.82rem]">
        <span className="text-ink-soft">{label}</span>
        <span className="text-ink-faint">
          {parts
            .filter((p) => p.n)
            .map((p) => `${p.n} ${p.name}`)
            .join(" · ")}
        </span>
      </div>
      <div className="mt-1 flex h-3 bg-ground">
        {parts.map((p) =>
          p.n ? (
            <span key={p.name} className={`${p.tone} h-full transition-[width] duration-300`} style={{ width: `${(100 * p.n) / (total || 1)}%` }} />
          ) : null,
        )}
      </div>
    </div>
  );
}

function Fig({ label, v }: { label: string; v: string }) {
  return (
    <div>
      <p className="num text-[1.1rem] font-bold text-ink">{v}</p>
      <p className="text-[0.76rem] text-ink-faint">{label}</p>
    </div>
  );
}
