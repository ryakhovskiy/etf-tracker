"use client";
import { useEffect, useMemo, useRef, useState } from "react";
import { exShade, instrumentColor } from "../lib/colors";
import { axisEuro } from "../lib/performance";
import { amount, dayLabel, listingAmount, money, monthName, payRule, perUnit, type DividendEntry, type DividendMonth } from "../lib/dividends";

/** One stacked segment: a dividend in the bar of its ex-date or of its payment. */
type Segment = {
  key: string; month: number; side: "ex" | "pay"; entry: DividendEntry; value: number;
  x: number; y: number; width: number; height: number; top: boolean;
};

const HEIGHT = 280;
const M = { top: 14, right: 8, bottom: 30, left: 64 };
const GAP = 2; // surface gap between stacked segments
const RADIUS = 4;

const shortMonth = (month: number) => new Date(Date.UTC(2026, month - 1, 1)).toLocaleDateString("de-DE", { month: "short", timeZone: "UTC" });

/** A step of 1, 2 or 5 × 10^n giving at most 4 intervals up to max. */
function niceStep(max: number) {
  const raw = max / 4;
  const power = 10 ** Math.floor(Math.log10(raw));
  return [1, 2, 5, 10].map(f => f * power).find(s => s >= raw) ?? raw;
}

/** A rectangle with rounded top corners (the data end); square at the baseline. */
function topRounded(x: number, y: number, w: number, h: number) {
  const r = Math.min(RADIUS, w / 2, h);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

function segmentLabel(s: Segment) {
  const e = s.entry;
  const what = s.side === "ex" ? "goes ex" : e.paid ? "paid" : "expected payment";
  return `${e.ticker} ${what}: ${money(e.total ?? "0")}, ex-date ${dayLabel(e.exDate)}, pay-date ${dayLabel(e.payDate)}`;
}

function Tooltip({ segment, month, left, top }: { segment: Segment; month: DividendMonth; left: number; top: number }) {
  const e = segment.entry;
  const ex = segment.side === "ex";
  return <div className="dividend-chart-tooltip" role="tooltip" style={{ left, top }}>
    <strong>{e.ticker} · {ex ? "Ex-date" : e.paid ? "Paid" : "Expected payment"}</strong>
    <p className="tooltip-name">{e.shortName}</p>
    <dl>
      <div><dt>Ex-date</dt><dd>{dayLabel(e.exDate)}</dd></div>
      <div><dt>Pay-date</dt><dd>{dayLabel(e.payDate)}</dd></div>
      <div className="tooltip-rule"><dt>Estimated as</dt><dd>{payRule(e)}</dd></div>
      <div><dt>Per share</dt><dd>{listingAmount(e.perUnitOrig, e.currency)}{e.perUnitEur && e.currency !== "EUR" ? ` · ${perUnit(e.perUnitEur)}` : ""}</dd></div>
      <div><dt>Units</dt><dd>{amount(e.units)}</dd></div>
      <div><dt>Total</dt><dd><strong>{money(e.total ?? "0")}</strong></dd></div>
      <div className="tooltip-month"><dt>{monthName(segment.month)} {ex ? "ex-dates" : "payments"}</dt><dd>{money(ex ? month.exTotal : month.total)}</dd></div>
    </dl>
  </div>;
}

/** 24 bars for the year: per month, the dividends going ex (translucent gray) and
 * the payments (instrument colors; expected ones hatched), stacked per dividend. */
export function DividendChart({ year, months }: { year: number; months: DividendMonth[] }) {
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(960);
  const [active, setActive] = useState<string | null>(null);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const measure = () => { if (el.clientWidth > 0) setWidth(el.clientWidth); };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, []);

  const { segments, ticks, scale, band } = useMemo(() => {
    const valueOf = (e: DividendEntry) => (e.total === null ? 0 : Number(e.total));
    const max = Math.max(0, ...months.map(m => Math.max(Number(m.total), Number(m.exTotal))));
    const step = max > 0 ? niceStep(max) : 1;
    const top = Math.max(step, Math.ceil(max / step) * step);
    const plot = HEIGHT - M.top - M.bottom;
    const scale = (v: number) => (v / top) * plot;
    const band = (width - M.left - M.right) / 12;
    const barWidth = Math.max(4, Math.min(24, (band - 10) / 2));
    const segments: Segment[] = [];
    for (const m of months) {
      const center = M.left + band * (m.month - 1) + band / 2;
      for (const side of ["ex", "pay"] as const) {
        const x = side === "ex" ? center - barWidth - 1 : center + 1; // 2px air between the pair
        const list = (side === "ex" ? m.exEntries : m.entries).filter(e => valueOf(e) > 0);
        let base = 0; // stacked bottom-up in the calendar's order (by date)
        list.forEach((entry, i) => {
          const value = valueOf(entry);
          const h = scale(value);
          const y = HEIGHT - M.bottom - scale(base) - h;
          base += value;
          segments.push({ key: `${side}-${entry.kind}-${entry.instrumentId}-${entry.exDate}`, month: m.month, side, entry, value, x, y, width: barWidth, height: Math.max(1, h), top: i === list.length - 1 });
        });
      }
    }
    const ticks = Array.from({ length: Math.round(top / step) + 1 }, (_, i) => i * step);
    return { segments, ticks, scale, band };
  }, [months, width]);

  const current = segments.find(s => s.key === active) ?? null;
  const instruments = useMemo(() => {
    const seen = new Map<number, string>();
    for (const s of segments) if (!seen.has(s.entry.instrumentId)) seen.set(s.entry.instrumentId, s.entry.ticker);
    return [...seen].sort((a, b) => a[0] - b[0]);
  }, [segments]);

  useEffect(() => {
    if (!active) return;
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") setActive(null); };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [active]);

  // Whole euros on the axis unless the steps are below 1 €.
  const stepLabel = (v: number) => (ticks.length > 1 && ticks[1] < 1 ? money(v.toFixed(2)) : axisEuro(v));
  const baseline = HEIGHT - M.bottom;
  return <figure className="dividend-chart chart-card" aria-labelledby="dividend-chart-title">
    <h2 id="dividend-chart-title">Dividends by month, {year}: ex-dates and payments</h2>
    {segments.length === 0 ? <p className="month-empty">No dividends in {year}.</p> : <>
      <div className="dividend-chart-plot" ref={box} onMouseLeave={() => setActive(null)}>
        <svg width={width} height={HEIGHT} role="group" aria-label={`Dividends by month in ${year}`} className={current ? "has-active" : undefined}>
          <defs>
            {instruments.map(([id]) => <pattern key={id} id={`div-hatch-${id}`} patternUnits="userSpaceOnUse" width="6" height="6" patternTransform="rotate(45)">
              <rect width="6" height="6" fill={instrumentColor(id)} fillOpacity="0.3" />
              <line x1="0" y1="0" x2="0" y2="6" stroke={instrumentColor(id)} strokeWidth="2.5" />
            </pattern>)}
          </defs>
          <g className="chart-grid" aria-hidden="true">
            {ticks.map(t => <g key={t}>
              <line x1={M.left} x2={width - M.right} y1={baseline - scale(t)} y2={baseline - scale(t)} />
              <text x={M.left - 8} y={baseline - scale(t)} dy="0.32em" textAnchor="end">{stepLabel(t)}</text>
            </g>)}
            {months.map(m => <text key={m.month} className="month-label" x={M.left + band * (m.month - 1) + band / 2} y={baseline + 18} textAnchor="middle">{shortMonth(m.month)}</text>)}
          </g>
          {segments.map(s => {
            // The 2px surface gap sits on top of every segment below another.
            const h = s.top ? s.height : Math.max(1, s.height - GAP);
            const y = s.top ? s.y : s.y + (s.height - h);
            const ex = s.side === "ex";
            const fill = ex ? `var(--div-ex-${exShade(s.entry.instrumentId)})` : s.entry.paid ? instrumentColor(s.entry.instrumentId) : `url(#div-hatch-${s.entry.instrumentId})`;
            const classes = ["seg", ex ? "seg-ex" : s.entry.paid ? "seg-paid" : "seg-expected", s.key === active ? "active" : ""].filter(Boolean).join(" ");
            const show = () => setActive(s.key);
            return <g key={s.key} className={classes} data-key={s.key} tabIndex={0} role="img" aria-label={segmentLabel(s)}
              onMouseEnter={show} onFocus={show} onBlur={() => setActive(a => (a === s.key ? null : a))} onClick={() => setActive(a => (a === s.key ? null : s.key))}>
              {/* The hit target is wider and at least 8px tall, beyond the painted mark. */}
              <rect className="seg-hit" x={s.x - 2} y={Math.min(y, y + h / 2 - 4)} width={s.width + 4} height={Math.max(8, h)} fill="transparent" />
              {/* style, not the fill attribute: var() tokens are reliable in CSS only. */}
              {s.top ? <path className="seg-mark" d={topRounded(s.x, y, s.width, h)} style={{ fill }} /> : <rect className="seg-mark" x={s.x} y={y} width={s.width} height={h} style={{ fill }} />}
            </g>;
          })}
          <line className="chart-baseline" x1={M.left} x2={width - M.right} y1={baseline} y2={baseline} aria-hidden="true" />
        </svg>
        {current && <Tooltip segment={current} month={months[current.month - 1]}
          left={Math.min(Math.max(current.x + current.width / 2, 120), width - 120)} top={Math.max(0, current.y)} />}
      </div>
      <div className="dividend-chart-legend">
        <span><i className="legend-ex" />Ex-date (left bar)</span>
        <span><i className="legend-paid" />Paid (right bar)</span>
        <span><i className="legend-expected" />Expected payment</span>
        {instruments.map(([id, ticker]) => <span key={id}><i style={{ background: instrumentColor(id) }} />{ticker}</span>)}
      </div>
    </>}
  </figure>;
}
