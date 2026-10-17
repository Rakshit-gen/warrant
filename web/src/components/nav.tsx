import Link from "next/link";
import { repoUrl } from "@/lib/site";

const links = [
  { href: "/results", label: "Results" },
  { href: "/verify", label: "Verify a pack" },
  { href: "/docs", label: "Docs" },
];

export function Nav() {
  return (
    <header className="border-b border-rule">
      <nav className="mx-auto flex h-14 max-w-6xl items-center gap-7 px-5">
        <Link href="/" className="flex items-center gap-2 text-[1.02rem] font-bold tracking-tight text-ink">
          <Seal />
          warrant
        </Link>
        <div className="flex gap-5 text-[0.93rem] text-ink-soft">
          {links.map((l) => (
            <Link key={l.href} href={l.href} className="hover:text-ink">
              {l.label}
            </Link>
          ))}
        </div>
        <a href={repoUrl} className="ml-auto hidden text-[0.93rem] text-ink-soft hover:text-ink sm:block">
          GitHub
        </a>
      </nav>
    </header>
  );
}

// A seal: a ring with a notch cut where the signature goes.
export function Seal({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden>
      <circle cx="8" cy="8" r="6.2" fill="none" stroke="var(--denied)" strokeWidth="2.2" />
      <rect x="6.6" y="0" width="2.8" height="5" fill="var(--ground)" />
      <circle cx="8" cy="8" r="2" fill="var(--ink)" />
    </svg>
  );
}
