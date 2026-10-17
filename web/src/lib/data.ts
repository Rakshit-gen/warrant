import evalJson from "@/data/eval.json";
import benchJson from "@/data/bench.json";
import storyJson from "@/data/story.json";

export type Reason = { code: string; hard: boolean; detail: string };

export type StoryStep = {
  text: string;
  outcome?: "approve" | "step_up" | "deny";
  reasons?: Reason[];
  debit?: { id: string; status: string; amount: number; attempts: number };
  notes?: string[];
};

export type Tally = {
  family: string;
  kind: "benign" | "attack" | "evasion";
  what: string;
  wanted: number;
  wanted_paid: number;
  wanted_asked: number;
  wanted_refused: number;
  unwanted: number;
  unwanted_paid: number;
  unwanted_asked: number;
  unwanted_stopped: number;
  money_tried: number;
  money_lost: number;
  reasons: Record<string, number>;
};

export type Totals = Record<"benign" | "attack" | "evasion", Record<string, number>>;

export type EvalResult = {
  when: string;
  seed: number;
  per_family: number;
  note: string;
  modes: { name: "caps_only" | "warrant"; families: Tally[]; totals: Totals; took_ms: number }[];
};

export type BenchResult = {
  when: string;
  machine: string;
  postgres: string;
  load: {
    mandates: number;
    workers: number;
    requests: number;
    seconds: number;
    per_second: number;
    by_outcome: Record<string, number>;
    latency_ms: Record<string, number>;
    approve_latency_ms: Record<string, number>;
    bank_latency: string;
  };
  books: {
    mandates: number;
    requests: number;
    faults: string;
    approved: number;
    approved_want: number;
    left_unknown: number;
    resent: number;
    bank_debits: number;
    warrant_debits: number;
    mismatched_blocks: number;
    overspent_blocks: number;
    settle_seconds: number;
  };
};

export const evalResult = evalJson as unknown as EvalResult | null;
export const benchResult = benchJson as unknown as BenchResult | null;
export const story = storyJson as unknown as StoryStep[];

export function mode(name: "caps_only" | "warrant") {
  return evalResult?.modes.find((m) => m.name === name) ?? null;
}

// Indian grouping, as warrant prints it: ₹1,00,000.
export function rupees(paise: number): string {
  const r = Math.round(paise / 100);
  const s = String(Math.abs(r));
  const head = s.slice(0, -3);
  const tail = s.slice(-3);
  const grouped = head ? head.replace(/\B(?=(\d{2})+(?!\d))/g, ",") + "," + tail : tail;
  return (r < 0 ? "-₹" : "₹") + grouped;
}

export function pct(n: number, d: number): string {
  if (!d) return "0%";
  const v = (100 * n) / d;
  return (v < 10 && v > 0 ? v.toFixed(1) : Math.round(v).toString()) + "%";
}

export const familyLabel = (f: string) => f.replaceAll("_", " ");
