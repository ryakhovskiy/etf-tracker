/** Types and display helpers for GET /api/portfolios/summaries and POST /api/price-refresh-jobs (contract v1). */
export type Money = { amount: string; percent: string | null };
export type SummaryAsOf = { quoteDate: string; quoteTime: string | null; fetchedAt: string; providers: string[]; realtime: boolean };
export type SummaryReason = "NO_QUOTE" | "PURCHASE_NEWER" | "NO_PREVIOUS_CLOSE" | "FX_PREVIOUS_UNAVAILABLE";
export type PortfolioSummary = {
  positionCount: number;
  invested: string;
  currentValue: string | null;
  performance: Money | null;
  change: Money | null;
  partial: boolean;
  changePartial: boolean;
  reasons: SummaryReason[];
  asOf: SummaryAsOf | null;
};
export type PortfolioSummaryRow = PortfolioSummary & { portfolioId: number };
export type TotalSummary = PortfolioSummary & { portfolioCount: number };
export type RefreshState = {
  stale: boolean;
  newestFetchedAt: string | null;
  maxAgeSeconds: number;
  autoFetch: boolean;
  runningJobId: string | null;
  lastAttemptAt: string | null;
  yahoo: { available: boolean; blockedUntil: string | null };
  estimate: { requests: number; remaining: number; noSymbol: number[] };
};
export type Summaries = { summaries: PortfolioSummaryRow[]; total: TotalSummary; refresh: RefreshState };
export type RefreshResult =
  | { started: true; reused: false; jobId: string; requests: number }
  | { started: false; reused: true; jobId: string }
  | { started: false; reason: "FRESH" | "THROTTLED" | "NOTHING_TO_PRICE" }
  | { started: false; reason: "YAHOO_UNAVAILABLE"; blockedUntil?: string | null };
export type RefreshJob = { status: "QUEUED" | "RUNNING" | "DONE"; kind?: string; items: { instrumentId: number; status: string }[] };

export const reasonText: Record<SummaryReason, string> = {
  NO_QUOTE: "no quote yet for some positions (valued at purchase price)",
  PURCHASE_NEWER: "a purchase is newer than the latest quote (valued at purchase price)",
  NO_PREVIOUS_CLOSE: "no previous close for some positions (left out of the change)",
  FX_PREVIOUS_UNAVAILABLE: "previous exchange rate unavailable (current rate used)",
};

const providerName = (provider: string) => provider === "YAHOO" ? "Yahoo" : provider === "EULERPOOL" ? "Eulerpool" : provider === "ALPHAVANTAGE" ? "Alpha Vantage" : provider;

/** "Yahoo real-time" / "Yahoo" / "Yahoo, Eulerpool": where the oldest used quote came from. */
export const sourceLabel = (asOf: SummaryAsOf) => {
  const names = asOf.providers.map(providerName).join(", ");
  return asOf.realtime && names ? `${names} real-time` : names;
};

/** "12 Yahoo requests" style cost label of the refresh control. */
export const refreshLabel = (requests: number) => requests > 0 ? `Refresh prices (${requests} Yahoo ${requests === 1 ? "request" : "requests"})` : "Refresh prices";
