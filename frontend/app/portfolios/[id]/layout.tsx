"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { api, PortfolioDetail } from "../../../lib/api";
import { PortfolioWorkspace } from "../../../components/portfolio-workspace";
export default function PortfolioLayout({ children }: { children: React.ReactNode }) {
 const {id}=useParams<{id:string}>();const [portfolio,setPortfolio]=useState<PortfolioDetail|null>(null);const [error,setError]=useState("");
 useEffect(()=>{let active=true;setPortfolio(null);setError("");api<PortfolioDetail>(`/api/portfolios/${encodeURIComponent(id)}`).then(p=>{if(active)setPortfolio(p);}).catch(e=>{if(active)setError(e.message);});return()=>{active=false;};},[id]);
 return <main className="workspace"><header><Link href="/" className="brand">holdings</Link><Link href="/">← All portfolios</Link></header><div className="content">{error?<p role="alert" className="error">{error}</p>:!portfolio?<p role="status">Loading portfolio…</p>:<><p className="eyebrow">Portfolio</p><h1>{portfolio.name}</h1><PortfolioWorkspace key={id} portfolio={portfolio}>{children}</PortfolioWorkspace></>}</div></main>;
}
