"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";
import { dayLabel, money } from "../lib/dividends";
import { usePortfolioUpdates } from "./portfolio-updates";

export type PortfolioItemOverview = {
  instrumentId: number; ticker: string; isin: string | null; name: string; type: "STOCK" | "ETF";
  latestQuote: { priceEur: string; quoteDate: string; provider: string } | null;
  payDelayDays: number;
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

/** The items already in the portfolio, with their latest stored quote and the
 * user's pay-date delay (one value per instrument across all portfolios). */
export function PortfolioHoldings({ id }: { id: number }) {
  const { activeTab, revision, changed } = usePortfolioUpdates();
  const active = activeTab === "items";
  const [items, setItems] = useState<PortfolioItemOverview[] | null>(null);
  const [error, setError] = useState("");
  const version = useRef(0);
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
    {items === null ? (!error && <p className="muted" role="status">Loading items…</p>) : items.length === 0 ? <p className="selection-empty">No items in this portfolio yet.</p> :
      <div className="table-scroll"><table>
        <thead><tr>{["Ticker", "ISIN", "Name", "Current price", "Pay-date delay"].map(h => <th key={h}>{h}</th>)}</tr></thead>
        <tbody>{items.map(item => <tr key={item.instrumentId}>
          <td><strong>{item.ticker}</strong></td>
          <td>{item.isin ?? "—"}</td>
          <td>{item.name}</td>
          <td className="price-cell">{item.latestQuote ? <>{money(item.latestQuote.priceEur)} <span className="muted">({dayLabel(item.latestQuote.quoteDate)})</span></> : <span className="muted" title="No stored quote yet.">–</span>}</td>
          <td><DelayInput item={item} onSaved={days => { setItems(list => list && list.map(i => i.instrumentId === item.instrumentId ? { ...i, payDelayDays: days } : i)); changed(); }} /></td>
        </tr>)}</tbody>
      </table></div>}
  </section>;
}
