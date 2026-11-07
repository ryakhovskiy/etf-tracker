"use client";
import { useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";

export type JobItem = { instrumentId: number; status: string; reason?: string; priceEur?: string; origPrice?: string; currency?: string; fxRate?: string; fxRateDate?: string; quoteDate?: string; quoteId?: number; provider?: string };
type Provider = "YAHOO" | "EULERPOOL" | "ALPHAVANTAGE";
type Estimate = {
  provider?: string; noSymbol: number[]; budget: { blockedUntil?: string };
  alphavantage: { requests: number; remaining: number };
  eulerpool?: { requests: number; remaining: number; noIsin?: number[] };
  yahoo?: { requests: number; remaining: number; noSymbol: number[] };
};
type Job = { status: string; nextRequestAt: string; items: JobItem[] };

/** Job statuses of a primary source that mean "this item still needs a price". */
export const BACKUP_STATUSES = ["NEEDS_BACKUP", "NO_SYMBOL", "NOT_FOUND", "RATE_LIMITED", "ERROR"];

const NAMES: Record<Provider, string> = { YAHOO: "Yahoo Finance", EULERPOOL: "Eulerpool", ALPHAVANTAGE: "Alpha Vantage" };
// Order of the fallbacks after the primary source: Eulerpool (large quota) before Alpha Vantage (tiny budget).
const ORDER: Provider[] = ["YAHOO", "EULERPOOL", "ALPHAVANTAGE"];

/**
 * Fallbacks for items the primary source could not price. They never start by
 * themselves: each shows its request count first and needs a click.
 */
export function BackupFetch({ portfolioId, date, primary, ids, names, onItems, onDone }: {
  portfolioId: string; date: string; primary: string; ids: number[]; names: Record<number, string>;
  onItems: (items: JobItem[]) => void; onDone: () => void | Promise<unknown>;
}) {
  const [estimate, setEstimate] = useState<Estimate | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [running, setRunning] = useState<Provider | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  const [priced, setPriced] = useState<number[]>([]);
  const callbacks = useRef({ onItems, onDone });
  callbacks.current = { onItems, onDone };
  const open = ids.filter(id => !priced.includes(id));
  const key = open.join(",");
  useEffect(() => {
    setEstimate(null);
    if (!key || jobId) return;
    const abort = new AbortController();
    api<Estimate>(`/api/portfolios/${portfolioId}/price-fetch/estimate?date=${date}&instrumentIds=${key}`, { signal: abort.signal })
      .then(e => { if (!abort.signal.aborted) setEstimate(e); })
      .catch(e => { if (!abort.signal.aborted) setError(e.message); });
    return () => abort.abort();
  }, [portfolioId, date, key, jobId]);
  useEffect(() => {
    if (!jobId) return;
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const poll = async () => {
      try {
        const next = await api<Job>(`/api/price-fetch-jobs/${jobId}`);
        if (!active) return;
        setJob(next); callbacks.current.onItems(next.items);
        setPriced(p => [...new Set([...p, ...next.items.filter(i => i.status === "OK" || i.status === "FRESH").map(i => i.instrumentId)])]);
        if (next.status === "DONE") { setJobId(null); setRunning(null); await callbacks.current.onDone(); window.dispatchEvent(new window.Event("av-budget-changed")); }
        else timer = setTimeout(poll, 2000);
      } catch (e) {
        if (!active) return;
        if ((e as APIError).status === 404) { setJobId(null); setRunning(null); setError("Fetch interrupted. Prices fetched so far were saved."); return; }
        setError((e as Error).message); timer = setTimeout(poll, 2000);
      }
    };
    void poll();
    return () => { active = false; clearTimeout(timer); clearInterval(tick); };
  }, [jobId]);
  if (!open.length) return null;
  // Per provider: requests, what is left, and the items it cannot price (no symbol / ISIN).
  const info = (p: Provider) => {
    if (!estimate) return null;
    if (p === "YAHOO") return estimate.yahoo ? { requests: estimate.yahoo.requests, remaining: estimate.yahoo.remaining, skip: estimate.yahoo.noSymbol } : null;
    if (p === "EULERPOOL") return estimate.eulerpool ? { requests: estimate.eulerpool.requests, remaining: estimate.eulerpool.remaining, skip: estimate.eulerpool.noIsin ?? [] } : null;
    return { requests: estimate.alphavantage.requests, remaining: estimate.alphavantage.remaining, skip: estimate.noSymbol };
  };
  const start = async (provider: Provider) => {
    const i = info(provider);
    if (!i) return;
    setStarting(true); setError("");
    try {
      const priceable = open.filter(id => !i.skip.includes(id));
      const created = await api<{ jobId: string }>(`/api/portfolios/${portfolioId}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: priceable, provider }) });
      setJob(null); setRunning(provider); setJobId(created.jobId);
    } catch (e) { setError((e as Error).message); }
    finally { setStarting(false); }
  };
  const avBlocked = !!estimate?.budget.blockedUntil && Date.parse(estimate.budget.blockedUntil) > now;
  const busy = starting || !!jobId;
  const plural = (n: number) => `${n} ${n === 1 ? "request" : "requests"}`;
  const providers = ORDER.filter(p => p !== primary);
  const offered = providers.filter(p => (info(p)?.requests ?? 0) > 0);
  const nothing = !!estimate && offered.length === 0;
  return <div className="backup-fetch" aria-label="Backup price sources">
    <p>{open.length} {open.length === 1 ? "item" : "items"} could not be priced by {NAMES[primary as Provider] ?? "the main source"}: {open.map(id => names[id] ?? `#${id}`).join(", ")}.</p>
    <div className="backup-actions">
      {offered.map(p => {
        const i = info(p)!;
        return <button key={p} type="button" disabled={busy || (p === "ALPHAVANTAGE" && avBlocked) || i.requests > i.remaining} onClick={() => void start(p)}>
          {running === p ? "Fetching…" : starting && !running && p === offered[0] ? "Starting…" : `Fetch ${i.requests} remaining via ${NAMES[p]} (uses ${plural(i.requests)})`}
        </button>;
      })}
    </div>
    {offered.map(p => { const i = info(p)!; return i.requests > i.remaining ? <p key={p}>{p === "ALPHAVANTAGE" ? `Only ${i.remaining} Alpha Vantage ${i.remaining === 1 ? "request is" : "requests are"} left today.` : `${NAMES[p]} has no requests left right now. Try again later.`}</p> : null; })}
    {avBlocked && <p>Alpha Vantage is temporarily paused.</p>}
    {nothing && <p className="muted">No other source knows {open.length === 1 ? "this item" : "these items"}. Link a symbol or ISIN first.</p>}
    {job && <p role="status">{job.items.filter(i => i.status !== "PENDING").length}/{job.items.length} · {job.status === "DONE" ? "Done" : running === "ALPHAVANTAGE" ? `next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt) - now) / 1000))} s` : "Fetching…"}</p>}
    {error && <p className="error" role="alert">{error}</p>}
  </div>;
}
