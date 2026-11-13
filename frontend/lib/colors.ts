/**
 * The per-instrument chart palette. An instrument keeps the same index-based slot everywhere
 * (area charts, dividend chart, legends, tiles); the colour behind a slot is the per-theme
 * token --series-N from app/styles/tokens.css, so charts restyle when the theme changes.
 */
export const SERIES_COUNT = 7;
export const instrumentSlot = (id: number) => ((id - 1) % SERIES_COUNT + SERIES_COUNT) % SERIES_COUNT + 1;
export const instrumentColor = (id: number) => `var(--series-${instrumentSlot(id)})`;
/** Ex-date marks use one of these gray tokens per instrument (--div-ex-a..e in tokens.css). */
export const EX_SHADES = 5;
export const exShade = (id: number) => "abcde"[(id - 1) % EX_SHADES];
