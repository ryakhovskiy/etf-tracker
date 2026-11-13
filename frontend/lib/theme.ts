/**
 * Theme engine (browser only, per-browser persistence in localStorage).
 *
 * html[data-theme]      the RESOLVED theme: light | dark | tech-green | tech-cappuccino | tech-red
 * html[data-theme-pref] what the user picked: the same names, or "system"
 * "system" resolves to light or dark from prefers-color-scheme and follows live OS changes.
 *
 * The colour values live in app/styles/tokens.css; nothing here knows a colour.
 */
export const THEME_STORAGE_KEY = "holdings.theme";
export const THEME_PREFERENCES = ["system", "light", "dark", "tech-green", "tech-cappuccino", "tech-red"] as const;
export type ThemePreference = (typeof THEME_PREFERENCES)[number];
export type ResolvedTheme = Exclude<ThemePreference, "system">;

export const THEME_LABELS: Record<ThemePreference, string> = {
  system: "System",
  light: "Light",
  dark: "Dark",
  "tech-green": "Tech Green",
  "tech-cappuccino": "Tech Cappuccino",
  "tech-red": "Tech Red",
};

export const isThemePreference = (value: unknown): value is ThemePreference =>
  typeof value === "string" && (THEME_PREFERENCES as readonly string[]).includes(value);

export const resolveTheme = (preference: ThemePreference, prefersDark: boolean): ResolvedTheme =>
  preference === "system" ? (prefersDark ? "dark" : "light") : preference;

const DARK_QUERY = "(prefers-color-scheme: dark)";
const systemPrefersDark = () => {
  try { return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia(DARK_QUERY).matches; }
  catch { return false; }
};

/** The stored preference; "system" when nothing (valid) is stored or storage throws. */
export function readStoredPreference(): ThemePreference {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    return isThemePreference(stored) ? stored : "system";
  } catch { return "system"; }
}

/** The preference currently applied to the document (set by the head script before paint). */
export function currentPreference(): ThemePreference {
  if (typeof document === "undefined") return "system";
  const value = document.documentElement.getAttribute("data-theme-pref");
  return isThemePreference(value) ? value : "system";
}

/** Writes the resolved theme and the preference to <html>. Does not touch storage. */
export function applyTheme(preference: ThemePreference) {
  const root = document.documentElement;
  root.setAttribute("data-theme", resolveTheme(preference, systemPrefersDark()));
  root.setAttribute("data-theme-pref", preference);
}

/** Apply and persist. If storage throws, the choice still holds for this page view. */
export function setThemePreference(preference: ThemePreference) {
  applyTheme(preference);
  try { window.localStorage.setItem(THEME_STORAGE_KEY, preference); } catch { /* storage unavailable: keep the in-memory choice */ }
}

/** Calls back when the applied preference or resolved theme changes. For useSyncExternalStore. */
export function subscribeTheme(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme", "data-theme-pref"] });
  return () => observer.disconnect();
}

/**
 * Inline script for <head>: sets data-theme before first paint so there is no flash of the
 * wrong theme. Everything is wrapped in try/catch because storage and matchMedia can throw.
 */
export const themeInitScript = `(function(){var d=document.documentElement,p="system";try{var s=localStorage.getItem(${JSON.stringify(THEME_STORAGE_KEY)});if(s&&${JSON.stringify(THEME_PREFERENCES)}.indexOf(s)>-1)p=s}catch(e){}var t=p;if(p==="system"){t="light";try{if(matchMedia(${JSON.stringify(DARK_QUERY)}).matches)t="dark"}catch(e){}}d.setAttribute("data-theme",t);d.setAttribute("data-theme-pref",p)})()`;
