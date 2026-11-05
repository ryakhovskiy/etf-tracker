"use client";

import { useEffect, useRef, useState } from "react";
import { api, APIError, Instrument, OnlineInstrument, PortfolioDetail } from "../lib/api";

import { usePortfolioUpdates } from "./portfolio-updates";

type PickerRow = Instrument & { online?: OnlineInstrument };
const onlineMessage = (code: string) => code === "FIGI_RATE_LIMITED" ? "Online search busy, try again in a minute" : "Online search unavailable";

export function InstrumentPicker({ initial }: { initial: PortfolioDetail }) {
  const [portfolio, setPortfolio] = useState(initial);
  const { changed } = usePortfolioUpdates();
  useEffect(() => { setPortfolio(initial); }, [initial]);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<PickerRow[]>([]);
  const [avFallback,setAVFallback]=useState(false);
  const [warning, setWarning] = useState("");
  const [selected, setSelected] = useState<Instrument[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [searchError, setSearchError] = useState("");
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");
  const [busy, setBusy] = useState(false);
  const [active, setActive] = useState(-1);
  const wrapper = useRef<HTMLDivElement>(null);
  const requestVersion = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const normalized = query.trim();
  useEffect(() => {
    const outside = (e: PointerEvent) => { if (!wrapper.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, []);
  useEffect(() => {
    if ([...normalized].length < 4) return;
    const version = requestVersion.current;
    const abort = new AbortController(); controller.current = abort;
    const timer = setTimeout(async () => {
      if (abort.signal.aborted) return;
      try {
        const found = await api<Instrument[]>(`/api/instruments/search?q=${encodeURIComponent(normalized)}&portfolioId=${portfolio.id}`, { signal: abort.signal });
        if (version === requestVersion.current && !abort.signal.aborted) { setResults(found); setLoading(false); }
      } catch (e) {
        if (version === requestVersion.current && !abort.signal.aborted) { setSearchError((e as Error).message); setLoading(false); }
      }
    }, 300);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [normalized, query, portfolio.id]);
  const changeQuery = (value: string) => {
    // Invalidate immediately, before effect cleanup, including when deleting below 4.
    requestVersion.current++; controller.current?.abort();
    setAVFallback(false); setQuery(value); setResults([]); setSearchError(""); setWarning(""); setActive(-1);
    const enough = [...value.trim()].length >= 4; setOpen(enough); setLoading(enough);
  };
  const matches = (a: Instrument, b: Instrument) => a.id === b.id || !!(a.figi && a.figi === b.figi) || !!(a.isin && a.isin === b.isin) || !!(a.wkn && a.wkn === b.wkn) || !!((a as PickerRow).online?.avSymbol && (a as PickerRow).online?.avSymbol === (b as PickerRow).online?.avSymbol);
  const disabled = (i: Instrument) => i.inPortfolio || portfolio.instrumentIds.includes(i.id) || selected.some(s => matches(s, i));
  const choose = async (i: PickerRow) => {
    if (disabled(i) || busy) return;
    setBusy(true); setError("");
    try {
      const saved = i.online ? await api<Instrument>("/api/instruments", { method: "POST", body: JSON.stringify(i.online) }) : i;
      setSelected(s => s.some(row => matches(row, saved)) ? s : [...s, saved]);
      setResults(rows => rows.map(row => row === i ? { ...saved, inPortfolio: i.inPortfolio } : row));
      setActive(-1); setToast("");
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  const searchOnline = async (provider="figi") => {
    controller.current?.abort();
    const version = ++requestVersion.current;
    const abort = new AbortController(); controller.current = abort;
    setLoading(true); setSearchError(""); setWarning(""); setActive(-1);
    try {
      const found = await api<{ results: OnlineInstrument[]; warning?: string }>(`/api/instruments/search/online?q=${encodeURIComponent(normalized)}&portfolioId=${portfolio.id}${provider==="av"?"&provider=av":""}`, { signal: abort.signal });
      if (version !== requestVersion.current || abort.signal.aborted) return;
      if(provider==="figi")setAVFallback(found.results.length===0||!!found.warning);
      setResults(found.results.map((row, index) => ({ ...row, id: row.existingInstrumentId ?? -(index + 1), isin: row.isin ?? "", wkn: row.wkn ?? null, online: row })));
      if (found.warning) setWarning(onlineMessage(found.warning));
    } catch (e) {
      if (version === requestVersion.current && !abort.signal.aborted) { if(provider==="figi")setAVFallback(true); setSearchError(e instanceof APIError && provider==="av"?e.message:onlineMessage(e instanceof APIError ? e.code : "UPSTREAM_ERROR")); }
    } finally { if(provider==="av")window.dispatchEvent(new window.Event("av-budget-changed")); if (version === requestVersion.current && !abort.signal.aborted) setLoading(false); }
  };
  const current = portfolio.instrumentIds.length;
  const overflow = current + selected.length > portfolio.itemLimit;
  const add = async () => {
    setBusy(true); setError(""); setToast("");
    try {
      const result = await api<{ added: number[]; skipped: number[] }>(`/api/portfolios/${portfolio.id}/items`, { method: "POST", body: JSON.stringify({ instrumentIds: selected.map(i => i.id) }) });
      changed();
      // Reflect the committed response even if the follow-up refresh fails.
      setPortfolio(p => ({ ...p, instrumentIds: [...new Set([...p.instrumentIds, ...result.added, ...result.skipped])] }));
      setSelected([]); setToast(`${result.added.length} added`);
      const updated = await api<PortfolioDetail>(`/api/portfolios/${portfolio.id}`); setPortfolio(updated);
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  return <>
    <div className="search-picker" ref={wrapper}>
      <label htmlFor="instrument-search">Find stocks and ETFs</label>
      <input id="instrument-search" role="combobox" aria-autocomplete="list" aria-expanded={open && normalized.length >= 4} aria-controls="instrument-results" aria-activedescendant={active >= 0 ? `instrument-${results[active]?.id}` : undefined} autoComplete="off" placeholder="Search name, ticker, ISIN or WKN (4+ characters)" value={query} onChange={e => changeQuery(e.target.value)} onFocus={() => { if (normalized.length >= 4) setOpen(true); }} onKeyDown={e => {
        if (e.key === "Escape") { setOpen(false); setActive(-1); }
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
          e.preventDefault(); setOpen(normalized.length >= 4);
          const available = results.map((i, n) => disabled(i) ? -1 : n).filter(n => n >= 0);
          if (!available.length) return;
          const position = available.indexOf(active);
          setActive(available[(position + (e.key === "ArrowDown" ? 1 : position < 0 ? 0 : -1) + available.length) % available.length]);
        }
        if (e.key === "Enter" && open && active >= 0) { e.preventDefault(); choose(results[active]); }
      }} />
      {open && normalized.length >= 4 && <div className="search-dropdown"><ul id="instrument-results" role="listbox" aria-label="Instrument results" aria-busy={loading}>
        {!loading && !searchError && results.map((i, index) => <li id={`instrument-${i.id}`} key={i.figi || i.id} role="option" aria-selected={selected.some(s => matches(s, i))} aria-disabled={disabled(i) || busy} className={`${disabled(i) ? "disabled" : ""} ${active === index ? "active" : ""}`} onClick={() => choose(i)}><div><strong>{i.ticker}</strong><span className="badge">{i.type}</span>{i.exchCode && <span className="badge">{i.exchCode === "GR" ? "Germany" : i.exchCode === "GY" ? "Xetra" : i.exchCode}</span>}<span className="result-label">{i.inPortfolio || portfolio.instrumentIds.includes(i.id) ? "In portfolio" : selected.some(s => matches(s, i)) ? "Selected" : ""}</span></div><span>{i.name}</span><small>ISIN {i.isin || "—"} · WKN {i.wkn ?? "—"}</small></li>)}
      </ul>{loading ? <p role="status">Searching…</p> : searchError ? <p className="error" role="alert">{searchError}</p> : results.length === 0 ? <p role="status">No results</p> : null}{warning && <p role="status">{warning}</p>}<button type="button" disabled={busy} onClick={()=>void searchOnline()}>Search online</button>{avFallback&&!/^(?:[A-Z]{2}[A-Z0-9]{9}[0-9]|(?=[A-Z0-9]{6}$)(?=.*[0-9])[A-Z0-9]{6})$/.test(normalized.toUpperCase())&&<button type="button" disabled={busy||loading} onClick={()=>void searchOnline("av")}>Try Alpha Vantage (1 request)</button>}</div>}
    </div>
    <section className="selection"><h2>Selected</h2>{selected.length === 0 ? <p className="selection-empty">Search above to select stocks or ETFs.</p> : <div className="table-scroll"><table><thead><tr>{["Ticker", "Name", "Type", "ISIN", "WKN", "Remove"].map(h => <th key={h}>{h}</th>)}</tr></thead><tbody>{selected.map(i => <tr key={i.id}><td><strong>{i.ticker}</strong></td><td>{i.name}</td><td><span className="badge">{i.type}</span></td><td>{i.isin || "—"}</td><td>{i.wkn ?? "—"}</td><td><button aria-label={`Remove ${i.ticker} from selection`} disabled={busy} onClick={() => setSelected(s => s.filter(row => row.id !== i.id))}>×</button></td></tr>)}</tbody></table></div>}</section>
    <div className="add-controls"><p className="muted">{current} / {portfolio.itemLimit} items | {selected.length} selected</p>{overflow && <p className="hint">Only {Math.max(0, portfolio.itemLimit - current)} more items fit in this portfolio. Remove some from the selection.</p>}{error && <p className="error" role="alert">{error}</p>}<button className="primary" disabled={!selected.length || busy || overflow || selected.length > 30} onClick={add}>{busy ? "Adding…" : "Add to portfolio"}</button>{toast && <p className="toast" role="status">{toast}</p>}</div>
  </>;
}
