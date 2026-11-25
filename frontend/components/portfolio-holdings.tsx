"use client";
import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import { api, APIError } from "../lib/api";
import { instrumentColor } from "../lib/colors";
import { IconStar } from "./icons";
import { dayLabel, money } from "../lib/dividends";
import { usePortfolioUpdates } from "./portfolio-updates";

export type PortfolioItemOverview = {
  instrumentId: number; ticker: string; isin: string | null; name: string; type: "STOCK" | "ETF";
  latestQuote: { priceEur: string; quoteDate: string; provider: string } | null;
  payDelayDays: number;
  /** Starred by the user (one star per title, across all portfolios). */
  favorite?: boolean;
};
export const MAX_PAY_DELAY = 90;

/** "" for a valid whole number of days 0–90, otherwise the message to show. */
export function delayError(raw: string) {
  return /^\d{1,2}$/.test(raw.trim()) && Number(raw) <= MAX_PAY_DELAY ? "" : `Enter a whole number of days from 0 to ${MAX_PAY_DELAY}.`;
}

type RowState = { draft: string; saving: boolean; error: string; saved: boolean };

function DelayInput({ item, onSaved }: { item: PortfolioItemOverview; onSaved: (days: number) => void }) {
  const [state, setState] = useState<RowState>({ draft: String(item.payDelayDays), saving: false, error: "", saved: false });
  const stored = useRef(item.payDelayDays);
  // A reload with a new stored value replaces an untouched draft.
  useEffect(() => {
    if (stored.current !== item.payDelayDays) {
      stored.current = item.payDelayDays;
      setState(s => ({ ...s, draft: String(item.payDelayDays), error: "" }));
    }
  }, [item.payDelayDays]);

  const save = async () => {
    const error = delayError(state.draft);
    if (error) { setState(s => ({ ...s, error, saved: false })); return; }
    const days = Number(state.draft);
    if (days === stored.current) { setState(s => ({ ...s, draft: String(days), error: "" })); return; }
    setState(s => ({ ...s, saving: true, error: "", saved: false }));
    try {
      await api(`/api/users/me/instruments/${item.instrumentId}/pay-delay`, { method: "PUT", body: JSON.stringify({ payDelayDays: days }) });
      stored.current = days;
      setState({ draft: String(days), saving: false, error: "", saved: true });
      onSaved(days);
    } catch (e) {
      const err = e as APIError;
      setState(s => ({ ...s, saving: false, error: err.details?.fields?.payDelayDays ?? err.message }));
    }
  };
  const id = `pay-delay-${item.instrumentId}`;
  return <div className="delay-field">
    <input id={id} type="number" inputMode="numeric" min={0} max={MAX_PAY_DELAY} step={1} value={state.draft}
      aria-label={`Pay-date delay for ${item.ticker} in days`} aria-invalid={state.error ? true : undefined} aria-describedby={state.error ? `${id}-error` : undefined}
      disabled={state.saving}
      onChange={e => setState(s => ({ ...s, draft: e.target.value, saved: false }))}
      onBlur={() => void save()}
      onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); void save(); } }} />
    <span className="delay-unit">days</span>
    {state.saving && <span className="delay-status" role="status">Saving…</span>}
    {state.saved && <span className="delay-status saved" role="status">Saved</span>}
    {state.error && <span className="delay-error error" id={`${id}-error`} role="alert">{state.error}</span>}
  </div>;
}

function StarToggle({ item, onToggle }: { item: PortfolioItemOverview; onToggle: (item: PortfolioItemOverview) => void }) {
  const on = !!item.favorite;
  return <button type="button" className={`star-toggle ghost${on ? " is-on" : ""}`} data-testid="favorite-toggle" aria-pressed={on}
    aria-label={on ? `Remove ${item.name} from favorites` : `Add ${item.name} to favorites`} onClick={() => onToggle(item)}><IconStar size={20} filled={on} /></button>;
}

/** The items already in the portfolio, with their latest stored quote and the
 * user's pay-date delay (one value per instrument across all portfolios). */
export function PortfolioHoldings({ id }: { id: number }) {
  const { activeTab, revision, changed } = usePortfolioUpdates();
  const active = activeTab === "items";
  const [items, setItems] = useState<PortfolioItemOverview[] | null>(null);
  const [error, setError] = useState("");
  const [starError, setStarError] = useState("");
  const version = useRef(0);
  const starring = useRef(new Set<number>());
  const setFavorite = useCallback((instrumentId: number, favorite: boolean) => setItems(list => list && list.map(i => i.instrumentId === instrumentId ? { ...i, favorite } : i)), []);
  /** Optimistic: the star flips at once and flips back, with a message, when the server refuses. */
  const toggleStar = useCallback(async (item: PortfolioItemOverview) => {
    const { instrumentId } = item;
    if (starring.current.has(instrumentId)) return;
    starring.current.add(instrumentId);
    const next = !item.favorite;
    setFavorite(instrumentId, next); setStarError("");
    try {
      await api(`/api/favorites/${instrumentId}`, { method: next ? "PUT" : "DELETE" });
    } catch (e) {
      // Unstarring something that is already gone is the state the user asked for.
      if (!next && (e as APIError).status === 404) return;
      setFavorite(instrumentId, !next);
      setStarError(`Could not ${next ? "add" : "remove"} ${item.name} ${next ? "to" : "from"} favorites. ${(e as Error).message}`);
    } finally { starring.current.delete(instrumentId); }
  }, [setFavorite]);
  const load = useCallback(async () => {
    const mine = ++version.current;
    try {
      const next = await api<PortfolioItemOverview[]>(`/api/portfolios/${id}/items`);
      if (mine === version.current) { setItems(next); setError(""); }
    } catch (e) {
      if (mine === version.current) setError((e as Error).message);
    }
  }, [id]);
  // Loads when the tab is first opened, then again only after portfolio changes:
  // switching tabs keeps what is shown, like the other panels.
  const loaded = useRef(false);
  useEffect(() => { if (active && !loaded.current) { loaded.current = true; void load(); } }, [active, load]);
  useEffect(() => { if (revision > 0 && loaded.current) void load(); }, [revision, load]);

  return <section className="selection portfolio-holdings" aria-labelledby="portfolio-holdings-title">
    <h2 id="portfolio-holdings-title">In the portfolio</h2>
    <p className="muted">The pay-date delay moves each dividend from its ex-date to the estimated payment day (moved to the next German working day). It applies to this instrument in all your portfolios.</p>
    {error && <p className="error" role="alert">{error}</p>}
    {starError && <p className="error" role="alert">{starError}</p>}
    {items === null ? (!error && <p className="muted" role="status">Loading items…</p>) : items.length === 0 ? <p className="selection-empty">No items in this portfolio yet.</p> :
      <ul className="item-tiles">{items.map(item => <li key={item.instrumentId} className="item-tile tile" data-testid="portfolio-item" aria-label={`${item.ticker} ${item.name}`} style={{ "--stripe": instrumentColor(item.instrumentId) } as CSSProperties}>
        <div className="item-head"><div className="item-title"><strong className="item-ticker">{item.ticker}</strong><span className="item-name">{item.name}</span></div><div className="item-head-side"><span className={`pill ${item.type === "ETF" ? "pill-etf" : "pill-stock"}`}>{item.type === "ETF" ? "ETF" : "Stock"}</span><StarToggle item={item} onToggle={i => void toggleStar(i)} /></div></div>
        <dl className="item-facts">
          <div><dt>ISIN</dt><dd className="num">{item.isin ?? "—"}</dd></div>
          <div><dt>Current price</dt><dd className="price-cell">{item.latestQuote ? <>{money(item.latestQuote.priceEur)} <span className="muted">({dayLabel(item.latestQuote.quoteDate)})</span></> : <span className="muted" title="No stored quote yet.">–</span>}</dd></div>
        </dl>
        <div className="item-delay"><span className="field-caption">Pay-date delay</span><DelayInput item={item} onSaved={days => { setItems(list => list && list.map(i => i.instrumentId === item.instrumentId ? { ...i, payDelayDays: days } : i)); changed(); }} /></div>
      </li>)}</ul>}
  </section>;
}
