/**
 * The only literal colours outside app/styles/tokens.css, kept in this one file because the platform
 * reads them before any CSS exists: <meta name="theme-color"> and the web manifest. Each value is the
 * theme's `--bg` token; tests/pwa-colors.test.mjs fails if one drifts from tokens.css.
 */
export const THEME_COLORS = {
  light: "#f3f5f4",
  dark: "#0d1110",
  "tech-green": "#050907",
  "tech-cappuccino": "#11111b",
  "tech-red": "#0a0708",
} as const;

export type ThemeColorKey = keyof typeof THEME_COLORS;

/** A manifest cannot vary by theme, so it uses the Light theme's page colour. */
export const MANIFEST_THEME_COLOR: string = THEME_COLORS.light;
export const MANIFEST_BACKGROUND_COLOR: string = THEME_COLORS.light;
