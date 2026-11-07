"use client";
import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { api, APIError, OnlineInstrument } from "../lib/api";
import { editRow, fetchable, fetchedSource, fetchDateAllowed, PlanRow, today } from "../lib/transactions";
import { BackupFetch, type JobItem } from "./backup-fetch";
import { Modal } from "./portfolios";
import { usePortfolioUpdates } from "./portfolio-updates";

type Budget = { globalRemaining: number; userRemaining: number; userLimit: number; nextSlotAt: string; blockedUntil?: string };
type YahooEstimate = { requests: number; remaining: number; historical: boolean; noSymbol: number[] };
type Estimate = { yahoo?: YahooEstimate; requestsNeeded: number; fresh: number[]; noSymbol: number[]; budget: Budget; provider?: string; eulerpool?: { requests: number; remaining: number } };
type QuoteItem = JobItem;
type Job = { status: "QUEUED" | "RUNNING" | "DONE"; nextRequestAt: string; items: QuoteItem[] };
type Reload = (recoverPrices?: boolean) => Promise<unknown>;
export function PriceFetch({ id, date, rows, selected, setRows, reload, busy, onFetching }: {
 id: string; date: string; rows: PlanRow[]; selected: number[];
 setRows: Dispatch<SetStateAction<PlanRow[] | null>>; reload: Reload; busy: boolean; onFetching: (fetching: boolean) => void;
}) {
 const { changed } = usePortfolioUpdates();
 const [budget, setBudget] = useState<Budget | null>(null);
 const [estimate, setEstimate] = useState<Estimate | null>(null);
 const [jobId, setJobId] = useState<string | null>(null);
 const [job, setJob] = useState<Job | null>(null);
 const [error, setError] = useState("");
 const [starting, setStarting] = useState(false);
 const [now, setNow] = useState(Date.now());
 const [backup, setBackup] = useState<number[]>([]);
 const [yahooEstimate, setYahooEstimate] = useState<YahooEstimate | null>(null);
 const [jobProvider, setJobProvider] = useState<string>("");
 // Draft setters belong to the parent; polling always uses their current versions.
 const callbacks = useRef({ setRows, reload, onFetching, changed });
 callbacks.current = { setRows, reload, onFetching, changed };
 const ids = selected.filter(id => rows.some(r => r.instrumentId === id && fetchable(r))).sort((a, b) => a - b).join(",");
 const symbols = rows.map(r => `${r.instrumentId}:${r.avSymbol ?? ""}`).join(",");
 const allowed = fetchDateAllowed(date);
 // Past dates are served by Yahoo Finance (daily closes), only when the user asks.
 const historical = !allowed && date >= "1990-01-01" && date <= today();
 const storageKey = `price-fetch:${id}`;
 const refreshBudget = useCallback(async () => {
  const b = await api<Budget>("/api/market-data/budget");
  if (typeof b.userRemaining === "number") setBudget(b);
 }, []);
 useEffect(() => {
  const refresh = () => { void refreshBudget().catch(e => setError(e.message)); };
  refresh();
  window.addEventListener("av-budget-changed", refresh);
  setJobId(sessionStorage.getItem(storageKey));
  return () => window.removeEventListener("av-budget-changed", refresh);
 }, [storageKey, refreshBudget]);
 useEffect(() => { callbacks.current.onFetching(starting || !!jobId); }, [starting, jobId]);
 useEffect(() => {
  setEstimate(null);
  if (!allowed || !ids) return;
  const abort = new AbortController();
  api<Estimate>(`/api/portfolios/${id}/price-fetch/estimate?date=${date}&instrumentIds=${ids}`, { signal: abort.signal }).then(e => {
   if (!abort.signal.aborted && typeof e.requestsNeeded === "number") { setEstimate(e); setBudget(e.budget); }
  }).catch(e => { if (!abort.signal.aborted) setError(e.message); });
  return () => abort.abort();
 }, [id, date, ids, symbols, allowed, job?.status]);
 useEffect(() => {
  setYahooEstimate(null);
  if (!historical || !ids) return;
  const abort = new AbortController();
  api<Estimate>(`/api/portfolios/${id}/price-fetch/estimate?date=${date}&instrumentIds=${ids}&provider=YAHOO`, { signal: abort.signal }).then(e => {
   if (!abort.signal.aborted && e.yahoo) setYahooEstimate(e.yahoo);
  }).catch(e => { if (!abort.signal.aborted) setError(e.message); });
  return () => abort.abort();
 }, [id, date, ids, symbols, historical, job?.status]);
 useEffect(() => {
  if (!jobId) return;
  let active = true;
  let timer: ReturnType<typeof setTimeout>;
  const applied = new Set<number>();
  const poll = async () => {
   try {
    const next = await api<Job>(`/api/price-fetch-jobs/${jobId}`);
    if (!active) return;
    setJob(next);
    applyItems(next.items, applied);
    if (next.status === "DONE") {
     setBackup(next.items.filter(i => i.status === "NEEDS_BACKUP").map(i => i.instrumentId));
     sessionStorage.removeItem(storageKey); setJobId(null);
     await refreshBudget();
     callbacks.current.changed();
    } else if (active) timer = setTimeout(poll, 2000);
   } catch (e) {
    if (!active) return;
    const err = e as APIError;
    setError(err.status === 404 ? "Fetch interrupted. Prices fetched so far were saved." : err.message);
    if (err.status === 404) {
     sessionStorage.removeItem(storageKey); setJobId(null);
     try { await callbacks.current.reload(true); await refreshBudget(); callbacks.current.changed(); } catch (recoveryError) { if (active) setError(`Fetch interrupted. ${(recoveryError as Error).message}`); }
    } else timer = setTimeout(poll, 2000);
   }
  };
  void poll();
  const tick = setInterval(() => setNow(Date.now()), 1000);
  return () => { active = false; clearTimeout(timer); clearInterval(tick); };
 }, [jobId, storageKey, refreshBudget]);
 // Applies fetched prices to the draft rows, once per item.
 const appliedItems = useRef(new Set<number>());
 const applyItems = (items: QuoteItem[], applied: Set<number> = appliedItems.current) => {
  const updates = items.filter(i => (i.status === "OK" || i.status === "FRESH") && i.priceEur && !applied.has(i.instrumentId));
  updates.forEach(i => applied.add(i.instrumentId));
  callbacks.current.setRows(current => current?.map(row => {
   const item = updates.find(i => i.instrumentId === row.instrumentId);
   return item?.priceEur ? { ...editRow(row, "price", item.priceEur), quoteId: item.quoteId, priceSource: fetchedSource(item) } : row;
  }) ?? null);
 };
 const start = async (provider?: "YAHOO") => {
  setStarting(true); setError(""); setBackup([]);
  try {
   const created = await api<{ jobId: string }>(`/api/portfolios/${id}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: ids.split(",").map(Number), ...(provider ? { provider } : {}) }) });
   sessionStorage.setItem(storageKey, created.jobId); setJob(null); setJobProvider(provider ?? ""); setJobId(created.jobId);
  } catch (e) { setError((e as Error).message); void refreshBudget().catch(() => {}); }
  finally { setStarting(false); }
 };
 const eulerpool = estimate?.provider !== "ALPHAVANTAGE" && !!estimate?.eulerpool;
 const over = !eulerpool && !!estimate && (estimate.requestsNeeded > estimate.budget.userRemaining || estimate.requestsNeeded > estimate.budget.globalRemaining);
 const blocked = !eulerpool && !!budget?.blockedUntil && Date.parse(budget.blockedUntil) > now && (estimate?.requestsNeeded ?? 1) > 0;
 const epRequests = estimate?.eulerpool?.requests;
 return <div className="price-fetch">
  <span className="badge" title={budget ? `${budget.globalRemaining} requests left app-wide` : undefined}>{budget ? `AV: ${Math.min(budget.userRemaining, budget.globalRemaining)} of ${budget.userLimit} left today` : "AV budget loading…"}</span>
  <button type="button" disabled={busy || starting || !!jobId || !allowed || !ids || !estimate || over || blocked} title={!allowed ? "Fetched prices are current prices; enter historical prices manually." : undefined} onClick={() => void start()}>{starting ? "Starting…" : eulerpool ? `Fetch prices (${epRequests} Eulerpool ${epRequests === 1 ? "request" : "requests"})` : `Fetch prices (uses ${estimate?.requestsNeeded ?? "…"} requests)`}</button>
  {historical && <button type="button" disabled={busy || starting || !!jobId || !ids || !yahooEstimate || yahooEstimate.requests > yahooEstimate.remaining} onClick={() => void start("YAHOO")}>{starting ? "Starting…" : yahooEstimate ? `Fetch historical prices via Yahoo Finance (uses ${yahooEstimate.requests} ${yahooEstimate.requests === 1 ? "request" : "requests"})` : "Fetch historical prices via Yahoo Finance (estimating…)"}</button>}
  {historical && yahooEstimate && yahooEstimate.requests > yahooEstimate.remaining && <p>Yahoo Finance is rate limited right now. Try again later.</p>}
  {historical && <small className="muted">Closing price on or before {date.split("-").reverse().join(".")} · Data by Yahoo Finance</small>}
  {eulerpool && <small className="muted">Data by Eulerpool</small>}
  {over && <p>Deselect rows to fit the available budget.</p>}
  {blocked && <p>Alpha Vantage is temporarily paused.</p>}
  {job && <p role="status">{job.items.filter(i => i.status !== "PENDING").length}/{job.items.length} · {job.status === "DONE" ? "Done" : jobProvider === "YAHOO" ? "Fetching…" : `next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt) - now) / 1000))} s`}</p>}
  {job?.items.filter(i => !["PENDING", "OK", "FRESH", "NEEDS_BACKUP"].includes(i.status)).map(i => <p key={i.instrumentId}>{rows.find(r => r.instrumentId === i.instrumentId)?.ticker}: {i.status.replaceAll("_", " ")}</p>)}
  {error && <p className="error" role="alert">{error}</p>}
  {!jobId && backup.length > 0 && <BackupFetch portfolioId={id} date={date} ids={backup} names={Object.fromEntries(rows.map(r => [r.instrumentId, r.ticker]))} onItems={items => applyItems(items)} onDone={async () => { await callbacks.current.reload(true); callbacks.current.changed(); }} />}
 </div>;
}
/** Price link column: the ISIN Eulerpool prices by, or a way to find it when missing. */
export function IsinLink({ row, reload }: { row: PlanRow; reload: Reload }) {
 const { changed } = usePortfolioUpdates();
 const [open, setOpen] = useState(false);
 const [results, setResults] = useState<OnlineInstrument[] | null>(null);
 const [term, setTerm] = useState("");
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState("");
 const search = async () => {
  setBusy(true); setError("");
  try { const found = await api<{ results: OnlineInstrument[] }>(`/api/instruments/${row.instrumentId}/isin/search?q=${encodeURIComponent(term.trim())}`); setResults(found.results); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 const link = async (r: OnlineInstrument) => {
  setBusy(true); setError("");
  try { await api(`/api/instruments/${row.instrumentId}/isin`, { method: "PUT", body: JSON.stringify({ isin: r.isin }) }); await reload(); changed(); setOpen(false); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 return <div className="price-link">
  {row.isin
   ? <><code className="isin" title="Prices are fetched by ISIN from Eulerpool">{row.isin}</code>{row.avSymbol && <small className="muted">Backup: {row.avSymbol}</small>}</>
   : <button type="button" className="link-button" onClick={() => { setResults(null); setError(""); setTerm(row.avSearchTerm ?? row.name); setOpen(true); }}>Link ISIN</button>}
  {open && <Modal title={`Link ISIN for ${row.ticker}`} close={() => { if (!busy) setOpen(false); }}>
   <p className="muted">Search Eulerpool by name or ticker to find the ISIN used for price fetching. Each search uses 1 Eulerpool request.</p>
   <form className="link-search" onSubmit={e => { e.preventDefault(); if (term.trim().length >= 2 && !busy) void search(); }}>
    <label htmlFor={`isin-term-${row.instrumentId}`}>Search term</label>
    <div className="link-search-row">
     <input id={`isin-term-${row.instrumentId}`} value={term} maxLength={100} disabled={busy} onChange={e => setTerm(e.target.value)} />
     <button type="submit" className="primary" disabled={busy || term.trim().length < 2}>{busy ? "Searching…" : "Search"}</button>
    </div>
   </form>
   {results && <ul className="link-results">{results.map(r => <li key={r.isin}>
    <button type="button" disabled={busy || !!r.existingInstrumentId} onClick={() => void link(r)}>
     <span className={`pill ${r.type === "ETF" ? "pill-etf" : "pill-stock"}`}>{r.type === "ETF" ? "ETF" : "Stock"}</span>
     <span className="link-main"><strong>{r.name}</strong><span className="option-meta">{r.ticker} · {r.isin}</span></span>
     {r.currency && <span className="chip">{r.currency}</span>}
     {r.existingInstrumentId ? <span className="option-state">Already in use</span> : null}
    </button></li>)}</ul>}
   {results?.length === 0 && <p role="status">No instruments found.</p>}
   {results && results.length > 0 && <small className="muted">Data by Eulerpool</small>}
   {error && <p role="alert" className="error">{error}</p>}
  </Modal>}
 </div>;
}
