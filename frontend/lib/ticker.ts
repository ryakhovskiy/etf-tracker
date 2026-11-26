import Decimal from "decimal.js";
import type { ChartRange } from "./chart-range";
import type { Favorite, Tone } from "./favorites";
import type { SummaryAsOf } from "./portfolio-summary";

/** One daily close, already converted to EUR at the ECB rate of that day. */
export type ChartPoint = { day: string; closeEur: string };
/** One BUY line of the instrument, in any of the user's portfolios. Prices are EUR. */
export type ChartBuy = { day: string; units: string; priceEur: string; portfolioId: number; portfolioName: string };
/** GET /api/instruments/{id}/chart */
export type InstrumentChart = {
  instrument: { id: number; name: string; isin: string; ticker: string };
  latest: { priceEur: string | null; change: Favorite["change"]; asOf: SummaryAsOf | null; reasons?: string[] };
  points: ChartPoint[];
  buys: ChartBuy[];
  from: string;
  to: string;
  /** Whether the title is one of the user's favorites. Older answers lack it. */
  favorite?: boolean;
  /** Set when Yahoo could not be asked: the stored points are still shown. */
  warning?: string;
};
export type BuyPosition = "above" | "below";

export const dayMs = (day: string) => Date.parse(`${day}T00:00:00Z`);

/** Above the close when the buy price was higher than the close; at or below it otherwise. */
export const buyMarkerPosition = (priceEur: string, closeEur: string): BuyPosition => new Decimal(priceEur).gt(closeEur) ? "above" : "below";

/** Green (positive) at or below the close, red (negative) above it. */
export const buyMarkerTone = (position: BuyPosition): Tone => position === "above" ? "negative" : "positive";

/** The last close on or before `day`; null when the series starts after it. `points` are ascending. */
export function closeOnOrBefore(points: ChartPoint[], day: string): ChartPoint | null {
  let found: ChartPoint | null = null;
  for (const point of points) { if (point.day <= day) found = point; else break; }
  return found;
}

export type Marker = { buy: ChartBuy; position: BuyPosition; tone: Tone; close: string };

/** Every buy with its marker position against the last close on or before its day. A buy before the first known close has nothing to be compared with and gets no marker. */
export function buyMarkers(points: ChartPoint[], buys: ChartBuy[]): Marker[] {
  return buys.flatMap(buy => {
    const close = closeOnOrBefore(points, buy.day)?.closeEur;
    if (close === undefined) return [];
    const position = buyMarkerPosition(buy.priceEur, close);
    return [{ buy, position, tone: buyMarkerTone(position), close }];
  });
}

/** Change of the close across the visible range: first close inside it to the last close inside it. null with fewer than two closes. */
export function periodChange(points: ChartPoint[], range: ChartRange): { amount: string; percent: string | null } | null {
  const inside = points.filter(point => dayMs(point.day) >= range[0] && dayMs(point.day) <= range[1]);
  if (inside.length < 2) return null;
  const start = new Decimal(inside[0].closeEur);
  const amount = new Decimal(inside[inside.length - 1].closeEur).minus(start);
  return {
    amount: amount.toDecimalPlaces(2, Decimal.ROUND_HALF_UP).toFixed(2),
    percent: start.isZero() ? null : amount.div(start).times(100).toDecimalPlaces(2, Decimal.ROUND_HALF_UP).toFixed(2),
  };
}

/**
 * The values of the drawn line inside `range`: the closes in it plus the line's value at both edges
 * (linear between the neighbouring closes, as the area is drawn). A range between two stored closes,
 * e.g. a weekend, therefore still has the sloping segment's values to scale the y axis with.
 */
export function visibleCloses(points: ChartPoint[], range: ChartRange): number[] {
  const values = points.filter(point => dayMs(point.day) >= range[0] && dayMs(point.day) <= range[1]).map(point => Number(point.closeEur));
  for (const edge of range) {
    const after = points.findIndex(point => dayMs(point.day) >= edge);
    if (after <= 0) continue; // before the first close or after the last: nothing is drawn there
    const a = points[after - 1], b = points[after];
    const t = (edge - dayMs(a.day)) / (dayMs(b.day) - dayMs(a.day));
    if (dayMs(b.day) !== edge) values.push(Number(a.closeEur) + (Number(b.closeEur) - Number(a.closeEur)) * t);
  }
  return values;
}

/** The y axis for the visible closes and markers: rounded outwards to a 1/2/5 step, with the decimals that step needs. */
export function priceAxis(values: number[]): { domain: [number, number]; decimals: number } {
  const finite = values.filter(Number.isFinite);
  if (finite.length === 0) return { domain: [0, 1], decimals: 0 };
  const low = Math.min(...finite), high = Math.max(...finite);
  const span = high - low || Math.max(Math.abs(high) * 0.02, 0.01);
  const raw = span / 4, magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map(m => m * magnitude).find(s => s >= raw) ?? magnitude * 10;
  const decimals = step >= 1 ? 0 : Math.min(4, Math.ceil(-Math.log10(step) - 1e-9));
  return { domain: [Math.floor(low / step) * step, Math.ceil(high / step) * step], decimals };
}

export const axisPrice = (decimals: number) => (value: number) => `${new Decimal(value).toFixed(decimals).replace(/\B(?=(\d{3})+(?!\d))/g, "_").replace(".", ",").replaceAll("_", ".")} €`;

/** The warning code from the API as a sentence. The stored prices are still on screen. */
export function chartWarning(code: string) {
  switch (code) {
    case "FX_UNAVAILABLE": return "Some days are missing because no euro exchange rate was available for them. Showing the prices that could be converted.";
    case "YAHOO_LIMIT_REACHED": return "Yahoo's request limit is reached for now, so the newest prices may be missing. Showing the stored prices.";
    case "YAHOO_AUTH_ERROR": return "Yahoo refused the request, so the newest prices may be missing. Showing the stored prices.";
    case "": case "YAHOO_UNAVAILABLE": return "Yahoo is not reachable right now, so the newest prices may be missing. Showing the stored prices.";
    default: return `Yahoo did not answer (${code}). Showing the stored prices.`;
  }
}
