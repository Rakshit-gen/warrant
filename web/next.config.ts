import type { NextConfig } from "next";

// A static export: pages, the result files the Go side wrote, and the
// verifier compiled to WebAssembly. It can be hosted anywhere.
const nextConfig: NextConfig = {
  output: "export",
  images: { unoptimized: true },
};

export default nextConfig;
