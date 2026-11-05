import Decimal from "decimal.js";
import { euro, type Performance, type Valuation } from "./transactions";

export const dateLabel = (date: string) => new Date(`${date}T00:00:00Z`).toLocaleDateString("de-DE", { timeZone: "UTC", day: "2-digit", month: "2-digit", year: "numeric" });
export function quoteDates(from: string, to: string) {
 if (from === to) return dateLabel(to);
 return `${from.slice(0, 4) === to.slice(0, 4) ? dateLabel(from).slice(0, 6) : dateLabel(from)}–${dateLabel(to)}`;
}
export const axisEuro = (value: number) => `${new Decimal(value).toFixed(0).replace(/\B(?=(\d{3})+(?!\d))/g, ".")} €`;
export function performanceDisplay(value: string, pct: string | null) {
 const rounded = new Decimal(value).toDecimalPlaces(2, Decimal.ROUND_HALF_UP);
 const direction = rounded.cmp(0);
 const percent = pct === null ? "–" : (() => {
  const n = new Decimal(pct).toDecimalPlaces(2, Decimal.ROUND_HALF_UP);
  return `${n.gt(0) ? "+" : ""}${n.isZero() ? "0,00" : n.toFixed(2).replace(".", ",")} %`;
 })();
 return { tone: direction > 0 ? "positive" : direction < 0 ? "negative" : "neutral", text: `${direction > 0 ? "▲ +" : direction < 0 ? "▼ " : ""}${euro(rounded.isZero() ? "0" : rounded.toFixed(2))} (${percent})` };
}
export function purchaseValuation(performance: Performance): Valuation {
 if (performance.atPurchase) return performance.atPurchase;
 const value = performance.totals.at(-1) ?? "0.00";
 const invested = new Decimal(performance.invested);
 const gain = new Decimal(value).sub(invested);
 return { value, performance: gain.toFixed(2), performancePct: invested.isZero() ? null : gain.div(invested).mul(100).toFixed(2) };
}
export const fallbackReasons = { NO_SYMBOL: "no Alpha Vantage symbol", NO_QUOTE: "no quote yet", PURCHASE_NEWER: "purchase newer than quote" };
