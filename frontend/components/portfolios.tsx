"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { FormEvent, useEffect, useRef, useState } from "react";
import { api, Portfolio } from "../lib/api";
import { IconPlus } from "./icons";
import { MetricsSkeleton, PortfolioMetrics } from "./portfolio-metrics";
import { SummaryStrip } from "./summary-strip";
import { usePortfolioSummaries } from "./use-portfolio-summaries";

export function Portfolios() {
  const router = useRouter();
  const [data, setData] = useState<{ portfolios: Portfolio[]; limit: number } | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<Portfolio | "create" | null>(null);
  const [deleting, setDeleting] = useState<Portfolio | null>(null);
  const [busy, setBusy] = useState(false);
  const summaries = usePortfolioSummaries(!!data && data.portfolios.length > 0);
  const load = () => api<{ portfolios: Portfolio[]; limit: number }>("/api/portfolios").then(setData);
  useEffect(() => { load().catch(e => setError(e.message)); }, []);
  const remove = async () => {
    if (!deleting) return;
    setBusy(true); setError("");
    try { await api(`/api/portfolios/${deleting.id}`, { method: "DELETE" }); setDeleting(null); await load(); void summaries.retry(); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  return <section className="portfolios">
    {error && <p className="error" role="alert">{error}</p>}
    {!data ? <p role="status">{error ? "Portfolios could not be loaded." : "Loading portfolios…"}</p> : <>
      {data.portfolios.length === 0 ? <div className="portfolio-empty tile"><div className="empty-mark" aria-hidden="true"><IconPlus size={26} /></div><h1>Your portfolios</h1><p className="muted">Create a portfolio to start selecting stocks and ETFs.</p><button className="primary" onClick={() => setEditing("create")}>Create portfolio</button></div> : <>
        <div className="section-heading"><div><p className="eyebrow">Your workspace</p><h1>Portfolios</h1><p className="muted">{data.portfolios.length} / {data.limit} portfolios</p></div><button className="primary" disabled={data.portfolios.length >= data.limit} onClick={() => setEditing("create")}>Create portfolio</button></div>
        {data.portfolios.length >= data.limit && <p className="hint">Portfolio limit reached. Delete a portfolio to create another.</p>}
        <SummaryStrip data={summaries.data} loading={summaries.loading} failed={summaries.failed} updating={summaries.updating} progress={summaries.progress} notice={summaries.notice} onRefresh={() => void summaries.refresh()} onRetry={() => void summaries.retry()} />
        <ul className="portfolio-grid" aria-busy={summaries.loading || summaries.updating}>{data.portfolios.map(p => { const summary = summaries.data?.summaries.find(row => row.portfolioId === p.id); return <li key={p.id} className="portfolio-card tile tile-interactive" data-testid="portfolio-card"><Link href={`/portfolios/${p.id}`} className="portfolio-card-link"><span className="portfolio-card-mark" aria-hidden="true">{p.name.trim().charAt(0).toUpperCase() || "#"}</span><strong>{p.name}</strong><span className="muted">{p.itemCount} {p.itemCount === 1 ? "item" : "items"}</span></Link>{summary ? <PortfolioMetrics summary={summary} updating={summaries.updating} /> : summaries.loading ? <MetricsSkeleton /> : null}<div className="row-actions"><button onClick={() => setEditing(p)}>Rename<span className="sr-only"> {p.name}</span></button><button onClick={() => setDeleting(p)}>Delete<span className="sr-only"> {p.name}</span></button></div></li>; })}</ul>
      </>}
    </>}
    {editing && <NameDialog portfolio={editing} close={() => setEditing(null)} saved={async p => { const create = editing === "create"; setEditing(null); if (create) router.push(`/portfolios/${p.id}`); else await load(); }} />}
    {deleting && <Modal title="Delete portfolio" close={() => { if (!busy) setDeleting(null); }}><p>Delete “{deleting.name}”? All items and transactions in this portfolio will be deleted.</p><div className="dialog-actions"><button disabled={busy} onClick={() => setDeleting(null)}>Cancel</button><button className="danger" disabled={busy} onClick={remove}>{busy ? "Deleting…" : "Delete portfolio"}</button></div></Modal>}
  </section>;
}

function NameDialog({ portfolio, close, saved }: { portfolio: Portfolio | "create"; close: () => void; saved: (p: Portfolio) => Promise<void> }) {
  const [name, setName] = useState(portfolio === "create" ? "" : portfolio.name);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try { const p = await api<Portfolio>(portfolio === "create" ? "/api/portfolios" : `/api/portfolios/${portfolio.id}`, { method: portfolio === "create" ? "POST" : "PATCH", body: JSON.stringify({ name }) }); await saved(p); }
    catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  return <Modal title={portfolio === "create" ? "Create portfolio" : "Rename portfolio"} close={() => { if (!busy) close(); }}><form onSubmit={submit}><label htmlFor="portfolio-name">Name</label><input id="portfolio-name" autoFocus maxLength={100} value={name} onChange={e => setName(e.target.value)} required />{error && <p className="error" role="alert">{error}</p>}<div className="dialog-actions"><button type="button" disabled={busy} onClick={close}>Cancel</button><button className="primary" disabled={busy || !name.trim()}>{busy ? "Saving…" : "Save"}</button></div></form></Modal>;
}

export function Modal({ title, close, children }: { title: string; close: () => void; children: React.ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => { const dialog = ref.current!; dialog.showModal(); return () => dialog.close(); }, []);
  return <dialog ref={ref} aria-label={title} onCancel={e => { e.preventDefault(); close(); }} onClick={e => { if (e.target === e.currentTarget) close(); }}><h2>{title}</h2>{children}</dialog>;
}
