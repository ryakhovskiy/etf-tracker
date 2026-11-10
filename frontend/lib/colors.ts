/** The per-instrument palette of every chart: an instrument keeps its color everywhere. */
export const instrumentColors = ["#1f5c47", "#6689b1", "#c39743", "#9867a1", "#c87967", "#789252", "#599d9d"];
export const instrumentColor = (id: number) => instrumentColors[(id - 1) % instrumentColors.length];
/** Ex-date marks use one of these gray tokens per instrument (see .dividends in globals.css). */
export const EX_SHADES = 5;
export const exShade = (id: number) => "abcde"[(id - 1) % EX_SHADES];
