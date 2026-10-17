"use client";

import { useState } from "react";

export function CopyCommand({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-stretch border border-rule-strong bg-sheet">
      <code className="mono min-w-0 flex-1 overflow-x-auto whitespace-nowrap px-4 py-3 text-[0.84rem] text-ink">{cmd}</code>
      <button
        type="button"
        onClick={() => {
          navigator.clipboard.writeText(cmd).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
        className="border-l border-rule-strong px-4 text-[0.84rem] text-ink-soft hover:bg-ground hover:text-ink"
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}
