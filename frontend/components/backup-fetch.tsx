"use client";
import { useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";

export type JobItem = { instrumentId: number; status: string; reason?: string; priceEur?: string; origPrice?: string; currency?: string; fxRate?: string; fxRateDate?: string; quoteDate?: string; quoteId?: number; provider?: string };
type Backup = "ALPHAVANTAGE" | "YAHOO";
type BackupEstimate = {
  alphavantage: { requests: number; remaining: number }; yahoo?: { requests: number; remaining: number; noSymbol: number[] };
  noSymbol: number[]; budget: { blockedUntil?: string };
};
type Job = { status: string; nextRequestAt: string; items: JobItem[] };

/**
 * Backups for items Eulerpool could not price: Alpha Vantage (by linked symbol)
 * and Yahoo Finance (by ISIN, also for past dates). Neither starts by itself:
 * the user sees the request count first and has to click.
 */
export function BackupFetch({ portfolioId, date, ids, names, onItems, onDone }: {
  portfolioId: string; date: string; ids: number[]; names: Record<number, string>;
  onItems: (items: JobItem[]) => void; onDone: () => void | Promise<unknown>;
}) {
  const [av, setAV] = useState<BackupEstimate | null>(null);
  const [yahoo, setYahoo] = useState<BackupEstimate | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [running, setRunning] = useState<Backup | null>(null);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  const [priced, setPriced] = useState<number[]>([]);
  const callbacks = useRef({ onItems, onDone });
  callbacks.current = { onItems, onDone };
  const open = ids.filter(id => !priced.includes(id));
  const key = open.join(",");
  useEffect(() => {
    setAV(null); setYahoo(null);
    if (!key || jobId) return;
    const abort = new AbortController();
    const url = (provider: Backup) => `/api/portfolios/${portfolioId}/price-fetch/estimate?date=${date}&instrumentIds=${key}&provider=${provider}`;
    api<BackupEstimate>(url("ALPHAVANTAGE"), { signal: abort.signal }).then(e => { if (!abort.signal.aborted) setAV(e); }).catch(e => { if (!abort.signal.aborted) setError(e.message); });
    api<BackupEstimate>(url("YAHOO"), { signal: abort.signal }).then(e => { if (!abort.signal.aborted) setYahoo(e); }).catch(() => {});
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
  const start = async (provider: Backup) => {
    const estimate = provider === "YAHOO" ? yahoo : av;
    if (!estimate) return;
    const skip = provider === "YAHOO" ? yahoo?.yahoo?.noSymbol ?? [] : estimate.noSymbol;
    setStarting(true); setError("");
    try {
      const priceable = open.filter(id => !skip.includes(id));
      const created = await api<{ jobId: string }>(`/api/portfolios/${portfolioId}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: priceable, provider }) });
      setJob(null); setRunning(provider); setJobId(created.jobId);
    } catch (e) { setError((e as Error).message); }
    finally { setStarting(false); }
  };
  const avRequests = av?.alphavantage.requests ?? 0;
  const avRemaining = av?.alphavantage.remaining ?? 0;
  const avBlocked = !!av?.budget.blockedUntil && Date.parse(av.budget.blockedUntil) > now;
  const yRequests = yahoo?.yahoo?.requests ?? 0;
  const yRemaining = yahoo?.yahoo?.remaining ?? 0;
  const noAVSymbol = av?.noSymbol.length ?? 0;
  const busy = starting || !!jobId;
  const plural = (n: number) => `${n} ${n === 1 ? "request" : "requests"}`;
  return <div className="backup-fetch" aria-label="Backup price sources">
    <p>{open.length} {open.length === 1 ? "item" : "items"} could not be priced by Eulerpool: {open.map(id => names[id] ?? `#${id}`).join(", ")}.</p>
    <div className="backup-actions">
      {yahoo && yRequests > 0 && <button type="button" disabled={busy || yRequests > yRemaining} onClick={() => void start("YAHOO")}>{running === "YAHOO" ? "Fetching…" : `Fetch ${yRequests} remaining via Yahoo Finance (uses ${plural(yRequests)})`}</button>}
      {av && avRequests > 0 && <button type="button" disabled={busy || avBlocked || avRequests > avRemaining} onClick={() => void start("ALPHAVANTAGE")}>{starting && !running ? "Starting…" : running === "ALPHAVANTAGE" ? "Fetching…" : `Fetch ${avRequests} remaining via Alpha Vantage (uses ${plural(avRequests)})`}</button>}
    </div>
    {av && avRequests > avRemaining && <p>Only {avRemaining} Alpha Vantage {avRemaining === 1 ? "request is" : "requests are"} left today.</p>}
    {yahoo && yRequests > yRemaining && <p>Yahoo Finance is rate limited right now. Try again later.</p>}
    {avBlocked && <p>Alpha Vantage is temporarily paused.</p>}
    {av && noAVSymbol > 0 && avRequests === 0 && yRequests === 0 && <p className="muted">No backup source knows {noAVSymbol === 1 ? "this item" : "these items"}. Link an ISIN or an Alpha Vantage symbol first.</p>}
    {job && <p role="status">{job.items.filter(i => i.status !== "PENDING").length}/{job.items.length} · {job.status === "DONE" ? "Done" : running === "ALPHAVANTAGE" ? `next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt) - now) / 1000))} s` : "Fetching…"}</p>}
    {error && <p className="error" role="alert">{error}</p>}
  </div>;
}
