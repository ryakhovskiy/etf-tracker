"use client";
import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { api, APIError, OnlineInstrument } from "../lib/api";
import { editRow, fetchable, fetchedSource, fetchDateInRange, PlanRow, today } from "../lib/transactions";
import { BackupFetch, BACKUP_STATUSES, type JobItem } from "./backup-fetch";
import { Modal } from "./portfolios";
import { usePortfolioUpdates } from "./portfolio-updates";

type Budget = { globalRemaining: number; userRemaining: number; userLimit: number; nextSlotAt: string; blockedUntil?: string; primary?: string };
type Estimate = {
  requestsNeeded: number; fresh: number[]; noSymbol: number[]; budget: Budget; provider?: string;
  eulerpool?: { requests: number; remaining: number }; yahoo?: { requests: number; remaining: number };
};
type QuoteItem = JobItem;
type Job = { status: "QUEUED" | "RUNNING" | "DONE"; nextRequestAt: string; items: QuoteItem[] };
type Reload = (recoverPrices?: boolean) => Promise<unknown>;
const count = (n: number | undefined, who: string) => `${n ?? "…"} ${who} ${n === 1 ? "request" : "requests"}`;
const german = (iso: string) => iso.split("-").reverse().join(".");

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
 // Draft setters belong to the parent; polling always uses their current versions.
 const callbacks = useRef({ setRows, reload, onFetching, changed });
 callbacks.current = { setRows, reload, onFetching, changed };
 const ids = selected.filter(id => rows.some(r => r.instrumentId === id && fetchable(r))).sort((a, b) => a - b).join(",");
 const symbols = rows.map(r => `${r.instrumentId}:${r.avSymbol ?? ""}:${r.yahooSymbol ?? ""}`).join(",");
 // Every provider prices past dates from its daily history; only the range is limited.
 const allowed = fetchDateInRange(date);
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
 // The primary provider comes with the estimate; older servers without it mean Alpha Vantage.
 const mode = estimate?.provider ?? budget?.primary ?? (estimate?.eulerpool ? "EULERPOOL" : "ALPHAVANTAGE");
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
     // Whatever the primary source could not price can be fetched from a backup, on request.
     const offer = mode === "ALPHAVANTAGE" ? ["NEEDS_BACKUP"] : BACKUP_STATUSES;
     setBackup(next.items.filter(i => offer.includes(i.status)).map(i => i.instrumentId));
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
 }, [jobId, storageKey, refreshBudget, mode]);
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
 const start = async () => {
  setStarting(true); setError(""); setBackup([]);
  try {
   const created = await api<{ jobId: string }>(`/api/portfolios/${id}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: ids.split(",").map(Number) }) });
   sessionStorage.setItem(storageKey, created.jobId); setJob(null); setJobId(created.jobId);
  } catch (e) { setError((e as Error).message); void refreshBudget().catch(() => {}); }
  finally { setStarting(false); }
 };
 const av = mode === "ALPHAVANTAGE";
 const over = av && !!estimate && (estimate.requestsNeeded > estimate.budget.userRemaining || estimate.requestsNeeded > estimate.budget.globalRemaining);
 const blocked = av && !!budget?.blockedUntil && Date.parse(budget.blockedUntil) > now && (estimate?.requestsNeeded ?? 1) > 0;
 const yahooOut = mode === "YAHOO" && !!estimate?.yahoo && estimate.yahoo.requests > estimate.yahoo.remaining;
 const label = mode === "YAHOO" ? `Fetch prices (${count(estimate?.yahoo?.requests, "Yahoo")})`
  : mode === "EULERPOOL" ? `Fetch prices (${count(estimate?.eulerpool?.requests, "Eulerpool")})`
  : `Fetch prices (uses ${estimate?.requestsNeeded ?? "…"} requests)`;
 const past = date !== today();
 const attribution = mode === "YAHOO" ? "Data by Yahoo Finance" : mode === "EULERPOOL" ? "Data by Eulerpool" : "";
 const failures = job?.items.filter(i => !["PENDING", "OK", "FRESH", "NEEDS_BACKUP", ...(av ? [] : BACKUP_STATUSES)].includes(i.status)) ?? [];
 return <div className="price-fetch">
  {(estimate ? av : !budget?.primary) && <span className="badge" title={budget ? `${budget.globalRemaining} requests left app-wide` : undefined}>{budget ? `AV: ${Math.min(budget.userRemaining, budget.globalRemaining)} of ${budget.userLimit} left today` : "AV budget loading…"}</span>}
  <button type="button" disabled={busy || starting || !!jobId || !allowed || !ids || !estimate || over || blocked || yahooOut} title={!allowed ? "Choose a date from 1990-01-01 through today." : undefined} onClick={() => void start()}>{starting ? "Starting…" : label}</button>
  {(attribution || past) && allowed && <small className="muted">{past ? `Closing price on or before ${german(date)}` : ""}{past && attribution ? " · " : ""}{attribution}</small>}
  {yahooOut && <p>Yahoo Finance is rate limited right now. Try again later.</p>}
  {over && <p>Deselect rows to fit the available budget.</p>}
  {blocked && <p>Alpha Vantage is temporarily paused.</p>}
  {job && <p role="status">{job.items.filter(i => i.status !== "PENDING").length}/{job.items.length} · {job.status === "DONE" ? "Done" : av ? `next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt) - now) / 1000))} s` : "Fetching…"}</p>}
  {failures.map(i => <p key={i.instrumentId}>{rows.find(r => r.instrumentId === i.instrumentId)?.ticker}: {i.status.replaceAll("_", " ")}</p>)}
  {error && <p className="error" role="alert">{error}</p>}
  {!jobId && backup.length > 0 && <BackupFetch portfolioId={id} date={date} primary={mode} ids={backup} names={Object.fromEntries(rows.map(r => [r.instrumentId, r.ticker]))} onItems={items => applyItems(items)} onDone={async () => { await callbacks.current.reload(true); callbacks.current.changed(); }} />}
 </div>;
}

/** Price link column: the Yahoo symbol prices are fetched by, and a way to find or change it. */
export function SymbolLink({ row, reload }: { row: PlanRow; reload: Reload }) {
 const { changed } = usePortfolioUpdates();
 const [open, setOpen] = useState(false);
 const [results, setResults] = useState<OnlineInstrument[] | null>(null);
 const [term, setTerm] = useState("");
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState("");
 const search = async () => {
  setBusy(true); setError("");
  try { const found = await api<{ results: OnlineInstrument[] }>(`/api/instruments/${row.instrumentId}/yahoo-symbol/search?q=${encodeURIComponent(term.trim())}`); setResults(found.results); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 const link = async (r: OnlineInstrument) => {
  setBusy(true); setError("");
  try { await api(`/api/instruments/${row.instrumentId}/yahoo-symbol`, { method: "PUT", body: JSON.stringify({ symbol: r.yahooSymbol }) }); await reload(); changed(); setOpen(false); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 const openModal = () => { setResults(null); setError(""); setTerm(row.avSearchTerm ?? row.name); setOpen(true); };
 return <div className="price-link">
  {row.yahooSymbol
   ? <><code className="isin" title="Prices are fetched by this Yahoo symbol">{row.yahooSymbol}</code>{row.isin && <small className="muted">{row.isin}</small>}<button type="button" className="link-button subtle" onClick={openModal}>Change</button></>
   : <>{row.isin && <code className="isin" title="The symbol is looked up from the ISIN when prices are fetched">{row.isin}</code>}<button type="button" className="link-button" onClick={openModal}>Link symbol</button></>}
  {row.avSymbol && <small className="muted">AV backup: {row.avSymbol}</small>}
  {open && <Modal title={`Link symbol for ${row.ticker}`} close={() => { if (!busy) setOpen(false); }}>
   <p className="muted">Search Yahoo Finance by name or ticker to find the symbol used for price fetching. The ISIN is linked as well where it can be found. Each search uses 1 Yahoo request.</p>
   <form className="link-search" onSubmit={e => { e.preventDefault(); if (term.trim().length >= 2 && !busy) void search(); }}>
    <label htmlFor={`symbol-term-${row.instrumentId}`}>Search term</label>
    <div className="link-search-row">
     <input id={`symbol-term-${row.instrumentId}`} value={term} maxLength={100} disabled={busy} onChange={e => setTerm(e.target.value)} />
     <button type="submit" className="primary" disabled={busy || term.trim().length < 2}>{busy ? "Searching…" : "Search"}</button>
    </div>
   </form>
   {results && <ul className="link-results">{results.map(r => {
    const taken = !!r.existingInstrumentId && r.existingInstrumentId !== row.instrumentId;
    return <li key={r.yahooSymbol}>
     <button type="button" disabled={busy || taken} onClick={() => void link(r)}>
      <span className={`pill ${r.type === "ETF" ? "pill-etf" : "pill-stock"}`}>{r.type === "ETF" ? "ETF" : "Stock"}</span>
      <span className="link-main"><strong>{r.name}</strong><span className="option-meta">{[r.yahooSymbol, r.exchangeName, r.sector].filter(Boolean).join(" · ")}</span></span>
      {taken ? <span className="option-state">Already in use</span> : r.existingInstrumentId ? <span className="option-state">Current</span> : null}
     </button></li>;
   })}</ul>}
   {results?.length === 0 && <p role="status">No instruments found.</p>}
   {results && results.length > 0 && <small className="muted">Data by Yahoo Finance</small>}
   {error && <p role="alert" className="error">{error}</p>}
  </Modal>}
 </div>;
}
