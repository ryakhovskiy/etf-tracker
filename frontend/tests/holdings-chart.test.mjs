import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';

const dom = new JSDOM('<!doctype html><html><body></body></html>',{url:'http://localhost/'});
for(const key of ['window','document','HTMLElement','SVGElement','Node','MutationObserver']) globalThis[key]=dom.window[key];
globalThis.self=dom.window;
Object.defineProperty(globalThis,'navigator',{configurable:true,value:dom.window.navigator});
dom.window.PointerEvent=class extends dom.window.MouseEvent {
 constructor(type,options={}){super(type,options);this.pointerId=options.pointerId??1;}
};
globalThis.PointerEvent=dom.window.PointerEvent;
globalThis.ResizeObserver=class {observe(){} unobserve(){} disconnect(){}};
const {render,screen,fireEvent,cleanup,waitFor}=await import('@testing-library/react');
const {HoldingsChart,HoldingsPlot,holdingsPlotData}=await import('../components/holdings-chart.tsx');
const {chartDay,chartPeriodStorageKey,monthsBefore}=await import('../lib/chart-range.ts');
const {dateLabel}=await import('../lib/performance.ts');
const {today}=await import('../lib/transactions.ts');
afterEach(()=>{cleanup();window.localStorage.clear();});
const date=day=>Date.parse(`${day}T00:00:00Z`);
const first=date('2019-01-01'), end=date(today());
const performance={dates:['2019-01-01','2020-01-01'],series:[{instrumentId:1,name:'Apple',values:['100.00','200.00']}],totals:['100.00','200.00'],invested:'200.00',current:null};
const shown=()=>screen.getByLabelText('Visible chart period').textContent;
const rangeLabel=(start,end)=>`${dateLabel(new Date(start).toISOString().slice(0,10))} – ${dateLabel(new Date(end).toISOString().slice(0,10))}`;

test('top-right selector offers all requested periods, defaults to 12 months and counts back from today',()=>{
 render(React.createElement(HoldingsChart,{performance}));
 const selector=screen.getByRole('combobox',{name:'Chart time period'});
 assert.equal(selector.value,'12');
 assert.deepEqual([...selector.options].map(option=>option.text),['Last 3 months','Last 6 months','Last 12 months','Last 18 months','Last 24 months','Last 48 months','Last 60 months','Max']);
 assert.equal(shown(),rangeLabel(monthsBefore(end,12),end));
 assert.equal(screen.getByRole('button',{name:'Zoom out'}).disabled,false);
 for(const months of [3,6,12,18,24,48,60]){
  fireEvent.change(selector,{target:{value:String(months)}});
  assert.equal(shown(),rangeLabel(Math.max(first,monthsBefore(end,months)),end));
 }
 fireEvent.change(selector,{target:{value:'max'}});
 assert.equal(shown(),rangeLabel(first,end));
});

test('zoom, directional buttons, keyboard navigation and reset update the visible range with bounded panning',()=>{
 render(React.createElement(HoldingsChart,{performance}));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'max'}});
 const initial=shown();
 fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
 const zoomed=shown();
 assert.notEqual(zoomed,initial);
 assert.equal(screen.getByRole('combobox',{name:'Chart time period'}).value,'custom');
 fireEvent.click(screen.getByRole('button',{name:'Pan left'}));
 assert.notEqual(shown(),zoomed);
 assert.ok(shown().startsWith('01.01.2019'));
 assert.equal(screen.getByRole('button',{name:'Pan left'}).disabled,true);
 fireEvent.click(screen.getByRole('button',{name:'Pan right'}));
 assert.equal(shown(),zoomed);
 const plot=screen.getByRole('group',{name:'Holdings chart'});
 fireEvent.keyDown(plot,{key:'ArrowRight'});
 assert.ok(shown().endsWith(dateLabel(today())));
 assert.equal(screen.getByRole('button',{name:'Pan right'}).disabled,true);
 fireEvent.keyDown(plot,{key:'-'});
 assert.equal(shown(),initial);
 fireEvent.keyDown(plot,{key:'+'});
 assert.notEqual(shown(),initial);
 fireEvent.keyDown(plot,{key:'Home'});
 assert.equal(shown(),initial);
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'6'}});
 fireEvent.click(screen.getByRole('button',{name:'Reset chart'}));
 assert.equal(shown(),initial);
});

test('reopening restores each selected preset, including Max, after using a custom range',()=>{
 for(const preset of ['3','6','12','18','24','48','60','max']){
  const view=render(React.createElement(HoldingsChart,{performance}));
  fireEvent.change(screen.getByRole('combobox'),{target:{value:preset}});
  const selectedRange=shown();
  assert.equal(window.localStorage.getItem(chartPeriodStorageKey),preset);
  fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
  assert.equal(screen.getByRole('combobox').value,'custom');
  assert.equal(window.localStorage.getItem(chartPeriodStorageKey),preset);
  view.unmount();
  const reopened=render(React.createElement(HoldingsChart,{performance}));
  assert.equal(screen.getByRole('combobox').value,preset);
  assert.equal(shown(),selectedRange);
  reopened.unmount();
 }
});

test('custom navigation before selecting any preset reopens at 12 months',()=>{
 const view=render(React.createElement(HoldingsChart,{performance}));
 fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
 fireEvent.click(screen.getByRole('button',{name:'Pan left'}));
 assert.equal(screen.getByRole('combobox').value,'custom');
 assert.equal(window.localStorage.getItem(chartPeriodStorageKey),null);
 view.unmount();
 render(React.createElement(HoldingsChart,{performance}));
 assert.equal(screen.getByRole('combobox').value,'12');
 assert.equal(shown(),rangeLabel(monthsBefore(end,12),end));
});

test('invalid saved periods fall back to 12 months and storage failures leave chart controls usable',t=>{
 for(const invalid of ['custom','invalid','0','36']){
  window.localStorage.setItem(chartPeriodStorageKey,invalid);
  const view=render(React.createElement(HoldingsChart,{performance}));
  assert.equal(screen.getByRole('combobox').value,'12');
  view.unmount();
 }
 t.mock.method(dom.window.Storage.prototype,'getItem',()=>{throw new Error('Storage unavailable');});
 t.mock.method(dom.window.Storage.prototype,'setItem',()=>{throw new Error('Storage unavailable');});
 render(React.createElement(HoldingsChart,{performance}));
 assert.equal(screen.getByRole('combobox').value,'12');
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'24'}});
 assert.equal(screen.getByRole('combobox').value,'24');
 fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
 assert.equal(screen.getByRole('combobox').value,'custom');
});

test('pointer dragging pans horizontally and Ctrl-scroll zooms without intercepting ordinary page scrolling',t=>{
 render(React.createElement(HoldingsChart,{performance}));
 const plot=screen.getByRole('group',{name:'Holdings chart'});
 t.mock.method(plot,'getBoundingClientRect',()=>({left:0,width:1000}));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'12'}});
 const before=shown();
 fireEvent.pointerDown(plot,{button:0,pointerId:1,clientX:400});
 fireEvent.pointerMove(plot,{pointerId:1,clientX:900});
 assert.notEqual(shown(),before);
 assert.ok(plot.classList.contains('is-dragging'));
 fireEvent.pointerUp(plot,{pointerId:1});
 assert.equal(plot.classList.contains('is-dragging'),false);
 const panned=shown();
 assert.equal(fireEvent.wheel(plot,{deltaY:100,clientX:500}),true);
 assert.equal(shown(),panned);
 assert.equal(fireEvent.wheel(plot,{deltaY:-100,clientX:500,ctrlKey:true}),false);
 assert.notEqual(shown(),panned);
});

test('selected periods and manual navigation survive refreshed chart data',()=>{
 const view=render(React.createElement(HoldingsChart,{performance}));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'12'}});
 fireEvent.click(screen.getByRole('button',{name:'Pan left'}));
 const before=shown();
 view.rerender(React.createElement(HoldingsChart,{performance:{...performance,invested:'300.00'}}));
 assert.equal(shown(),before);
 assert.equal(screen.getByRole('combobox').value,'custom');
});

test('the responsive chart renders clipped areas after changing the period',async t=>{
 t.mock.method(HTMLElement.prototype,'getBoundingClientRect',()=>({x:0,y:0,left:0,top:0,right:900,bottom:340,width:900,height:340}));
 const view=render(React.createElement(HoldingsChart,{performance}));
 await waitFor(()=>assert.ok(view.container.querySelector('path.recharts-area-area')!==null));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'3'}});
 await waitFor(()=>assert.ok(!!view.container.querySelector('path.recharts-area-area')?.closest('[clip-path]')));
 const path=view.container.querySelector('path.recharts-area-area');
 assert.equal(path.getAttribute('d').includes('NaN'),false);
 await waitFor(()=>assert.ok(view.container.querySelectorAll('.recharts-xAxis-tick-labels .recharts-cartesian-axis-tick-value').length>0));
 const ticks=[...view.container.querySelectorAll('.recharts-xAxis-tick-labels .recharts-cartesian-axis-tick-value')].map(tick=>tick.textContent).join(' ');
 assert.ok(ticks.includes(today().slice(2,4)));
});

test('short and single-day histories stay within their available range without invalid zoom',()=>{
 const recent={...performance,dates:[today()],series:[{instrumentId:1,name:'Apple',values:['100.00']}],totals:['100.00']};
 render(React.createElement(HoldingsChart,{performance:recent}));
 const initial=shown();
 assert.equal(initial,rangeLabel(end,end));
 fireEvent.change(screen.getByRole('combobox'),{target:{value:'60'}});
 assert.equal(shown(),initial);
 for(const name of ['Pan left','Pan right','Zoom in','Zoom out']) assert.equal(screen.getByRole('button',{name}).disabled,true);
});

test('zoom clips the time axis while retaining earlier holdings and source dates for the last known value',()=>{
 const domain=[end-30*chartDay,end];
 const chart=HoldingsPlot({performance,domain});
 const axis=React.Children.toArray(chart.props.children).find(child=>child?.props.dataKey==='date');
 assert.deepEqual(axis.props.domain,domain);
 assert.equal(axis.props.allowDataOverflow,true);
 const points=chart.props.data;
 assert.equal(points[0].date,first);
 assert.equal(points.at(-1).date,end);
 assert.equal(points.at(-1).sourceDate,date('2020-01-01'));
 assert.equal(points.at(-1).total,'200.00');
 assert.equal(points.at(-1).i1,200);
 assert.equal(points.at(-1).isBoundary,true);
 const current={value:'250.00',asOfFrom:'2026-01-01',asOfTo:'2026-01-01',chartPoint:true,items:[{instrumentId:1,value:'250.00',source:'QUOTE'}]};
 const currentPoints=holdingsPlotData({...performance,current},end);
 assert.equal(currentPoints.at(-1).sourceDate,date('2026-01-01'));
 assert.equal(currentPoints.at(-1).c1,250);
 assert.equal(currentPoints.at(-1).isCurrent,true);
 const tooltip=React.Children.toArray(chart.props.children).find(child=>typeof child?.props.content==='function');
 const tooltipContent=tooltip.props.content({active:true,label:end,payload:[{payload:points.at(-1)}]});
 assert.equal(tooltipContent.props.date,date('2020-01-01'));
});
