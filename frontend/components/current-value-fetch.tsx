"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";
import { today, type Performance } from "../lib/transactions";
import { BackupFetch, BACKUP_STATUSES } from "./backup-fetch";

type Budget = { globalRemaining: number; userRemaining: number; nextSlotAt: string; blockedUntil?: string };
type Estimate = { requestsNeeded: number; fresh: number[]; noSymbol: number[]; budget: Budget; provider?: string; eulerpool?: { requests: number; remaining: number; noIsin?: number[] }; yahoo?: { requests: number; remaining: number; noSymbol: number[] } };
type Job = { status: string; nextRequestAt: string; items: { instrumentId: number; status: string }[] };

export function CurrentValueFetch({ id, performance, reload }: { id: string; performance: Performance; reload: () => Promise<unknown> }) {
 const [estimate, setEstimate] = useState<Estimate | null>(null);
 const [jobId, setJobId] = useState<string | null>(null);
 const [job, setJob] = useState<Job | null>(null);
 const [starting, setStarting] = useState(false);
 const [error, setError] = useState("");
 const [now, setNow] = useState(Date.now());
 const [version, setVersion] = useState(0);
 const [backup, setBackup] = useState<number[]>([]);
 const reloadRef = useRef(reload); reloadRef.current = reload;
 const ids = (performance.current ? performance.current.items.map(i => i.instrumentId) : performance.series.map(s => s.instrumentId)).sort((a,b) => a-b).join(",");
 const date = today();
 const storageKey = `current-value-fetch:${id}`;
 useEffect(() => {
  setJobId(sessionStorage.getItem(storageKey));
  const timer = setInterval(() => setNow(Date.now()), 1000);
  const refresh = () => setVersion(v => v+1);
  window.addEventListener("av-budget-changed", refresh);
  window.addEventListener("focus", refresh);
  return () => { clearInterval(timer); window.removeEventListener("av-budget-changed", refresh); window.removeEventListener("focus", refresh); };
 }, [storageKey]);
 useEffect(() => {
  setEstimate(null);
  if (!ids) return;
  const abort = new AbortController();
  api<Estimate>(`/api/portfolios/${id}/price-fetch/estimate?date=${date}&instrumentIds=${ids}`, { signal: abort.signal }).then(e => {
   if (!abort.signal.aborted && typeof e.requestsNeeded === "number") { setEstimate(e); setError(""); }
  }).catch(e => { if (!abort.signal.aborted) setError(e.message); });
  return () => abort.abort();
 }, [id, ids, date, version, performance]);
 // Refresh the estimate when an exhausted rolling budget becomes available again.
 useEffect(() => {
  if (!estimate || estimate.requestsNeeded === 0 || Math.min(estimate.budget.userRemaining, estimate.budget.globalRemaining) > 0) return;
  const next = Date.parse(estimate.budget.nextSlotAt);
  if (!Number.isFinite(next)) return;
  const timer = setTimeout(() => setVersion(v => v+1), Math.min(2147483647, Math.max(1000, next-Date.now()+100)));
  return () => clearTimeout(timer);
 }, [estimate]);
 useEffect(() => {
  if (!jobId) return;
  let active = true;
  let timer: ReturnType<typeof setTimeout>;
  const finish = async () => {
   sessionStorage.removeItem(storageKey); setJobId(null);
   await reloadRef.current();
   window.dispatchEvent(new window.Event("av-budget-changed"));
  };
  const poll = async () => {
   try {
    const next = await api<Job>(`/api/price-fetch-jobs/${jobId}`);
    if (!active) return;
    setJob(next);
    if (next.status === "DONE") { setBackup(next.items.filter(i => (mode === "ALPHAVANTAGE" ? ["NEEDS_BACKUP"] : BACKUP_STATUSES).includes(i.status)).map(i => i.instrumentId)); await finish(); }
    else timer = setTimeout(poll, 2000);
   } catch (e) {
    if (!active) return;
    const err = e as APIError;
    setError(err.status === 404 ? "Fetch interrupted. Prices fetched so far were saved." : err.message);
    if (err.status === 404) {
     try { await finish(); } catch (recoveryError) { if (active) setError((recoveryError as Error).message); }
    } else timer = setTimeout(poll, 2000);
   }
  };
  void poll();
  return () => { active = false; clearTimeout(timer); };
 }, [jobId, storageKey]);
 const start = async () => {
  setStarting(true); setError(""); setBackup([]);
  try {
   const created = await api<{ jobId: string }>(`/api/portfolios/${id}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: ids.split(",").map(Number), priority: "HOLDINGS_VALUE" }) });
   sessionStorage.setItem(storageKey, created.jobId); setJob(null); setJobId(created.jobId);
  } catch (e) { setError((e as Error).message); setVersion(v => v+1); }
  finally { setStarting(false); }
 };
 // The primary provider comes with the estimate; older servers without it mean Alpha Vantage.
 const mode = estimate?.provider ?? (estimate?.eulerpool ? "EULERPOOL" : "ALPHAVANTAGE");
 const yahoo = mode === "YAHOO";
 const eulerpool = mode === "EULERPOOL" || yahoo; // both are request-counted, quota-based sources
 const sourceName = yahoo ? "Yahoo" : "Eulerpool";
 const epRequests = (yahoo ? estimate?.yahoo?.requests : estimate?.eulerpool?.requests) ?? 0;
 const epRemaining = (yahoo ? estimate?.yahoo?.remaining : estimate?.eulerpool?.remaining) ?? 0;
 const remaining = estimate ? Math.min(estimate.budget.userRemaining, estimate.budget.globalRemaining) : 0;
 const missingSymbols = yahoo ? estimate?.yahoo?.noSymbol ?? [] : eulerpool ? estimate?.eulerpool?.noIsin ?? [] : estimate?.noSymbol ?? [];
 const allUnlinked = !!estimate && missingSymbols.length === ids.split(",").filter(Boolean).length && missingSymbols.length > 0;
 const blocked = !!estimate?.budget.blockedUntil && Date.parse(estimate.budget.blockedUntil) > now;
 const limited = !eulerpool && !!estimate && estimate.requestsNeeded > 0 && (remaining === 0 || blocked);
 const epLimited = eulerpool && epRequests > 0 && epRemaining === 0;
 const nextAt = estimate ? new Date(Math.max(Date.parse(estimate.budget.nextSlotAt), Date.parse(estimate.budget.blockedUntil ?? "") || 0)).toLocaleString("de-DE", { timeZone: "Europe/Berlin", dateStyle: "short", timeStyle: "short" }) : "";
 const label = !estimate ? "Fetch current value (estimating…)" : eulerpool ? (epRequests === 0 ? "Up to date (uses 0 requests)" : `Fetch current value (${epRequests} ${sourceName} ${epRequests === 1 ? "request" : "requests"})`) : allUnlinked ? "Fetch current value" : estimate.requestsNeeded === 0 ? `${missingSymbols.length ? "Refresh current value" : "Up to date"} (uses 0 requests)` : estimate.requestsNeeded > remaining ? `Fetch ${remaining} of ${estimate.requestsNeeded} (budget limit)` : `Fetch current value (uses ${estimate.requestsNeeded} requests)`;
 const updated = job?.items.filter(i => ["OK", "FRESH"].includes(i.status)).length ?? 0;
 const failures = job?.items.filter(i => !["PENDING", "OK", "FRESH", "NO_SYMBOL", "NEEDS_BACKUP", ...(mode === "ALPHAVANTAGE" ? [] : BACKUP_STATUSES)].includes(i.status)) ?? [];
 return <div className="current-fetch" aria-label="Market prices">
  <div className="fetch-toolbar">
   <div className="fetch-description"><strong>Market prices</strong><p>{allUnlinked ? eulerpool ? "No symbols are linked. Link your holdings to fetch current value." : "No price symbols are linked. Link your holdings to fetch current value." : "Update current value using the latest available quotes."}</p>{eulerpool && <small className="muted">Data by {yahoo ? "Yahoo Finance" : "Eulerpool"}</small>}{allUnlinked && <Link href={`/portfolios/${id}/transactions`}>Link symbols in Transactions</Link>}</div>
   <span className="fetch-action" title={limited ? `Alpha Vantage limit reached, next request possible at ${nextAt}` : allUnlinked ? "Link price symbols in Transactions first." : undefined}><button type="button" disabled={!ids || !estimate || allUnlinked || limited || epLimited || starting || !!jobId} title={limited ? `Alpha Vantage limit reached, next request possible at ${nextAt}` : allUnlinked ? eulerpool ? "Link ISINs in Transactions first." : "Link price symbols in Transactions first." : undefined} onClick={() => void start()}>{starting ? "Starting…" : jobId ? "Fetching prices…" : label}</button></span>
  </div>
  {epLimited && <p className="fetch-notice">{sourceName} request budget is used up for now.</p>}
  {limited && <p className="fetch-notice">Alpha Vantage limit reached. Next request possible at {nextAt}.</p>}
  {!eulerpool && !limited && estimate && estimate.requestsNeeded > remaining && <p className="fetch-notice">The available budget covers {remaining} of {estimate.requestsNeeded} requests. Largest holdings are updated first.</p>}
  {job && <p className="fetch-progress" role="status">{job.status === "DONE" ? updated ? `${updated} of ${job.items.length} holdings updated` : "No prices updated. Link symbols or review the fetch results below." : `${job.items.filter(i => i.status !== "PENDING").length}/${job.items.length} · next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt)-now)/1000))} s`}</p>}
  {failures.length > 0 && <details className="fetch-details"><summary>{failures.length} {failures.length === 1 ? "price could" : "prices could"} not be updated</summary><ul>{failures.map(i => <li key={i.instrumentId}>{performance.series.find(s => s.instrumentId === i.instrumentId)?.name}: {i.status.replaceAll("_", " ")}</li>)}</ul></details>}
  {missingSymbols.length > 0 && <details className="fetch-details"><summary>{missingSymbols.length} {missingSymbols.length === 1 ? "holding needs" : "holdings need"} {yahoo ? "a Yahoo symbol" : eulerpool ? "an ISIN" : "a price symbol"}</summary><ul>{missingSymbols.map(instrumentId => <li key={instrumentId}><span>{performance.series.find(s => s.instrumentId === instrumentId)?.name}</span><Link href={`/portfolios/${id}/transactions`}>Link symbol in Transactions tab</Link></li>)}</ul></details>}
  {error && <p className="error" role="alert">{error}</p>}
  {!jobId && backup.length > 0 && <BackupFetch portfolioId={id} date={date} primary={mode} ids={backup} names={Object.fromEntries(performance.series.map(x => [x.instrumentId, x.name]))} onItems={() => {}} onDone={async () => { await reloadRef.current(); }} />}
 </div>;
}
