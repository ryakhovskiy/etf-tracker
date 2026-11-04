import { authenticatedFetch } from "./session";

export class APIError extends Error {
  constructor(message: string, public code: string, public status: number, public details?: { fields?: Record<string, string>; lines?: import("./transactions").Line[]; preview?: import("./csv-import").Preview }) { super(message); }
}
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await authenticatedFetch(path, { ...init, headers: { ...(init?.body instanceof FormData ? {} : { "Content-Type": "application/json" }), ...init?.headers } });
  if (response.status === 401) {
    window.location.assign("/");
    throw new APIError("Please sign in.", "UNAUTHORIZED", 401);
  }
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new APIError(body?.error?.message ?? "Could not complete the request. Please try again.", body?.error?.code ?? "UNKNOWN", response.status, body?.error);
  }
  return response.status === 204 ? undefined as T : response.json();
}
export type Portfolio = { id: number; name: string; itemCount: number; createdAt: string };
export type PortfolioDetail = Portfolio & { instrumentIds: number[]; itemLimit: number };
export type Instrument = { id: number; name: string; ticker: string; type: "STOCK" | "ETF"; isin: string; wkn: string | null; inPortfolio: boolean };
