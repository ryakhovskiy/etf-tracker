"use client";
import Link from "next/link";
import { PriceFetch } from "./price-fetch";
import { PlanTile } from "./plan-tile";
import { useRouter } from "next/navigation";
import { useEffect, useState, type SetStateAction } from "react";
import { api, APIError } from "../lib/api";
import { decimal, fetchable, hasPrice, editRow, euro, Line, PlanItem, PlanRow, planRow, rowProblem, today, total } from "../lib/transactions";
import { Modal } from "./portfolios";
import { useTransactionDraft } from "./transaction-draft";
import { usePortfolioUpdates } from "./portfolio-updates";
export function Transactions({id}:{id:string}){
 const [selected,setSelected]=useState<number[]|null>(null);const [fetching,setFetching]=useState(false);
 const router=useRouter();const {revision,changed}=usePortfolioUpdates();const {draft,setDraft}=useTransactionDraft();const {rows,date}=draft;
 const setRows=(update:SetStateAction<PlanRow[]|null>)=>setDraft(current=>({...current,rows:typeof update==="function"?update(current.rows):update}));
 const setDate=(date:string)=>setDraft(current=>({...current,date}));const [error,setError]=useState("");const [busy,setBusy]=useState(false);const [duplicate,setDuplicate]=useState<{lines:Line[];matches:Line[]}|null>(null);
 const load=(preserve=true,active=()=>true,recoverPrices=false)=>api<PlanItem[]>(`/api/portfolios/${id}/saving-plan`).then(items=>{
  if(!active())return;
  setDraft(current=>({date:preserve?current.date:today(),rows:items.map(item=>{
   const previous=preserve?current.rows?.find(row=>row.instrumentId===item.instrumentId):undefined;
   if(previous&&(recoverPrices||(previous.price.replace(",", ".")===previous.defaultPrice&&previous.quoteId!==item.quoteId))&&item.quoteId)return {...editRow({...previous,...item},"price",item.defaultPrice),quoteId:item.quoteId};
   return previous?{...planRow(item),amount:previous.amount,price:previous.price,quantity:previous.quantity,errors:previous.errors,quoteId:previous.quoteId}:planRow(item);
  })}));
 });
 useEffect(()=>{let active=true;load(true,()=>active).catch(e=>{if(active)setError(e.message);});return()=>{active=false;};},[id,revision]);
 const commit=async(lines:Line[],force=false)=>{setBusy(true);setError("");try{await api(`/api/portfolios/${id}/transactions`,{method:"POST",body:JSON.stringify({date,lines,force})});changed();setDuplicate(null);setDraft({date:today(),rows:null});await load(false);sessionStorage.setItem("transaction-committed",id);router.push(`/portfolios/${id}/dashboard`);}catch(e){const err=e as APIError;if(err.code==="DUPLICATE_TRANSACTION")setDuplicate({lines,matches:err.details?.lines??[]});else {setError(err.message);if(err.details?.fields){setRows(current=>current?.map((row,i)=>{const errors={...row.errors};for(const field of ["amount","price","quantity"] as const){const message=err.details?.fields?.[`lines.${i}.${field}`];if(message)errors[field]=message;}return {...row,errors};})??null);}}}finally{setBusy(false);}};
 const enabled=rows?.some(r=>{try{return decimal(r.amount,2,true).gt(0);}catch{return false;}})??false;
 const problems=rows?.some(r=>!!rowProblem(r));
 const dateError=date<"1990-01-01"||date>today()||!date;
  const fetchIds = selected ?? rows?.filter(fetchable).map(r => r.instrumentId) ?? [];
  const toggleFetch = (instrumentId: number, checked: boolean) => setSelected(current => {
    const ids = current ?? rows?.filter(fetchable).map(r => r.instrumentId) ?? [];
    return checked ? [...ids, instrumentId] : ids.filter(value => value !== instrumentId);
  });
  const commitPlan = () => {
    const lines = rows!.filter(r => decimal(r.amount, 2, true).gt(0)).map(r => ({
      instrumentId: r.instrumentId,
      ...(r.quoteId ? { quoteId: r.quoteId } : {}),
      amount: decimal(r.amount, 2).toFixed(2),
      price: decimal(r.price, 4).toFixed(4),
      quantity: decimal(r.quantity, 6).toFixed(6),
    }));
    void commit(lines);
  };
  const skipMatching = () => {
    if (!duplicate) return;
    const rest = duplicate.lines.filter(l => !duplicate.matches.some(m => m.instrumentId === l.instrumentId && m.quantity === l.quantity && m.price === l.price));
    if (rest.length) void commit(rest);
    else { setDuplicate(null); setError("All matching lines were skipped. No transaction was created."); }
  };
  return (
    <section className="transactions">
      <h2>Transactions</h2>
      {error && <p className="error" role="alert">{error}</p>}
      {!rows ? <p role="status">Loading transactions…</p>
        : !rows.length ? <p>No items yet. <Link href={`/portfolios/${id}/items`}>Manage items</Link></p>
        : <>
          <div className="plan-controls tile">
            <div className="date-field">
              <label htmlFor="trade-date">Transaction date</label>
              <input id="trade-date" type="date" min="1990-01-01" max={today()} value={date} onChange={e => setDate(e.target.value)} disabled={busy || fetching} />
              {dateError && <p className="error">Choose a date from 1990-01-01 through today.</p>}
            </div>
            <PriceFetch id={id} date={date} rows={rows} selected={fetchIds} setRows={setRows} reload={recover => load(true, () => true, recover)} busy={busy} onFetching={setFetching} />
          </div>
          <ul className="plan-grid" aria-label="Instruments">
            {rows.map((row, index) => (
              <PlanTile
                key={row.instrumentId}
                row={row}
                canFetch={fetchable(row)}
                fetchChecked={fetchIds.includes(row.instrumentId)}
                fetchDisabled={busy || fetching}
                inputsDisabled={busy || fetching}
                onToggleFetch={checked => toggleFetch(row.instrumentId, checked)}
                onEdit={(field, value) => setRows(current => current!.map((r, i) => i === index ? editRow(r, field, value) : r))}
                reload={() => load(true)}
              />
            ))}
          </ul>
          <div className="saving-total plan-summary tile">
            <strong>Grand total: {euro(total(rows))}</strong>
            <button className="primary" disabled={busy || fetching || !enabled || problems || dateError} onClick={commitPlan}>{busy ? "Committing…" : "Commit transaction"}</button>
          </div>
        </>}
      {duplicate && (
        <Modal title="A matching transaction exists" close={() => { if (!busy) setDuplicate(null); }}>
          <p>A matching transaction exists. Skip matching lines or commit anyway.</p>
          <ul>{duplicate.matches.map((l, i) => <li key={i}>{rows?.find(r => r.instrumentId === l.instrumentId)?.ticker}: {l.quantity} × {euro(l.price)}</li>)}</ul>
          <div className="dialog-actions">
            <button disabled={busy} onClick={() => setDuplicate(null)}>Cancel</button>
            <button disabled={busy} onClick={skipMatching}>Skip</button>
            <button className="primary" disabled={busy} onClick={() => void commit(duplicate.lines, true)}>Commit anyway</button>
          </div>
        </Modal>
      )}
    </section>
  );
}
