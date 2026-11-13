"use client";
import { useEffect } from "react";
import { THEME_STORAGE_KEY, applyTheme, currentPreference, readStoredPreference } from "../lib/theme";

/**
 * Keeps the applied theme in step with the outside world once the page is live:
 * a "System" choice follows OS light/dark changes, and a change made in another
 * tab (storage event) is adopted here too. Renders nothing.
 */
export function ThemeSync() {
  useEffect(() => {
    // Covers a head script that could not run (or storage that changed before hydration).
    // Always re-resolve: an OS change between the head script and this effect would otherwise be missed.
    applyTheme(readStoredPreference());
    let query: MediaQueryList | null = null;
    try { query = typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)") : null; } catch { query = null; }
    const onSystemChange = () => { if (currentPreference() === "system") applyTheme("system"); };
    const onStorage = (event: StorageEvent) => { if (event.key === THEME_STORAGE_KEY || event.key === null) applyTheme(readStoredPreference()); };
    query?.addEventListener?.("change", onSystemChange);
    window.addEventListener("storage", onStorage);
    return () => { query?.removeEventListener?.("change", onSystemChange); window.removeEventListener("storage", onStorage); };
  }, []);
  return null;
}
