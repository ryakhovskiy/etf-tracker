"use client";
import { AreaChart, Area, XAxis, YAxis, Tooltip, CartesianGrid, ResponsiveContainer, ReferenceLine } from "recharts";
import { chartData, euro, type Performance } from "../lib/transactions";
import { axisEuro, dateLabel, quoteDates } from "../lib/performance";

const colors = ["#1f5c47", "#6689b1", "#c39743", "#9867a1", "#c87967", "#789252", "#599d9d"];
export const instrumentColor = (id: number) => colors[(id-1)%colors.length];
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
export function HoldingsChart({ performance }: { performance: Performance }) {
 const current = performance.current;
 return <div className="chart-card"><h2>Holdings value at purchase prices (units held × purchase price)</h2><ResponsiveContainer width="100%" height={340}>
  <AreaChart data={chartData(performance)} margin={{ top: 30, right: 30, bottom: 10, left: 0 }}>
   <CartesianGrid strokeDasharray="3 3" vertical={false}/>
   <XAxis minTickGap={32} tickMargin={10} tick={{fontSize:12}} dataKey="date" type="number" scale="time" domain={["dataMin","dataMax"]} tickFormatter={value => new Date(value).toLocaleDateString("de-DE", { month: "short", year: "2-digit", timeZone: "UTC" })}/>
   {/* Recharts measures the rendered tick text and reserves its widest width. */}
   <YAxis width="auto" tickFormatter={axisEuro}/>
   <Tooltip content={({ active, payload, label }) => active && payload?.length ? <HoldingsTooltip performance={performance} date={Number(label)} current={!!payload[0].payload.isCurrent} total={String(payload[0].payload.total)}/> : null}/>
   {current?.chartPoint && <ReferenceLine x={Date.parse(`${current.asOfTo}T00:00:00Z`)} stroke="var(--muted)" strokeDasharray="3 3" label={{ value: "Current", angle: -90, position: "insideTopRight", fill: "var(--text)" }}/>}
   {current?.chartPoint && performance.series.map(s => <Area key={`current-${s.instrumentId}`} dataKey={`c${s.instrumentId}`} name={s.name} stackId="current" type="linear" stroke={instrumentColor(s.instrumentId)} strokeDasharray="6 4" fill={instrumentColor(s.instrumentId)} fillOpacity={0.2} isAnimationActive={false} dot={props => props.payload.isCurrent ? <circle className="current-value-dot" key={props.key} cx={props.cx} cy={props.cy} r={4} stroke={instrumentColor(s.instrumentId)} fill="var(--chart-bg, white)"/> : <g key={props.key}/>}/>)}
   {performance.series.map(s => <Area key={s.instrumentId} dataKey={`i${s.instrumentId}`} name={s.name} stackId="holdings" type="linear" stroke={instrumentColor(s.instrumentId)} fill={instrumentColor(s.instrumentId)} fillOpacity={0.65} isAnimationActive={false}/>)}
  </AreaChart>
 </ResponsiveContainer><div className="chart-legend">{performance.series.map(s => <span key={s.instrumentId}><i style={{ background: instrumentColor(s.instrumentId) }}/>{s.name}</span>)}{current && <span><i className="current-line"/>Current value (quotes)</span>}</div>{current && !current.chartPoint && <p className="hint">Quotes are older than your last transaction. Fetch again to update the chart.</p>}</div>;
}
