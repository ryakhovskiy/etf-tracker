"use client";
import { AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid, ResponsiveContainer, ReferenceLine } from "recharts";
import { chartData, euro, today, type Performance } from "../lib/transactions";
import { axisEuro, dateLabel, quoteDates } from "../lib/performance";
import { chartDay, type ChartRange } from "../lib/chart-range";
import { ChartShell } from "./chart-shell";

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
   {current?.chartPoint && Date.parse(`${current.asOfTo}T00:00:00Z`) >= domain[0] && Date.parse(`${current.asOfTo}T00:00:00Z`) <= domain[1] && <ReferenceLine x={Date.parse(`${current.asOfTo}T00:00:00Z`)} stroke="var(--text-muted)" strokeDasharray="3 3" label={{ value: "Current", angle: -90, position: "insideTopRight", fill: "var(--text)" }}/>}
   {current?.chartPoint && performance.series.map(s => <Area key={`current-${s.instrumentId}`} dataKey={`c${s.instrumentId}`} name={s.name} stackId="current" type="linear" stroke={instrumentColor(s.instrumentId)} strokeDasharray="6 4" fill={instrumentColor(s.instrumentId)} fillOpacity={0.2} isAnimationActive={false} dot={props => props.payload.isCurrent && !props.payload.isBoundary ? <circle className="current-value-dot" key={props.key} cx={props.cx} cy={props.cy} r={4} stroke={instrumentColor(s.instrumentId)} fill="var(--surface)"/> : <g key={props.key}/>}/>)}
   {performance.series.map(s => <Area key={s.instrumentId} dataKey={`i${s.instrumentId}`} name={s.name} stackId="holdings" type="linear" stroke={instrumentColor(s.instrumentId)} fill={instrumentColor(s.instrumentId)} fillOpacity={0.65} isAnimationActive={false}/>)}
  </AreaChart>;
}

export function HoldingsChart({ performance, instruments = performance.series, historyStart, hiddenIds = [], onToggleInstrument }: { performance: Performance; instruments?: Performance["series"]; historyStart?: string; hiddenIds?: number[]; onToggleInstrument?: (instrumentId: number) => void }) {
 const first = Date.parse(`${historyStart ?? performance.dates[0] ?? today()}T00:00:00Z`);
 const last = Math.max(first, Date.parse(`${today()}T00:00:00Z`), performance.current?.chartPoint ? Date.parse(`${performance.current.asOfTo}T00:00:00Z`) : first);
 const bounds: ChartRange = [first, last === first ? first + chartDay - 1 : last];
 const current = performance.current;
 return <ChartShell title="Holdings value at purchase prices (units held × purchase price)" bounds={bounds} plotLabel="Holdings chart" helpId="holdings-chart-help" footer={<>
  {onToggleInstrument && <p className="chart-interaction-hint">Toggle positions to update the chart and figures above.</p>}
  <div className="chart-legend" role="group" aria-label="Chart positions">{instruments.map(s => onToggleInstrument ? <button key={s.instrumentId} type="button" aria-pressed={!hiddenIds.includes(s.instrumentId)} onClick={() => onToggleInstrument(s.instrumentId)}><i style={{ background: instrumentColor(s.instrumentId) }}/><span>{s.name}</span></button> : <span key={s.instrumentId}><i style={{ background: instrumentColor(s.instrumentId) }}/>{s.name}</span>)}{current && <span><i className="current-line"/>Current value (quotes)</span>}</div>{current && !current.chartPoint && <p className="hint">Quotes are older than your last transaction. Fetch again to update the chart.</p>}
 </>}>
  {({ range, dragging }) => performance.series.length ? <ResponsiveContainer width="100%" height={340}><HoldingsPlot performance={performance} domain={range} dragging={dragging}/></ResponsiveContainer> : <div className="chart-empty" role="status">No positions selected. Use the legend to show a position.</div>}
 </ChartShell>;
}
