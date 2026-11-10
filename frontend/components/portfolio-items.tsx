"use client";

import { useEffect, useState } from "react";
import { api, type PortfolioDetail } from "../lib/api";
import { InstrumentPicker } from "./instrument-picker";
import { PortfolioHoldings } from "./portfolio-holdings";
import { usePortfolioUpdates } from "./portfolio-updates";

export function PortfolioItems({ initial }: { initial: PortfolioDetail }) {
  const [data, setData] = useState(initial);
  const [error, setError] = useState("");
  const { revision } = usePortfolioUpdates();
  useEffect(() => {
    if (revision === 0) return;
    let active = true;
    api<PortfolioDetail>(`/api/portfolios/${initial.id}`)
      .then(portfolio => { if (active) { setData(portfolio); setError(""); } })
      .catch(e => { if (active) setError(e.message); });
    return () => { active = false; };
  }, [initial.id, revision]);
  return <>{error && <p role="alert" className="error">{error}</p>}<InstrumentPicker initial={data} /><PortfolioHoldings id={initial.id} /></>;
}
