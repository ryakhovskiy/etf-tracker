"use client";

import { createContext, useCallback, useContext, useState, type ReactNode } from "react";

export type PortfolioTab = "dashboard" | "transactions" | "items";
const PortfolioUpdatesContext = createContext({
  revision: 0,
  changed: () => {},
  activeTab: "dashboard" as PortfolioTab,
});

export function PortfolioUpdatesProvider({ activeTab, children }: { activeTab: PortfolioTab; children: ReactNode }) {
  const [revision, setRevision] = useState(0);
  const changed = useCallback(() => setRevision(value => value + 1), []);
  return <PortfolioUpdatesContext.Provider value={{ revision, changed, activeTab }}>{children}</PortfolioUpdatesContext.Provider>;
}

export const usePortfolioUpdates = () => useContext(PortfolioUpdatesContext);
