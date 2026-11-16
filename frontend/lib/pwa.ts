/**
 * Service worker registration and the update flow (browser only).
 * - Registers /sw.js in production builds only, so `next dev` never ends up with a stale worker.
 * - An update (a new worker waiting while an old one controls the page) raises `updateReady`; nothing
 *   reloads by itself. `applyUpdate()` (the "Reload" button) asks the waiting worker to activate and the
 *   page reloads once, when the new worker takes over. A first install shows no notice.
 * - Checks for updates on load, when the tab becomes visible again, and hourly.
 */
// The worker script is served at a stable URL; its bytes change with every build (app/sw.js/route.ts).
export const SW_URL = "/sw.js";
const HOUR = 60 * 60 * 1000;

export type PwaState = { updateReady: boolean; updateDismissed: boolean };
let state: PwaState = { updateReady: false, updateDismissed: false };
const listeners = new Set<() => void>();
const publish = (next: Partial<PwaState>) => { state = { ...state, ...next }; listeners.forEach(listener => listener()); };

export const getPwaState = () => state;
export const subscribePwa = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };

let waiting: ServiceWorker | null = null;
let reloadRequested = false;
let started = false;

export function applyUpdate() {
  if (!waiting) return;
  reloadRequested = true;
  waiting.postMessage({ type: "SKIP_WAITING" });
}
export const dismissUpdate = () => publish({ updateDismissed: true });

export function initPwa() {
  if (started || typeof window === "undefined") return;
  started = true;
  if (process.env.NODE_ENV !== "production" || !("serviceWorker" in navigator)) return;
  const container = navigator.serviceWorker;
  // Reload exactly once, and only after the user chose to update (the first install also changes the controller).
  let reloaded = false;
  container.addEventListener("controllerchange", () => {
    if (!reloadRequested || reloaded) return;
    reloaded = true;
    window.location.reload();
  });
  container.register(SW_URL, { scope: "/", updateViaCache: "none" }).then(registration => {
    const offer = (worker: ServiceWorker | null) => {
      // No controller yet means this is the very first install: nothing to update from.
      if (worker && container.controller) { waiting = worker; publish({ updateReady: true, updateDismissed: false }); }
    };
    offer(registration.waiting);
    registration.addEventListener("updatefound", () => {
      const installing = registration.installing;
      installing?.addEventListener("statechange", () => { if (installing.state === "installed") offer(installing); });
    });
    const check = () => { registration.update().catch(() => { /* offline or server down: try again later */ }); };
    document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") check(); });
    window.setInterval(check, HOUR);
  }).catch(() => { /* registration is an enhancement; the app works without it */ });
}
