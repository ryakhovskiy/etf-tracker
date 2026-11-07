"use client";

import { useEffect, useId, useRef, useState } from "react";
import { api, APIError, Instrument, OnlineInstrument, OnlineSearch, PortfolioDetail } from "../lib/api";
import { highlightParts } from "../lib/etf";
import { EtfInfoButton, EtfInfoPanel } from "./etf-info";
import { usePortfolioUpdates } from "./portfolio-updates";

type PickerRow = Instrument & { online?: OnlineInstrument };
type Section = { key: "local" | "online"; title: string; rows: PickerRow[] };

const onlineMessage = (code: string) => code === "FIGI_RATE_LIMITED" ? "Online search busy, try again in a minute" : "Online search unavailable";
const providerName = (p?: string) => p === "YAHOO" ? "Yahoo Finance" : p === "EULERPOOL" ? "Eulerpool" : p === "ALPHAVANTAGE" ? "Alpha Vantage" : "OpenFIGI";
// Warning codes come from the sources that failed; the text names the one that answered instead.
const warningText = (code: string, shown?: string) => {
  const failed = code.startsWith("YAHOO") ? "Yahoo Finance" : code.startsWith("EULERPOOL") ? "Eulerpool" : "";
  if (!failed) return onlineMessage(code);
  const reason = code.endsWith("LIMIT_REACHED") ? "is rate limited" : code.endsWith("AUTH_ERROR") ? "is not configured correctly" : "unavailable";
  return `${failed} ${reason}, showing ${providerName(shown)} results`;
};
const providerTitle = (p?: string) => `Online · ${providerName(p)}`;
const identifier = /^(?:[A-Z]{2}[A-Z0-9]{9}[0-9]|(?=[A-Z0-9]{6}$)(?=.*[0-9])[A-Z0-9]{6})$/;

const SearchIcon = () => <svg className="icon" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true" focusable="false"><circle cx="9" cy="9" r="6" fill="none" stroke="currentColor" strokeWidth="1.8" /><path d="m14 14 4 4" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>;
const Spinner = () => <svg className="icon spin" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true" focusable="false"><circle cx="10" cy="10" r="7" fill="none" stroke="currentColor" strokeWidth="2" strokeOpacity=".3" /><path d="M17 10a7 7 0 0 0-7-7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>;

function Highlight({ text, query }: { text: string; query: string }) {
  return <>{highlightParts(text, query).map((p, i) => p.match ? <mark key={i}>{p.text}</mark> : <span key={i}>{p.text}</span>)}</>;
}

export function InstrumentPicker({ initial }: { initial: PortfolioDetail }) {
  const [portfolio, setPortfolio] = useState(initial);
  const { changed } = usePortfolioUpdates();
  useEffect(() => { setPortfolio(initial); }, [initial]);
  const [query, setQuery] = useState("");
  const [local, setLocal] = useState<PickerRow[]>([]);
  const [online, setOnline] = useState<{ provider?: string; rows: PickerRow[] } | null>(null);
  const [avOffer, setAVOffer] = useState(false);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [selected, setSelected] = useState<Instrument[]>([]);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [onlineLoading, setOnlineLoading] = useState(false);
  const [searchError, setSearchError] = useState("");
  const [onlineError, setOnlineError] = useState("");
  const [lastOnline, setLastOnline] = useState<"default" | "av">("default");
  const [retry, setRetry] = useState(0);
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");
  const [busy, setBusy] = useState(false);
  const [active, setActive] = useState(-1);
  const wrapper = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const requestVersion = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const onlineVersion = useRef(0);
  const onlineController = useRef<AbortController | null>(null);
  const [info, setInfo] = useState<{ id: number; name: string } | null>(null);
  const uid = useId();
  const listId = `${uid}-results`;
  const normalized = query.trim();
  const enough = [...normalized].length >= 4;

  useEffect(() => {
    const outside = (e: PointerEvent) => { if (!wrapper.current?.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, []);
  // Local search: live while typing, free.
  useEffect(() => {
    if (!enough) return;
    const version = requestVersion.current;
    const abort = new AbortController(); controller.current = abort;
    const timer = setTimeout(async () => {
      if (abort.signal.aborted) return;
      try {
        const found = await api<Instrument[]>(`/api/instruments/search?q=${encodeURIComponent(normalized)}&portfolioId=${portfolio.id}`, { signal: abort.signal });
        if (version === requestVersion.current && !abort.signal.aborted) { setLocal(found); setLoading(false); }
      } catch (e) {
        if (version === requestVersion.current && !abort.signal.aborted) { setSearchError((e as Error).message); setLoading(false); }
      }
    }, 300);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [normalized, query, portfolio.id, enough, retry]);

  const changeQuery = (value: string) => {
    // Invalidate immediately, before effect cleanup, including when deleting below 4.
    requestVersion.current++; controller.current?.abort(); onlineVersion.current++; onlineController.current?.abort();
    setAVOffer(false); setQuery(value); setLocal([]); setOnline(null); setOnlineError(""); setOnlineLoading(false); setSearchError(""); setWarnings([]); setActive(-1);
    const long = [...value.trim()].length >= 4; setOpen(long); setLoading(long);
  };
  const matches = (a: Instrument, b: Instrument) => a.id === b.id || !!((a as PickerRow).online?.yahooSymbol && (a as PickerRow).online?.yahooSymbol === (b as PickerRow).online?.yahooSymbol) || !!(a.figi && a.figi === b.figi) || !!(a.isin && a.isin === b.isin) || !!(a.wkn && a.wkn === b.wkn) || !!((a as PickerRow).online?.avSymbol && (a as PickerRow).online?.avSymbol === (b as PickerRow).online?.avSymbol);
  const disabled = (i: Instrument) => i.inPortfolio || portfolio.instrumentIds.includes(i.id) || selected.some(s => matches(s, i));
  const isSelected = (i: Instrument) => selected.some(s => matches(s, i));
  const inPortfolio = (i: Instrument) => i.inPortfolio || portfolio.instrumentIds.includes(i.id);

  const localIds = new Set(local.map(r => r.id));
  const sections: Section[] = [
    { key: "local" as const, title: "In your database", rows: local },
    { key: "online" as const, title: providerTitle(online?.provider), rows: (online?.rows ?? []).filter(r => !(r.id > 0 && localIds.has(r.id))) },
  ].filter(s => s.rows.length > 0);
  const flat = sections.flatMap(s => s.rows.map(row => ({ row, section: s.key })));
  const optionId = (section: string, row: PickerRow) => section === "local" ? `instrument-${row.id}` : `instrument-online-${row.id}`;

  const choose = async (i: PickerRow) => {
    if (disabled(i) || busy) return;
    setBusy(true); setError("");
    try {
      const saved = i.online ? await api<Instrument>("/api/instruments", { method: "POST", body: JSON.stringify(i.online) }) : i;
      setSelected(s => s.some(row => matches(row, saved)) ? s : [...s, saved]);
      const update = (rows: PickerRow[]) => rows.map(row => row === i ? { ...saved, inPortfolio: i.inPortfolio } : row);
      setLocal(update); setOnline(o => o && { ...o, rows: update(o.rows) });
      setActive(-1); setToast("");
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  const searchOnline = async (provider: "default" | "av" = "default") => {
    if (!enough) return;
    // Independent of the local search, which keeps running (and finishing) on its own.
    onlineController.current?.abort();
    const version = ++onlineVersion.current;
    const abort = new AbortController(); onlineController.current = abort;
    setOpen(true); setOnlineLoading(true); setOnlineError(""); setWarnings([]); setActive(-1); setLastOnline(provider);
    try {
      const found = await api<OnlineSearch>(`/api/instruments/search/online?q=${encodeURIComponent(normalized)}&portfolioId=${portfolio.id}${provider === "av" ? "&provider=av" : ""}`, { signal: abort.signal });
      if (version !== onlineVersion.current || abort.signal.aborted) return;
      const resolved = provider === "av" ? "ALPHAVANTAGE" : found.provider;
      // Alpha Vantage is offered only when no online source had anything to show.
      if (provider === "default") setAVOffer(found.results.length === 0 && resolved !== "EULERPOOL" && resolved !== "YAHOO");
      setOnline({ provider: resolved, rows: found.results.map((row, index) => ({ ...row, id: row.existingInstrumentId ?? -(index + 1), isin: row.isin ?? "", wkn: row.wkn ?? null, online: row })) });
      setWarnings([...(found.warnings ?? []).map(code => warningText(code, resolved)), ...(found.warning ? [onlineMessage(found.warning)] : [])]);
      if (provider === "default" && found.warning) setAVOffer(true);
    } catch (e) {
      if (version === onlineVersion.current && !abort.signal.aborted) {
        if (provider === "default") setAVOffer(true);
        setOnlineError(e instanceof APIError && provider === "av" ? e.message : onlineMessage(e instanceof APIError ? e.code : "UPSTREAM_ERROR"));
      }
    } finally {
      if (provider === "av") window.dispatchEvent(new window.Event("av-budget-changed"));
      if (version === onlineVersion.current && !abort.signal.aborted) setOnlineLoading(false);
    }
  };
  const skipFocusOpen = useRef(false);
  // Closing returns focus to the input without reopening the panel.
  const close = () => { setOpen(false); setActive(-1); skipFocusOpen.current = true; input.current?.focus(); skipFocusOpen.current = false; };
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

  const showPanel = open && enough;
  const resultCount = flat.length;
  const nothing = !loading && !onlineLoading && !searchError && !onlineError && resultCount === 0;
  const announcement = !showPanel || loading || onlineLoading ? "" : searchError || onlineError ? "Search failed" : `${resultCount} ${resultCount === 1 ? "result" : "results"}`;
  const activeRow = active >= 0 ? flat[active] : undefined;
  const canOfferAV = avOffer && !identifier.test(normalized.toUpperCase());

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") { if (showPanel) { e.preventDefault(); close(); } return; }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault(); setOpen(enough);
      const available = flat.map((r, n) => disabled(r.row) ? -1 : n).filter(n => n >= 0);
      if (!available.length) return;
      const position = available.indexOf(active);
      setActive(available[(position + (e.key === "ArrowDown" ? 1 : position < 0 ? 0 : -1) + available.length) % available.length]);
      return;
    }
    if ((e.key === "i" || e.key === "I") && e.altKey) {
      if (activeRow?.row.type === "ETF" && activeRow.row.id > 0) { e.preventDefault(); setInfo({ id: activeRow.row.id, name: activeRow.row.name }); }
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      if (open && activeRow) void choose(activeRow.row); else void searchOnline();
    }
  };

  return <>
    <div className="search-picker" ref={wrapper}>
      <label htmlFor="instrument-search">Find stocks and ETFs</label>
      <div className="search-bar">
        <div className="search-field">
          <SearchIcon />
          <input ref={input} id="instrument-search" role="combobox" aria-autocomplete="list" aria-expanded={showPanel} aria-controls={listId} aria-keyshortcuts="Alt+I" aria-activedescendant={activeRow ? optionId(activeRow.section, activeRow.row) : undefined} autoComplete="off" placeholder="Name, ticker, ISIN or WKN" value={query} onChange={e => changeQuery(e.target.value)} onFocus={() => { if (enough && !skipFocusOpen.current) setOpen(true); }} onKeyDown={onKeyDown} />
          {query && <button type="button" className="clear-button" aria-label="Clear search" onClick={() => { changeQuery(""); input.current?.focus(); }}>×</button>}
        </div>
        <button type="button" className="search-button" aria-label="Search" disabled={!enough || busy} aria-busy={onlineLoading} onClick={() => void searchOnline()}>{onlineLoading ? <Spinner /> : <SearchIcon />}<span className="label">Search</span></button>
      </div>
      <div className="sr-only" role="status" aria-live="polite">{announcement}</div>
      {showPanel && <div className="search-dropdown">
        <div id={listId} role="listbox" aria-label="Instrument results" aria-busy={loading || onlineLoading}>
          {sections.map(section => <div key={section.key} role="group" aria-labelledby={`${uid}-${section.key}`}>
            <div className="section-title" id={`${uid}-${section.key}`}>{section.title}</div>
            {warnings.length > 0 && section.key === "online" && <div className="banner" role="note">{warnings.join(" · ")}</div>}
            {section.rows.map(i => {
              const index = flat.findIndex(r => r.row === i && r.section === section.key);
              const off = disabled(i);
              return <div key={optionId(section.key, i)} id={optionId(section.key, i)} role="option" aria-selected={isSelected(i)} aria-disabled={off || busy} className={`option ${off ? "disabled" : ""} ${active === index ? "active" : ""}`} onClick={() => void choose(i)}>
                <span className={`pill ${i.type === "ETF" ? "pill-etf" : "pill-stock"}`}>{i.type === "ETF" ? "ETF" : "Stock"}</span>
                <span className="option-main">
                  <strong className="option-name"><Highlight text={i.name} query={normalized} /></strong>
                  <span className="option-meta">{(i.online?.exchangeName ? [i.ticker, i.isin || "no ISIN", i.online.exchangeName, i.online.sector] : [i.ticker, i.isin || "—", i.wkn ?? "—"]).filter(Boolean).join(" · ")}</span>
                </span>
                <span className="option-side">
                  {off && <span className="option-state">{inPortfolio(i) ? "✓ In portfolio" : "Selected"}</span>}
                  {i.currency && <span className="chip">{i.currency}</span>}
                  {/* Not a button: interactive children are not allowed inside a listbox. Keyboard users press Alt+I. */}
                  {i.type === "ETF" && i.id > 0 && <span className="info-button" aria-hidden="true" title="ETF profile (Alt+I)" onClick={e => { e.stopPropagation(); setInfo({ id: i.id, name: i.name }); }}>ⓘ</span>}
                </span>
              </div>;
            })}
          </div>)}
        </div>
        {(loading || onlineLoading) && <div className="skeleton" aria-hidden="true">{[0, 1, 2].map(n => <div key={n} className="skeleton-row"><span /><span /></div>)}</div>}
        {(loading || onlineLoading) && <p className="sr-only" role="status">Searching…</p>}
        {searchError && !loading && <div className="state error-state" role="alert"><p>{searchError}</p><button type="button" onClick={() => { setSearchError(""); setLoading(true); setRetry(n => n + 1); }}>Retry</button></div>}
        {onlineError && !onlineLoading && <div className="state error-state" role="alert"><p>{onlineError}</p><button type="button" disabled={busy} onClick={() => void searchOnline(lastOnline)}>Retry</button></div>}
        {warnings.length > 0 && !sections.some(s => s.key === "online") && !onlineLoading && <div className="banner" role="note">{warnings.join(" · ")}</div>}
        {nothing && <div className="state empty-state"><svg viewBox="0 0 24 24" width="28" height="28" aria-hidden="true" focusable="false"><circle cx="11" cy="11" r="7" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="m16.5 16.5 4 4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" /></svg><p role="status">No results for “{normalized}”</p>{!online && <small>Press Search to look online.</small>}</div>}
        {canOfferAV && !onlineLoading && <div className="state"><button type="button" disabled={busy || loading} onClick={() => void searchOnline("av")}>Try Alpha Vantage (1 request)</button></div>}
        {(online?.provider === "EULERPOOL" || online?.provider === "YAHOO") && <div className="dropdown-footer">Data by {providerName(online.provider)}</div>}
      </div>}
      {info && <EtfInfoPanel instrumentId={info.id} name={info.name} close={() => setInfo(null)} />}
    </div>
    <section className="selection"><h2>Selected</h2>{selected.length === 0 ? <p className="selection-empty">Search above to select stocks or ETFs.</p> : <div className="table-scroll"><table><thead><tr>{["Ticker", "Name", "Type", "ISIN", "WKN", "Remove"].map(h => <th key={h}>{h}</th>)}</tr></thead><tbody>{selected.map(i => <tr key={i.id}><td><strong>{i.ticker}</strong></td><td>{i.name}{i.type === "ETF" && <EtfInfoButton instrumentId={i.id} name={i.name} />}</td><td><span className="badge">{i.type}</span></td><td>{i.isin || "—"}</td><td>{i.wkn ?? "—"}</td><td><button aria-label={`Remove ${i.ticker} from selection`} disabled={busy} onClick={() => setSelected(s => s.filter(row => row.id !== i.id))}>×</button></td></tr>)}</tbody></table></div>}</section>
    <div className="add-controls"><p className="muted">{current} / {portfolio.itemLimit} items | {selected.length} selected</p>{overflow && <p className="hint">Only {Math.max(0, portfolio.itemLimit - current)} more items fit in this portfolio. Remove some from the selection.</p>}{error && <p className="error" role="alert">{error}</p>}<button className="primary" disabled={!selected.length || busy || overflow || selected.length > 30} onClick={add}>{busy ? "Adding…" : "Add to portfolio"}</button>{toast && <p className="toast" role="status">{toast}</p>}</div>
  </>;
}
