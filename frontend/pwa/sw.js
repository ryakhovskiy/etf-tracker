/*
 * Holdings service worker (plain JS, scope "/"). Source file: it is served at /sw.js by app/sw.js/route.ts,
 * which replaces __BUILD_ID__ at build time, so every build ships different bytes and an open (old) tab's
 * registration.update() detects a redeploy. It exists for one reason: when the network is gone,
 * show the branded offline page instead of the browser's error page.
 *
 * What is stored on the device (Cache Storage), and nothing else:
 *   holdings-shell-<v>   the precache below: offline page, its CSS/JS/fonts, icons, manifest
 *   holdings-static-<v>  /_next/static/* (immutable, hashed build files), cache-first
 * Never stored, never intercepted: /api/*, /auth/* (OAuth redirects), non-GET and cross-origin requests,
 * RSC/data fetches, other pages' HTML, and any response that is not a plain 200.
 * <v> is the build id embedded in this file.
 */
"use strict";

const VERSION = "__BUILD_ID__";
const SHELL_CACHE = "holdings-shell-" + VERSION;
const STATIC_CACHE = "holdings-static-" + VERSION;
const OFFLINE_URL = "/offline.html";
const PRECACHE = [
  OFFLINE_URL,
  "/offline.css",
  "/offline.js",
  "/fonts/Inter-latin-variable.woff2",
  "/fonts/JetBrainsMono-latin-variable.woff2",
  "/icons/icon-192.png",
  "/icons/icon-512.png",
  "/icons/icon-maskable-512.png",
  "/icons/apple-touch-icon.png",
  "/favicon.svg",
  "/favicon-32.png",
  "/manifest.webmanifest",
];
const PRECACHED = new Set(PRECACHE);
const NEVER_HANDLED = /^\/(api|auth)(\/|$)/;

self.addEventListener("install", (event) => {
  // No skipWaiting here: an update waits until the page asks for it (see the message handler).
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) =>
      Promise.all(PRECACHE.map((url) =>
        fetch(new Request(url, { cache: "reload" })).then((response) => {
          if (!response.ok) throw new Error("Precache failed: " + url + " " + response.status);
          return cache.put(url, response);
        })
      ))
    )
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys()
      .then((names) => Promise.all(names
        .filter((name) => name.startsWith("holdings-") && name !== SHELL_CACHE && name !== STATIC_CACHE)
        .map((name) => caches.delete(name))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("message", (event) => {
  // Only the page can promote a waiting worker (its "Reload" button); nothing reloads by itself.
  if (event.data && event.data.type === "SKIP_WAITING") self.skipWaiting();
});

const isPlainOk = (response) => response && response.status === 200 && response.type === "basic";

async function cacheFirst(request, cacheName) {
  const cache = await caches.open(cacheName);
  const hit = await cache.match(request);
  if (hit) return hit;
  const response = await fetch(request);
  if (isPlainOk(response)) await cache.put(request, response.clone());
  return response;
}

async function fromShell(request) {
  const cache = await caches.open(SHELL_CACHE);
  return (await cache.match(new URL(request.url).pathname)) || fetch(request);
}

async function navigate(request) {
  try {
    // Network first: an online navigation is never answered from a cache and its HTML is never stored.
    return await fetch(request);
  } catch (error) {
    const cache = await caches.open(SHELL_CACHE);
    return (await cache.match(OFFLINE_URL)) || Response.error();
  }
}

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (NEVER_HANDLED.test(url.pathname)) return;
  if (url.pathname === "/sw.js") return;
  // Next.js App Router data requests (RSC payloads and prefetches) go straight to the network.
  if (request.headers.has("RSC") || request.headers.has("Next-Router-Prefetch") || url.searchParams.has("_rsc")) return;

  if (PRECACHED.has(url.pathname)) { event.respondWith(fromShell(request)); return; }
  if (url.pathname.startsWith("/_next/static/")) { event.respondWith(cacheFirst(request, STATIC_CACHE)); return; }
  if (request.mode === "navigate") { event.respondWith(navigate(request)); return; }
});
