import assert from 'node:assert/strict';
import {test,afterEach} from 'node:test';
import {JSDOM} from 'jsdom';
import React from 'react';
const dom=new JSDOM('<!doctype html><html><body></body></html>',{url:'http://localhost/portfolios/1/import'});
for(const key of ['window','document','HTMLElement','Node','MutationObserver','HTMLDialogElement','sessionStorage'])globalThis[key]=dom.window[key];
globalThis.self=dom.window;
Object.defineProperty(globalThis,'navigator',{configurable:true,value:dom.window.navigator});
HTMLDialogElement.prototype.showModal=function(){this.open=true;};HTMLDialogElement.prototype.close=function(){this.open=false;};
const {render,screen,fireEvent,waitFor,cleanup}=await import('@testing-library/react');
const {CSVImport}=await import('../components/csv-import.tsx');
const {PortfolioUpdatesProvider}=await import('../components/portfolio-updates.tsx');
const {defaultSettings,editImportFields,displayImportField,canImport,same}=await import('../lib/csv-import.ts');
afterEach(()=>{cleanup();sessionStorage.clear();});
const fields={isin:'IE00B4X9L533',wkn:'A1C9KK',ticker:'HSBC',name:'HSBC UCITS ETF',type:'ETF',quantity:'7.79277',price:'32.08',total:'249.99',date:'2024-06-01'};
const row={rowNumber:2,include:true,fields,status:'NEW_INSTRUMENT',errors:{},warnings:[],raw:['HSBC']};
const preview={headers:['ISIN','WKN','Symbol','Name','Amount','Open Price','Date','Total Invest'],rows:[row],revision:'r1',summary:{rows:1,included:1,errors:0,undecidedDuplicates:0,newInstruments:1,newItems:1,transactions:1,lines:1,itemsAfter:1,itemLimit:30}};
const json=(value,status=200)=>new Response(JSON.stringify(value),{status});
const mount=()=>render(React.createElement(PortfolioUpdatesProvider,{activeTab:'import'},React.createElement(CSVImport,{id:'1'})));
function chooseFile(){fireEvent.change(screen.getByLabelText('CSV file'),{target:{files:[new File(['sample'],'reference.csv',{type:'text/csv'})]}});}
async function ready(){await waitFor(()=>assert.equal(screen.getByRole('button',{name:'Import',exact:true}).disabled,false),{timeout:3000});}
function mockAPI(t,override=()=>undefined){const calls=[];let saved={settings:defaultSettings(),saved:false};t.mock.method(globalThis,'fetch',async(url,init)=>{calls.push({url,init});const result=override(url,init);if(result)return result;if(url.endsWith('csv-settings')){if(init.method==='PUT')saved={settings:JSON.parse(init.body).settings,saved:true,updatedAt:'2026-11-04T14:32:00Z'};return json(saved);}if(url.endsWith('/parse'))return json(preview);if(url.endsWith('/validate')){const rows=JSON.parse(init.body).rows.map(r=>({...row,...r}));return json({...preview,rows});}if(url.endsWith('/imports')){saved={settings:JSON.parse(init.body).settings,saved:true,updatedAt:'2026-11-04T14:32:00Z'};return json({linesCreated:1,transactionsCreated:1},201);}throw new Error(url);});return calls;}

test('CSV coupling uses broker units and price, half-up rounding, and German display',()=>{
 let f=editImportFields(fields,'quantity','7,79277');assert.equal(f.total,'249.99');
 f=editImportFields(f,'price','1.234,56');assert.equal(f.price,'1234.56');assert.equal(f.total,'9620.64');
 f=editImportFields(f,'total','2.469,12');assert.equal(f.quantity,'2.000000');
 f=editImportFields(f,'date','03.04.2024');assert.equal(f.date,'2024-04-03');
 assert.equal(displayImportField('total','6231.43'),'6.231,43');assert.equal(displayImportField('date',fields.date),'01.06.2024');
 assert.equal(editImportFields(fields,'quantity','bad').quantity,'bad');assert.equal(editImportFields(fields,'total','').quantity,fields.quantity);
 assert.equal(same({a:1,b:2},{b:2,a:1}),true);
});
test('import enablement blocks errors, unresolved duplicates, item limits and pending validation',()=>{
 assert.equal(canImport(preview,false),true);assert.equal(canImport(preview,true),false);
 for(const summary of [{lines:0},{undecidedDuplicates:1},{itemsAfter:31}])assert.equal(canImport({...preview,summary:{...preview.summary,...summary}},false),false);
 assert.equal(canImport({...preview,rows:[{...row,status:'ERROR'}]},false),false);
 assert.equal(canImport({...preview,rows:[{...row,status:'ERROR',include:false}]},false),true);
});
test('configuration saves without a file, resets without saving, discards, and survives remount',async t=>{
 const calls=mockAPI(t);const view=mount();await screen.findByLabelText('Delimiter');const save=screen.getByRole('button',{name:'Save configuration'});assert.equal(save.disabled,true);
 fireEvent.change(screen.getByLabelText('Delimiter'),{target:{value:'|'}});await screen.findByText('Unsaved changes');fireEvent.click(save);
 await screen.findByText(/Saved/);assert.ok(screen.queryByText('Unsaved changes')===null);assert.equal(save.disabled,true);
 fireEvent.click(screen.getByRole('button',{name:'Reset to defaults'}));assert.equal(screen.getByLabelText('Delimiter').value,';');assert.equal(calls.filter(c=>c.init.method==='PUT').length,1);
 fireEvent.click(screen.getByRole('button',{name:'Discard changes'}));assert.equal(screen.getByLabelText('Delimiter').value,'|');
 view.unmount();mount();await waitFor(()=>assert.equal(screen.getByLabelText('Delimiter').value,'|'));
});
test('file upload uses multipart defaults, editing validates and highlights, revert restores file, import confirms and clears',async t=>{
 const calls=mockAPI(t);mount();await screen.findByLabelText('CSV file');chooseFile();await ready();
 const parse=calls.find(c=>c.url.endsWith('/parse'));assert.equal(parse.init.body instanceof FormData,true);assert.equal(parse.init.headers['Content-Type'],undefined);assert.deepEqual(JSON.parse(parse.init.body.get('settings')),defaultSettings());
 fireEvent.change(screen.getByLabelText('Row 2 Amount'),{target:{value:'2,5'}});assert.equal(screen.getByLabelText('Row 2 Total €').value,'80,20');assert.equal(screen.getByRole('button',{name:'Import',exact:true}).disabled,true);assert.ok(screen.getByLabelText('Row 2 Amount').closest('td').classList.contains('csv-edited'));await ready();
 const validation=JSON.parse(calls.filter(c=>c.url.endsWith('/validate')).at(-1).init.body);assert.equal(validation.rows[0].fields.quantity,'2.5');assert.equal(validation.rows[0].fields.total,'80.20');
 fireEvent.click(screen.getByRole('button',{name:'Revert to file value'}));await ready();assert.equal(screen.getByLabelText('Row 2 Amount').value,'7,79277');
 fireEvent.click(screen.getByRole('button',{name:'Import',exact:true}));await screen.findByText('Import 1 rows into 1 transactions, create 1 instruments, add 1 portfolio items');fireEvent.click(screen.getByRole('button',{name:'Confirm import',exact:true}));
 await screen.findByText(/Imported 1 rows into 1 transactions/);assert.ok(screen.queryByLabelText('Row 2 Amount')===null);assert.equal(screen.getByRole('button',{name:'Import',exact:true}).disabled,true);
 const commit=JSON.parse(calls.find(c=>c.url.endsWith('/imports')).init.body);assert.equal(commit.revision,'r1');assert.equal(commit.rows[0].fields.quantity,fields.quantity);
});
test('reparse prompts before discarding edits and leaving blocks navigation',async t=>{
 const calls=mockAPI(t);let answer=false,questions=[];t.mock.method(window,'confirm',q=>{questions.push(q);return answer;});mount();await screen.findByLabelText('CSV file');chooseFile();await ready();
 fireEvent.change(screen.getByLabelText('Row 2 Price €'),{target:{value:'10'}});await ready();fireEvent.change(screen.getByLabelText('Delimiter'),{target:{value:'|'}});assert.equal(screen.getByLabelText('Delimiter').value,';');assert.ok(questions.includes('Re-parsing discards your edits. Continue?'));
 const anchor=document.createElement('a');anchor.href='/portfolios/1/dashboard';document.body.append(anchor);const event=new window.MouseEvent('click',{bubbles:true,cancelable:true});anchor.dispatchEvent(event);assert.equal(event.defaultPrevented,true);anchor.remove();
 answer=true;fireEvent.change(screen.getByLabelText('Delimiter'),{target:{value:'|'}});await ready();assert.equal(calls.filter(c=>c.url.endsWith('/parse')).length,2);
});
test('stale import shows fresh statuses and requires reviewing duplicates',async t=>{
 mockAPI(t,(url)=>url.endsWith('/imports')?json({error:{code:'IMPORT_STALE',message:'Review updated preview.',preview:{...preview,rows:[{...row,status:'DUPLICATE'}],summary:{...preview.summary,undecidedDuplicates:1}}}},409):undefined);
 mount();await screen.findByLabelText('CSV file');chooseFile();await ready();fireEvent.click(screen.getByRole('button',{name:'Import',exact:true}));fireEvent.click(screen.getByRole('button',{name:'Confirm import',exact:true}));await screen.findByText('Review updated preview.');await screen.findByLabelText('Row 2 duplicate decision');assert.equal(screen.getByRole('button',{name:'Import',exact:true}).disabled,true);
});

test('fixing an error includes it automatically unless the user explicitly excluded it',async t=>{
 const broken={...row,include:false,fields:{...fields,quantity:'bad'},status:'ERROR',errors:{quantity:'Use a positive decimal.'}};
 mockAPI(t,(url,init)=>{
  if(url.endsWith('/parse'))return json({...preview,rows:[broken],summary:{...preview.summary,included:0,errors:1,lines:0}});
  if(url.endsWith('/validate')){const rows=JSON.parse(init.body).rows.map(r=>({...row,...r,status:r.fields.quantity==='bad'?'ERROR':'NEW_INSTRUMENT',errors:r.fields.quantity==='bad'?broken.errors:{}}));return json({...preview,rows});}
 });mount();await screen.findByLabelText('CSV file');chooseFile();await screen.findByText('Use a positive decimal.');assert.equal(screen.getByLabelText('Include row 2').checked,false);
 fireEvent.change(screen.getByLabelText('Row 2 Amount'),{target:{value:'2'}});await waitFor(()=>assert.equal(screen.getByLabelText('Include row 2').checked,true),{timeout:3000});await ready();
 fireEvent.click(screen.getByLabelText('Include row 2'));fireEvent.change(screen.getByLabelText('Row 2 Amount'),{target:{value:'3'}});await waitFor(()=>assert.ok(screen.queryByText('Validating preview…')===null),{timeout:3000});assert.equal(screen.getByLabelText('Include row 2').checked,false);
});
test('filters, duplicate bulk decisions, pagination and deletion act on source rows',async t=>{
 const rows=Array.from({length:101},(_,i)=>({...row,rowNumber:i+2,status:i===0?'NEW_INSTRUMENT':'DUPLICATE',warnings:i===0?['A warning']:[]}));
 const summarize=rows=>({...preview,rows,summary:{...preview.summary,rows:rows.length,lines:rows.filter(r=>r.include&&r.decision!=='skip').length,included:rows.filter(r=>r.include).length,undecidedDuplicates:rows.filter(r=>r.include&&r.status==='DUPLICATE'&&!r.decision).length}});
 const calls=mockAPI(t,(url,init)=>url.endsWith('/parse')?json(summarize(rows)):url.endsWith('/validate')?json(summarize(JSON.parse(init.body).rows.map(r=>({...rows.find(v=>v.rowNumber===r.rowNumber),...r})))):undefined);
 mount();await screen.findByLabelText('CSV file');chooseFile();await screen.findByLabelText('Row 2 Amount');assert.ok(screen.queryByLabelText('Row 102 Amount')===null);fireEvent.click(screen.getByRole('button',{name:'Next',exact:true}));await screen.findByLabelText('Row 102 Amount');
 fireEvent.change(screen.getByLabelText('Filter'),{target:{value:'Warnings'}});await screen.findByLabelText('Row 2 Amount');assert.ok(screen.queryByLabelText('Row 3 Amount')===null);
 fireEvent.click(screen.getByRole('button',{name:'Skip all duplicates'}));await ready();const payload=JSON.parse(calls.filter(c=>c.url.endsWith('/validate')).at(-1).init.body);assert.equal(payload.rows.filter(r=>r.decision==='skip').length,100);
 fireEvent.change(screen.getByLabelText('Filter'),{target:{value:'Duplicates'}});fireEvent.click(screen.getByRole('button',{name:'Import all duplicates'}));await ready();assert.equal(JSON.parse(calls.filter(c=>c.url.endsWith('/validate')).at(-1).init.body).rows.filter(r=>r.decision==='import').length,100);
 fireEvent.click(screen.getByLabelText('Delete row 3'));await ready();assert.equal(JSON.parse(calls.filter(c=>c.url.endsWith('/validate')).at(-1).init.body).rows.length,100);
});

test('import page size resets pagination and column controls keep headers and edited cells aligned',async t=>{
 const rows=Array.from({length:101},(_,i)=>({...row,rowNumber:i+2}));
 mockAPI(t,url=>url.endsWith('/parse')?json({...preview,rows}):undefined);
 mount();await screen.findByLabelText('CSV file');chooseFile();await ready();
 fireEvent.click(screen.getByRole('button',{name:'Next',exact:true}));await screen.findByLabelText('Row 102 Amount');
 fireEvent.change(screen.getByLabelText('Rows per page'),{target:{value:'25'}});
 assert.ok(screen.getByText('Page 1 / 5'));assert.ok(screen.getByLabelText('Row 26 Amount'));assert.ok(screen.queryByLabelText('Row 27 Amount')===null);
 fireEvent.click(screen.getByText('Columns',{exact:true}));
 fireEvent.click(screen.getByRole('checkbox',{name:'Name',exact:true}));
 assert.ok(screen.queryByRole('columnheader',{name:'Name',exact:true})===null);assert.ok(screen.queryByLabelText('Row 2 Name')===null);
 fireEvent.click(screen.getByRole('button',{name:'Move Amount left'}));
 const headers=screen.getAllByRole('columnheader').map(h=>h.textContent);
 const quantityIndex=headers.indexOf('Amount');
 assert.equal(headers[quantityIndex+1],'Type');
 assert.equal(screen.getByLabelText('Row 2 Amount').closest('td').cellIndex,quantityIndex);
 fireEvent.click(screen.getByRole('button',{name:'Reset columns'}));
 assert.ok(screen.getByLabelText('Row 2 Name'));
 fireEvent.change(screen.getByLabelText('Rows per page'),{target:{value:'250'}});assert.ok(screen.getByLabelText('Row 102 Amount'));assert.equal(screen.getByRole('button',{name:'Next',exact:true}).disabled,true);
});
