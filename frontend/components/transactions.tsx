"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState, type SetStateAction } from "react";
import { api, APIError } from "../lib/api";
import { decimal, hasPrice, editRow, euro, Line, PlanItem, PlanRow, planRow, rowProblem, today, total } from "../lib/transactions";
import { Modal } from "./portfolios";
import { useTransactionDraft } from "./transaction-draft";
import { usePortfolioUpdates } from "./portfolio-updates";
export function Transactions({id}:{id:string}){
 const router=useRouter();const {revision,changed}=usePortfolioUpdates();const {draft,setDraft}=useTransactionDraft();const {rows,date}=draft;
 const setRows=(update:SetStateAction<PlanRow[]|null>)=>setDraft(current=>({...current,rows:typeof update==="function"?update(current.rows):update}));
 const setDate=(date:string)=>setDraft(current=>({...current,date}));const [error,setError]=useState("");const [busy,setBusy]=useState(false);const [duplicate,setDuplicate]=useState<{lines:Line[];matches:Line[]}|null>(null);
 const load=(preserve=true,active=()=>true)=>api<PlanItem[]>(`/api/portfolios/${id}/saving-plan`).then(items=>{
  if(!active())return;
  setDraft(current=>({date:preserve?current.date:today(),rows:items.map(item=>{
   const previous=preserve?current.rows?.find(row=>row.instrumentId===item.instrumentId):undefined;
   return previous?{...planRow(item),amount:previous.amount,price:previous.price,quantity:previous.quantity,errors:previous.errors}:planRow(item);
  })}));
 });
 useEffect(()=>{let active=true;load(true,()=>active).catch(e=>{if(active)setError(e.message);});return()=>{active=false;};},[id,revision]);
 const commit=async(lines:Line[],force=false)=>{setBusy(true);setError("");try{await api(`/api/portfolios/${id}/transactions`,{method:"POST",body:JSON.stringify({date,lines,force})});changed();setDuplicate(null);setDraft({date:today(),rows:null});await load(false);sessionStorage.setItem("transaction-committed",id);router.push(`/portfolios/${id}/dashboard`);}catch(e){const err=e as APIError;if(err.code==="DUPLICATE_TRANSACTION")setDuplicate({lines,matches:err.details?.lines??[]});else {setError(err.message);if(err.details?.fields){setRows(current=>current?.map((row,i)=>{const errors={...row.errors};for(const field of ["amount","price","quantity"] as const){const message=err.details?.fields?.[`lines.${i}.${field}`];if(message)errors[field]=message;}return {...row,errors};})??null);}}}finally{setBusy(false);}};
 const enabled=rows?.some(r=>{try{return decimal(r.amount,2,true).gt(0);}catch{return false;}})??false;
 const problems=rows?.some(r=>!!rowProblem(r));
 const dateError=date<"1990-01-01"||date>today()||!date;
 return <section><h2>Transactions</h2>{error&&<p className="error" role="alert">{error}</p>}{!rows?<p role="status">Loading transactions…</p>:!rows.length?<p>No items yet. <Link href={`/portfolios/${id}/items`}>Manage items</Link></p>:<><label htmlFor="trade-date">Transaction date</label><input className="date-field" id="trade-date" type="date" min="1990-01-01" max={today()} value={date} onChange={e=>setDate(e.target.value)} disabled={busy}/>{dateError&&<p className="error">Choose a date from 1990-01-01 through today.</p>}<div className="table-scroll"><table className="saving-table"><thead><tr>{["Ticker","Name","Price €","Amount €","Quantity","Price source"].map(c=><th key={c}>{c}</th>)}</tr></thead><tbody>{rows.map((row,index)=><tr key={row.instrumentId}><td>{row.ticker}</td><td>{row.name}</td>{(["price","amount","quantity"] as const).map(field=>{const noPrice=!hasPrice(row.price);return <td key={field}><label className="sr-only" htmlFor={`${field}-${row.instrumentId}`}>{row.ticker} {field}</label><input id={`${field}-${row.instrumentId}`} inputMode="decimal" value={field==="quantity"&&noPrice?"":row[field].replace(".",",")} placeholder={field==="quantity"&&noPrice?"–":undefined} disabled={busy||(field==="quantity"&&noPrice)} aria-invalid={!!row.errors[field]} onChange={e=>setRows(current=>current!.map((r,i)=>i===index?editRow(r,field,e.target.value):r))}/>{row.errors[field]&&<p className="error">{row.errors[field]}</p>}{field==="price"&&rowProblem(row)&&!row.errors.price&&<p className="error">{rowProblem(row)}</p>}</td>;})}<td>{row.priceSource}</td></tr>)}</tbody></table></div><div className="saving-total"><strong>Grand total: {euro(total(rows))}</strong><button className="primary" disabled={busy||!enabled||problems||dateError} onClick={()=>{const lines=rows.filter(r=>decimal(r.amount,2,true).gt(0)).map(r=>({instrumentId:r.instrumentId,amount:decimal(r.amount,2).toFixed(2),price:decimal(r.price,4).toFixed(4),quantity:decimal(r.quantity,6).toFixed(6)}));void commit(lines);}}>{busy?"Committing…":"Commit transaction"}</button></div></>}
 {duplicate&&<Modal title="A matching transaction exists" close={()=>{if(!busy)setDuplicate(null);}}><p>A matching transaction exists. Skip matching lines or commit anyway.</p><ul>{duplicate.matches.map((l,i)=><li key={i}>{rows?.find(r=>r.instrumentId===l.instrumentId)?.ticker}: {l.quantity} × {euro(l.price)}</li>)}</ul><div className="dialog-actions"><button disabled={busy} onClick={()=>setDuplicate(null)}>Cancel</button><button disabled={busy} onClick={()=>{const rest=duplicate.lines.filter(l=>!duplicate.matches.some(m=>m.instrumentId===l.instrumentId&&m.quantity===l.quantity&&m.price===l.price));if(rest.length)void commit(rest);else{setDuplicate(null);setError("All matching lines were skipped. No transaction was created.");}}}>Skip</button><button className="primary" disabled={busy} onClick={()=>void commit(duplicate.lines,true)}>Commit anyway</button></div></Modal>}
 </section>;
}
