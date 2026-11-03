"use client";

import { useEffect, useState } from "react";
import { API_URL, authenticatedFetch, withSessionLock } from "../lib/session";

type User = { name: string; email: string; picture?: string };

export default function Home() {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [signingOut, setSigningOut] = useState(false);

  const signOut = async () => {
    setSigningOut(true);
    setError("");
    try {
      await withSessionLock(async () => {
        const response = await fetch(`${API_URL}/auth/logout`, { method: "POST", credentials: "include" });
        if (!response.ok) throw new Error("Sign out failed");
      });
      setUser(null);
    } catch {
      setError("Could not sign out. Please try again.");
    } finally {
      setSigningOut(false);
    }
  };

  useEffect(() => {
    const oauthError = new URLSearchParams(window.location.search).get("error");
    if (oauthError) setError(`Google sign-in was cancelled or failed: ${oauthError}.`);
    authenticatedFetch("/auth/me")
      .then((res) => { if (!res.ok) throw new Error(); return res.json(); })
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <main className="loading"><span className="spinner" /></main>;
  if (!user) return <Login error={error} setError={setError} />;

  return <main className="workspace"><header><span className="brand">holdings</span><div className="profile"><span>{user.name}</span><button onClick={signOut} disabled={signingOut}>{signingOut ? "Signing out…" : "Sign out"}</button></div></header>{error && <p className="error" role="alert">{error}</p>}<section className="empty-state" aria-label="Empty workspace"><div className="empty-mark">＋</div><h1>Your workspace is ready.</h1><p>Nothing here yet.</p></section></main>;
}

function Login({ error, setError }: { error: string; setError: (value: string) => void }) {
  const googleLogin = () => { setError(""); window.location.href = `${API_URL}/auth/google`; };
  return <main className="login-shell"><div className="login-card"><div className="logo">h<span>.</span></div><p className="eyebrow">A quiet place for your work</p><h1>Welcome to holdings</h1><p className="subcopy">Log in or create an account to continue to your private workspace.</p>{error && <p className="error">{error}</p>}<button className="google-button" onClick={googleLogin}><GoogleIcon />Log in or sign up with Google</button><p className="terms">By continuing, you agree to our <a href="#terms">Terms</a> and <a href="#privacy">Privacy Policy</a>.</p></div><p className="login-footer">© {new Date().getFullYear()} holdings</p></main>;
}

function GoogleIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path fill="#4285F4" d="M21.35 12.23c0-.72-.06-1.42-.18-2.09H12v3.96h5.24a4.48 4.48 0 0 1-1.94 2.94v2.45h3.14c1.84-1.69 2.91-4.18 2.91-7.26Z"/><path fill="#34A853" d="M12 21.6c2.63 0 4.84-.87 6.45-2.36l-3.14-2.45c-.87.58-1.98.92-3.31.92-2.54 0-4.69-1.72-5.46-4.03H3.3v2.53A9.75 9.75 0 0 0 12 21.6Z"/><path fill="#FBBC05" d="M6.54 13.68A5.86 5.86 0 0 1 6.23 12c0-.58.1-1.15.31-1.68V7.79H3.3A9.75 9.75 0 0 0 2.25 12c0 1.57.38 3.05 1.05 4.21l3.24-2.53Z"/><path fill="#EA4335" d="M12 6.29c1.43 0 2.72.49 3.73 1.46l2.8-2.8C16.83 3.38 14.63 2.4 12 2.4a9.75 9.75 0 0 0-8.7 5.39l3.24 2.53C7.31 8.01 9.46 6.29 12 6.29Z"/></svg>; }
