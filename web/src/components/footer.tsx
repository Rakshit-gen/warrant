import { repoUrl } from "@/lib/site";

export function Footer() {
  return (
    <footer className="mt-24 border-t border-rule">
      <div className="mx-auto flex max-w-6xl flex-wrap gap-x-8 gap-y-2 px-5 py-8 text-[0.86rem] text-ink-faint">
        <span>warrant, MIT licensed</span>
        <span>A personal project. Not affiliated with Razorpay, NPCI or any bank.</span>
        <a href={repoUrl} className="hover:text-ink">
          Source
        </a>
      </div>
    </footer>
  );
}
