import type { Metadata } from "next";
import { Verifier } from "./verifier";

export const metadata: Metadata = { title: "Verify an evidence pack" };

export default function VerifyPage() {
  return (
    <div className="mx-auto max-w-6xl px-5 pt-14">
      <h1 className="text-[2.2rem] leading-tight font-bold tracking-tight">Verify an evidence pack</h1>
      <div className="prose-w mt-4 max-w-3xl">
        <p>
          A pack is every log record behind one payment, with a Merkle proof for each against a checkpoint
          warrant signed. This page checks it with the same Go code as <code>warrant verify</code>, compiled to
          WebAssembly and run in your browser. Nothing is uploaded. It checks the signatures, the proofs, that the
          records agree with each other, and reruns the policy on the recorded input to see the decision was the
          one the policy gives. Then it applies the liability rule to the claim you pick.
        </p>
        <p>
          The sample is the disputed order from the demo. Change a character in it and watch which checks fail.
        </p>
      </div>
      <Verifier />
    </div>
  );
}
