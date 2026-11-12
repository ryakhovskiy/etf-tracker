"use client";
import { type CurrentValuation, euro, type Performance, type Valuation } from "../lib/transactions";
import { fallbackReasons, performanceDisplay, purchaseValuation, quoteDates } from "../lib/performance";
import { CurrentValueFetch } from "./current-value-fetch";

export function PerformanceAmount({ valuation }: { valuation: Valuation }) {
 const display = performanceDisplay(valuation.performance, valuation.performancePct);
 const split = display.text.lastIndexOf(" (");
 return <strong className={`performance-amount performance-${display.tone}`}><span className="performance-value">{display.text.slice(0, split)}</span>{" "}<span className="performance-percent">{display.text.slice(split + 1)}</span></strong>;
}
function CurrentMeta({ current, series }: { current: CurrentValuation; series: Performance["series"] }) {
 const fetched = new Date(current.lastFetchedAt).toLocaleString("de-DE", { timeZone: "Europe/Berlin", day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit" });
 const reasons = current.items.filter(i => i.source === "PURCHASE").map(i => `${series.find(s => s.instrumentId === i.instrumentId)?.name ?? i.instrumentId}: ${i.reason ? fallbackReasons[i.reason] : "purchase price"}`);
 return <><small className="muted">as of {quoteDates(current.asOfFrom, current.asOfTo)} · fetched {fetched}</small>{current.partial && <span className="partial-detail" tabIndex={0} title={reasons.join("\n")} aria-label={`Partial current value: ${reasons.join("; ")}`}><span className="badge">partial</span><span className="partial-tooltip" role="tooltip">{reasons.map(reason => <span key={reason}>{reason}</span>)}</span></span>}</>;
}
export function PerformanceFigures({ id, performance, fetchPerformance = performance, reload }: { id: string; performance: Performance; fetchPerformance?: Performance; reload: () => Promise<unknown> }) {
 const atPurchase = purchaseValuation(performance);
 const current = performance.current;
 const showCurrent = !!current || !!fetchPerformance.current;
 const empty = performance.series.length === 0;
 return <div className="performance-summary">
  <div className="figures" aria-label="Portfolio figures">
  <div className="figure"><span className="muted">Total invested</span><strong>{euro(performance.invested)}</strong></div>
  <div className="figure"><span className="muted">Holdings value at purchase prices</span><strong>{euro(atPurchase.value)}</strong></div>
  <div className="figure"><span className="muted">Performance at purchase prices</span><PerformanceAmount valuation={atPurchase}/></div>
  {showCurrent && <>
   <div className="figure figure-current"><span className="muted">Current value</span><strong>{current ? euro(current.value) : empty ? euro("0") : "Unavailable"}</strong>{current && <CurrentMeta current={current} series={performance.series}/>}</div>
   <div className="figure figure-current"><span className="muted">Current performance</span>{current || empty ? <PerformanceAmount valuation={current ?? { value: "0.00", performance: "0.00", performancePct: null }}/>: <strong>Unavailable</strong>}{current && <CurrentMeta current={current} series={performance.series}/>}</div>
  </>}
  </div>
  <CurrentValueFetch id={id} performance={fetchPerformance} reload={reload}/>
 </div>;
}
