"use client";
import { useEffect, useRef, useState, type PointerEvent } from "react";
import { AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid, ResponsiveContainer, ReferenceLine } from "recharts";
import { chartData, euro, today, type Performance } from "../lib/transactions";
import { axisEuro, dateLabel, quoteDates } from "../lib/performance";
import { chartDay, chartPeriods, chartPeriodStorageKey, clampChartRange, panChartRange, parseChartPreset, periodChartRange, zoomChartRange, type ChartPeriod, type ChartPreset, type ChartRange } from "../lib/chart-range";

import { instrumentColor } from "../lib/colors";
export { instrumentColor };
export function HoldingsTooltip({ performance, date, current, total }: { performance: Performance; date: number; current: boolean; total: string }) {
 const valuation = performance.current;
 const i = performance.dates.indexOf(new Date(date).toISOString().slice(0,10));
 return <div className="chart-tooltip">
  <strong>{current && valuation ? `Current value · quotes as of ${quoteDates(valuation.asOfFrom, valuation.asOfTo)}` : dateLabel(new Date(date).toISOString().slice(0,10))}</strong>
  {performance.series.map(s => {
   const item = current ? valuation?.items.find(item => item.instrumentId === s.instrumentId) : null;
   return <p key={s.instrumentId}><i style={{ background: instrumentColor(s.instrumentId) }}/>{s.name}: {euro(current ? item?.value ?? "0" : s.values[i] ?? "0")}{item && ` · ${item.source === "QUOTE" ? "Quote" : "Purchase price"}`}</p>;
  })}
  <p><strong>Total: {euro(total)}</strong></p>
 </div>;
}
// Keep the last known value visible through today without labelling it as a new
// purchase or quote. Tooltips on this continuation retain the actual source date.
export function holdingsPlotData(performance: Performance, end: number) {
 const points = chartData(performance);
 const last = points.at(-1);
 if (last && end > Number(last.date)) points.push({ ...last, date: end, sourceDate: last.date, isBoundary: true });
 return points;
}

export function HoldingsPlot({ performance, domain, dragging = false }: { performance: Performance; domain: ChartRange; dragging?: boolean }) {
 const current = performance.current;
 return <AreaChart data={holdingsPlotData(performance, domain[1])} margin={{ top: 30, right: 30, bottom: 10, left: 0 }}>
   <CartesianGrid strokeDasharray="3 3" vertical={false}/>
   <XAxis minTickGap={32} tickMargin={10} tick={{fontSize:12}} dataKey="date" type="number" scale="time" domain={domain} allowDataOverflow tickFormatter={value => new Date(value).toLocaleDateString("de-DE", { ...(domain[1] - domain[0] < 90 * chartDay ? { day: "2-digit" as const } : {}), month: "short", year: "2-digit", timeZone: "UTC" })}/>
   {/* Recharts measures the rendered tick text and reserves its widest width. */}
   <YAxis width="auto" tickFormatter={axisEuro}/>
   <Tooltip active={dragging ? false : undefined} content={({ active, payload, label }) => active && payload?.length && Number(label) >= domain[0] && Number(label) <= domain[1] ? <HoldingsTooltip performance={performance} date={Number(payload[0].payload.sourceDate ?? label)} current={!!payload[0].payload.isCurrent} total={String(payload[0].payload.total)}/> : null}/>
   {current?.chartPoint && Date.parse(`${current.asOfTo}T00:00:00Z`) >= domain[0] && Date.parse(`${current.asOfTo}T00:00:00Z`) <= domain[1] && <ReferenceLine x={Date.parse(`${current.asOfTo}T00:00:00Z`)} stroke="var(--muted)" strokeDasharray="3 3" label={{ value: "Current", angle: -90, position: "insideTopRight", fill: "var(--text)" }}/>}
   {current?.chartPoint && performance.series.map(s => <Area key={`current-${s.instrumentId}`} dataKey={`c${s.instrumentId}`} name={s.name} stackId="current" type="linear" stroke={instrumentColor(s.instrumentId)} strokeDasharray="6 4" fill={instrumentColor(s.instrumentId)} fillOpacity={0.2} isAnimationActive={false} dot={props => props.payload.isCurrent && !props.payload.isBoundary ? <circle className="current-value-dot" key={props.key} cx={props.cx} cy={props.cy} r={4} stroke={instrumentColor(s.instrumentId)} fill="var(--chart-bg, white)"/> : <g key={props.key}/>}/>)}
   {performance.series.map(s => <Area key={s.instrumentId} dataKey={`i${s.instrumentId}`} name={s.name} stackId="holdings" type="linear" stroke={instrumentColor(s.instrumentId)} fill={instrumentColor(s.instrumentId)} fillOpacity={0.65} isAnimationActive={false}/>)}
  </AreaChart>;
}

export function HoldingsChart({ performance, instruments = performance.series, historyStart, hiddenIds = [], onToggleInstrument }: { performance: Performance; instruments?: Performance["series"]; historyStart?: string; hiddenIds?: number[]; onToggleInstrument?: (instrumentId: number) => void }) {
 const first = Date.parse(`${historyStart ?? performance.dates[0] ?? today()}T00:00:00Z`);
 const last = Math.max(first, Date.parse(`${today()}T00:00:00Z`), performance.current?.chartPoint ? Date.parse(`${performance.current.asOfTo}T00:00:00Z`) : first);
 const bounds: ChartRange = [first, last === first ? first + chartDay - 1 : last];
 const [view, setView] = useState<{ period: ChartPeriod; range: ChartRange | null }>({ period: 12, range: null });
 useEffect(() => {
  try {
   const period = parseChartPreset(window.localStorage.getItem(chartPeriodStorageKey));
   if (period !== null) setView({ period, range: null });
  } catch { /* Use the default when browser storage is unavailable. */ }
 }, []);
 const selectPeriod = (period: ChartPreset) => {
  setView({ period, range: null });
  // Custom zoom/pan ranges never replace the last explicitly selected preset.
  try { window.localStorage.setItem(chartPeriodStorageKey, String(period)); } catch { /* The current view still works without storage. */ }
 };
 const range = view.range ? clampChartRange(view.range, bounds) : periodChartRange(bounds, view.period);
 const [dragging, setDragging] = useState(false);
 const plot = useRef<HTMLDivElement>(null);
 const drag = useRef<{ pointerId: number; x: number; range: ChartRange } | null>(null);
 const latest = useRef({ range, bounds });
 latest.current = { range, bounds };
 const setRange = (range: ChartRange) => {
  if (range[0] !== latest.current.range[0] || range[1] !== latest.current.range[1]) setView({ period: "custom", range });
 };
 const pan = (direction: number) => setRange(panChartRange(range, bounds, direction * (range[1] - range[0]) / 2));
 const zoom = (factor: number) => setRange(zoomChartRange(range, bounds, factor));
 const reset = () => selectPeriod("max");
 const metrics = () => {
  const element = plot.current;
  const rect = (element?.querySelector(".recharts-cartesian-grid") ?? element)?.getBoundingClientRect();
  return { left: rect?.left ?? 0, width: Math.max(1, rect?.width ?? 1) };
 };
 useEffect(() => {
  const element = plot.current;
  if (!element) return;
  const wheel = (event: WheelEvent) => {
   if (!(event.ctrlKey || event.metaKey) || event.deltaY === 0) return;
   event.preventDefault();
   const { range, bounds } = latest.current;
   const { left, width } = metrics();
   const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 340 : 1);
   setRange(zoomChartRange(range, bounds, Math.exp(Math.max(-1, Math.min(1, delta * 0.003))), (event.clientX - left) / width));
  };
  element.addEventListener("wheel", wheel, { passive: false });
  return () => element.removeEventListener("wheel", wheel);
 }, []);
 const pointerDown = (event: PointerEvent<HTMLDivElement>) => {
  if (event.button !== 0 || drag.current || range[1] - range[0] >= bounds[1] - bounds[0]) return;
  drag.current = { pointerId: event.pointerId, x: event.clientX, range: [...range] };
  event.currentTarget.setPointerCapture?.(event.pointerId);
 };
 const pointerMove = (event: PointerEvent<HTMLDivElement>) => {
  const start = drag.current;
  if (!start || start.pointerId !== event.pointerId || (!dragging && Math.abs(event.clientX - start.x) < 3)) return;
  setDragging(true);
  setRange(panChartRange(start.range, bounds, (start.x - event.clientX) / metrics().width * (start.range[1] - start.range[0])));
 };
 const pointerUp = (event: PointerEvent<HTMLDivElement>) => {
  if (drag.current?.pointerId !== event.pointerId) return;
  drag.current = null;
  setDragging(false);
  if (event.currentTarget.hasPointerCapture?.(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
 };
 const current = performance.current;
 const full = range[1] - range[0] >= bounds[1] - bounds[0];
 return <div className="chart-card">
  <div className="chart-heading"><h2>Holdings value at purchase prices (units held × purchase price)</h2>
   <label className="chart-period">Time period<select aria-label="Chart time period" value={view.period} onChange={event => { const period = parseChartPreset(event.target.value); if (period !== null) selectPeriod(period); }}>
    {view.period === "custom" && <option value="custom" disabled>Custom</option>}
    {chartPeriods.map(months => <option key={months} value={months}>Last {months} months</option>)}<option value="max">Max</option>
   </select></label>
  </div>
  <div className="chart-navigation" role="group" aria-label="Chart navigation">
   <div className="chart-navigation-buttons"><button type="button" aria-label="Pan left" title="Earlier" disabled={range[0] <= bounds[0]} onClick={() => pan(-1)}>←</button><button type="button" aria-label="Pan right" title="Later" disabled={range[1] >= bounds[1]} onClick={() => pan(1)}>→</button>
    <button type="button" aria-label="Zoom in" disabled={range[1] - range[0] <= Math.min(chartDay, bounds[1] - bounds[0])} onClick={() => zoom(0.5)}>+</button><button type="button" aria-label="Zoom out" disabled={full} onClick={() => zoom(2)}>−</button><button type="button" onClick={reset} disabled={view.period === "max"}>Reset chart</button></div>
   <output aria-label="Visible chart period">{dateLabel(new Date(range[0]).toISOString().slice(0,10))} – {dateLabel(new Date(range[1]).toISOString().slice(0,10))}</output>
  </div>
  <p className="chart-interaction-hint" id="holdings-chart-help">Drag to pan · Ctrl/⌘ + scroll to zoom · Arrow keys to pan, +/− to zoom</p>
  <div ref={plot} className={`chart-plot${dragging ? " is-dragging" : ""}`} tabIndex={0} role="group" aria-label="Holdings chart" aria-describedby="holdings-chart-help" onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={pointerUp} onLostPointerCapture={pointerUp} onKeyDown={event => {
   if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); event.stopPropagation(); pan(event.key === "ArrowLeft" ? -1 : 1); }
   else if (["+", "=", "-", "Home"].includes(event.key)) { event.preventDefault(); event.stopPropagation(); if (event.key === "Home") reset(); else zoom(event.key === "-" ? 2 : 0.5); }
  }}>
   {performance.series.length ? <ResponsiveContainer width="100%" height={340}><HoldingsPlot performance={performance} domain={range} dragging={dragging}/></ResponsiveContainer> : <div className="chart-empty" role="status">No positions selected. Use the legend to show a position.</div>}
  </div>
  {onToggleInstrument && <p className="chart-interaction-hint">Toggle positions to update the chart and figures above.</p>}
  <div className="chart-legend" role="group" aria-label="Chart positions">{instruments.map(s => onToggleInstrument ? <button key={s.instrumentId} type="button" aria-pressed={!hiddenIds.includes(s.instrumentId)} onClick={() => onToggleInstrument(s.instrumentId)}><i style={{ background: instrumentColor(s.instrumentId) }}/><span>{s.name}</span></button> : <span key={s.instrumentId}><i style={{ background: instrumentColor(s.instrumentId) }}/>{s.name}</span>)}{current && <span><i className="current-line"/>Current value (quotes)</span>}</div>{current && !current.chartPoint && <p className="hint">Quotes are older than your last transaction. Fetch again to update the chart.</p>}
 </div>;
}
