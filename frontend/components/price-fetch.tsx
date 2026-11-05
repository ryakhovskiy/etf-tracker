"use client";
import { useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { api, APIError, OnlineInstrument } from "../lib/api";
import { editRow, fetchedSource, fetchDateAllowed, PlanRow } from "../lib/transactions";
import { Modal } from "./portfolios";

type Budget = { globalRemaining: number; userRemaining: number; userLimit: number; nextSlotAt: string; blockedUntil?: string };
type Estimate = { requestsNeeded: number; fresh: number[]; noSymbol: number[]; budget: Budget };
type QuoteItem = { instrumentId: number; status: string; priceEur?: string; origPrice?: string; currency?: string; fxRate?: string; fxRateDate?: string; quoteDate?: string; quoteId?: number };
type Job = { status: "QUEUED" | "RUNNING" | "DONE"; nextRequestAt: string; items: QuoteItem[] };
type Reload = (recoverPrices?: boolean) => Promise<unknown>;
export function PriceFetch({ id, date, rows, selected, setRows, reload, busy, onFetching }: {
 id: string; date: string; rows: PlanRow[]; selected: number[];
 setRows: Dispatch<SetStateAction<PlanRow[] | null>>; reload: Reload; busy: boolean; onFetching: (fetching: boolean) => void;
}) {
 const [budget, setBudget] = useState<Budget | null>(null);
 const [estimate, setEstimate] = useState<Estimate | null>(null);
 const [jobId, setJobId] = useState<string | null>(null);
 const [job, setJob] = useState<Job | null>(null);
 const [error, setError] = useState("");
 const [starting, setStarting] = useState(false);
 const [now, setNow] = useState(Date.now());
 // Draft setters belong to the parent; polling always uses their current versions.
 const callbacks = useRef({ setRows, reload, onFetching });
 callbacks.current = { setRows, reload, onFetching };
 const ids = selected.filter(id => rows.some(r => r.instrumentId === id && r.avSymbol)).sort((a, b) => a - b).join(",");
 const symbols = rows.map(r => `${r.instrumentId}:${r.avSymbol ?? ""}`).join(",");
 const allowed = fetchDateAllowed(date);
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
  if (!jobId) return;
  let active = true;
  let timer: ReturnType<typeof setTimeout>;
  const applied = new Set<number>();
  const poll = async () => {
   try {
    const next = await api<Job>(`/api/price-fetch-jobs/${jobId}`);
    if (!active) return;
    setJob(next);
    const updates = next.items.filter(i => (i.status === "OK" || i.status === "FRESH") && i.priceEur && !applied.has(i.instrumentId));
    updates.forEach(i => applied.add(i.instrumentId));
    callbacks.current.setRows(current => current?.map(row => {
     const item = updates.find(i => i.instrumentId === row.instrumentId);
     return item?.priceEur ? { ...editRow(row, "price", item.priceEur), quoteId: item.quoteId, priceSource: fetchedSource(item) } : row;
    }) ?? null);
    if (next.status === "DONE") {
     sessionStorage.removeItem(storageKey); setJobId(null);
     await refreshBudget();
    } else if (active) timer = setTimeout(poll, 2000);
   } catch (e) {
    if (!active) return;
    const err = e as APIError;
    setError(err.status === 404 ? "Fetch interrupted. Prices fetched so far were saved." : err.message);
    if (err.status === 404) {
     sessionStorage.removeItem(storageKey); setJobId(null);
     try { await callbacks.current.reload(true); await refreshBudget(); } catch (recoveryError) { if (active) setError(`Fetch interrupted. ${(recoveryError as Error).message}`); }
    } else timer = setTimeout(poll, 2000);
   }
  };
  void poll();
  const tick = setInterval(() => setNow(Date.now()), 1000);
  return () => { active = false; clearTimeout(timer); clearInterval(tick); };
 }, [jobId, storageKey, refreshBudget]);
 const start = async () => {
  setStarting(true); setError("");
  try {
   const created = await api<{ jobId: string }>(`/api/portfolios/${id}/price-fetch-jobs`, { method: "POST", body: JSON.stringify({ date, instrumentIds: ids.split(",").map(Number) }) });
   sessionStorage.setItem(storageKey, created.jobId); setJob(null); setJobId(created.jobId);
  } catch (e) { setError((e as Error).message); void refreshBudget().catch(() => {}); }
  finally { setStarting(false); }
 };
 const over = !!estimate && (estimate.requestsNeeded > estimate.budget.userRemaining || estimate.requestsNeeded > estimate.budget.globalRemaining);
 const blocked = !!budget?.blockedUntil && Date.parse(budget.blockedUntil) > now && (estimate?.requestsNeeded ?? 1) > 0;
 return <div className="price-fetch">
  <span className="badge" title={budget ? `${budget.globalRemaining} requests left app-wide` : undefined}>{budget ? `AV: ${Math.min(budget.userRemaining, budget.globalRemaining)} of ${budget.userLimit} left today` : "AV budget loading…"}</span>
  <button type="button" disabled={busy || starting || !!jobId || !allowed || !ids || !estimate || over || blocked} title={!allowed ? "Fetched prices are current prices; enter historical prices manually." : undefined} onClick={start}>{starting ? "Starting…" : `Fetch prices (uses ${estimate?.requestsNeeded ?? "…"} requests)`}</button>
  {over && <p>Deselect rows to fit the available budget.</p>}
  {blocked && <p>Alpha Vantage is temporarily paused.</p>}
  {job && <p role="status">{job.items.filter(i => i.status !== "PENDING").length}/{job.items.length} · {job.status === "DONE" ? "Done" : `next request in ${Math.max(0, Math.ceil((Date.parse(job.nextRequestAt) - now) / 1000))} s`}</p>}
  {job?.items.filter(i => !["PENDING", "OK", "FRESH"].includes(i.status)).map(i => <p key={i.instrumentId}>{rows.find(r => r.instrumentId === i.instrumentId)?.ticker}: {i.status.replaceAll("_", " ")}</p>)}
  {error && <p className="error" role="alert">{error}</p>}
 </div>;
}
export function SymbolLink({ row, reload }: { row: PlanRow; reload: Reload }) {
 const [open, setOpen] = useState(false);
 const [confirm, setConfirm] = useState(false);
 const [results, setResults] = useState<OnlineInstrument[] | null>(null);
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState("");
 const search = async () => {
  setBusy(true); setError("");
  try { const found = await api<{ results: OnlineInstrument[] }>(`/api/instruments/${row.instrumentId}/av-symbol/search`); setResults(found.results); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); window.dispatchEvent(new window.Event("av-budget-changed")); }
 };
 const link = async (r: OnlineInstrument) => {
  setBusy(true); setError("");
  try { await api(`/api/instruments/${row.instrumentId}/av-symbol`, { method: "PUT", body: JSON.stringify({ avSymbol: r.avSymbol, currency: r.currency, region: r.region }) }); await reload(); setOpen(false); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 const unlink = async () => {
  setBusy(true); setError("");
  try { await api(`/api/instruments/${row.instrumentId}/av-symbol`, { method: "DELETE" }); await reload(); setConfirm(false); }
  catch (e) { setError((e as Error).message); }
  finally { setBusy(false); }
 };
 return <>
  {row.avSymbol ? <><span>{row.avSymbol}</span><small>{row.avLinkedByYou ? "Linked by you" : `Linked by another user · ${row.avLinkedAt?.slice(0, 10).split("-").reverse().join(".") ?? ""}`}</small><button type="button" onClick={() => setConfirm(true)}>Unlink</button></> : <button type="button" onClick={() => { setResults(null); setError(""); setOpen(true); }}>Link symbol</button>}
  {open && <Modal title={`Link symbol for ${row.ticker}`} close={() => { if (!busy) setOpen(false); }}>
   <p>Search Alpha Vantage for {row.ticker}. Uses 1 request; cached searches are free. EUR listings appear first.</p>
   <button type="button" disabled={busy} onClick={search}>Search symbols (uses 1 request)</button>
   {results?.map(r => <button type="button" disabled={busy} key={r.avSymbol} onClick={() => void link(r)}>{r.ticker} · {r.name} · {r.currency} · {r.region}</button>)}
   {results?.length === 0 && <p>No symbols found.</p>}{error && <p role="alert">{error}</p>}
  </Modal>}
  {confirm && <Modal title="Unlink symbol?" close={() => { if (!busy) setConfirm(false); }}><p>Unlinking deletes fetched quotes for {row.ticker}. Saved transactions are kept.</p><button type="button" disabled={busy} onClick={() => void unlink()}>Confirm unlink</button>{error && <p role="alert">{error}</p>}</Modal>}
 </>;
}
