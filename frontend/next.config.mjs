// Every build is a new service worker: the id is embedded in the served /sw.js (app/sw.js/route.ts).
const buildId = process.env.BUILD_ID ?? Date.now().toString(36);

/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  output: "standalone",
  env: { NEXT_PUBLIC_BUILD_ID: buildId },
  async headers() {
    return [
      {
        // The worker script must never be cached by the browser, or updates would not be seen.
        source: "/sw.js",
        headers: [
          { key: "Content-Type", value: "application/javascript; charset=utf-8" },
          { key: "Cache-Control", value: "no-cache, no-store, must-revalidate" },
          { key: "Content-Security-Policy", value: "default-src 'self'; script-src 'self'" },
          { key: "Service-Worker-Allowed", value: "/" },
        ],
      },
    ];
  },
  async rewrites() {
    // Docker builds use a same-origin API; local development keeps its direct API URL.
    const backend = process.env.API_PROXY_URL;
    if (!backend) return [];
    return [
      { source: "/api/:path*", destination: `${backend}/api/:path*` },
      { source: "/auth/:path*", destination: `${backend}/auth/:path*` },
    ];
  },
};
export default nextConfig;
