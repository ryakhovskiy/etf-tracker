import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
const dom=new JSDOM('<!doctype html><html><body></body></html>',{url:'http://localhost/'});
for(const key of ['window','document','HTMLElement','Node','MutationObserver','HTMLDialogElement','sessionStorage'])globalThis[key]=dom.window[key];
globalThis.self=dom.window;Object.defineProperty(globalThis,'navigator',{configurable:true,value:dom.window.navigator});
HTMLDialogElement.prototype.showModal=function(){this.open=true;};HTMLDialogElement.prototype.close=function(){this.open=false;};
const {render,screen,fireEvent,waitFor,cleanup}=await import('@testing-library/react');
const {Transactions}=await import('../components/transactions.tsx');
const {TransactionDraftProvider}=await import('../components/transaction-draft.tsx');
const {InstrumentPicker}=await import('../components/instrument-picker.tsx');
const {AppRouterContext}=await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const {today,fetchDateAllowed,fetchDateInRange}=await import('../lib/transactions.ts');
afterEach(()=>{cleanup();sessionStorage.clear();});
const item={instrumentId:1,ticker:'SAP',name:'SAP SE',defaultPrice:'10.0000',defaultAmount:'20.00',priceSource:'Last purchase 01.01.2025',avSymbol:'SAP.DEX',avCurrency:'EUR',avLinkedByYou:true};
const json=(body,status=200)=>new Response(JSON.stringify(body),{status});
const budget=(user=10)=>({globalRemaining:25,userRemaining:user,userLimit:10,nextSlotAt:new Date().toISOString()});
const mount=()=>render(React.createElement(AppRouterContext.Provider,{value:{push(){}}},React.createElement(TransactionDraftProvider,null,React.createElement(Transactions,{id:'1'}))));
function responder(extra,items=[item]){return async(url,init)=>{
 const result=await extra?.(url,init);if(result)return result;
 if(url.endsWith('/saving-plan'))return json(items);
 if(url.includes('/market-data/budget'))return json(budget());
 if(url.includes('/price-fetch/estimate')){const ids=new URL(url,'http://localhost').searchParams.get('instrumentIds').split(',').filter(Boolean);return json({requestsNeeded:ids.length,fresh:[],noSymbol:[],budget:budget()});}
 throw new Error(`Unexpected outbound call ${url}`);
};}
test('estimate controls request cost, selection and quota availability',async t=>{
 const rows=[item,{...item,instrumentId:2,ticker:'MSFT',avSymbol:'MSFT'},{...item,instrumentId:3,ticker:'NONE',avSymbol:undefined}];
 t.mock.method(globalThis,'fetch',responder((url)=>{if(url.includes('/estimate')){const n=new URL(url,'http://localhost').searchParams.get('instrumentIds').split(',').length;return json({requestsNeeded:n,fresh:[],noSymbol:[],budget:budget(1)});}},rows));
 mount();const button=await screen.findByRole('button',{name:'Fetch prices (uses 2 requests)'});assert.equal(button.disabled,true);assert.equal(screen.getByLabelText('Fetch SAP').checked,true);assert.equal(screen.getByLabelText('Fetch NONE').disabled,true);assert.ok(screen.getByText(/Deselect rows/));
 fireEvent.click(screen.getByLabelText('Fetch MSFT'));await screen.findByRole('button',{name:'Fetch prices (uses 1 requests)'});await waitFor(()=>assert.equal(button.disabled,false));
});
test('job progress, applied price coupling, quote commit and budget refresh',async t=>{
 let polls=0,finished=false,committed;
 t.mock.method(globalThis,'fetch',responder((url,init)=>{
  if(url.endsWith('/price-fetch-jobs'))return json({jobId:'job'},202);
  if(url.includes('/price-fetch-jobs/job')){polls++;if(polls===1)return json({status:'RUNNING',nextRequestAt:new Date(Date.now()+9000).toISOString(),items:[{instrumentId:1,status:'PENDING'}]});finished=true;return json({status:'DONE',nextRequestAt:new Date().toISOString(),items:[{instrumentId:1,status:'OK',priceEur:'25.0000',currency:'USD',fxRate:'0.92310000',fxRateDate:'2026-10-30',quoteDate:'2026-11-04',quoteId:7}]});}
  if(url.includes('/market-data/budget'))return json(budget(finished?9:10));
  if(init.method==='POST'&&url.endsWith('/transactions')){committed=JSON.parse(init.body);return json({id:1},201);}
 }));
 mount();fireEvent.click(await screen.findByRole('button',{name:'Fetch prices (uses 1 requests)'}));await screen.findByText(/0\/1 · next request in/);assert.equal(screen.getByRole('button',{name:'Commit transaction'}).disabled,true);
 await screen.findByText('1/1 · Done',{}, {timeout:4000});assert.equal(screen.getByLabelText('SAP price').value,'25,0000');assert.equal(screen.getByLabelText('SAP amount').value,'20,00');assert.equal(screen.getByLabelText('SAP quantity').value,'0,800000');assert.ok(screen.getByText('AV: 9 of 10 left today'));assert.ok(screen.getByText(/ECB 30.10.2026/));
 await waitFor(()=>assert.equal(screen.getByRole('button',{name:'Commit transaction'}).disabled,false));fireEvent.click(screen.getByRole('button',{name:'Commit transaction'}));await waitFor(()=>assert.equal(committed.lines[0].quoteId,7));assert.equal(committed.lines[0].amount,'20.00');
});
test('an interrupted job reloads saved quote defaults and preserves invested total',async t=>{
 sessionStorage.setItem('price-fetch:1','lost');let loads=0;
 t.mock.method(globalThis,'fetch',responder(url=>{
  if(url.includes('/price-fetch-jobs/lost'))return json({error:{code:'NOT_FOUND',message:'Missing job'}},404);
  if(url.endsWith('/saving-plan'))return json([{...item,...(++loads>1?{defaultPrice:'25.0000',quoteId:8,priceSource:'Fetched 04.11.2026'}:{})}]);
 }));mount();await screen.findByText('Fetch interrupted. Prices fetched so far were saved.');await waitFor(()=>assert.equal(screen.getByLabelText('SAP price').value,'25,0000'));assert.equal(screen.getByLabelText('SAP amount').value,'20,00');assert.equal(screen.getByLabelText('SAP quantity').value,'0,800000');assert.equal(sessionStorage.getItem('price-fetch:1'),null);
});
test('dates outside 1990..today disable fetching; past dates in range are fetchable by every provider',async t=>{
 const urls=[];t.mock.method(globalThis,'fetch',responder((url)=>{urls.push(url);}));mount();await screen.findByLabelText('SAP amount');
 fireEvent.change(screen.getByLabelText('Transaction date'),{target:{value:'1989-12-31'}});const button=screen.getByRole('button',{name:/Fetch prices/});assert.equal(button.disabled,true);assert.equal(button.title,'Choose a date from 1990-01-01 through today.');
 fireEvent.change(screen.getByLabelText('Transaction date'),{target:{value:'2000-01-03'}});await waitFor(()=>assert.equal(screen.getByRole('button',{name:/Fetch prices/}).disabled,false));assert.ok(urls.some(u=>u.includes('/estimate?date=2000-01-03')));
 assert.equal(fetchDateInRange('1990-01-01',new Date('2026-11-09T10:00:00Z')),true);assert.equal(fetchDateInRange('1989-12-31',new Date('2026-11-09T10:00:00Z')),false);assert.equal(fetchDateInRange('2026-11-10',new Date('2026-11-09T10:00:00Z')),false);
 assert.equal(fetchDateAllowed('2026-11-06',new Date('2026-11-09T10:00:00Z')),true);assert.equal(fetchDateAllowed('2026-11-08',new Date('2026-11-09T10:00:00Z')),false);
});
test('linking a Yahoo symbol searches Yahoo only on request and blocks symbols already in use',async t=>{
 let linked=false,searches=0;const queries=[],puts=[];
 t.mock.method(globalThis,'fetch',responder((url,init)=>{
  if(url.endsWith('/saving-plan'))return json([{...item,ticker:'SAP',avSymbol:undefined,yahooSymbol:linked?'SAP.DE':undefined,avSearchTerm:linked?undefined:'SAP SE'}]);
  if(url.includes('/yahoo-symbol/search')){searches++;queries.push(new URL(url,'http://localhost').searchParams.get('q'));return json({provider:'YAHOO',results:[{yahooSymbol:'SAP.DE',ticker:'SAP.DE',name:'SAP SE',type:'STOCK',exchangeName:'XETRA',sector:'Technology'},{yahooSymbol:'SAP',ticker:'SAP',name:'SAP SE ADR',type:'STOCK',exchangeName:'NYSE',existingInstrumentId:9}]});}
  if(init.method==='PUT'&&url.endsWith('/yahoo-symbol')){linked=true;puts.push(JSON.parse(init.body));return json({symbol:'SAP.DE',isin:''});}
 }));mount();
 fireEvent.click(await screen.findByRole('button',{name:'Link symbol'}));assert.equal(searches,0);
 const term=screen.getByLabelText('Search term');assert.equal(term.value,'SAP SE');assert.ok(screen.getByText(/uses 1 Yahoo request/)!==null);
 fireEvent.change(term,{target:{value:' '}});assert.equal(screen.getByRole('button',{name:'Search'}).disabled,true);
 fireEvent.change(term,{target:{value:'SAP'}});fireEvent.click(screen.getByRole('button',{name:'Search'}));
 const used=await screen.findByRole('button',{name:/SAP SE ADR/});assert.equal(used.disabled,true);assert.equal(searches,1);assert.deepEqual(queries,['SAP']);
 assert.ok(screen.getByText('SAP.DE · XETRA · Technology')!==null);assert.ok(screen.getByText('Data by Yahoo Finance')!==null);
 fireEvent.click(screen.getByRole('button',{name:/^Stock\s*SAP SE\s*SAP\.DE/}));
 await waitFor(()=>assert.ok(screen.queryByText('SAP.DE')!==null));
 assert.deepEqual(puts,[{symbol:'SAP.DE'}]);assert.ok(screen.queryByRole('button',{name:'Link symbol'})===null);assert.ok(screen.getByRole('button',{name:'Change'})!==null);
});
test('transaction fields expose their labels, price provenance and symbol action',async t=>{
 t.mock.method(globalThis,'fetch',responder());
 mount();
 assert.equal((await screen.findByRole('checkbox',{name:'Fetch SAP'})).checked,true);
 for(const field of ['price','amount','quantity']) assert.ok(screen.getByLabelText(`SAP ${field}`));
 assert.ok(screen.getByText(item.priceSource));
 assert.ok(screen.getByRole('button',{name:'Link symbol'}));
});
test('keyword fallback is explicit and follows an unsuccessful OpenFIGI search',async t=>{
 const calls=[];t.mock.method(globalThis,'fetch',async(url)=>{calls.push(url);if(url.includes('/online'))return json({results:[]});if(url.includes('/search'))return json([]);throw new Error(`Unexpected ${url}`);});
 render(React.createElement(InstrumentPicker,{initial:{id:1,name:'Test',itemCount:0,instrumentIds:[],itemLimit:30,createdAt:''}}));fireEvent.change(screen.getByRole('combobox'),{target:{value:'apple'}});await screen.findByText(/No results for/);assert.ok(screen.queryByRole('button',{name:'Try Alpha Vantage (1 request)'})===null);fireEvent.click(screen.getByRole('button',{name:'Search'}));fireEvent.click(await screen.findByRole('button',{name:'Try Alpha Vantage (1 request)'}));await waitFor(()=>assert.ok(calls.some(u=>u.includes('provider=av'))));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'US0378331005'}});await screen.findByText(/No results for/);fireEvent.click(screen.getByRole('button',{name:'Search'}));await waitFor(()=>assert.ok(calls.some(u=>u.includes('/online?q=US0378331005'))));assert.ok(screen.queryByRole('button',{name:'Try Alpha Vantage (1 request)'})===null);
});
