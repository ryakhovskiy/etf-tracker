/** How often a running price-fetch job is polled. Yahoo jobs run in parallel and finish in about a second, so they are polled quickly. */
export const YAHOO_POLL_MS = 500;
/** Alpha Vantage and Eulerpool jobs are paced by their quotas, so a slower poll is enough. */
export const SLOW_POLL_MS = 2000;

export const pollInterval = (yahoo: boolean): number => (yahoo ? YAHOO_POLL_MS : SLOW_POLL_MS);
