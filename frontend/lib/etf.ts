import Decimal from "decimal.js";

export type EtfProfile = {
  instrumentId: number; isin: string; etfCompany: string; trackingIndex: string; assetClass: string; domicile: string;
  expenseRatio: string; aumEur: string; dividendYield: string; inceptionDate: string; currency: string; website: string;
  fetchedAt: string; stale: boolean;
};

const de = (value: Decimal, digits: number) => new Intl.NumberFormat("de-DE", { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value.toNumber());

/** "121,4 Mrd. €" style amounts; empty input gives an em dash. */
export function formatAum(eur: string): string {
  if (!eur) return "—";
  const n = new Decimal(eur);
  if (n.gte(1e9)) return `${de(n.div(1e9), 1)} Mrd. €`;
  if (n.gte(1e6)) return `${de(n.div(1e6), 1)} Mio. €`;
  return `${de(n, 0)} €`;
}
export const formatPercent = (value: string) => value ? `${de(new Decimal(value), 2)} %` : "—";
export const formatDate = (iso: string) => iso ? iso.slice(0, 10).split("-").reverse().join(".") : "—";

/** Splits text into [text, matched] parts for highlighting the query, case-insensitively. */
export function highlightParts(text: string, query: string): { text: string; match: boolean }[] {
  const q = query.trim();
  if (!q) return [{ text, match: false }];
  const parts: { text: string; match: boolean }[] = [];
  const lower = text.toLowerCase(), needle = q.toLowerCase();
  let from = 0;
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, from)) {
    if (at > from) parts.push({ text: text.slice(from, at), match: false });
    parts.push({ text: text.slice(at, at + q.length), match: true });
    from = at + q.length;
  }
  if (from < text.length) parts.push({ text: text.slice(from), match: false });
  return parts.length ? parts : [{ text, match: false }];
}
