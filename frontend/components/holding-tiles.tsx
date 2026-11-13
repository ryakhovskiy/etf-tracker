import Decimal from "decimal.js";
import type { CSSProperties } from "react";
import { instrumentColor } from "../lib/colors";
import { performanceDisplay } from "../lib/performance";
import { euro, units, type Performance } from "../lib/transactions";

/**
 * One tile per position. The stripe has the instrument's chart colour (the same
 * --series-N slot as the chart and legend); the bar shows its share of the portfolio.
 * Values come from the loaded performance; nothing is fetched or recalculated server-side.
 */
export function HoldingTiles({ performance, hiddenIds = [] }: { performance: Performance; hiddenIds?: number[] }) {
  const current = performance.current;
  const rows = performance.series.map(series => {
    const item = current?.items.find(candidate => candidate.instrumentId === series.instrumentId);
    const atPurchase = new Decimal(series.values.at(-1) ?? "0");
    const value = item ? new Decimal(item.value) : atPurchase;
    const change = value.sub(atPurchase);
    return { series, item, atPurchase, value, change, changePct: atPurchase.isZero() ? null : change.div(atPurchase).mul(100) };
  });
  const total = rows.reduce((sum, row) => sum.add(row.value), new Decimal(0));
  if (!rows.length) return null;
  return <section className="holdings" aria-labelledby="holdings-title">
    <h2 id="holdings-title" className="section-title-inline">Positions</h2>
    <ul className="holding-tiles">{rows.map(({ series, item, value, change, changePct }) => {
      const share = total.isZero() ? new Decimal(0) : value.div(total).mul(100);
      const shown = !hiddenIds.includes(series.instrumentId);
      const display = performanceDisplay(change.toFixed(2), changePct === null ? null : changePct.toFixed(2));
      return <li key={series.instrumentId} className={`holding-tile tile${shown ? "" : " is-hidden"}`} data-testid="holding-tile" style={{ "--stripe": instrumentColor(series.instrumentId) } as CSSProperties}>
        <div className="holding-head"><h3>{series.name}</h3>{!shown && <span className="badge">hidden in chart</span>}</div>
        <p className="holding-value num">{euro(value.toFixed(2))}</p>
        <p className={`holding-change num performance-${display.tone}`}>{display.text}<span className="sr-only"> versus purchase prices</span></p>
        <p className="holding-meta num muted">{item ? `${units(item.units)} ${new Decimal(item.units).eq(1) ? "unit" : "units"} · ${euro(item.priceEur)} each${item.source === "PURCHASE" ? " (purchase price)" : ""}` : "Value at purchase prices"}</p>
        <div className="holding-share"><span className="share-bar" aria-hidden="true"><span style={{ width: `${Math.min(100, share.toNumber()).toFixed(1)}%` }} /></span><span className="num muted">{share.toFixed(1).replace(".", ",")} %</span></div>
      </li>;
    })}</ul>
  </section>;
}
