"use client";
import { useEffect, useRef, useState, type PointerEvent, type ReactNode } from "react";
import { dateLabel } from "../lib/performance";
import { chartDay, chartPeriods, chartPeriodStorageKey, clampChartRange, panChartRange, parseChartPreset, periodChartRange, zoomChartRange, type ChartPeriod, type ChartPreset, type ChartRange } from "../lib/chart-range";

export type ChartShellView = { range: ChartRange; bounds: ChartRange; dragging: boolean };

/**
 * The chart card shared by the portfolio chart and the ticker chart: heading with the period select,
 * pan/zoom buttons, the visible-period output, the hint line and the draggable plot area (drag, Ctrl/⌘ + wheel,
 * arrow keys, +/−, Home). The plot itself comes from `children`; `summary` renders between the heading
 * and the navigation and `footer` below the plot. The chosen preset is stored under the shared period key.
 */
export function ChartShell({ title, bounds, plotLabel, helpId, summary, footer, children }: {
  title: ReactNode;
  bounds: ChartRange;
  plotLabel: string;
  helpId: string;
  summary?: (view: ChartShellView) => ReactNode;
  footer?: ReactNode;
  children: (view: ChartShellView) => ReactNode;
}) {
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
 const full = range[1] - range[0] >= bounds[1] - bounds[0];
 const shellView: ChartShellView = { range, bounds, dragging };
 return <div className="chart-card">
  <div className="chart-heading"><h2>{title}</h2>
   <label className="chart-period">Time period<select aria-label="Chart time period" value={view.period} onChange={event => { const period = parseChartPreset(event.target.value); if (period !== null) selectPeriod(period); }}>
    {view.period === "custom" && <option value="custom" disabled>Custom</option>}
    {chartPeriods.map(months => <option key={months} value={months}>Last {months} months</option>)}<option value="max">Max</option>
   </select></label>
  </div>
  {summary?.(shellView)}
  <div className="chart-navigation" role="group" aria-label="Chart navigation">
   <div className="chart-navigation-buttons"><button type="button" aria-label="Pan left" title="Earlier" disabled={range[0] <= bounds[0]} onClick={() => pan(-1)}>←</button><button type="button" aria-label="Pan right" title="Later" disabled={range[1] >= bounds[1]} onClick={() => pan(1)}>→</button>
    <button type="button" aria-label="Zoom in" disabled={range[1] - range[0] <= Math.min(chartDay, bounds[1] - bounds[0])} onClick={() => zoom(0.5)}>+</button><button type="button" aria-label="Zoom out" disabled={full} onClick={() => zoom(2)}>−</button><button type="button" onClick={reset} disabled={view.period === "max"}>Reset chart</button></div>
   <output aria-label="Visible chart period">{dateLabel(new Date(range[0]).toISOString().slice(0,10))} – {dateLabel(new Date(range[1]).toISOString().slice(0,10))}</output>
  </div>
  <p className="chart-interaction-hint" id={helpId}>Drag to pan · Ctrl/⌘ + scroll to zoom · Arrow keys to pan, +/− to zoom</p>
  <div ref={plot} className={`chart-plot${dragging ? " is-dragging" : ""}`} tabIndex={0} role="group" aria-label={plotLabel} aria-describedby={helpId} onPointerDown={pointerDown} onPointerMove={pointerMove} onPointerUp={pointerUp} onPointerCancel={pointerUp} onLostPointerCapture={pointerUp} onKeyDown={event => {
   if (event.key === "ArrowLeft" || event.key === "ArrowRight") { event.preventDefault(); event.stopPropagation(); pan(event.key === "ArrowLeft" ? -1 : 1); }
   else if (["+", "=", "-", "Home"].includes(event.key)) { event.preventDefault(); event.stopPropagation(); if (event.key === "Home") reset(); else zoom(event.key === "-" ? 2 : 0.5); }
  }}>
   {children(shellView)}
  </div>
  {footer}
 </div>;
}
