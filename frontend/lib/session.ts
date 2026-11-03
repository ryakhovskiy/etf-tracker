export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

let sessionQueue: Promise<unknown> = Promise.resolve();

export function withSessionLock<T>(action: () => Promise<T>): Promise<T> {
  // Web Locks coordinates tabs; the promise queue also covers browsers without it.
  const run = () => typeof navigator !== "undefined" && navigator.locks
    ? navigator.locks.request("holdings-session", action)
    : action();
  const result = sessionQueue.then(run, run);
  sessionQueue = result.then(() => undefined, () => undefined);
  return result;
}

export async function authenticatedFetch(path: string, init?: RequestInit) {
  const request = () => fetch(`${API_URL}${path}`, { ...init, credentials: "include" });
  const response = await request();
  if (response.status !== 401) return response;
  return withSessionLock(async () => {
    // Another request or tab may already have refreshed while this one waited.
    const retried = await request();
    if (retried.status !== 401) return retried;
    const refreshed = await fetch(`${API_URL}/auth/refresh`, { method: "POST", credentials: "include" });
    return refreshed.ok ? request() : refreshed;
  });
}
