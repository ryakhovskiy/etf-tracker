import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import React from 'react';
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window','document','HTMLElement','SVGElement','Node','MutationObserver','HTMLDialogElement','sessionStorage']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
const { render, screen, fireEvent, waitFor, cleanup, within, act } = await import('@testing-library/react');
const { PerformanceAmount, PerformanceFigures } = await import('../components/performance-figures.tsx');
const { CurrentValueFetch } = await import('../components/current-value-fetch.tsx');
const { HoldingsChart, HoldingsPlot, HoldingsTooltip } = await import('../components/holdings-chart.tsx');

const plotFor = performance => HoldingsPlot({ performance, domain: [Date.parse(`${performance.dates[0]}T00:00:00Z`), Date.parse(`${performance.current?.chartPoint ? performance.current.asOfTo : performance.dates.at(-1)}T00:00:00Z`)] });
const { Dashboard } = await import('../components/dashboard.tsx');
const { PerformanceOverview } = await import('../components/performance-overview.tsx');
const { performanceDisplay, axisEuro, quoteDates } = await import('../lib/performance.ts');
const { chartData, today } = await import('../lib/transactions.ts');
afterEach(() => { cleanup(); sessionStorage.clear(); window.localStorage.clear(); });
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const p = {
 dates: ['2026-10-01','2026-11-01'], series: [{ instrumentId: 1, name: 'Apple', values: ['100.00','110.00'] }, { instrumentId: 2, name: 'SAP', values: ['100.00','100.00'] }],
 totals: ['200.00','210.00'], invested: '200.00', atPurchase: { value: '210.00', performance: '10.00', performancePct: '5.00' }, current: null,
};
const current = {
 value: '220.00', performance: '20.00', performancePct: '10.00', asOfFrom: '2026-11-03', asOfTo: '2026-11-04', lastFetchedAt: '2026-11-04T13:32:00Z', partial: true, chartPoint: true,
 items: [{ instrumentId: 1, units: '10', priceEur: '12.00', value: '120.00', source: 'QUOTE', quoteDate: '2026-11-04', fxRate: null, fxRateDate: null }, { instrumentId: 2, units: '10', priceEur: '10.00', value: '100.00', source: 'PURCHASE', reason: 'NO_SYMBOL', fxRate: null, fxRateDate: null }],
};
const estimate = (needed = 2, remaining = 10, extra = {}) => ({ requestsNeeded: needed, fresh: [], noSymbol: [], budget: { userRemaining: remaining, globalRemaining: 25, nextSlotAt: '2026-11-06T13:32:00Z' }, ...extra });
const mountFetch = (performance = p, reload = async () => {}) => render(React.createElement(CurrentValueFetch, { id: '1', performance, reload }));

const fullSelection = {...p,current};
const appleSelection = {...p,series:[p.series[0]],invested:'100.00',totals:['100.00','110.00'],atPurchase:{value:'110.00',performance:'10.00',performancePct:'10.00'},current:{...current,value:'120.00',performance:'20.00',performancePct:'20.00',partial:false,items:[current.items[0]],asOfFrom:'2026-11-04'}};
const sapSelection = {...p,series:[p.series[1]],invested:'100.00',totals:['100.00','100.00'],atPurchase:{value:'100.00',performance:'0.00',performancePct:'0.00'},current:null};
const emptySelection = {dates:[],series:[],totals:[],invested:'0.00',atPurchase:{value:'0.00',performance:'0.00',performancePct:null},current:null};
function selectionAPI(t,override=()=>undefined){
 const calls=[];
 t.mock.method(globalThis,'fetch',async(url,init)=>{
  calls.push({url,init});
  const overridden=override(url,init);
  if(overridden!==undefined)return overridden;
  if(url.includes('/performance?instrumentIds='))return json({'1':appleSelection,'2':sapSelection,'':emptySelection}[new URL(url,'http://localhost').searchParams.get('instrumentIds')]);
  if(url.endsWith('/performance'))return json(fullSelection);
  if(url.includes('/transactions?'))return json({lines:[],total:0,page:1,groups:{}});
  if(url.includes('/estimate'))return json(estimate(0,10,{fresh:[1],noSymbol:[2]}));
  if(url.endsWith('/portfolios/1'))return json({itemCount:2});
  throw Error(`Unexpected request ${url}`);
 });
 return calls;
}
const figureValue=label=>within(screen.getByLabelText('Portfolio figures')).getByText(label,{exact:true}).parentElement.querySelector('strong').textContent;

test('legend toggles remove positions and recalculate all five figures, quote metadata and chart areas',async t=>{
 t.mock.method(HTMLElement.prototype,'getBoundingClientRect',()=>({x:0,y:0,left:0,top:0,right:900,bottom:340,width:900,height:340}));
 const calls=selectionAPI(t);
 const view=render(React.createElement(Dashboard,{id:'1'}));
 const legend=await screen.findByRole('group',{name:'Chart positions'});
 await waitFor(()=>assert.equal(view.container.querySelectorAll('path.recharts-area-area').length,4));
 assert.equal(figureValue('Total invested'),'200,00 €');
 fireEvent.change(screen.getByRole('combobox',{name:'Chart time period'}),{target:{value:'6'}});
 const range=screen.getByLabelText('Visible chart period').textContent;
 const priceEstimates=calls.filter(call=>call.url.includes('/estimate')).length;
 fireEvent.click(within(legend).getByRole('button',{name:'SAP',exact:true}));
 await waitFor(()=>assert.equal(figureValue('Total invested'),'100,00 €'));
 assert.equal(figureValue('Holdings value at purchase prices'),'110,00 €');
 assert.equal(figureValue('Performance at purchase prices'),'▲ +10,00 € (+10,00 %)');
 assert.equal(figureValue('Current value'),'120,00 €');
 assert.equal(figureValue('Current performance'),'▲ +20,00 € (+20,00 %)');
 assert.equal(within(legend).getByRole('button',{name:'SAP',exact:true}).getAttribute('aria-pressed'),'false');
 assert.equal(within(legend).getByRole('button',{name:'Apple',exact:true}).getAttribute('aria-pressed'),'true');
 assert.equal(within(screen.getByLabelText('Portfolio figures')).queryAllByText('partial',{exact:true}).length,0);
 await waitFor(()=>assert.equal(view.container.querySelectorAll('path.recharts-area-area').length,2));
 assert.equal(screen.getByRole('combobox',{name:'Chart time period'}).value,'6');
 assert.equal(screen.getByLabelText('Visible chart period').textContent,range);
 assert.equal(calls.filter(call=>call.url.includes('/estimate')).length,priceEstimates);
 assert.ok(calls.some(call=>call.url.includes('/performance?instrumentIds=1')));
 fireEvent.click(within(legend).getByRole('button',{name:'Apple',exact:true}));
 await screen.findByText('No positions selected. Use the legend to show a position.');
 for(const label of ['Total invested','Holdings value at purchase prices','Current value'])assert.equal(figureValue(label),'0,00 €');
 for(const label of ['Performance at purchase prices','Current performance'])assert.equal(figureValue(label),'0,00 € (–)');
 assert.equal(view.container.querySelectorAll('path.recharts-area-area').length,0);
 fireEvent.click(within(legend).getByRole('button',{name:'SAP',exact:true}));
 await waitFor(()=>assert.equal(figureValue('Total invested'),'100,00 €'));
 assert.equal(figureValue('Current value'),'Unavailable');
 assert.equal(figureValue('Current performance'),'Unavailable');
 fireEvent.click(screen.getByRole('button',{name:'Show all'}));
 assert.equal(figureValue('Total invested'),'200,00 €');
 assert.equal(figureValue('Holdings value at purchase prices'),'210,00 €');
 assert.equal(figureValue('Current value'),'220,00 €');
 assert.equal(figureValue('Current performance'),'▲ +20,00 € (+10,00 %)');
 assert.equal(within(legend).getByRole('button',{name:'Apple',exact:true}).getAttribute('aria-pressed'),'true');
 assert.equal(calls.some(call=>call.init.method==='POST'||call.init.method==='DELETE'),false);
});

test('rapid toggles ignore stale responses and preserve the most recent selection',async t=>{
 let finishApple,finishEmpty;
 selectionAPI(t,url=>{
  if(url.endsWith('/performance?instrumentIds=1'))return new Promise(resolve=>{finishApple=()=>resolve(json(appleSelection));});
  if(url.endsWith('/performance?instrumentIds='))return new Promise(resolve=>{finishEmpty=()=>resolve(json(emptySelection));});
 });
 render(React.createElement(PerformanceOverview,{id:'1',performance:fullSelection,reload:async()=>{}}));
 const legend=screen.getByRole('group',{name:'Chart positions'});
 fireEvent.click(within(legend).getByRole('button',{name:'SAP',exact:true}));
 await waitFor(()=>assert.equal(typeof finishApple,'function'));
 fireEvent.click(within(legend).getByRole('button',{name:'Apple',exact:true}));
 await waitFor(()=>assert.equal(typeof finishEmpty,'function'));
 await act(async()=>finishEmpty());
 assert.equal(figureValue('Total invested'),'0,00 €');
 await act(async()=>finishApple());
 assert.equal(figureValue('Total invested'),'0,00 €');
 assert.equal(within(legend).getByRole('button',{name:'Apple',exact:true}).getAttribute('aria-pressed'),'false');
 assert.equal(within(legend).getByRole('button',{name:'SAP',exact:true}).getAttribute('aria-pressed'),'false');
});

test('selection survives a quote refresh and failed requests restore the displayed selection with an error',async t=>{
 let refreshed=false,failEmpty=false;
 selectionAPI(t,url=>{
  if(url.endsWith('/performance?instrumentIds=1')&&refreshed)return json({...appleSelection,current:{...appleSelection.current,value:'130.00',performance:'30.00',performancePct:'30.00'}});
  if(url.endsWith('/performance?instrumentIds=')&&failEmpty)return json({error:{code:'INTERNAL_ERROR',message:'Try again.'}},500);
 });
 const view=render(React.createElement(PerformanceOverview,{id:'1',performance:fullSelection,reload:async()=>{}}));
 const legend=screen.getByRole('group',{name:'Chart positions'});
 fireEvent.click(within(legend).getByRole('button',{name:'SAP',exact:true}));
 await waitFor(()=>assert.equal(figureValue('Current value'),'120,00 €'));
 refreshed=true;
 view.rerender(React.createElement(PerformanceOverview,{id:'1',performance:{...fullSelection,current:{...current,value:'230.00'}},reload:async()=>{}}));
 await waitFor(()=>assert.equal(figureValue('Current value'),'130,00 €'));
 assert.equal(within(legend).getByRole('button',{name:'SAP',exact:true}).getAttribute('aria-pressed'),'false');
 failEmpty=true;
 fireEvent.click(within(legend).getByRole('button',{name:'Apple',exact:true}));
 const error=await screen.findByRole('alert');
 assert.match(error.textContent,/Could not update the chart selection/);
 await waitFor(()=>assert.equal(within(legend).getByRole('button',{name:'Apple',exact:true}).getAttribute('aria-pressed'),'true'));
 assert.equal(figureValue('Current value'),'130,00 €');
 fireEvent.click(screen.getByRole('button',{name:'Show all'}));
 assert.ok(screen.queryByRole('alert')===null);
 assert.equal(figureValue('Current value'),'230,00 €');
});

test('performance signs, rounded color thresholds, arrows and zero-investment percentage', () => {
 const cases = [
  ['8892.34','24.33','positive','▲ +8.892,34 € (+24,33 %)'],
  ['-10.005','-1.005','negative','▼ -10,01 € (-1,01 %)'],
  ['-0.004','0','neutral','0,00 € (0,00 %)'],
  ['0.004','0','neutral','0,00 € (0,00 %)'],
  ['0.005','0.01','positive','▲ +0,01 € (+0,01 %)'],
  ['0',null,'neutral','0,00 € (–)'],
 ];
 for (const [value,pct,tone,text] of cases) {
  assert.deepEqual(performanceDisplay(value,pct), { tone, text });
  const view = render(React.createElement(PerformanceAmount, { valuation: { value: '0', performance: value, performancePct: pct } }));
  assert.ok(view.container.firstChild.classList.contains(`performance-${tone}`));
  assert.equal(view.container.textContent, text);
  view.unmount();
 }
 const css = readFileSync(new URL('../app/globals.css',import.meta.url),'utf8');
 assert.match(css, /@media\(prefers-color-scheme:dark\)[\s\S]*--positive:[^;]+;--negative:[^;]+;--text:/);
 assert.match(css, /\.figures\{[^}]*display:grid/);
 assert.doesNotMatch(css, /\.current-fetch\{[^}]*position:absolute/);
 assert.match(css, /performance-neutral\{color:var\(--text\)\}/);
});

test('figures show date range, Berlin fetch time and partial reasons; null hides current labels', async t => {
 t.mock.method(globalThis,'fetch',async () => json(estimate(1,10,{noSymbol:[2]})));
 const view = render(React.createElement(PerformanceFigures, { id: '1', performance: {...p,current}, reload: async () => {} }));
 assert.ok(screen.getByText('Current value',{exact:true}));
 assert.ok(screen.getByText('Current performance',{exact:true}));
 assert.equal(screen.getAllByText('as of 03.11.–04.11.2026 · fetched 04.11.2026, 14:32').length,2);
 assert.equal(screen.getAllByText('partial',{exact:true}).length,2);
 assert.equal(screen.getAllByRole('tooltip').filter(el => el.textContent.includes('SAP: no Alpha Vantage symbol')).length,2);
 fireEvent.click(await screen.findByText("1 holding needs a price symbol"));
 assert.ok(screen.getByRole('link',{name:'Link symbol in Transactions tab'}));
 view.rerender(React.createElement(PerformanceFigures, { id: '1', performance:p, reload:async()=>{} }));
 assert.equal(screen.queryByText('Current value',{exact:true}),null);
 assert.equal(screen.queryByText('Current performance',{exact:true}),null);
 assert.ok(screen.getByText('Performance at purchase prices'));
 assert.equal(quoteDates('2026-11-04','2026-11-04'),'04.11.2026');
});

test('estimate, partial-budget, exhausted-budget and fresh button states', async t => {
 for (const [est,label,disabled] of [
  [estimate(4,10),'Fetch current value (uses 4 requests)',false],
  [estimate(12,7),'Fetch 7 of 12 (budget limit)',false],
  [estimate(12,0),'Fetch 0 of 12 (budget limit)',true],
  [estimate(0,0),'Up to date (uses 0 requests)',false],
  [estimate(4,10,{budget:{userRemaining:10,globalRemaining:2,nextSlotAt:'2026-11-06T13:32:00Z'}}),'Fetch 2 of 4 (budget limit)',false],
  [estimate(2,10,{budget:{userRemaining:10,globalRemaining:25,nextSlotAt:new Date().toISOString(),blockedUntil:new Date(Date.now()+3600000).toISOString()}}),'Fetch current value (uses 2 requests)',true],
 ]) {
  const mock = t.mock.method(globalThis,'fetch',async () => json(est));
  const view = mountFetch();
  const button = await screen.findByRole('button',{name:label});
  assert.equal(button.disabled,disabled);
  if (disabled) assert.match(button.title,/Alpha Vantage limit reached, next request possible at/);
  view.unmount(); mock.mock.restore();
 }
});

test('job submits Berlin today and holdings priority, shows progress, then reloads persistent figures', async t => {
 let polls = 0, submitted, loads = 0, finished = false;
 t.mock.method(globalThis,'fetch',async (url,init) => {
  if(url.endsWith('/performance')) {loads++;return json({...p,current:finished?current:null});}
  if(url.includes('/transactions?')) return json({lines:[],total:0,page:1});
  if(url.endsWith('/portfolios/1')) return json({itemCount:2});
  if(url.includes('/estimate')) return json(estimate(finished?0:1,10,{noSymbol:[2]}));
  if(url.endsWith('/price-fetch-jobs')) {submitted=JSON.parse(init.body);return json({jobId:'job'},202);}
  if(url.endsWith('/price-fetch-jobs/job')) {polls++;finished=polls>1;return json({status:finished?'DONE':'RUNNING',nextRequestAt:new Date(Date.now()+9000).toISOString(),items:[{instrumentId:1,status:finished?'OK':'PENDING'},{instrumentId:2,status:'NO_SYMBOL'}]});}
  throw Error(`Unexpected call ${url}`);
 });
 const view = render(React.createElement(Dashboard,{id:'1'}));
 fireEvent.click(await screen.findByRole('button',{name:'Fetch current value (uses 1 requests)'}));
 await waitFor(()=>assert.deepEqual(submitted,{date:today(),instrumentIds:[1,2],priority:'HOLDINGS_VALUE'}));
 await screen.findByText(/1\/2 · next request in/);
 await screen.findByText('1 of 2 holdings updated',{}, {timeout:4000});
 await screen.findByText('Current performance',{exact:true});
 assert.ok(loads>=2);assert.equal(sessionStorage.getItem('current-value-fetch:1'),null);
 view.unmount();
 render(React.createElement(Dashboard,{id:'1'}));
 await screen.findByText('Current performance',{exact:true});
});

test('interrupted fetch reloads quotes already saved', async t => {
 sessionStorage.setItem('current-value-fetch:1','lost');let reloads=0;
 t.mock.method(globalThis,'fetch',async url => url.includes('/price-fetch-jobs/') ? json({error:{code:'NOT_FOUND',message:'Missing job'}},404) : json(estimate()));
 mountFetch(p,async()=>{reloads++;});
 await screen.findByText('Fetch interrupted. Prices fetched so far were saved.');
 await waitFor(()=>assert.equal(reloads,1));
 assert.equal(sessionStorage.getItem('current-value-fetch:1'),null);
});

test('current chart point uses a dashed segment and marker with per-instrument tooltip sources', async () => {
 const performance = {...p,current};
 const points = chartData(performance);
 assert.equal(points.length,3);assert.equal(points[2].date,Date.parse('2026-11-04T00:00:00Z'));
 assert.equal(points[2].i1,null);assert.equal(points[2].c1,120);assert.equal(points[1].c1,110);assert.equal(points[0].c1,null);
 const chart = plotFor(performance);
 const {container} = render(React.cloneElement(chart,{width:900,height:340}));
 await waitFor(()=>assert.equal(container.querySelectorAll('path.recharts-area-curve[stroke-dasharray="6 4"]').length,2));
 assert.equal(container.querySelectorAll('.current-value-dot').length,2);
 assert.ok([...container.querySelectorAll('text')].some(el=>el.textContent==='Current'));
 const fills = [...container.querySelectorAll('path.recharts-area-area')];
 assert.equal(fills.filter(path=>path.getAttribute('fill-opacity')==='0.2').length,2);
 render(React.createElement(HoldingsTooltip,{performance,date:points[2].date,current:true,total:'220.00'}));
 assert.ok(screen.getByText('Current value · quotes as of 03.11.–04.11.2026'));
 assert.ok(screen.getByText(/Apple: 120,00\s€ · Quote/));
 assert.ok(screen.getByText(/SAP: 100,00\s€ · Purchase price/));
 assert.ok(screen.getByText(/Total: 220,00\s€/));
});

test('stale quote hint replaces chart extension when chartPoint is false', () => {
 const performance = {...p,current:{...current,chartPoint:false}};
 assert.equal(chartData(performance).length,2);
 render(React.createElement(HoldingsChart,{performance}));
 assert.ok(screen.getByText('Quotes are older than your last transaction. Fetch again to update the chart.'));
 assert.equal(document.querySelector('path[stroke-dasharray="6 4"]'),null);
 assert.ok(screen.getByText('Current value (quotes)'));
});

for (const max of [40000,9999999]) test(`Y axis reserves measured space for ${max} EUR without decimals`, async t => {
 t.mock.method(SVGElement.prototype,'getBoundingClientRect',function(){return {x:0,y:0,width:this.textContent.length*8,height:16,top:0,left:0,bottom:16,right:this.textContent.length*8};});
 const performance = {...p,series:[{instrumentId:1,name:'Apple',values:['0',String(max)]}],totals:['0',String(max)]};
 const chart = plotFor(performance);
 const children = React.Children.toArray(chart.props.children).map(child => child?.props?.width==='auto' ? React.cloneElement(child,{ticks:[0,max],domain:[0,max]}) : child);
 const {container} = render(React.cloneElement(chart,{width:900,height:340},children));
 const label = axisEuro(max);
 assert.equal(label,max===40000?'40.000 €':'9.999.999 €');
 await waitFor(()=>assert.ok(container.querySelector('.recharts-yAxis')));
 const tick = [...container.querySelectorAll('.recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value')].find(el=>el.textContent===label);
 assert.ok(tick,`Missing full label ${label}: ${container.querySelector('.recharts-yAxis')?.outerHTML}`);
 await waitFor(()=>assert.ok(Number(tick.getAttribute('x'))-label.length*8>=0,'measured label starts inside the SVG'));
});


test('unlinked holdings show one collapsed notice and never claim to be up to date', async t => {
 const names = ['HSBC MSCI World UCITS ETF USD','Vanguard FTSE All-World High Dividend Yield UCITS ETF Distributing','Vanguard FTSE Emerging Markets UCITS ETF Distributing','iShares STOXX Global Select Dividend 100 UCITS ETF (DE)','Xtrackers MSCI EMU UCITS ETF'];
 const ids = [1,2,3,4,5];let posts=0;
 t.mock.method(globalThis,'fetch',async(url,init)=>{if(init.method==='POST')posts++;return json(estimate(0,10,{noSymbol:ids}));});
 const performance = {...p,series:names.map((name,i)=>({instrumentId:i+1,name,values:['100','100']}))};
 const {container}=render(React.createElement(PerformanceFigures,{id:'1',performance,reload:async()=>{}}));
 const button=await screen.findByRole('button',{name:'Fetch current value',exact:true});
 assert.equal(button.disabled,true);
 assert.ok(screen.getByText('No price symbols are linked. Link your holdings to fetch current value.'));
 assert.equal(screen.queryByRole('button',{name:/Up to date/}),null);
 assert.equal(container.querySelector('.current-fetch').parentElement,container.querySelector('.figures').parentElement);
 const details=container.querySelector('.fetch-details');assert.equal(details.open,false);
 assert.equal(container.querySelectorAll('.fetch-details').length,1);
 fireEvent.click(screen.getByText('5 holdings need a price symbol'));
 assert.equal(details.open,true);
 assert.equal(screen.getAllByRole('link',{name:'Link symbol in Transactions tab'}).length,5);
 fireEvent.click(button);assert.equal(posts,0);
});

test('fresh linked holdings with missing symbols offer a free refresh without claiming full coverage',async t=>{
 t.mock.method(globalThis,'fetch',async()=>json(estimate(0,10,{fresh:[1],noSymbol:[2]})));
 mountFetch({...p,current});
 const button=await screen.findByRole('button',{name:'Refresh current value (uses 0 requests)'});
 assert.equal(button.disabled,false);
 assert.ok(screen.getByText('1 holding needs a price symbol'));
});

test('real-time value button submits a real-time holdings job and reports it', async t => {
 let submitted, reloads = 0, polls = 0;
 const rt = estimate(1, 10, { realtime: { requests: 3, remaining: 900, noSymbol: [] } });
 t.mock.method(globalThis,'fetch',async (url,init) => {
  if (url.includes('/estimate')) return json(rt);
  if (url.endsWith('/price-fetch-jobs')) { submitted = JSON.parse(init.body); return json({ jobId: 'rt' }, 202); }
  if (url.endsWith('/price-fetch-jobs/rt')) { polls++; return json({ status: polls > 1 ? 'DONE' : 'RUNNING', nextRequestAt: new Date(Date.now()+9000).toISOString(), items: [{ instrumentId: 1, status: polls > 1 ? 'OK' : 'PENDING' }, { instrumentId: 2, status: 'NO_SYMBOL' }] }); }
  throw Error(`Unexpected call ${url}`);
 });
 mountFetch(p, async () => { reloads++; });
 const button = await screen.findByRole('button', { name: 'Fetch real-time value (3 Yahoo requests)' });
 assert.match(button.title, /delayed by about 15 minutes/);
 assert.equal(screen.getByRole('button', { name: 'Fetch current value (uses 1 requests)' }).disabled, false);
 fireEvent.click(button);
 await waitFor(() => assert.deepEqual(submitted, { date: today(), instrumentIds: [1,2], priority: 'HOLDINGS_VALUE', realtime: true }));
 await screen.findByText('1/2 · fetching real-time prices…');
 assert.equal(screen.getByRole('button', { name: 'Fetching real-time prices…' }).disabled, true);
 assert.equal(screen.getByRole('button', { name: 'Fetch current value (uses 1 requests)' }).disabled, true, 'one job at a time');
 await screen.findByText('1 of 2 holdings updated with real-time prices', {}, { timeout: 4000 });
 assert.equal(reloads, 1);
 assert.equal(sessionStorage.getItem('current-value-fetch:1:kind'), null);
});

test('no real-time value button without a real-time estimate, or when all holdings are unlinked', async t => {
 for (const [est, shown] of [[estimate(1, 10), false], [estimate(1, 10, { realtime: null }), false], [estimate(0, 10, { noSymbol: [1,2], realtime: { requests: 0, remaining: 900, noSymbol: [1,2] } }), true]]) {
  const mock = t.mock.method(globalThis,'fetch',async () => json(est));
  const view = mountFetch();
  await screen.findByRole('button', { name: /current value|Up to date/ });
  const button = screen.queryByRole('button', { name: /real-time/ });
  assert.equal(!!button, shown);
  if (button) assert.equal(button.disabled, true, 'nothing to fetch without symbols');
  view.unmount(); mock.mock.restore();
 }
});
