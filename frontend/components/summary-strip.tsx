import type { RefreshProgress } from "./use-portfolio-summaries";
import { refreshLabel, type Summaries } from "../lib/portfolio-summary";
import { euro } from "../lib/transactions";
import { AsOf, Gain, PartialHint, timeLabel } from "./portfolio-metrics";

type Props = {
  data: Summaries | null;
  loading: boolean;
  failed: boolean;
  updating: boolean;
  progress: RefreshProgress;
  notice: string;
  onRefresh: () => void;
  onRetry: () => void;
};

/** Combined figures of all portfolios, above the grid, with the price refresh status and control. */
export function SummaryStrip({ data, loading, failed, updating, progress, notice, onRefresh, onRetry }: Props) {
  const total = data?.total;
  const refresh = data?.refresh;
  const blockedUntil = refresh?.yahoo.blockedUntil && Date.parse(refresh.yahoo.blockedUntil) > Date.now() ? refresh.yahoo.blockedUntil : null;
  const yahooDown = !!refresh && (!refresh.yahoo.available || !!blockedUntil);
  const hasPositions = !!total && total.positionCount > 0;
  const message = notice || (yahooDown ? `Yahoo is cooling down${blockedUntil ? ` until ${timeLabel(blockedUntil)}` : ""}. Showing stored prices.` : "");
  return <section className={`summary-strip tile${updating ? " is-updating" : ""}`} data-testid="portfolio-summary-strip" aria-label="All portfolios total" aria-busy={loading || updating}>
    {loading && !data ? <>
      <div className="summary-strip-main" aria-hidden="true"><span className="skeleton-line is-hero" /></div>
      <div aria-hidden="true"><span className="skeleton-line" /></div>
      <div aria-hidden="true"><span className="skeleton-line" /></div>
      <span className="sr-only" role="status">Loading totals…</span>
    </> : failed && !data ? <>
      <p className="summary-note summary-strip-main">Portfolio figures are unavailable right now.</p>
      <div className="summary-status"><button type="button" onClick={onRetry}>Try again</button></div>
    </> : total && <>
      <div className="summary-strip-main kpi">
        <span className="kpi-label">Total value</span>
        <strong className="kpi-value num">{total.currentValue === null ? (hasPositions ? "Unavailable" : "–") : euro(total.currentValue)}{total.partial && <PartialHint reasons={total.reasons} />}</strong>
        <span className="kpi-sub">{total.portfolioCount} {total.portfolioCount === 1 ? "portfolio" : "portfolios"} · {total.positionCount} {total.positionCount === 1 ? "position" : "positions"}</span>
      </div>
      <div className="kpi"><span className="kpi-label">Performance</span><strong className="kpi-value"><Gain money={total.performance} label="Performance" /></strong></div>
      <div className="kpi"><span className="kpi-label">Change vs yesterday</span><strong className="kpi-value"><Gain money={total.change} label="Change vs yesterday" />{total.changePartial && <PartialHint reasons={total.reasons} />}</strong></div>
      <div className="summary-status">
        {updating && <span className="summary-updating" role="status"><span className="spinner" aria-hidden="true" />Updating prices…{progress && progress.total > 0 ? ` ${progress.done}/${progress.total}` : ""}</span>}
        {!updating && hasPositions && <p className="summary-asof"><AsOf summary={total} /></p>}
        {message && !updating && <p className="summary-note" role="status">{message}</p>}
        {hasPositions && <button type="button" className="summary-refresh" disabled={updating || yahooDown} onClick={onRefresh}>{updating ? "Updating…" : refreshLabel(refresh?.estimate?.requests ?? 0)}</button>}
      </div>
    </>}
  </section>;
}
