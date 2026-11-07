"use client";
import { useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { api, APIError } from "../lib/api";
import {
  amount, berlinYearMonth, dayLabel, exLabel, fxLabel, listingAmount, money, monthName, percent, perUnit, syncedLabel, warningText,
  type DividendCalendar, type DividendEntry, type DividendSyncJob,
} from "../lib/dividends";
import { usePortfolioUpdates } from "./portfolio-updates";

const POLL_MS = 2000;

function parseYear(raw: string | null | undefined) {
  return raw && /^\d{4}$/.test(raw) ? Number(raw) : null;
}

/** A value that may be missing; the tooltip gives the reason. */
function Value({ value, reason, strong }: { value: string | null; reason: string; strong?: boolean }) {
  if (value !== null) return strong ? <strong>{value}</strong> : <span>{value}</span>;
  return <span className="missing" title={reason}>–<span className="sr-only"> (not available: {reason})</span></span>;
}

function EntryDetails({ entry, id, close }: { entry: DividendEntry; id: string; close: () => void }) {
  const forecast = entry.kind === "FORECAST";
  return <div className="dividend-popover" role="dialog" aria-modal="false" aria-label={`${entry.ticker} dividend details`} id={id}>
    <div className="popover-head"><strong>{entry.ticker}</strong><button type="button" className="popover-close" aria-label="Close details" onClick={close}>×</button></div>
    <p className="popover-name">{entry.shortName}</p>
    <dl>
      <div><dt>Ex-date</dt><dd>{dayLabel(entry.exDate)}</dd></div>
      <div><dt>Per share</dt><dd>{listingAmount(entry.perUnitOrig, entry.currency)}</dd></div>
      <div><dt>FX rate</dt><dd>{entry.fxRate && entry.fxRateDate ? `${fxLabel(entry.fxRate, entry.currency)} (ECB ${dayLabel(entry.fxRateDate)})` : entry.warning ? warningText(entry) : "not needed (EUR)"}</dd></div>
      <div><dt>{forecast ? `Latest close${entry.closeDate ? ` (${dayLabel(entry.closeDate)})` : ""}` : "Price before ex-date"}</dt><dd>{entry.close ? listingAmount(entry.close, entry.currency) : "–"}</dd></div>
      <div><dt>{forecast ? "Units held now" : "Units held"}</dt><dd>{amount(entry.units)}</dd></div>
      {forecast && entry.basisExDate && <div><dt>Forecast</dt><dd>based on {dayLabel(entry.basisExDate)}</dd></div>}
      <div><dt>Source</dt><dd>Yahoo Finance</dd></div>
    </dl>
  </div>;
}

function Entry({ entry, open, toggle }: { entry: DividendEntry; open: boolean; toggle: () => void }) {
  const popover = useId();
  const forecast = entry.kind === "FORECAST";
  const reason = warningText(entry);
  const classes = ["dividend-entry", forecast ? "forecast" : "", entry.held ? "" : "not-held"].filter(Boolean).join(" ");
  return <li className={classes}>
    <button type="button" className="entry-button" aria-expanded={open} aria-controls={open ? popover : undefined} onClick={toggle}>
      <span className="entry-line">
        <strong className="entry-ticker">{entry.ticker}</strong>
        <span className="entry-name" title={entry.shortName}>{entry.shortName}</span>
        {forecast && <span className="state-pill pill-expected">expected</span>}
        {!entry.held && <span className="state-pill pill-not-held">not held</span>}
      </span>
      <span className="entry-figures">
        <span className="entry-ex">{exLabel(entry.exDate)}</span>
        <Value value={entry.yieldPct === null ? null : percent(entry.yieldPct)} reason="No price before the ex-date." />
        <Value value={entry.perUnitEur === null ? null : perUnit(entry.perUnitEur)} reason={reason} />
        <span className="entry-units">× {amount(entry.units)}</span>
        <Value strong value={entry.total === null ? null : money(entry.total)} reason={reason} />
      </span>
    </button>
    {open && <EntryDetails entry={entry} id={popover} close={toggle} />}
  </li>;
}

function SkeletonGrid() {
  return <ol className="dividend-grid" aria-hidden="true">{Array.from({ length: 12 }, (_, i) => <li key={i} className="month-card skeleton-card"><span /><span /><span /></li>)}</ol>;
}

export function Dividends({ id }: { id: string }) {
  const { activeTab, revision } = usePortfolioUpdates();
  const active = activeTab === "dividends";
  const router = useRouter();
  const params = useSearchParams();
  const urlYear = parseYear(params?.get("year"));
  const [year, setYear] = useState<number | null>(null);
  const [data, setData] = useState<DividendCalendar | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [syncError, setSyncError] = useState("");
  const [jobId, setJobId] = useState<string | null>(null);
  const [job, setJob] = useState<DividendSyncJob | null>(null);
  const [starting, setStarting] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const requestVersion = useRef(0);
  const autoSynced = useRef(false);

  // The URL owns the year while the tab is open: reload and back navigation keep it.
  useEffect(() => {
    if (active) setYear(urlYear ?? berlinYearMonth().year);
    else autoSynced.current = false; // the next tab open checks for stale data again
  }, [active, urlYear]);

  const load = useCallback(async (wanted: number) => {
    const version = ++requestVersion.current;
    setLoading(true);
    try {
      const next = await api<DividendCalendar>(`/api/portfolios/${id}/dividends?year=${wanted}`);
      if (version !== requestVersion.current) return null;
      setData(next); setError("");
      return next;
    } catch (e) {
      if (version === requestVersion.current) setError((e as Error).message);
      return null;
    } finally {
      if (version === requestVersion.current) setLoading(false);
    }
  }, [id]);

  const startSync = useCallback(async (scope: "ALL" | "STALE") => {
    setStarting(true); setSyncError("");
    try {
      const created = await api<{ jobId: string }>(`/api/portfolios/${id}/dividend-sync`, { method: "POST", body: JSON.stringify({ scope }) });
      setJob(null); setJobId(created.jobId);
    } catch (e) {
      const err = e as APIError;
      const running = (err.details as { jobId?: string } | undefined)?.jobId;
      if (err.code === "SYNC_RUNNING" && running) setJobId(running);
      else setSyncError(err.message);
    } finally {
      setStarting(false);
    }
  }, [id]);

  // Cached data renders at once; stale instruments are then synced in the background.
  useEffect(() => {
    if (!active || year === null) return;
    let cancelled = false;
    void load(year).then(next => {
      if (cancelled || !next || autoSynced.current) return;
      autoSynced.current = true;
      if (next.sync.jobId) setJobId(next.sync.jobId);
      else if (next.sync.stale.length > 0) void startSync("STALE");
    });
    return () => { cancelled = true; };
  }, [active, year, load, startSync]);

  // Transactions change the units held: reload what has been shown before.
  const loadedYear = useRef<number | null>(null);
  loadedYear.current = data?.year ?? null;
  useEffect(() => {
    if (revision > 0 && loadedYear.current !== null) void load(loadedYear.current);
  }, [revision, load]);

  // Poll the job every 2 s; reload the calendar when it is done.
  const yearRef = useRef(year); yearRef.current = year;
  useEffect(() => {
    if (!jobId) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const finish = async () => {
      setJobId(null);
      if (yearRef.current !== null) await load(yearRef.current);
    };
    const poll = async () => {
      try {
        const next = await api<DividendSyncJob>(`/api/dividend-sync-jobs/${jobId}`);
        if (!alive) return;
        setJob(next);
        if (next.status === "DONE") await finish();
        else timer = setTimeout(poll, POLL_MS);
      } catch (e) {
        if (!alive) return;
        const err = e as APIError;
        if (err.status === 404) await finish(); // finished long ago or the server restarted
        else { setSyncError(err.message); timer = setTimeout(poll, POLL_MS); }
      }
    };
    void poll();
    return () => { alive = false; clearTimeout(timer); };
  }, [jobId, load]);

  // Close the popover with Escape or a click elsewhere.
  useEffect(() => {
    if (!open) return;
    const key = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(null); };
    const click = (e: MouseEvent) => { if (!(e.target as Element).closest?.(".dividend-entry")) setOpen(null); };
    document.addEventListener("keydown", key);
    document.addEventListener("click", click);
    return () => { document.removeEventListener("keydown", key); document.removeEventListener("click", click); };
  }, [open]);

  const go = (next: number) => {
    setOpen(null);
    setYear(next);
    router.push(`/portfolios/${id}/dividends?year=${next}`, { scroll: false });
  };

  const shown = data && data.year === year ? data : null;
  const bounds = data?.bounds;
  const today = berlinYearMonth();
  const syncing = !!jobId || starting;
  return <section className="dividends" aria-busy={loading || syncing}>
    <div className="dividends-header">
      <div className="year-nav">
        <button type="button" className="icon-button" aria-label="Previous year" disabled={!bounds || year === null || year <= bounds.minYear} onClick={() => year !== null && go(year - 1)}>‹</button>
        <h2 aria-live="polite">{year ?? ""}</h2>
        <button type="button" className="icon-button" aria-label="Next year" disabled={!bounds || year === null || year >= bounds.maxYear} onClick={() => year !== null && go(year + 1)}>›</button>
      </div>
      {shown && <dl className="dividend-summary">
        <div><dt>Received</dt><dd>{money(shown.totals.received)}</dd></div>
        <div><dt>Expected</dt><dd>{money(shown.totals.expected)}</dd></div>
        <div><dt>Total</dt><dd>{money(shown.totals.total)}</dd></div>
      </dl>}
      <div className="dividend-sync">
        <span className="muted">{data?.sync.lastSyncedAt ? `Dividend data updated ${syncedLabel(data.sync.lastSyncedAt)}` : "Dividend data not loaded yet"} · Yahoo Finance</span>
        <button type="button" disabled={syncing || !data} onClick={() => void startSync("ALL")}>{syncing ? "Refreshing…" : "Refresh dividends"}</button>
        {syncing && <span className="sync-progress" role="status">{job && job.status !== "DONE" ? `${job.done}/${job.total} instruments` : "Starting…"}</span>}
      </div>
    </div>
    {error && <p className="error" role="alert">{error}</p>}
    {syncError && <p className="error" role="alert">{syncError}</p>}
    {data && data.sync.errors.length > 0 && <div className="dividend-banner" role="alert"><ul>{data.sync.errors.map(e => <li key={e.instrumentId}>
      Couldn&apos;t load dividends for {e.label}. {e.lastSyncedAt ? `Showing data from ${syncedLabel(e.lastSyncedAt)}.` : "No cached data yet."} <span className="banner-reason">{e.message}</span>
    </li>)}</ul></div>}
    {!shown ? (error && !loading ? <p><button type="button" onClick={() => go(today.year)}>Show {today.year}</button></p> : <div role="status" aria-label="Loading dividends"><span className="sr-only">Loading dividends…</span><SkeletonGrid /></div>) :
      <ol className="dividend-grid">{shown.months.map(m => {
        const current = shown.year === today.year && m.month === today.month;
        return <li key={m.month} className={`month-card${current ? " current" : ""}`} aria-current={current ? "date" : undefined} aria-label={`${monthName(m.month)} ${shown.year}`}>
          <header><h3>{monthName(m.month)}</h3><strong>{money(m.total)}</strong></header>
          {m.entries.length === 0 ? <p className="month-empty">No dividends</p> : <ul className="dividend-entries">{m.entries.map(e => {
            const key = `${e.kind}-${e.instrumentId}-${e.exDate}`;
            return <Entry key={key} entry={e} open={open === key} toggle={() => setOpen(open === key ? null : key)} />;
          })}</ul>}
        </li>;
      })}</ol>}
  </section>;
}
