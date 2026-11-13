"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { api } from "../lib/api";
import { Performance, TransactionPage, TransactionRow, units, euro } from "../lib/transactions";
import { Modal } from "./portfolios";
import { usePortfolioUpdates } from "./portfolio-updates";
import {PageSize,useTableColumns} from "./table-controls";
import { TransactionGroups, groupLines } from "./transaction-groups";
import { PerformanceOverview } from "./performance-overview";
import { dateLabel } from "../lib/performance";
export { HoldingsChart, instrumentColor } from "./holdings-chart";
export function Dashboard({id}:{id:string}){
 const [pageSize,setPageSize]=useState(50);
 const fieldLabels=["Date","Ticker","Name","Quantity","Price €","Amount €","Performance","Source"];
 const columns=useTableColumns(fieldLabels,{summary:"Fields shown",reset:"Reset fields"});
 const {revision,changed,activeTab}=usePortfolioUpdates();
 const requestVersion=useRef(0);
 const [data,setData]=useState<{performance:Performance;transactions:TransactionPage;items:number}|null>(null);const [page,setPage]=useState(1);const [error,setError]=useState("");const [toast,setToast]=useState("");const [deleting,setDeleting]=useState<TransactionRow|null>(null);const [busy,setBusy]=useState(false);const [loading,setLoading]=useState(false);
 const load=async()=>{const version=++requestVersion.current;setLoading(true);try{const [performance,transactions,portfolio]=await Promise.all([api<Performance>(`/api/portfolios/${id}/performance`),api<TransactionPage>(`/api/portfolios/${id}/transactions?pageSize=${pageSize}&page=${page}`),api<{itemCount:number}>(`/api/portfolios/${id}`)]);if(version!==requestVersion.current)return;setData({performance,transactions,items:portfolio.itemCount});setError("");}catch(e){if(version===requestVersion.current)setError((e as Error).message);}finally{if(version===requestVersion.current)setLoading(false);}};
 useEffect(()=>{void load();return()=>{requestVersion.current++;};},[id,page,pageSize,revision]);useEffect(()=>{if(activeTab==="dashboard"&&sessionStorage.getItem("transaction-committed")===id){setToast("Transaction committed successfully.");sessionStorage.removeItem("transaction-committed");}},[id,activeTab,revision]);
 const remove=async()=>{if(!deleting)return;setBusy(true);try{await api(`/api/portfolios/${id}/transactions/${deleting.transactionId}`,{method:"DELETE"});changed();setDeleting(null);if(page>1&&data&&data.transactions.lines.every(l=>l.transactionId===deleting.transactionId))setPage(page-1);else await load();}catch(e){setError((e as Error).message);}finally{setBusy(false);}};
  return (
    <section className="dashboard" aria-busy={loading}>
      {toast && <p className="toast" role="status">{toast}</p>}
      {error && <p className="error" role="alert">{error}</p>}
      {!data ? <p>Loading dashboard…</p>
        : !data.items ? <p>No items yet. <Link href={`/portfolios/${id}/items`}>Manage items</Link></p>
        : !data.performance.dates.length ? <p>Commit a transaction to get started. <Link href={`/portfolios/${id}/transactions`}>Go to transactions</Link></p>
        : <>
          <PerformanceOverview id={id} performance={data.performance} reload={async () => { changed(); await load(); }} />
          <h2>Transactions</h2>
          <div className="table-toolbar">
            <PageSize value={pageSize} disabled={loading || busy} onChange={value => { setPageSize(value); setPage(1); }} />
            {columns.controls}
          </div>
          <TransactionGroups
            groups={groupLines(data.transactions.lines)}
            summaries={data.transactions.groups}
            labels={fieldLabels}
            visible={columns.visible}
            disabled={busy || loading}
            onDelete={setDeleting}
          />
          <div className="pagination">
            <button disabled={page === 1 || loading} onClick={() => setPage(p => p - 1)}>Previous</button>
            <span>Page {page} of {Math.max(1, Math.ceil(data.transactions.total / pageSize))}</span>
            <button disabled={page * pageSize >= data.transactions.total || loading} onClick={() => setPage(p => p + 1)}>Next</button>
          </div>
        </>}
      {deleting && (
        <Modal title="Delete transaction" close={() => { if (!busy) setDeleting(null); }}>
          <p>Delete the entire transaction from {dateLabel(deleting.date)}? All its lines will be removed.</p>
          <div className="dialog-actions">
            <button disabled={busy} onClick={() => setDeleting(null)}>Cancel</button>
            <button className="danger" disabled={busy} onClick={() => void remove()}>{busy ? "Deleting…" : "Delete transaction"}</button>
          </div>
        </Modal>
      )}
    </section>
  );
}
