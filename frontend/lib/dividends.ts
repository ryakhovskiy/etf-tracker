import Decimal from "decimal.js";
import { euro, units } from "./transactions";

export type DividendEntry = {
  instrumentId: number; ticker: string; isin: string | null; shortName: string;
  kind: "ACTUAL" | "FORECAST"; exDate: string; basisExDate: string | null;
  perUnitOrig: string; currency: string; perUnitEur: string | null; fxRate: string | null; fxRateDate: string | null;
  yieldPct: string | null; close: string | null; closeDate: string | null;
  units: string; total: string | null; held: boolean; warning: "FX_UNSUPPORTED_CURRENCY" | "FX_RATE_UNAVAILABLE" | null;
};
export type DividendMonth = { month: number; total: string; entries: DividendEntry[] };
export type DividendSyncError = { instrumentId: number; label: string; message: string; at: string; lastSyncedAt: string | null };
export type DividendCalendar = {
  year: number; bounds: { minYear: number; maxYear: number };
  totals: { received: string; expected: string; total: string };
  months: DividendMonth[];
  sync: { provider: string; lastSyncedAt: string | null; stale: number[]; errors: DividendSyncError[]; jobId: string | null };
};
export type DividendSyncJob = { status: "RUNNING" | "DONE"; done: number; total: number; items: { instrumentId: number; status: "PENDING" | "OK" | "ERROR"; error?: string }[] };

const berlinDate = (d: Date, options: Intl.DateTimeFormatOptions) => d.toLocaleDateString("de-DE", { timeZone: "Europe/Berlin", ...options });
/** Today's year and month in Europe/Berlin, the server's calendar. */
export function berlinYearMonth(now = new Date()) {
  const [year, month] = new Intl.DateTimeFormat("sv-SE", { timeZone: "Europe/Berlin", year: "numeric", month: "2-digit" }).format(now).split("-").map(Number);
  return { year, month };
}
/** "Januar" … "Dezember", like the dashboard chart's de-DE month labels. */
export const monthName = (month: number) => new Date(Date.UTC(2026, month - 1, 1)).toLocaleDateString("de-DE", { month: "long", timeZone: "UTC" });
/** 2026-05-14 -> "14.05.2026" */
export const dayLabel = (date: string) => date.split("-").reverse().join(".");
/** 2026-05-14 -> "Ex 14.05." */
export const exLabel = (date: string) => `Ex ${date.slice(8, 10)}.${date.slice(5, 7)}.`;
/** An instant as a Berlin calendar day: "04.11.2026". */
export const syncedLabel = (iso: string) => berlinDate(new Date(iso), { day: "2-digit", month: "2-digit", year: "numeric" });
const grouped = (fixed: string) => { const [whole, fraction] = fixed.split("."); return whole.replace(/\B(?=(\d{3})+(?!\d))/g, ".") + (fraction ? `,${fraction}` : ""); };
export const money = (value: string) => euro(value);
/** Per-unit EUR value with 4 decimals: "0,4123 €" (no-break space, like euro()). */
export const perUnit = (value: string) => `${grouped(new Decimal(value).toFixed(4))}\u00a0€`;
/** "1,38 %" */
export const percent = (value: string) => `${grouped(new Decimal(value).toFixed(2))}\u00a0%`;
/** Units without trailing zeros: "120,5". */
export const amount = (value: string) => units(value);
/** A listing-currency amount with at least 2 decimals: "0,4123 EUR", "39,82 GBp". */
export function listingAmount(value: string, currency: string) {
  const d = new Decimal(value);
  return `${grouped(d.toFixed(Math.max(2, d.decimalPlaces())))} ${currency}`;
}
/** "1 USD = 0,9231 €" (pence listings convert through GBP). */
export function fxLabel(rate: string, currency: string) {
  const base = currency === "GBp" || currency === "GBX" ? "GBP" : currency === "ZAc" ? "ZAR" : currency;
  return `1 ${base} = ${grouped(new Decimal(rate).toFixed(4))} €`;
}
export const warningText = (entry: DividendEntry) => entry.warning === "FX_UNSUPPORTED_CURRENCY"
  ? `The ECB publishes no exchange rate for ${entry.currency}.`
  : entry.warning === "FX_RATE_UNAVAILABLE" ? (entry.kind === "FORECAST" ? `No current ECB exchange rate is available for ${entry.currency}.` : `No ECB exchange rate for ${entry.currency} is available for ${dayLabel(entry.exDate)}.`) : "";
