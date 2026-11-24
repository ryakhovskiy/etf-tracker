import { api } from "./api";

/** Wait before retrying after a failed poll. */
export const ERROR_RETRY_MS = 2000;
/** Safety net: wait this long before asking again when an answer brought no new version. */
export const UNCHANGED_GUARD_MS = 1000;

/** What the progress loop needs from a price-fetch job answer. `version` is absent on servers without long polling. */
export type PolledJob = { status: string; version?: number };

export type PollJobHandlers<T extends PolledJob> = {
  /** Called with every answer, including the final DONE one. Throwing here is handled like a failed request. */
  onJob: (job: T) => void | Promise<void>;
  /**
   * Called after a failed request (or a throwing `onJob`) with the number of consecutive failures.
   * Return true to retry after ERROR_RETRY_MS, false to stop polling.
   */
  onError: (error: unknown, consecutiveErrors: number) => boolean | Promise<boolean>;
};

/** Resolves after `ms`, or as soon as `signal` aborts. */
const sleep = (ms: number, signal: AbortSignal) => new Promise<void>(resolve => {
  if (signal.aborted) return resolve();
  const done = () => { clearTimeout(timer); signal.removeEventListener("abort", done); resolve(); };
  const timer = setTimeout(done, ms);
  signal.addEventListener("abort", done);
});

/**
 * Follows a price-fetch job by long polling and returns a function that stops it.
 *
 * The first GET has no `since`. Every later GET carries `since=<last version>` and is sent as soon as the
 * previous answer arrives: the server holds it open until the job changes. Polling ends after the DONE answer.
 * If an answer comes back with the version we already had (an old server, a mock, or the server's wait
 * timeout), the next request waits UNCHANGED_GUARD_MS so it can never become a hot loop.
 * Stopping aborts the request in flight, so no held request outlives its component.
 */
export function pollJob<T extends PolledJob>(path: string, { onJob, onError }: PollJobHandlers<T>): () => void {
  const abort = new AbortController();
  const { signal } = abort;
  void (async () => {
    let since: number | undefined;
    let errors = 0;
    while (!signal.aborted) {
      try {
        const job = await api<T>(since === undefined ? path : `${path}?since=${since}`, { signal });
        if (signal.aborted) return;
        errors = 0;
        const unchanged = job.version === undefined || job.version === since;
        since = job.version;
        await onJob(job);
        if (job.status === "DONE" || signal.aborted) return;
        if (unchanged) await sleep(UNCHANGED_GUARD_MS, signal);
      } catch (error) {
        if (signal.aborted) return;
        if (!(await onError(error, ++errors)) || signal.aborted) return;
        await sleep(ERROR_RETRY_MS, signal);
      }
    }
  })();
  return () => abort.abort();
}
