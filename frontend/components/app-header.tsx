"use client";
import Link from "next/link";
import { IconSignOut } from "./icons";
import { ThemeSwitcher } from "./theme-switcher";
import type { User } from "./use-account";

export function Brand() {
  return <Link href="/" className="brand" aria-label="holdings home"><span className="brand-mark" aria-hidden="true">h</span><span className="brand-name">holdings</span></Link>;
}

/** Sticky top bar: brand, theme switcher and (when signed in) the account. Used on every page. */
export function AppHeader({ user, onSignOut, signingOut = false }: { user?: User | null; onSignOut?: () => void; signingOut?: boolean }) {
  return <header className="app-header" data-testid="app-header">
    <div className="app-header-inner">
      <Brand />
      <div className="header-actions">
        <ThemeSwitcher />
        {user && <div className="account">
          <span className="avatar" aria-hidden="true">{(user.name || user.email || "?").trim().charAt(0).toUpperCase()}</span>
          <span className="account-name">{user.name}</span>
          {onSignOut && <button type="button" className="ghost" onClick={onSignOut} disabled={signingOut}><IconSignOut size={18} /><span>{signingOut ? "Signing out…" : "Sign out"}</span></button>}
        </div>}
      </div>
    </div>
  </header>;
}
