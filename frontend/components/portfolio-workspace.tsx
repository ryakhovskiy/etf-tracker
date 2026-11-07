"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useEffect, useState, type ReactNode } from "react";
import type { PortfolioDetail } from "../lib/api";
import { Dashboard } from "./dashboard";
import { PortfolioItems } from "./portfolio-items";
import { PortfolioUpdatesProvider, type PortfolioTab } from "./portfolio-updates";
import { TransactionDraftProvider } from "./transaction-draft";
import { Transactions } from "./transactions";
import { CSVImport } from "./csv-import";
import { Dividends } from "./dividends";

const tabs: { path: PortfolioTab; label: string }[] = [
  { path: "dashboard", label: "Dashboard" },
  { path: "transactions", label: "Transactions" },
  { path: "dividends", label: "Dividends" },
  { path: "import", label: "Import" },
  { path: "items", label: "Manage items" },
];

export function PortfolioPanels({ portfolio, activeTab }: { portfolio: PortfolioDetail; activeTab: PortfolioTab }) {
  const id = String(portfolio.id);
  // Keep the panels mounted and load their data once. Switching tabs only changes visibility.
  return <PortfolioUpdatesProvider activeTab={activeTab}><TransactionDraftProvider>
    <div hidden={activeTab !== "dashboard"} aria-label="Dashboard view"><Dashboard id={id} /></div>
    <div hidden={activeTab !== "transactions"} aria-label="Transactions view"><Transactions id={id} /></div>
    <div hidden={activeTab !== "dividends"} aria-label="Dividends view"><Suspense fallback={null}><Dividends id={id} /></Suspense></div>
    <div hidden={activeTab !== "import"} aria-label="Import view"><CSVImport id={id} /></div>
    <div hidden={activeTab !== "items"} aria-label="Manage items view"><PortfolioItems initial={portfolio} /></div>
  </TransactionDraftProvider></PortfolioUpdatesProvider>;
}

export function PortfolioWorkspace({ portfolio, children }: { portfolio: PortfolioDetail; children: ReactNode }) {
  const pathname = usePathname();
  const routeTab = tabs.find(tab => pathname.endsWith(`/${tab.path}`))?.path ?? "dashboard";
  const [activeTab, setActiveTab] = useState<PortfolioTab>(routeTab);
  // URL changes also cover browser back/forward and navigation after a commit.
  useEffect(() => { setActiveTab(routeTab); }, [routeTab]);
  return <>
    <nav className="portfolio-tabs" aria-label="Portfolio">{tabs.map(tab => <Link
      key={tab.path}
      href={`/portfolios/${portfolio.id}/${tab.path}`}
      prefetch={true}
      scroll={false}
      aria-current={activeTab === tab.path ? "page" : undefined}
      onNavigate={() => setActiveTab(tab.path)}
    >{tab.label}</Link>)}</nav>
    <PortfolioPanels key={portfolio.id} portfolio={portfolio} activeTab={activeTab} />
    {children}
  </>;
}
