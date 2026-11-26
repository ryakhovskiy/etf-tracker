"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, APIError } from "../lib/api";
import { favoriteChange, favoritePrice, type FavoritesResponse } from "../lib/favorites";
import { chartWarning, type InstrumentChart } from "../lib/ticker";
import { AppHeader } from "./app-header";
import { IconArrowLeft } from "./icons";
import { StarToggle } from "./star-toggle";
import { TickerChart } from "./ticker-chart";
import { useAccount } from "./use-account";

type State = { status: "loading" } | { status: "missing" } | { status: "error"; message: string } | { status: "ready"; chart: InstrumentChart };

/** The page of one title: header (name, price, day change, star) and the price chart with the user's buys. */
export function InstrumentView({ id }: { id: string }) {
  const router = useRouter();
  const signedOut = useCallback(() => router.push("/"), [router]);
  const account = useAccount(signedOut);
  const [state, setState] = useState<State>({ status: "loading" });
  const [favorite, setFavorite] = useState<boolean | null>(null);
  const [starError, setStarError] = useState("");
  const starring = useRef(false);

  useEffect(() => {
    let active = true;
    setState({ status: "loading" }); setFavorite(null); setStarError("");
    const loadFavorite = () => api<FavoritesResponse>("/api/favorites")
      .then(list => { if (active) setFavorite(list.favorites.some(f => String(f.instrumentId) === id)); })
      .catch(() => { /* Without the list the star stays disabled; the chart still works. */ });
    api<InstrumentChart>(`/api/instruments/${encodeURIComponent(id)}/chart`)
      .then(chart => {
        if (!active) return;
        setState({ status: "ready", chart });
        // The answer says whether the title is starred; only an answer without the flag needs the favorites list.
        if (typeof chart.favorite === "boolean") setFavorite(chart.favorite); else void loadFavorite();
      })
      .catch((e: unknown) => { if (active) setState((e as APIError).status === 404 ? { status: "missing" } : { status: "error", message: (e as Error).message }); });
    return () => { active = false; };
  }, [id]);

  const toggle = async () => {
    if (favorite === null || starring.current || state.status !== "ready") return;
    starring.current = true;
    const next = !favorite, name = state.chart.instrument.name;
    setFavorite(next); setStarError("");
    try {
      await api(`/api/favorites/${encodeURIComponent(id)}`, { method: next ? "PUT" : "DELETE" });
    } catch (e) {
      // Unstarring something that is already gone is the state the user asked for.
      if (!(!next && (e as APIError).status === 404)) { setFavorite(!next); setStarError(`Could not ${next ? "add" : "remove"} ${name} ${next ? "to" : "from"} favorites. ${(e as Error).message}`); }
    } finally { starring.current = false; }
  };

  return <div className="app-shell">
    <AppHeader user={account.user} onSignOut={account.signOut} signingOut={account.signingOut} />
    <main className="page instrument-view" id="main" data-testid="instrument-view">
      <Link href="/" className="back-link"><IconArrowLeft size={16} />All portfolios</Link>
      {account.error && <p className="error" role="alert">{account.error}</p>}
      {state.status === "loading" && <p className="muted" role="status">Loading chart…</p>}
      {state.status === "missing" && <p className="error" role="alert" data-testid="instrument-not-found">This title is not in your portfolios.</p>}
      {state.status === "error" && <p className="error" role="alert">Could not load the chart. {state.message}</p>}
      {state.status === "ready" && <Ready chart={state.chart} favorite={favorite} onToggle={() => void toggle()} starError={starError} />}
    </main>
  </div>;
}

function Ready({ chart, favorite, onToggle, starError }: { chart: InstrumentChart; favorite: boolean | null; onToggle: () => void; starError: string }) {
  const { instrument, latest } = chart;
  const change = favoriteChange(latest.change);
  const ids = [instrument.isin, instrument.ticker].filter(Boolean).join(" · ");
  return <>
    <header className="instrument-head">
      <div className="instrument-title">
        <h1>{instrument.name}</h1>
        <p className="muted" data-testid="instrument-ids">{ids}</p>
      </div>
      <StarToggle name={instrument.name} on={!!favorite} disabled={favorite === null} onToggle={onToggle} />
    </header>
    {starError && <p className="error" role="alert">{starError}</p>}
    <div className="instrument-quote">
      <span className="instrument-price num" data-testid="instrument-price">{favoritePrice(latest.priceEur)}</span>
      <span className={`instrument-change num performance-${change.tone}`} data-testid="instrument-change">
        {latest.change && <span className="sr-only">Change vs previous close: </span>}{change.text}
      </span>
    </div>
    {chart.warning && <p className="muted instrument-warning" role="status" data-testid="instrument-warning">{chartWarning(chart.warning)}</p>}
    <TickerChart chart={chart} />
  </>;
}
