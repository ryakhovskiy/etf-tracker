import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
const dom=new JSDOM('<!doctype html><html><body></body></html>',{url:'http://localhost/'});
for(const key of ['window','document','HTMLElement','Node','MutationObserver','HTMLDialogElement','sessionStorage'])globalThis[key]=dom.window[key];
globalThis.self=dom.window;Object.defineProperty(globalThis,'navigator',{configurable:true,value:dom.window.navigator});
HTMLDialogElement.prototype.showModal=function(){this.open=true;};HTMLDialogElement.prototype.close=function(){this.open=false;};
globalThis.ResizeObserver=class{observe(){} unobserve(){} disconnect(){}};
const {render,screen,fireEvent,waitFor,cleanup}=await import('@testing-library/react');
const {Transactions}=await import('../components/transactions.tsx');
const {TransactionDraftProvider}=await import('../components/transaction-draft.tsx');
const {Dashboard,HoldingsChart}=await import('../components/dashboard.tsx');
const {AppRouterContext}=await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const {editRow,planRow,total,euro,chartData}=await import('../lib/transactions.ts');
afterEach(()=>{cleanup();sessionStorage.clear();});
const item={instrumentId:1,ticker:'AAPL',name:'Apple',defaultPrice:'10.0000',defaultAmount:'20.00',priceSource:'Last purchase 01.01.2025'};
const other={...item,instrumentId:2,ticker:'MSFT',name:'Microsoft'};
const json=(body,status=200)=>new Response(JSON.stringify(body),{status});
const mount=()=>render(React.createElement(AppRouterContext.Provider,{value:{push(){}}},React.createElement(TransactionDraftProvider,null,React.createElement(Transactions,{id:'1'}))));
test('field coupling is exact, live, and preserves independent values on invalid or cleared input',()=>{
 let row=planRow(item);row=editRow(row,'amount','25,50');assert.equal(row.price,'10.0000');assert.equal(row.quantity,'2.550000');
 row=editRow(row,'price','20');assert.equal(row.amount,'25,50');assert.equal(row.quantity,'1.275000');
 row=editRow(row,'quantity','2,5');assert.equal(row.amount,'50.00');assert.equal(row.price,'20');
 const cleared=editRow(row,'amount','');assert.equal(cleared.quantity,row.quantity);
 const invalid=editRow(row,'price','abc');assert.equal(invalid.amount,row.amount);assert.equal(invalid.quantity,row.quantity);assert.ok(invalid.errors.price);
 assert.equal(editRow(row,'amount','12.34').quantity,editRow(row,'amount','12,34').quantity);
 assert.equal(total([planRow(item),planRow(other)]),'40.00');assert.equal(euro('1234.56'),'1.234,56 €');
 const missing=editRow(planRow({...item,defaultPrice:''}),'amount','invalid');assert.ok(missing.errors.amount);
});
test('transactions disables quantity without a price, blocks commit, and updates the total on input',async t=>{
 t.mock.method(globalThis,'fetch',async()=>json([{...item,defaultPrice:''}]));mount();
 const q=await screen.findByLabelText('AAPL quantity');assert.equal(q.disabled,true);assert.equal(q.placeholder,'–');assert.equal(screen.getByRole('button',{name:'Commit transaction'}).disabled,true);
 fireEvent.change(screen.getByLabelText('AAPL amount'),{target:{value:'30,50'}});assert.match(screen.getByText(/Grand total/).textContent,/30,50/);
 fireEvent.change(screen.getByLabelText('AAPL price'),{target:{value:'10'}});assert.equal(q.value,'3,050000');assert.equal(screen.getByRole('button',{name:'Commit transaction'}).disabled,false);
 fireEvent.change(screen.getByLabelText('AAPL price'),{target:{value:'20'}});assert.equal(screen.getByLabelText('AAPL amount').value,'30,50');assert.equal(q.value,'1,525000');
 fireEvent.change(q,{target:{value:'2'}});assert.equal(screen.getByLabelText('AAPL amount').value,'40,00');
});
for(const action of ['Skip','Commit anyway'])test(`duplicate dialog ${action} resends the requested lines`,async t=>{
 const posts=[];t.mock.method(globalThis,'fetch',async(url,init)=>{if(init.method==='POST'){const body=JSON.parse(init.body);posts.push(body);return posts.length===1?json({error:{code:'DUPLICATE_TRANSACTION',message:'A matching transaction exists.',lines:[body.lines[0]]}},409):json({id:1},201);}return json([item,other]);});
 mount();fireEvent.click(await screen.findByRole('button',{name:'Commit transaction'}));fireEvent.click(await screen.findByRole('button',{name:action}));await waitFor(()=>assert.equal(posts.length,2));
 if(action==='Skip')assert.deepEqual(posts[1].lines.map(l=>l.instrumentId),[2]);else{assert.equal(posts[1].force,true);assert.equal(posts[1].lines.length,2);}
});
const performance={dates:['2025-01-01','2025-01-02','2025-03-01'],series:[{instrumentId:1,name:'Apple',values:['10.00','20.00','30.00']}],totals:['10.00','20.00','30.00'],invested:'20.00'};
test('chart uses timestamps with unequal gaps and declares stacked time axes',()=>{
 const points=chartData(performance);assert.equal(points[1].date-points[0].date,86400000);assert.equal(points[2].date-points[1].date,58*86400000);
 // Inspect Recharts component props without depending on browser layout internals.
 const tree=HoldingsChart({performance});const container=tree.props.children[1];const chart=container.props.children;const children=chart.props.children;
 const axis=children.find(c=>c?.props?.dataKey==='date');assert.equal(axis.props.type,'number');assert.equal(axis.props.scale,'time');
 const areas=children.at(-1);assert.equal(areas[0].props.stackId,'holdings');
});
test('dashboard paginates 50 lines and deletion reloads totals and chart',async t=>{
 let deleted=false;let performanceCalls=0;const requested=[];
 const line={id:1,transactionId:1,instrumentId:1,date:'2025-01-01',ticker:'AAPL',name:'Apple',quantity:'2.000000',price:'10.0000',amount:'20.00',source:'MANUAL'};
 t.mock.method(globalThis,'fetch',async(url,init)=>{requested.push(url);if(init.method==='DELETE'){deleted=true;return new Response(null,{status:204});}if(url.includes('performance')){performanceCalls++;return json({...performance,invested:deleted?'10.00':'20.00',totals:deleted?['10.00']:performance.totals});}if(url.includes('transactions?'))return json({lines:[{...line,transactionId:url.endsWith('2')?2:1}],total:51,page:url.endsWith('2')?2:1});return json({itemCount:1});});
 render(React.createElement(Dashboard,{id:'1'}));await screen.findAllByText(/20,00\s€/);fireEvent.click(screen.getByRole('button',{name:'Next'}));await screen.findByRole('button',{name:'Delete transaction 2'});assert.ok(requested.some(url=>url.endsWith('page=2')));
 fireEvent.click(screen.getByRole('button',{name:'Delete transaction 2'}));fireEvent.click(screen.getByRole('button',{name:'Delete transaction',exact:true}));await waitFor(()=>assert.ok(deleted));await waitFor(()=>assert.ok(performanceCalls>=3));await screen.findAllByText(/10,00\s€/);
});

test('rendered chart spaces uneven purchase dates by elapsed time',async()=>{
 const tree=HoldingsChart({performance});const chart=tree.props.children[1].props.children;
 const children=chart.props.children.map(child=>Array.isArray(child)?child.map(area=>React.cloneElement(area,{dot:true})):child);
 const {container}=render(React.cloneElement(chart,{width:900,height:340},children));
 await waitFor(()=>assert.equal(container.querySelectorAll('.recharts-area-dot').length,3));
 const xs=[...container.querySelectorAll('.recharts-area-dot')].map(dot=>Number(dot.getAttribute('cx')));
 assert.ok(Math.abs((xs[2]-xs[1])/(xs[1]-xs[0])-58)<0.001);
 assert.equal(container.querySelectorAll('.recharts-area-area').length,1);
});

// Match the portfolio layout lifecycle: only the tab content unmounts.
const tabView=(tab='transactions',id='1',push=()=>{})=>React.createElement(
 AppRouterContext.Provider,{value:{push}},
 React.createElement(TransactionDraftProvider,{key:id},
  tab==='transactions'?React.createElement(Transactions,{id}):React.createElement('p',null,tab)));

test('tab navigation preserves edited fields, cleared inputs, validation and date while reconciling items',async t=>{
 let items=[item,other,{...item,instrumentId:3,ticker:'THIRD'}];let loads=0;
 t.mock.method(globalThis,'fetch',async(url)=>{if(url.includes('/market-data/budget'))return json({globalRemaining:25,userRemaining:10,nextSlotAt:new Date().toISOString()});loads++;return json(items);});
 const view=render(tabView());await screen.findByLabelText('AAPL amount');
 fireEvent.change(screen.getByLabelText('Transaction date'),{target:{value:'2025-04-03'}});
 fireEvent.change(screen.getByLabelText('AAPL price'),{target:{value:'20'}});
 fireEvent.change(screen.getByLabelText('AAPL quantity'),{target:{value:'3,5'}});
 fireEvent.change(screen.getByLabelText('MSFT price'),{target:{value:'invalid'}});
 fireEvent.change(screen.getByLabelText('THIRD amount'),{target:{value:''}});
 view.rerender(tabView('dashboard'));assert.ok(screen.queryByLabelText('AAPL amount')===null);
 view.rerender(tabView());await waitFor(()=>assert.equal(loads,2));
 assert.equal(screen.getByLabelText('Transaction date').value,'2025-04-03');
 assert.equal(screen.getByLabelText('AAPL amount').value,'70,00');
 assert.equal(screen.getByLabelText('AAPL price').value,'20');
 assert.equal(screen.getByLabelText('AAPL quantity').value,'3,5');
 assert.equal(screen.getByLabelText('MSFT price').value,'invalid');
 assert.equal(screen.getByLabelText('MSFT price').getAttribute('aria-invalid'),'true');
 assert.equal(screen.getByLabelText('THIRD amount').value,'');
 assert.equal(screen.getByRole('button',{name:'Commit transaction'}).disabled,true);
 view.rerender(tabView('items'));
 items=[{...item,name:'Apple updated'},other,{...item,instrumentId:4,ticker:'NEW'}];
 view.rerender(tabView());await screen.findByText('Apple updated');
 assert.equal(screen.getByLabelText('AAPL amount').value,'70,00');
 assert.ok(screen.queryByLabelText('THIRD amount')===null);
 assert.equal(screen.getByLabelText('NEW amount').value,'20,00');
});

test('drafts are scoped to the current portfolio',async t=>{
 t.mock.method(globalThis,'fetch',async()=>json([item]));
 const view=render(tabView());await screen.findByLabelText('AAPL amount');
 fireEvent.change(screen.getByLabelText('AAPL amount'),{target:{value:'99'}});
 view.rerender(tabView('transactions','2'));
 assert.equal((await screen.findByLabelText('AAPL amount')).value,'20,00');
});

test('successful commit resets the draft and date before returning to transactions',async t=>{
 let committed=false;const pushes=[];
 t.mock.method(globalThis,'fetch',async(url,init)=>{
  if(init.method==='POST'){committed=true;assert.equal(JSON.parse(init.body).lines[0].amount,'60.00');return json({id:1},201);}
  return json([{...item,defaultAmount:committed?'60.00':'20.00'}]);
 });
 const push=path=>pushes.push(path);const view=render(tabView('transactions','1',push));
 await screen.findByLabelText('AAPL amount');const originalDate=screen.getByLabelText('Transaction date').value;
 fireEvent.change(screen.getByLabelText('Transaction date'),{target:{value:'2025-04-03'}});
 fireEvent.change(screen.getByLabelText('AAPL amount'),{target:{value:'60'}});
 fireEvent.click(screen.getByRole('button',{name:'Commit transaction'}));
 await waitFor(()=>assert.deepEqual(pushes,['/portfolios/1/dashboard']));
 view.rerender(tabView('dashboard','1',push));view.rerender(tabView('transactions','1',push));
 assert.equal((await screen.findByLabelText('AAPL amount')).value,'60,00');
 assert.equal(screen.getByLabelText('Transaction date').value,originalDate);
});

test('failed commit keeps the draft across tab navigation',async t=>{
 t.mock.method(globalThis,'fetch',async(url,init)=>init.method==='POST'?json({error:{code:'INTERNAL_ERROR',message:'Try again.'}},500):json([item]));
 const view=render(tabView());await screen.findByLabelText('AAPL amount');
 fireEvent.change(screen.getByLabelText('AAPL amount'),{target:{value:'60'}});
 fireEvent.click(screen.getByRole('button',{name:'Commit transaction'}));await screen.findByRole('alert');
 view.rerender(tabView('dashboard'));view.rerender(tabView());
 assert.equal((await screen.findByLabelText('AAPL amount')).value,'60');
});

const { within } = await import('@testing-library/react');
const { PortfolioPanels, PortfolioWorkspace } = await import('../components/portfolio-workspace.tsx');
const { PathnameContext } = await import('next/dist/shared/lib/hooks-client-context.shared-runtime.js');
const { RouterContext } = await import('next/dist/shared/lib/router-context.shared-runtime.js');
const portfolio = { id: 1, name: 'Growth', itemCount: 1, instrumentIds: [1], itemLimit: 30, createdAt: '2025-01-01' };
const txLine = { id: 1, transactionId: 1, instrumentId: 1, date: '2025-01-01', ticker: 'AAPL', name: 'Apple', quantity: '2.000000', price: '10.0000', amount: '20.00', source: 'MANUAL' };
function workspaceFetch(url) {
 if (url.includes('/saving-plan')) return json([item]);
 if (url.includes('/performance')) return json(performance);
 if (url.includes('/transactions?')) return json({ lines: [txLine], total: 51, page: url.endsWith('2') ? 2 : 1 });
 if (url.includes('/search')) return json([{ id: 2, ticker: 'MSFT', name: 'Microsoft', type: 'STOCK', isin: 'TEST', wkn: null, inPortfolio: false }]);
 return json(portfolio);
}
const panelView = (activeTab, router = { push() {} }) => React.createElement(AppRouterContext.Provider, { value: router }, React.createElement(PortfolioPanels, { portfolio, activeTab }));

test('switching panels preserves DOM, pagination, form edits, item selection and makes no new data requests', async t => {
 const requests = [];
 t.mock.method(globalThis, 'fetch', async url => { requests.push(url); return workspaceFetch(url); });
 const view = render(panelView('dashboard'));
 const dashboard = view.container.querySelector('[aria-label="Dashboard view"]');
 const transactions = view.container.querySelector('[aria-label="Transactions view"]');
 const items = view.container.querySelector('[aria-label="Manage items view"]');
 await within(dashboard).findAllByText(/20,00\s€/);
 await within(transactions).findByLabelText('AAPL amount');
 fireEvent.click(within(dashboard).getByRole('button', { name: 'Next' }));
 await within(dashboard).findByText('Page 2 of 2');
 await waitFor(() => assert.equal(dashboard.getAttribute('aria-busy') ?? dashboard.firstChild.getAttribute('aria-busy'), 'false'));
 const chart = dashboard.querySelector('.chart-card');
 view.rerender(panelView('items'));
 fireEvent.change(within(items).getByRole('combobox'), { target: { value: 'microsoft' } });
 fireEvent.click(await within(items).findByRole('option', { name: /MSFT/ }));
 view.rerender(panelView('transactions'));
 fireEvent.change(within(transactions).getByLabelText('AAPL amount'), { target: { value: '75' } });
 const requestCount = requests.length;
 view.rerender(panelView('dashboard'));
 assert.equal(dashboard.querySelector('.chart-card'), chart);
 assert.ok(within(dashboard).getByText('Page 2 of 2'));
 assert.equal(dashboard.hidden, false); assert.equal(transactions.hidden, true);
 view.rerender(panelView('transactions'));
 assert.equal(within(transactions).getByLabelText('AAPL amount').value, '75');
 view.rerender(panelView('items'));
 assert.equal(within(items).getByRole('combobox').value, 'microsoft');
 assert.ok(within(items).getByRole('button', { name: 'Remove MSFT from selection' }));
 assert.equal(requests.length, requestCount);
});

test('committing refreshes the mounted dashboard and displays its success message on return', async t => {
 let committed = false; const pushes = [];
 t.mock.method(globalThis, 'fetch', async (url, init) => {
  if (init.method === 'POST') { committed = true; return json({ id: 2 }, 201); }
  if (url.includes('/performance')) return json({ ...performance, invested: committed ? '40.00' : '20.00' });
  return workspaceFetch(url);
 });
 const router = { push: path => pushes.push(path) };
 const view = render(panelView('transactions', router));
 fireEvent.click(await screen.findByRole('button', { name: 'Commit transaction' }));
 await waitFor(() => assert.deepEqual(pushes, ['/portfolios/1/dashboard']));
 const dashboard = view.container.querySelector('[aria-label="Dashboard view"]');
 await within(dashboard).findByText(/40,00\s€/);
 view.rerender(panelView('dashboard', router));
 assert.ok(await screen.findByText('Transaction committed successfully.'));
 assert.ok(screen.queryByText('Loading dashboard…') === null);
});

test('adding items refreshes the mounted transaction form without losing its edits', async t => {
 let added = false;
 t.mock.method(globalThis, 'fetch', async (url, init) => {
  if (init.method === 'POST') { added = true; return json({ added: [2], skipped: [] }); }
  if (url.includes('/saving-plan')) return json(added ? [item, other] : [item]);
  if (url.endsWith('/portfolios/1')) return json(added ? { ...portfolio, itemCount: 2, instrumentIds: [1, 2] } : portfolio);
  return workspaceFetch(url);
 });
 const view = render(panelView('transactions'));
 fireEvent.change(await screen.findByLabelText('AAPL amount'), { target: { value: '75' } });
 view.rerender(panelView('items'));
 fireEvent.change(screen.getByRole('combobox'), { target: { value: 'microsoft' } });
 fireEvent.click(await screen.findByRole('option', { name: /MSFT/ }));
 fireEvent.click(screen.getByRole('button', { name: 'Add to portfolio' }));
 await screen.findByText('1 added');
 view.rerender(panelView('transactions'));
 assert.ok(await screen.findByLabelText('MSFT amount'));
 assert.equal(screen.getByLabelText('AAPL amount').value, '75');
});

test('tab links switch immediately before the route resolves and follow browser history changes', async t => {
 t.mock.method(globalThis, 'fetch', async url => workspaceFetch(url));
 const router = { push() {}, prefetch() {}, pathname: '/portfolios/1/dashboard', asPath: '/portfolios/1/dashboard' };
 // Node resolves next/link to the Pages Router implementation; the application build aliases it to App Router.
 const tree = path => React.createElement(AppRouterContext.Provider, { value: router }, React.createElement(RouterContext.Provider, { value: router }, React.createElement(PathnameContext.Provider, { value: path }, React.createElement(PortfolioWorkspace, { portfolio }))));
 const view = render(tree('/portfolios/1/dashboard'));
 await waitFor(() => assert.equal(view.container.querySelector('[aria-label="Transactions view"] input') !== null, true));
 const link = screen.getByRole('link', { name: 'Transactions', exact: true });
 fireEvent.click(link);
 assert.equal(link.getAttribute('aria-current'), 'page');
 assert.equal(view.container.querySelector('[aria-label="Transactions view"]').hidden, false);
 assert.equal(view.container.querySelector('[aria-label="Dashboard view"]').hidden, true);
 view.rerender(tree('/portfolios/1/transactions'));
 view.rerender(tree('/portfolios/1/dashboard'));
 assert.equal(screen.getByRole('link', { name: 'Dashboard', exact: true }).getAttribute('aria-current'), 'page');
 assert.equal(view.container.querySelector('[aria-label="Dashboard view"]').hidden, false);
});

test('dashboard sends selected page size, resets to page one and aligns configurable columns',async t=>{
 const requests=[];
 t.mock.method(globalThis,'fetch',async url=>{requests.push(url);return workspaceFetch(url);});
 render(React.createElement(Dashboard,{id:'1'}));await screen.findByRole('button',{name:'Delete transaction 1'});
 fireEvent.click(screen.getByRole('button',{name:'Next'}));await screen.findByText('Page 2 of 2');
 await waitFor(()=>assert.equal(screen.getByLabelText('Rows per page').disabled,false));
 fireEvent.change(screen.getByLabelText('Rows per page'),{target:{value:'100'}});
 await screen.findByText('Page 1 of 1');assert.ok(requests.some(url=>url.includes('pageSize=100&page=1')));
 assert.equal(screen.getByRole('button',{name:'Next'}).disabled,true);
 fireEvent.click(screen.getByText('Columns',{exact:true}));
 fireEvent.click(screen.getByRole('checkbox',{name:'Name',exact:true}));assert.ok(screen.queryByRole('columnheader',{name:'Name',exact:true})===null);
 assert.equal(document.querySelector('.transaction-group td').colSpan,6);
 fireEvent.click(screen.getByRole('button',{name:'Move Ticker left'}));
 assert.equal(screen.getAllByRole('columnheader')[0].textContent,'Ticker');
 assert.equal(document.querySelector('tbody tr:not(.transaction-group) td').textContent,'AAPL');
});

for(const editedPrice of [false,true])test(`dashboard fetch updates transaction defaults while preserving ${editedPrice?'edited prices':'edited amounts'}`,async t=>{
 let fetched=false;
 const budget={globalRemaining:25,userRemaining:10,userLimit:10,nextSlotAt:new Date().toISOString()};
 const current={value:'50.00',performance:'30.00',performancePct:'150.00',asOfFrom:'2026-11-04',asOfTo:'2026-11-04',lastFetchedAt:'2026-11-04T13:32:00Z',partial:false,chartPoint:true,items:[{instrumentId:1,units:'2',priceEur:'25.0000',value:'50.00',source:'QUOTE',quoteDate:'2026-11-04',fxRate:null,fxRateDate:null}]};
 t.mock.method(globalThis,'fetch',async(url,init)=>{
  if(url.includes('/saving-plan'))return json([{...item,avSymbol:'AAPL',...(fetched?{defaultPrice:'25.0000',quoteId:7,priceSource:'Fetched 04.11.2026'}:{})}]);
  if(url.includes('/performance'))return json({...performance,current:fetched?current:null});
  if(url.includes('/market-data/budget'))return json(budget);
  if(url.includes('/estimate'))return json({requestsNeeded:fetched?0:1,fresh:fetched?[1]:[],noSymbol:[],budget});
  if(url.endsWith('/price-fetch-jobs'))return json({jobId:'dashboard-job'},202);
  if(url.includes('/price-fetch-jobs/dashboard-job')){fetched=true;return json({status:'DONE',nextRequestAt:new Date().toISOString(),items:[{instrumentId:1,status:'OK',priceEur:'25.0000',quoteId:7}]});}
  return workspaceFetch(url);
 });
 const view=render(panelView('dashboard'));
 const dashboard=view.container.querySelector('[aria-label="Dashboard view"]');
 const transactions=view.container.querySelector('[aria-label="Transactions view"]');
 await within(transactions).findByLabelText('AAPL price');
 fireEvent.change(within(transactions).getByLabelText('AAPL amount'),{target:{value:'75'}});
 if(editedPrice)fireEvent.change(within(transactions).getByLabelText('AAPL price'),{target:{value:'15'}});
 fireEvent.click(await within(dashboard).findByRole('button',{name:'Fetch current value (uses 1 requests)'}));
 await within(dashboard).findByText('Current performance',{exact:true});
 await within(transactions).findByText('Fetched 04.11.2026');
 assert.equal(within(transactions).getByLabelText('AAPL price').value,editedPrice?'15':'25,0000');
 assert.equal(within(transactions).getByLabelText('AAPL amount').value,'75');
 assert.equal(within(transactions).getByLabelText('AAPL quantity').value,editedPrice?'5,000000':'3,000000');
});

test('fetching quotes in Transactions refreshes current value in the mounted dashboard',async t=>{
 let fetched=false;
 const budget={globalRemaining:25,userRemaining:10,userLimit:10,nextSlotAt:new Date().toISOString()};
 t.mock.method(globalThis,'fetch',async(url,init)=>{
  if(url.includes('/saving-plan'))return json([{...item,avSymbol:'AAPL',...(fetched?{defaultPrice:'25.0000',quoteId:7,priceSource:'Fetched 04.11.2026'}:{})}]);
  if(url.includes('/performance'))return json({...performance,current:fetched?{value:'50.00',performance:'30.00',performancePct:'150.00',asOfFrom:'2026-11-04',asOfTo:'2026-11-04',lastFetchedAt:'2026-11-04T13:32:00Z',partial:false,chartPoint:true,items:[{instrumentId:1,units:'2',priceEur:'25.0000',value:'50.00',source:'QUOTE',quoteDate:'2026-11-04',fxRate:null,fxRateDate:null}]}:null});
  if(url.includes('/market-data/budget'))return json(budget);
  if(url.includes('/estimate'))return json({requestsNeeded:fetched?0:1,fresh:fetched?[1]:[],noSymbol:[],budget});
  if(url.endsWith('/price-fetch-jobs'))return json({jobId:'transaction-job'},202);
  if(url.includes('/price-fetch-jobs/transaction-job')){fetched=true;return json({status:'DONE',nextRequestAt:new Date().toISOString(),items:[{instrumentId:1,status:'OK',priceEur:'25.0000',quoteId:7}]});}
  return workspaceFetch(url);
 });
 const view=render(panelView('transactions'));
 const transactions=view.container.querySelector('[aria-label="Transactions view"]');
 const dashboard=view.container.querySelector('[aria-label="Dashboard view"]');
 fireEvent.click(await within(transactions).findByRole('button',{name:'Fetch prices (uses 1 requests)'}));
 await within(transactions).findByText('1/1 · Done');
 await within(dashboard).findByText('Current performance',{exact:true});
 assert.equal(within(transactions).getByLabelText('AAPL price').value,'25,0000');
});
