import { readFileSync } from "node:fs";
import { join } from "node:path";

// Evaluated once at build time (force-static): the worker's bytes carry this build's id.
export const dynamic = "force-static";

export function GET() {
  const source = readFileSync(join(process.cwd(), "pwa", "sw.js"), "utf8")
    .replace('"__BUILD_ID__"', JSON.stringify(process.env.NEXT_PUBLIC_BUILD_ID ?? "dev"));
  return new Response(source, {
    headers: {
      "Content-Type": "application/javascript; charset=utf-8",
      "Cache-Control": "no-cache, no-store, must-revalidate",
      "Content-Security-Policy": "default-src 'self'; script-src 'self'",
      "Service-Worker-Allowed": "/",
    },
  });
}
