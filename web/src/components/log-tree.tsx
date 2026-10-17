"use client";

import { useEffect, useRef } from "react";

// warrant's log, growing behind the hero. Each payment adds its records
// as leaves (request, decision, the user's answer if asked, the debit),
// pairs hash up into the Merkle tree, and every 16 leaves get a signed
// checkpoint. Decorative: the outcomes are random, not data.
//
// Canvas 2D at most 30 frames a second, paused off screen or in a hidden
// tab, and one still frame when the user asks for less motion.

const SP = 22; // px between leaves
const GAP = 26; // px between tree levels
const LEVELS = 5;
const EVERY = 650; // ms per new leaf
const CHECK = 16;

type Leaf = { kind: string; tone: string; born: number };

function* records(): Generator<{ kind: string; tone: string }> {
  for (;;) {
    yield { kind: "request", tone: "faint" };
    const r = Math.random();
    const outcome = r < 0.68 ? "paid" : r < 0.88 ? "asked" : "denied";
    yield { kind: "decision", tone: outcome };
    if (outcome === "asked") {
      const yes = Math.random() < 0.6;
      yield { kind: "answer", tone: yes ? "paid" : "denied" };
      if (yes) yield { kind: "debit", tone: "paid" };
    } else if (outcome === "paid") yield { kind: "debit", tone: "paid" };
  }
}

export function LogTree({ className = "" }: { className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = ref.current;
    const ctx = cv?.getContext("2d");
    if (!cv || !ctx) return;

    const css = getComputedStyle(document.documentElement);
    const c = (v: string) => css.getPropertyValue(v).trim();
    const tones: Record<string, string> = { faint: c("--ink-faint"), paid: c("--paid"), asked: c("--asked"), denied: c("--denied") };
    const rule = c("--rule-strong");
    const ink = c("--ink-soft");
    const sheet = c("--sheet");
    const mono = `10px ${css.getPropertyValue("--font-mono") || "ui-monospace"}, ui-monospace, monospace`;

    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    const gen = records();
    const leaves: Leaf[] = [];
    const add = (born: number) => {
      const r = gen.next().value!;
      leaves.push({ ...r, born });
    };
    for (let i = 0; i < 70; i++) add(-1e9);

    let W = 0;
    let H = 0;
    let offset = 0;
    let dropped = 0;
    let raf = 0;
    let last = 0;
    let lastLeaf = performance.now();
    let visible = true;

    const size = () => {
      const dpr = Math.min(devicePixelRatio || 1, 2);
      W = cv.clientWidth;
      H = cv.clientHeight;
      cv.width = Math.round(W * dpr);
      cv.height = Math.round(H * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    // Newest leaf sits near the right edge.
    const target = () => (leaves.length - 1) * SP - W * 0.9;

    const draw = (now: number) => {
      const n = leaves.length;
      ctx.clearRect(0, 0, W, H);
      const base = H - 56;
      const xOf = (l: number, j: number) => (j * 2 ** l + (2 ** l - 1) / 2) * SP - offset;
      const fade = (born: number) => Math.min(1, (now - born) / 400);
      const first = Math.max(0, Math.floor(offset / SP) - 2);

      ctx.lineWidth = 1;
      for (let l = 1; l <= LEVELS; l++) {
        const size = 2 ** l;
        for (let j = Math.floor(first / size); (j + 1) * size <= n; j++) {
          const x = xOf(l, j);
          if (x < -SP * size || x > W + SP) continue;
          const y = base - l * GAP;
          const a = fade(leaves[(j + 1) * size - 1].born);
          ctx.globalAlpha = a;
          ctx.strokeStyle = rule;
          ctx.beginPath();
          for (const k of [2 * j, 2 * j + 1]) {
            ctx.moveTo(x, y);
            ctx.lineTo(xOf(l - 1, k), y + GAP);
          }
          ctx.stroke();
          ctx.fillStyle = sheet;
          ctx.fillRect(x - 3, y - 3, 6, 6);
          ctx.strokeRect(x - 3.5, y - 3.5, 7, 7);
        }
      }

      // Signed checkpoints over every full 16.
      ctx.font = mono;
      for (let j = Math.floor(first / CHECK); (j + 1) * CHECK <= n; j++) {
        const x = xOf(4, j);
        if (x < -200 || x > W + 50) continue;
        const y = base - 4 * GAP;
        ctx.globalAlpha = fade(leaves[(j + 1) * CHECK - 1].born);
        ctx.strokeStyle = ink;
        ctx.strokeRect(x - 4.5, y - 4.5, 9, 9);
        ctx.fillStyle = ink;
        ctx.fillText(`checkpoint ${dropped + (j + 1) * CHECK}, signed`, x + 9, y - 6);
      }

      // Leaves, labelled with the kind of record.
      for (let i = first; i < n; i++) {
        const x = i * SP - offset;
        if (x > W + SP) break;
        const L = leaves[i];
        ctx.globalAlpha = fade(L.born);
        ctx.fillStyle = tones[L.tone];
        ctx.fillRect(x - 4, base - 4, 8, 8);
        ctx.save();
        ctx.translate(x + 3, base + 10);
        ctx.rotate(Math.PI / 2.6);
        ctx.fillStyle = ink;
        ctx.fillText(L.kind, 0, 0);
        ctx.restore();
      }
      ctx.globalAlpha = 1;
    };

    const tick = (now: number) => {
      raf = requestAnimationFrame(tick);
      if (now - last < 33) return;
      last = now;
      if (now - lastLeaf > EVERY) {
        lastLeaf = now;
        add(now);
        if (leaves.length > 400) {
          leaves.splice(0, 256);
          offset -= 256 * SP;
          dropped += 256;
        }
      }
      offset += (target() - offset) * 0.12;
      draw(now);
    };
    const start = () => {
      cancelAnimationFrame(raf);
      if (still) draw(performance.now());
      else if (visible && !document.hidden) {
        lastLeaf = performance.now();
        raf = requestAnimationFrame(tick);
      }
    };

    size();
    offset = target();
    start();
    const ro = new ResizeObserver(() => {
      size();
      draw(performance.now());
    });
    ro.observe(cv);
    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting;
      if (visible) start();
      else cancelAnimationFrame(raf);
    });
    io.observe(cv);
    const onVis = () => (document.hidden ? cancelAnimationFrame(raf) : start());
    document.addEventListener("visibilitychange", onVis);

    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
    };
  }, []);

  return (
    <canvas
      ref={ref}
      aria-hidden
      className={className}
      style={{ maskImage: "linear-gradient(to right, transparent 0%, black 40%)" }}
    />
  );
}
