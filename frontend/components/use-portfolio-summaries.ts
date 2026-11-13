"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../lib/api";
import type { RefreshJob, RefreshResult, Summaries } from "../lib/portfolio-summary";

export type RefreshProgress = { done: number; total: number } | null;
const POLL_MS = 2000;
const MAX_POLL_ERRORS = 5;

/**
 * Summaries for the portfolio list, plus the user-level real-time price refresh:
 * - loads GET /api/portfolios/summaries (stored figures show immediately);
 * - once per visit, when the server says autoFetch is true, POSTs /api/price-refresh-jobs
 *   (Yahoo only; the server re-checks staleness, the 15-minute throttle and any running job);
 * - polls the job, keeps the stored figures on screen, then reloads the summaries in place;
 * - `refresh()` is the manual "Refresh prices" (force: skips only the 15-minute check).
 * Yahoo being unavailable is a quiet note (`notice`), never an error.
 */
export function usePortfolioSummaries(enabled: boolean) {
  const [data, setData] = useState<Summaries | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [jobId, setJobId] = useState<string | null>(null);
  const [progress, setProgress] = useState<RefreshProgress>(null);
  const [starting, setStarting] = useState(false);
  const [notice, setNotice] = useState("");
  const autoTried = useRef(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const load = useCallback(async () => {
    try {
      const next = await api<Summaries>("/api/portfolios/summaries");
      // Anything but the documented shape counts as "unavailable"; the tiles keep working without it.
      if (!Array.isArray(next?.summaries) || !next.total || !next.refresh) throw new Error("Unexpected summaries response");
      if (!mounted.current) return null;
      setData(next); setFailed(false);
      return next;
    } catch {
      if (mounted.current) setFailed(true);
      return null;
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, []);

  const request = useCallback(async (force: boolean) => {
    setStarting(true); setNotice("");
    try {
      const result = await api<RefreshResult>("/api/price-refresh-jobs", { method: "POST", body: JSON.stringify(force ? { force: true } : {}) });
      if (!mounted.current) return;
      if (result.started || ("reused" in result && result.reused)) setJobId(result.jobId);
      else if (result.reason === "YAHOO_UNAVAILABLE") setNotice("Yahoo is cooling down. Showing stored prices.");
    } catch {
      // A failed refresh never raises an error: the stored figures stay on screen.
    } finally {
      if (mounted.current) setStarting(false);
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    void load().then(next => {
      if (!next || autoTried.current) return;
      autoTried.current = true;
      if (next.refresh.runningJobId) setJobId(next.refresh.runningJobId);
      else if (next.refresh.autoFetch) void request(false);
    });
  }, [enabled, load, request]);

  useEffect(() => {
    if (!jobId) { setProgress(null); return; }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    let errors = 0;
    const finish = async () => { if (!active) return; setJobId(null); await load(); };
    const poll = async () => {
      try {
        const job = await api<RefreshJob>(`/api/price-fetch-jobs/${jobId}`);
        if (!active) return;
        errors = 0;
        setProgress({ done: job.items.filter(item => item.status !== "PENDING").length, total: job.items.length });
        if (job.status === "DONE") await finish(); else timer = setTimeout(poll, POLL_MS);
      } catch {
        if (!active) return;
        // The job may be gone (restart) or the network flaky: retry a few times, then show what is stored.
        if (++errors >= MAX_POLL_ERRORS) await finish(); else timer = setTimeout(poll, POLL_MS);
      }
    };
    void poll();
    return () => { active = false; clearTimeout(timer); };
  }, [jobId, load]);

  const refresh = useCallback(() => request(true), [request]);
  return { data, loading, failed, updating: !!jobId || starting, progress, notice, refresh, retry: load };
}
