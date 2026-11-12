import Decimal from "decimal.js";
import { type TransactionGroupPerformance, type TransactionLinePerformance, units } from "../lib/transactions";
import { dateLabel, performanceDisplay } from "../lib/performance";

export function TransactionPerformance({ performance }: { performance: TransactionLinePerformance | null }) {
 if (!performance) return <span className="muted">No current quote</span>;
 const priceChange = new Decimal(performance.priceChange);
 const tone = priceChange.gt(0) ? "positive" : priceChange.lt(0) ? "negative" : "neutral";
 const pct = performance.priceChangePct === null ? "–" : `${new Decimal(performance.priceChangePct).gt(0) ? "+" : ""}${performance.priceChangePct.replace(".", ",")} %`;
 const total = performanceDisplay(performance.change, performance.changePct);
 return <div className={`transaction-performance performance-${tone}`}>
  <strong>Current: {units(performance.currentPrice)} €</strong>
  <small className="muted">as of {dateLabel(performance.quoteDate)}</small>
  <span>Per unit: {priceChange.gt(0) ? "+" : ""}{units(performance.priceChange)} € ({pct})</span>
  <strong>Position: {total.text}</strong>
 </div>;
}

export function TransactionPerformanceTotal({ summary }: { summary?: TransactionGroupPerformance }) {
 if (!summary?.pricedLines || summary.change === undefined) return <span className="transaction-performance-total muted">Performance: no current quotes</span>;
 const display = performanceDisplay(summary.change, summary.changePct ?? null);
 return <span className="transaction-performance-total">
  <strong className={`performance-${display.tone}`}>Performance: {display.text}</strong>
  {summary.pricedLines < summary.totalLines && <span className="muted"> · partial ({summary.pricedLines}/{summary.totalLines} positions priced)</span>}
 </span>;
}
