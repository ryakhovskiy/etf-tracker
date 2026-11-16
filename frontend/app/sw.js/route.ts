import { readFileSync } from "node:fs";
import { join } from "node:path";

// Why this is a route and not a static file in public/: a service worker is only updated when the bytes at
// its URL change. A static sw.js would be byte-identical across deploys, so an already-open tab (whose
// registration.update() fetches /sw.js) would never notice a redeploy. Here the build id is embedded into
// the served script, at the stable URL /sw.js, so every build is a different worker.
// The source is pwa/sw.js (plain JS). The route is evaluated once at build time (force-static); the headers
// below are part of the stored response, and next.config.mjs repeats them for /sw.js, so they come out the
// same under `next start` and in the standalone Docker image.
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
