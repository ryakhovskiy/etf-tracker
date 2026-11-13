"use client";
import { memo, useCallback, useEffect, useRef, useState } from "react";
import { api } from "../lib/api";
import type { Performance } from "../lib/transactions";
import { HoldingsChart } from "./holdings-chart";
import { HoldingTiles } from "./holding-tiles";
import { PerformanceFigures } from "./performance-figures";

const MemoizedHoldingsChart = memo(HoldingsChart);

export function PerformanceOverview({ id, performance, reload }: { id: string; performance: Performance; reload: () => Promise<unknown> }) {
 const [hiddenIds, setHiddenIds] = useState<number[]>([]);
 const [filtered, setFiltered] = useState<Performance | null>(null);
 const [loading, setLoading] = useState(false);
 const [error, setError] = useState("");
 const appliedHidden = useRef<number[]>([]);
 const selectedIds = performance.series.filter(series => !hiddenIds.includes(series.instrumentId)).map(series => series.instrumentId).join(",");
 const allShown = performance.series.every(series => !hiddenIds.includes(series.instrumentId));
 useEffect(() => {
  if (allShown) {
   setFiltered(null);
   setLoading(false);
   appliedHidden.current = [];
   return;
  }
  const abort = new AbortController();
  setLoading(true);
  api<Performance>(`/api/portfolios/${id}/performance?instrumentIds=${selectedIds}`, { signal: abort.signal }).then(next => {
   if (abort.signal.aborted) return;
   setFiltered(next);
   appliedHidden.current = [...hiddenIds];
  }).catch(e => {
   if (abort.signal.aborted) return;
   setError(`Could not update the chart selection. ${(e as Error).message}`);
   setHiddenIds(appliedHidden.current);
  }).finally(() => { if (!abort.signal.aborted) setLoading(false); });
  return () => abort.abort();
 }, [id, performance, selectedIds, allShown]);
 const toggle = useCallback((instrumentId: number) => {
  setError("");
  setHiddenIds(current => current.includes(instrumentId) ? current.filter(id => id !== instrumentId) : [...current, instrumentId]);
 }, []);
 const displayed = allShown ? performance : filtered ?? performance;
 return <div className="performance-overview" aria-busy={loading}>
  <div className="performance-selection"><span>{performance.series.length - performance.series.filter(series => hiddenIds.includes(series.instrumentId)).length} of {performance.series.length} positions shown</span>
   {!allShown && <button type="button" onClick={() => { setError(""); setHiddenIds([]); }}>Show all</button>}
   {loading && <span role="status">Updating chart and totals…</span>}
  </div>
  {error && <p className="error" role="alert">{error}</p>}
  <PerformanceFigures id={id} performance={displayed} fetchPerformance={performance} reload={reload}/>
  <MemoizedHoldingsChart performance={displayed} instruments={performance.series} historyStart={performance.dates[0]} hiddenIds={hiddenIds} onToggleInstrument={toggle}/>
  <HoldingTiles performance={performance} hiddenIds={hiddenIds}/>
 </div>;
}
