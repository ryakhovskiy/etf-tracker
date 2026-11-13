"use client";
import { useCallback, useEffect, useState } from "react";
import { API_URL, authenticatedFetch, withSessionLock } from "../lib/session";

export type User = { name: string; email: string; picture?: string };

/** The signed-in user (GET /auth/me) and the sign-out action shared by every page's top bar. */
export function useAccount(onSignedOut?: () => void) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [signingOut, setSigningOut] = useState(false);

  const signOut = useCallback(async () => {
    setSigningOut(true);
    setError("");
    try {
      await withSessionLock(async () => {
        const response = await fetch(`${API_URL}/auth/logout`, { method: "POST", credentials: "include" });
        if (!response.ok) throw new Error("Sign out failed");
      });
      setUser(null);
      onSignedOut?.();
    } catch {
      setError("Could not sign out. Please try again.");
    } finally {
      setSigningOut(false);
    }
  }, [onSignedOut]);

  useEffect(() => {
    let active = true;
    authenticatedFetch("/auth/me")
      .then((res) => { if (!res.ok) throw new Error(); return res.json(); })
      .then((next: User) => { if (active) setUser(next); })
      .catch(() => { if (active) setUser(null); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, []);

  return { user, loading, error, setError, signingOut, signOut };
}
