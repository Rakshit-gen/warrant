// The Go side of the site: evidence.Verify, evidence.Rule and
// policy.Evaluate, compiled to WebAssembly (cmd/verify-wasm). Loaded once,
// on first use, and shared by every component that needs it.

import type { Reason } from "./data";

export type Decision = { outcome: "approve" | "step_up" | "deny"; reasons: Reason[] };
export type Decided = { caps_only: Decision; warrant: Decision; scope_error?: string; error?: string };

export type Check = { name: string; ok: boolean; detail?: string };
export type Ruling = { claim: string; liable: string; refund: boolean; verified: boolean; why: string[] };
export type Verified = { report?: { ok: boolean; checks: Check[] }; ruling?: Ruling; error?: string };

declare global {
  interface Window {
    Go: new () => { importObject: WebAssembly.Imports; run(i: WebAssembly.Instance): Promise<void> };
    warrantVerify?: (pack: string, claim: string) => string;
    warrantDecide?: (input: string) => string;
  }
}

let loading: Promise<void> | null = null;

export function loadWarrant(): Promise<void> {
  if (typeof window !== "undefined" && window.warrantDecide) return Promise.resolve();
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

export function decide(input: unknown): Decided {
  return JSON.parse(window.warrantDecide!(JSON.stringify(input)));
}

export function verify(pack: string, claim: string): Verified {
  return JSON.parse(window.warrantVerify!(pack, claim));
}

export const parties: Record<string, string> = {
  platform: "the platform that ran the authorization",
  user: "the user",
  merchant: "the merchant, through its refund process",
  agent_developer: "the agent's developer",
  none: "nobody; no money moved",
};
