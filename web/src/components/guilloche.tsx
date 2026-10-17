"use client";

import { useEffect, useRef } from "react";

// The fine crossing lines printed on cheques and stamp paper, drawn by a
// shader and drifting slowly. Two families of wavy lines cross to make the
// moire. It caps the pixel ratio, draws at most 30 frames a second, stops
// when off screen or in a hidden tab, and draws one still frame when the
// user asks for less motion. Without WebGL2 the plain ground shows.

const vert = `#version 300 es
in vec2 p;
void main() { gl_Position = vec4(p, 0.0, 1.0); }`;

const frag = `#version 300 es
precision mediump float;
uniform vec2 res;
uniform float t;
out vec4 o;

float lines(float phase, float w) {
  float d = abs(fract(phase) - 0.5);
  return 1.0 - smoothstep(0.0, fwidth(phase) * w, d);
}

void main() {
  vec2 p = gl_FragCoord.xy / res.y;
  float x = p.x, y = p.y;

  float a = (y + 0.06 * sin(x * 5.0 + t * 0.35) + 0.03 * sin(x * 13.0 - t * 0.5)) * 34.0;
  float b = (y - 0.07 * sin(x * 4.0 - t * 0.28 + 1.3) - 0.025 * sin(x * 11.0 + t * 0.4)) * 34.0;
  float r = length(p - vec2(res.x / res.y * 0.78, 0.55));
  float c = (r + 0.012 * sin(atan(p.y - 0.55, p.x - res.x / res.y * 0.78) * 24.0 + t * 0.3)) * 60.0;

  vec3 ground = vec3(0.929, 0.937, 0.910);
  vec3 rule = vec3(0.663, 0.694, 0.643);
  vec3 green = vec3(0.176, 0.416, 0.294);

  float ink = max(lines(a, 1.0), lines(b, 1.0)) * 0.55;
  float rose = lines(c, 1.0) * smoothstep(0.42, 0.1, r) * 0.5;

  vec3 col = mix(ground, rule, ink);
  col = mix(col, green, rose * 0.45);

  // Fade toward the left, where the headline sits, and toward the bottom.
  float fade = smoothstep(0.05, 0.75, gl_FragCoord.x / res.x) * smoothstep(0.0, 0.35, gl_FragCoord.y / res.y);
  o = vec4(mix(ground, col, 0.35 + 0.65 * fade), 1.0);
}`;

export function Guilloche({ className = "" }: { className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const cv = ref.current;
    const gl = cv?.getContext("webgl2", { antialias: false, alpha: false, powerPreference: "low-power" });
    if (!cv || !gl) return;

    const sh = (type: number, src: string) => {
      const s = gl.createShader(type)!;
      gl.shaderSource(s, src);
      gl.compileShader(s);
      return s;
    };
    const prog = gl.createProgram()!;
    gl.attachShader(prog, sh(gl.VERTEX_SHADER, vert));
    gl.attachShader(prog, sh(gl.FRAGMENT_SHADER, frag));
    gl.linkProgram(prog);
    if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) return;
    gl.useProgram(prog);

    gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
    const loc = gl.getAttribLocation(prog, "p");
    gl.enableVertexAttribArray(loc);
    gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);
    const uRes = gl.getUniformLocation(prog, "res");
    const uT = gl.getUniformLocation(prog, "t");

    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let raf = 0;
    let last = 0;
    let visible = true;
    const t0 = performance.now();

    const size = () => {
      const dpr = Math.min(devicePixelRatio || 1, 1.5);
      cv.width = Math.round(cv.clientWidth * dpr);
      cv.height = Math.round(cv.clientHeight * dpr);
      gl.viewport(0, 0, cv.width, cv.height);
      gl.uniform2f(uRes, cv.width, cv.height);
    };
    const draw = (now: number) => {
      gl.uniform1f(uT, still ? 0 : (now - t0) / 1000);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    };
    const loop = (now: number) => {
      raf = requestAnimationFrame(loop);
      if (now - last < 33) return;
      last = now;
      draw(now);
    };
    const start = () => {
      cancelAnimationFrame(raf);
      if (still) draw(t0);
      else if (visible && !document.hidden) raf = requestAnimationFrame(loop);
    };

    size();
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

  return <canvas ref={ref} aria-hidden className={className} />;
}
