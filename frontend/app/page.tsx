"use client";

import { useEffect } from "react";
import { API_URL } from "../lib/session";
import { AppHeader, Brand } from "../components/app-header";
import { ThemeSwitcher } from "../components/theme-switcher";
import { useAccount } from "../components/use-account";
import { Portfolios } from "../components/portfolios";

export default function Home() {
  const { user, loading, error, setError, signingOut, signOut } = useAccount();

  useEffect(() => {
    const oauthError = new URLSearchParams(window.location.search).get("error");
    if (oauthError) setError(oauthError === "signups_closed" ? "Signups are currently closed." : `Google sign-in was cancelled or failed: ${oauthError}.`);
  }, [setError]);

  if (loading) return <main className="loading"><span className="spinner" role="status" aria-label="Loading" /></main>;
  if (!user) return <Login error={error} setError={setError} />;

  return <div className="app-shell">
    <AppHeader user={user} onSignOut={signOut} signingOut={signingOut} />
    <main className="page" id="main">{error && <p className="error" role="alert">{error}</p>}<Portfolios /></main>
  </div>;
}

function Login({ error, setError }: { error: string; setError: (value: string) => void }) {
  const googleLogin = () => { setError(""); window.location.href = `${API_URL}/auth/google`; };
  return <div className="app-shell">
    <header className="app-header" data-testid="app-header"><div className="app-header-inner"><Brand /><div className="header-actions"><ThemeSwitcher /></div></div></header>
    <main className="login-shell" id="main">
      <div className="login-card tile">
        <div className="logo" aria-hidden="true">h<span>.</span></div>
        <p className="eyebrow">A quiet place for your work</p>
        <h1>Welcome to holdings</h1>
        <p className="subcopy">Log in or create an account to continue to your private workspace.</p>
        {error && <p className="error" role="alert">{error}</p>}
        <button className="google-button" onClick={googleLogin}><GoogleIcon />Log in or sign up with Google</button>
        <p className="terms">By continuing, you agree to our <a href="#terms">Terms</a> and <a href="#privacy">Privacy Policy</a>.</p>
      </div>
      <p className="login-footer">© {new Date().getFullYear()} holdings</p>
    </main>
  </div>;
}

// The logo's four brand colours are tokens too (--google-*, see tokens.css) and are the same in every theme.
function GoogleIcon() { return <svg viewBox="0 0 24 24" aria-hidden="true"><path className="g-blue" d="M21.35 12.23c0-.72-.06-1.42-.18-2.09H12v3.96h5.24a4.48 4.48 0 0 1-1.94 2.94v2.45h3.14c1.84-1.69 2.91-4.18 2.91-7.26Z"/><path className="g-green" d="M12 21.6c2.63 0 4.84-.87 6.45-2.36l-3.14-2.45c-.87.58-1.98.92-3.31.92-2.54 0-4.69-1.72-5.46-4.03H3.3v2.53A9.75 9.75 0 0 0 12 21.6Z"/><path className="g-yellow" d="M6.54 13.68A5.86 5.86 0 0 1 6.23 12c0-.58.1-1.15.31-1.68V7.79H3.3A9.75 9.75 0 0 0 2.25 12c0 1.57.38 3.05 1.05 4.21l3.24-2.53Z"/><path className="g-red" d="M12 6.29c1.43 0 2.72.49 3.73 1.46l2.8-2.8C16.83 3.38 14.63 2.4 12 2.4a9.75 9.75 0 0 0-8.7 5.39l3.24 2.53C7.31 8.01 9.46 6.29 12 6.29Z"/></svg>; }
