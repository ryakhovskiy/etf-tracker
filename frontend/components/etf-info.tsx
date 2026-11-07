"use client";
import { useEffect, useState } from "react";
import { api, APIError } from "../lib/api";
import { formatAum, formatDate, formatPercent, type EtfProfile } from "../lib/etf";
import { Modal } from "./portfolios";

/** The ⓘ icon next to an ETF name; opens the profile panel. */
export function EtfInfoButton({ instrumentId, name }: { instrumentId: number; name: string }) {
  const [open, setOpen] = useState(false);
  return <>
    <button type="button" className="info-button" aria-label={`About ${name}`} aria-haspopup="dialog" onClick={e => { e.stopPropagation(); setOpen(true); }}>ⓘ</button>
    {open && <EtfInfoPanel instrumentId={instrumentId} name={name} close={() => setOpen(false)} />}
  </>;
}

export function EtfInfoPanel({ instrumentId, name, close }: { instrumentId: number; name: string; close: () => void }) {
  const [profile, setProfile] = useState<EtfProfile | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let active = true;
    const refresh = () => api<EtfProfile>(`/api/instruments/${instrumentId}/etf-profile/refresh`, { method: "POST" });
    (async () => {
      try {
        let found: EtfProfile | null = null;
        try { found = await api<EtfProfile>(`/api/instruments/${instrumentId}/etf-profile`); }
        catch (e) { if (!(e instanceof APIError) || e.status !== 404) throw e; }
        // Missing or stale profiles are fetched on demand (1 Eulerpool request).
        if (!found || found.stale) {
          try { found = await refresh(); } catch (e) { if (!found) throw e; }
        }
        if (active) setProfile(found);
      } catch (e) {
        if (active) setError(e instanceof APIError && e.status === 404 ? "No profile is available for this ETF." : (e as Error).message);
      } finally { if (active) setLoading(false); }
    })();
    return () => { active = false; };
  }, [instrumentId]);
  const rows: [string, string][] = profile ? [
    ["Issuer", profile.etfCompany || "—"], ["Tracking index", profile.trackingIndex || "—"], ["TER", formatPercent(profile.expenseRatio)],
    ["Fund size (AUM)", formatAum(profile.aumEur)], ["Domicile", profile.domicile || "—"], ["Distribution yield", formatPercent(profile.dividendYield)],
    ["Inception", formatDate(profile.inceptionDate)],
  ] : [];
  return <Modal title={`${name} · ETF profile`} close={close}>
    {loading && <p role="status">Loading profile…</p>}
    {error && <p role="alert" className="error">{error}</p>}
    {profile && <dl className="etf-facts">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
      <div><dt>Website</dt><dd>{/^https?:\/\//.test(profile.website) ? <a href={profile.website} target="_blank" rel="noopener noreferrer">{profile.website.replace(/^https?:\/\/(www\.)?/, "").split("/")[0]}</a> : "—"}</dd></div></dl>}
    {profile && <p className="muted">Updated {formatDate(profile.fetchedAt)} · Data by Eulerpool</p>}
    <button type="button" onClick={close}>Close</button>
  </Modal>;
}
