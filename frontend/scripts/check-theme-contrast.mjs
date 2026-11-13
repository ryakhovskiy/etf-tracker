// WCAG AA check of every theme in app/styles/tokens.css.
// CLI (from frontend/): node scripts/check-theme-contrast.mjs
// API: import { checkThemeContrast } from "./scripts/check-theme-contrast.mjs"
//      -> { themeCount, themes: string[], failures: string[] } (no logging, no process.exit)
// Text pairs need 4.5:1, UI components / graphics 3:1. Exits 1 on any failure.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const TOKENS = new URL("../app/styles/tokens.css", import.meta.url);

export function parseThemes(css) {
  const themes = {};
  for (const m of css.matchAll(/html\[data-theme="([a-z-]+)"\]\s*(?:,[^{]*)?\{([^}]*)\}/g)) {
    const vars = {};
    for (const v of m[2].matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\b/g)) vars[v[1]] = v[2];
    const alpha = /--div-ex-opacity:\s*([\d.]+)/.exec(m[2]);
    if (alpha) vars["--div-ex-opacity"] = alpha[1];
    themes[m[1]] = vars;
  }
  return themes;
}

const lum = (hex) => {
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
export const ratio = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };

const surfaces = ["--bg", "--surface", "--surface-raised", "--surface-sunken"];
const series = [1, 2, 3, 4, 5, 6, 7].map((n) => `--series-${n}`);
// [foreground, backgrounds, minimum ratio, why]
export const rules = [
  ["--text", surfaces, 4.5, "body text"],
  ["--text-muted", surfaces, 4.5, "muted text"],
  ["--link", surfaces, 4.5, "links"],
  ["--accent", ["--bg", "--surface", "--surface-raised"], 4.5, "accent as text"],
  ["--on-accent", ["--accent", "--accent-hover"], 4.5, "text on accent buttons"],
  ["--text", ["--accent-soft"], 4.5, "text on accent tint"],
  ["--accent", ["--accent-soft"], 4.5, "accent on its tint"],
  ["--positive", [...surfaces, "--positive-soft"], 4.5, "gain text"],
  ["--negative", [...surfaces, "--negative-soft"], 4.5, "loss text"],
  ["--warning", [...surfaces, "--warning-soft"], 4.5, "warning text"],
  ["--info", [...surfaces, "--info-soft"], 4.5, "info text"],
  ["--text", ["--positive-soft", "--negative-soft", "--warning-soft", "--info-soft"], 4.5, "text on tints"],
  ["--text-muted", ["--accent-soft", "--positive-soft", "--negative-soft"], 4.5, "muted on tints"],
  ["--border-strong", ["--bg", "--surface", "--surface-raised"], 3, "control borders"],
  ["--focus-ring", surfaces, 3, "focus ring"],
  ["--chart-axis", ["--surface", "--surface-raised"], 4.5, "axis labels"],
  ...series.map((s) => [s, ["--surface", "--surface-raised"], 3, "chart series"]),
  ...["a", "b", "c", "d", "e"].map((s) => [`--div-ex-${s}`, ["--surface"], 3, "ex-date shade (full opacity)"]),
];

const blend = (fg, bg, alpha) => "#" + [1, 3, 5].map((i) => Math.round(parseInt(fg.slice(i, i + 2), 16) * alpha + parseInt(bg.slice(i, i + 2), 16) * (1 - alpha)).toString(16).padStart(2, "0")).join("");

/** Checks every theme in tokens.css (or the given CSS text). Pure: returns the result, prints nothing. */
export function checkThemeContrast(css = readFileSync(TOKENS, "utf8")) {
  const themes = parseThemes(css);
  const failures = [];
  for (const [name, vars] of Object.entries(themes)) {
    for (const [fg, bgs, min, why] of rules) {
      for (const bg of bgs) {
        if (!vars[fg] || !vars[bg]) { failures.push(`MISSING ${name}: ${fg} or ${bg}`); continue; }
        const r = ratio(vars[fg], vars[bg]);
        if (r < min) failures.push(`FAIL ${name}: ${fg} on ${bg} = ${r.toFixed(2)} (< ${min}) [${why}]`);
      }
    }
  }
  // Ex-date chart marks are drawn at --div-ex-opacity: check the composited colour too.
  for (const [name, vars] of Object.entries(themes)) {
    const alpha = Number(vars["--div-ex-opacity"]);
    if (!Number.isFinite(alpha)) { failures.push(`MISSING ${name}: --div-ex-opacity`); continue; }
    for (const shade of ["a", "b", "c", "d", "e"]) {
      const fg = vars[`--div-ex-${shade}`], bg = vars["--surface"];
      if (!fg || !bg) continue;
      const r = ratio(blend(fg, bg, alpha), bg);
      if (r < 3) failures.push(`FAIL ${name}: --div-ex-${shade} at opacity ${alpha} = ${r.toFixed(2)} (< 3) [ex-date mark after opacity]`);
    }
  }
  return { themeCount: Object.keys(themes).length, themes: Object.keys(themes), failures };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const { themeCount, failures } = checkThemeContrast();
  for (const line of failures) console.log(line);
  console.log(failures.length ? `${failures.length} failure(s)` : `OK: ${themeCount} themes pass WCAG AA`);
  process.exit(failures.length ? 1 : 0);
}
