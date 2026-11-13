"use client";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api, PortfolioDetail } from "../../../lib/api";
import { AppHeader } from "../../../components/app-header";
import { IconArrowLeft } from "../../../components/icons";
import { PortfolioWorkspace } from "../../../components/portfolio-workspace";
import { useAccount } from "../../../components/use-account";

export default function PortfolioLayout({ children }: { children: React.ReactNode }) {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [portfolio, setPortfolio] = useState<PortfolioDetail | null>(null);
  const [error, setError] = useState("");
  const signedOut = useCallback(() => router.push("/"), [router]);
  const account = useAccount(signedOut);
  useEffect(() => {
    let active = true;
    setPortfolio(null);
    setError("");
    api<PortfolioDetail>(`/api/portfolios/${encodeURIComponent(id)}`).then(p => { if (active) setPortfolio(p); }).catch(e => { if (active) setError(e.message); });
    return () => { active = false; };
  }, [id]);
  return <div className="app-shell">
    <AppHeader user={account.user} onSignOut={account.signOut} signingOut={account.signingOut} />
    <main className="page page-with-tabbar" id="main">
      <Link href="/" className="back-link"><IconArrowLeft size={16} />All portfolios</Link>
      {account.error && <p className="error" role="alert">{account.error}</p>}
      {error ? <p role="alert" className="error">{error}</p> : !portfolio ? <p role="status" className="muted">Loading portfolio…</p> : <>
        <div className="page-title"><p className="eyebrow">Portfolio</p><h1>{portfolio.name}</h1></div>
        <PortfolioWorkspace key={id} portfolio={portfolio}>{children}</PortfolioWorkspace>
      </>}
    </main>
  </div>;
}
