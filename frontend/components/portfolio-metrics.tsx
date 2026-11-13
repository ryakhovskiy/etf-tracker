"use client";
import { useEffect, useId, useRef, useState } from "react";
import { dateLabel, performanceDisplay } from "../lib/performance";
import { reasonText, sourceLabel, type Money, type PortfolioSummary } from "../lib/portfolio-summary";
import { euro } from "../lib/transactions";

/** "14:32" for a time today, "12.11. 14:32" for an earlier day, in Berlin time (like every other timestamp). */
export function timeLabel(iso: string, now = new Date()) {
  const at = new Date(iso);
  const day = (date: Date) => date.toLocaleDateString("de-DE", { timeZone: "Europe/Berlin", day: "2-digit", month: "2-digit", year: "numeric" });
  const time = at.toLocaleTimeString("de-DE", { timeZone: "Europe/Berlin", hour: "2-digit", minute: "2-digit" });
  return day(at) === day(now) ? time : `${day(at).slice(0, 6)} ${time}`;
}

/** Gain/loss text always carries its sign and ▲/▼; the tone class only adds the colour. */
export function Gain({ money, label }: { money: Money | null; label: string }) {
  if (!money) return <span className="metric-value muted" aria-label={`${label}: not available`}>–</span>;
  const display = performanceDisplay(money.amount, money.percent);
  return <span className={`metric-value performance-${display.tone}`}>{display.text}</span>;
}

/**
 * "partial" badge with the reasons. A button: click/tap toggles the explanation, Escape and a
 * tap elsewhere dismiss it (focus stays on the button), hover shows it for pointer users.
 */
export function PartialHint({ reasons }: { reasons: PortfolioSummary["reasons"] }) {
  const text = reasons.map(reason => reasonText[reason] ?? reason);
  // `pinned` is the explicit toggle (tap, click, Enter/Space); `hover` is a mouse passing over.
  const [pinned, setPinned] = useState(false);
  const [hover, setHover] = useState(false);
  const open = pinned || hover;
  const close = () => { setPinned(false); setHover(false); };
  const root = useRef<HTMLButtonElement>(null);
  const tooltipId = useId();
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) close(); };
    // Escape dismisses the explanation wherever focus or the pointer is (WCAG 1.4.13).
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("pointerdown", outside); document.removeEventListener("keydown", escape); };
  }, [open]);
  return <button ref={root} type="button" className="partial-hint ghost" aria-expanded={open} aria-describedby={open ? tooltipId : undefined}
    aria-label={`Partial figures: ${text.join("; ")}`}
    onClick={() => { if (open) close(); else setPinned(true); }}
    onPointerEnter={event => { if (event.pointerType === "mouse") setHover(true); }}
    onPointerLeave={() => setHover(false)}>
    <span className="badge">partial</span>
    <span className="partial-tooltip" role="tooltip" id={tooltipId} hidden={!open}>{text.map(line => <span key={line}>{line}</span>)}</span>
  </button>;
}

/** "as of 14:32 · Yahoo real-time" (oldest used quote); the quote's date when it has no time. */
export function AsOf({ summary }: { summary: PortfolioSummary }) {
  const { asOf } = summary;
  if (!asOf) return <span>No quotes yet</span>;
  const when = asOf.quoteTime ? timeLabel(asOf.quoteTime) : dateLabel(asOf.quoteDate);
  const source = sourceLabel(asOf);
  return <span>as of {when}{source ? ` · ${source}` : ""}</span>;
}

/** Metrics inside a portfolio tile: hero value, performance, change vs yesterday, as-of line. */
export function PortfolioMetrics({ summary, updating }: { summary: PortfolioSummary; updating: boolean }) {
  if (summary.positionCount === 0) {
    return <div className="portfolio-metrics" data-testid="portfolio-metrics"><p className="muted">No positions yet</p></div>;
  }
  return <div className={`portfolio-metrics${updating ? " is-updating" : ""}`} data-testid="portfolio-metrics">
    <dl className="metric-list">
      <div className="portfolio-hero">
        <dt className="kpi-label">Current value</dt>
        <dd className="kpi-value num">{summary.currentValue === null ? "Unavailable" : euro(summary.currentValue)}{summary.partial && <PartialHint reasons={summary.reasons} />}</dd>
      </div>
      <div className="metric-row"><dt className="metric-label">Performance</dt><dd><Gain money={summary.performance} label="Performance" /></dd></div>
      <div className="metric-row"><dt className="metric-label">Change vs yesterday</dt><dd><Gain money={summary.change} label="Change vs yesterday" />{summary.changePartial && <PartialHint reasons={summary.reasons} />}</dd></div>
    </dl>
    <p className="portfolio-asof"><AsOf summary={summary} /></p>
  </div>;
}

export function MetricsSkeleton() {
  return <div className="portfolio-metrics" aria-hidden="true" data-testid="portfolio-metrics-skeleton">
    <span className="skeleton-line is-hero" /><span className="skeleton-line" /><span className="skeleton-line" /><span className="skeleton-line is-short" />
  </div>;
}
