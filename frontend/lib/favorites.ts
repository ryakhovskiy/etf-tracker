import Decimal from "decimal.js";
import type { SummaryAsOf } from "./portfolio-summary";
import { euro } from "./transactions";

/** One starred instrument as GET /api/favorites returns it (in the user's order). */
export type Favorite = {
  instrumentId: number;
  name: string;
  ticker: string;
  position: number;
  priceEur: string | null;
  change: { amount: string; percent: string | null } | null;
  /** The quote behind the price: the same object as the summaries' asOf. null without a quote. */
  asOf: SummaryAsOf | null;
  reasons: string[];
};
export type FavoritesResponse = { favorites: Favorite[] };

export type Tone = "positive" | "negative" | "neutral";

const MINUS = "−";
const signed = (value: Decimal, text: string) => value.gt(0) ? `+${text}` : value.lt(0) ? `${MINUS}${text.replace(/^-/, "")}` : text;

/** "42,04 €", or "–" when there is no price. */
export const favoritePrice = (priceEur: string | null) => priceEur === null ? "–" : euro(priceEur);

/** "−0,64 € (−1,50 %)": signed amount, then the signed percentage when known. "–" without a change. */
export function favoriteChange(change: Favorite["change"]): { text: string; tone: Tone } {
  if (!change) return { text: "–", tone: "neutral" };
  const amount = new Decimal(change.amount).toDecimalPlaces(2, Decimal.ROUND_HALF_UP);
  const tone: Tone = amount.gt(0) ? "positive" : amount.lt(0) ? "negative" : "neutral";
  const money = signed(amount, euro(amount.abs().toFixed(2)));
  if (change.percent === null) return { text: money, tone };
  const percent = new Decimal(change.percent).toDecimalPlaces(2, Decimal.ROUND_HALF_UP);
  const percentText = signed(percent, `${percent.abs().toFixed(2).replace(".", ",")} %`);
  return { text: `${money} (${percentText})`, tone };
}

/** The tile's accessible name: "HSBC MSCI World, 42,04 €, −0,64 € (−1,50 %)". */
export function favoriteLabel(favorite: Favorite) {
  const price = favorite.priceEur === null ? "no price" : favoritePrice(favorite.priceEur);
  return favorite.change ? `${favorite.name}, ${price}, ${favoriteChange(favorite.change).text}` : `${favorite.name}, ${price}`;
}

/** The ids in display order. */
export const favoriteIds = (favorites: Favorite[]) => favorites.map(favorite => favorite.instrumentId);

/** `favorites` re-ordered to `ids`; ids that are unknown are skipped, favorites missing from `ids` keep their order at the end. */
export function reorderFavorites(favorites: Favorite[], ids: number[]): Favorite[] {
  const byId = new Map(favorites.map(favorite => [favorite.instrumentId, favorite]));
  const ordered = ids.flatMap(id => byId.get(id) ?? []);
  const rest = favorites.filter(favorite => !ids.includes(favorite.instrumentId));
  return [...ordered, ...rest].map((favorite, position) => ({ ...favorite, position }));
}

const quoteKey = (asOf: SummaryAsOf) => asOf.quoteTime ?? `${asOf.quoteDate}T00:00:00Z`;

/** The section's single freshness line: the oldest quote among the favorites and the newest fetch time. null when no favorite has a quote. */
export function favoritesAsOf(favorites: Favorite[]): { asOf: SummaryAsOf; fetchedAt: string } | null {
  const quotes = favorites.flatMap(favorite => favorite.asOf ? [favorite.asOf] : []);
  if (quotes.length === 0) return null;
  const oldest = quotes.reduce((a, b) => quoteKey(b) < quoteKey(a) ? b : a);
  const fetchedAt = quotes.map(q => q.fetchedAt).reduce((a, b) => b > a ? b : a);
  return { asOf: oldest, fetchedAt };
}
