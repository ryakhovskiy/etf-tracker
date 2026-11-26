"use client";
import { useRef, useState } from "react";
import { Area, AreaChart, CartesianGrid, ReferenceDot, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { dateLabel } from "../lib/performance";
import { chartDay, type ChartRange } from "../lib/chart-range";
import { favoriteChange } from "../lib/favorites";
import { axisPrice, buyMarkers, dayMs, periodChange, priceAxis, visibleCloses, type ChartBuy, type InstrumentChart, type Marker } from "../lib/ticker";
import { euro, units } from "../lib/transactions";
import { instrumentColor } from "../lib/colors";
import { ChartShell } from "./chart-shell";

type Group = { day: string; markers: Marker[]; x: number; y: number };

/** The tooltip of a buy marker: the date, then units, price paid and portfolio of every buy of that day. */
export function BuyTooltip({ id, day, markers }: { id?: string; day: string; markers: Marker[] }) {
  return <div className="chart-tooltip buy-tooltip" role="tooltip" id={id} data-testid="buy-tooltip">
    <strong>{dateLabel(day)}</strong>
    {markers.map(({ buy, tone }, i) => <p key={i}><i className={`buy-swatch performance-${tone}`}/>Bought {unitsLabel(buy.units)} at {euro(buy.priceEur)} · {buy.portfolioName}</p>)}
  </div>;
}

const unitsLabel = (value: string) => `${units(value)} ${value === "1" || /^1\.0*$/.test(value) ? "unit" : "units"}`;
export const buyLabel = (buy: ChartBuy) => `Buy ${dateLabel(buy.day)}: ${unitsLabel(buy.units)} at ${euro(buy.priceEur)} (${buy.portfolioName})`;

const TRIANGLE = "M0 -8 L8 6 L-8 6 Z";

export function TickerPlot({ chart, domain, dragging = false }: { chart: InstrumentChart; domain: ChartRange; dragging?: boolean }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [active, setActive] = useState<Group | null>(null);
  const color = instrumentColor(chart.instrument.id);
  const data = chart.points.map(point => ({ date: dayMs(point.day), day: point.day, close: Number(point.closeEur) }));
  const inRange = (ms: number) => ms >= domain[0] && ms <= domain[1];
  const markers = buyMarkers(chart.points, chart.buys).filter(marker => inRange(dayMs(marker.buy.day)));
  const axis = priceAxis([...visibleCloses(chart.points, domain), ...markers.map(marker => Number(marker.buy.priceEur))]);
  const asOfDay = chart.latest.asOf?.quoteDate;
  const current = asOfDay && chart.points.at(-1)?.day === asOfDay && inRange(dayMs(asOfDay)) ? dayMs(asOfDay) : null;
  const shown = dragging ? null : active;
  const open = (marker: Marker, x: number, y: number) => setActive({ day: marker.buy.day, markers: markers.filter(m => m.buy.day === marker.buy.day), x, y });
  const width = wrap.current?.clientWidth ?? 320;
  const half = Math.min(120, width / 2);
  return <div className="ticker-plot" ref={wrap} onPointerLeave={() => setActive(null)}>
    <ResponsiveContainer width="100%" height={340}>
      <AreaChart data={data} margin={{ top: 30, right: 30, bottom: 10, left: 0 }}>
        <CartesianGrid strokeDasharray="3 3" vertical={false}/>
        <XAxis minTickGap={32} tickMargin={10} tick={{fontSize:12}} dataKey="date" type="number" scale="time" domain={domain} allowDataOverflow tickFormatter={value => new Date(value).toLocaleDateString("de-DE", { ...(domain[1] - domain[0] < 90 * chartDay ? { day: "2-digit" as const } : {}), month: "short", year: "2-digit", timeZone: "UTC" })}/>
        <YAxis width="auto" domain={axis.domain} allowDataOverflow tickFormatter={axisPrice(axis.decimals)}/>
        <Tooltip active={dragging || shown ? false : undefined} content={({ active: on, payload, label }) => on && payload?.length && inRange(Number(label)) ? <div className="chart-tooltip"><strong>{dateLabel(String(payload[0].payload.day))}</strong><p><i style={{ background: color }}/>Close: {euro(String(payload[0].payload.close))}</p></div> : null}/>
        {current !== null && <ReferenceLine x={current} stroke="var(--text-muted)" strokeDasharray="3 3" label={{ value: "Current", angle: -90, position: "insideTopRight", fill: "var(--text)" }}/>}
        <Area dataKey="close" name={chart.instrument.name} type="linear" stroke={color} fill={color} fillOpacity={0.65} isAnimationActive={false}/>
        {markers.map((marker, i) => <ReferenceDot key={`${marker.buy.day}-${i}`} x={dayMs(marker.buy.day)} y={Number(marker.buy.priceEur)} ifOverflow="hidden" shape={({ cx, cy }: { cx?: number; cy?: number }) => {
          if (cx === undefined || cy === undefined) return <g/>;
          const show = () => open(marker, cx, cy);
          return <g key={i} className={`buy-marker performance-${marker.tone}`} data-testid="buy-marker" data-position={marker.position} data-tone={marker.tone} data-day={marker.buy.day}
            transform={`translate(${cx} ${cy})`} tabIndex={0} role="img" aria-label={buyLabel(marker.buy)} aria-describedby={shown?.day === marker.buy.day ? "buy-tooltip" : undefined}
            onPointerEnter={show} onFocus={show} onBlur={() => setActive(null)} onClick={show} onKeyDown={event => { if (event.key === "Escape") setActive(null); }}>
            <circle r={14} fill="transparent"/>
            <path d={TRIANGLE} fill="currentColor" stroke="var(--surface)" strokeWidth={1.5} strokeLinejoin="round"/>
          </g>;
        }}/>)}
      </AreaChart>
    </ResponsiveContainer>
    {shown && <div className={`buy-tooltip-anchor${shown.y < 110 ? " is-below" : ""}`} style={{ left: Math.min(Math.max(shown.x, half), width - half), top: shown.y }}>
      <BuyTooltip id="buy-tooltip" day={shown.day} markers={shown.markers}/>
    </div>}
  </div>;
}

/** The ticker's price chart on the shared chart card: closes in EUR with one ▲ per buy at the price paid. */
export function TickerChart({ chart }: { chart: InstrumentChart }) {
  // The backend decides the first day (first buy or 60 months back, limited by the first stored price).
  const first = dayMs(chart.from);
  const last = Math.max(dayMs(chart.to), first);
  const bounds: ChartRange = [first, last === first ? first + chartDay - 1 : last];
  const hidden = chart.buys.length - buyMarkers(chart.points, chart.buys).length;
  return <ChartShell title="Price in € and your buys" bounds={bounds} plotLabel="Price chart" helpId="ticker-chart-help"
    summary={({ range }) => <PeriodChange chart={chart} range={range}/>}
    footer={<div className="chart-legend" role="group" aria-label="Chart legend"><span><i className="buy-legend performance-positive"/>Buy at or below the day's close</span><span><i className="buy-legend performance-negative"/>Buy above the day's close</span>{hidden > 0 && <span>{hidden} {hidden === 1 ? "buy is" : "buys are"} older than the first stored price and not shown</span>}</div>}>
    {({ range, dragging }) => chart.points.length ? <TickerPlot chart={chart} domain={range} dragging={dragging}/> : <div className="chart-empty" role="status">No prices stored for this title yet.</div>}
  </ChartShell>;
}

/** "+3,75 € (+10,08 %) · 07.11.2025 – 19.05.2026": the change over the visible period. */
export function PeriodChange({ chart, range }: { chart: InstrumentChart; range: ChartRange }) {
  const change = periodChange(chart.points, range);
  const figure = favoriteChange(change);
  const from = dateLabel(new Date(range[0]).toISOString().slice(0, 10)), to = dateLabel(new Date(range[1]).toISOString().slice(0, 10));
  return <p className={`period-change num performance-${figure.tone}`} data-testid="period-change">
    <span className="sr-only">Change over the visible period: </span>{figure.text} <span className="period-change-range">· {from} – {to}</span>
  </p>;
}
