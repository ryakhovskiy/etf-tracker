import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { tickerChart } from './fixtures/ticker.mjs';
import { favoritesAPI } from './fixtures/favorites.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'SVGElement', 'Element', 'Node', 'MutationObserver', 'MouseEvent', 'KeyboardEvent']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
const { render, screen, fireEvent, waitFor, cleanup, within, act, configure } = await import('@testing-library/react');
configure({ asyncUtilTimeout: 5000 });
const { InstrumentView } = await import('../components/instrument-view.tsx');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const mountView = () => render(React.createElement(AppRouterContext.Provider, { value: { push() {}, prefetch() {} } }, React.createElement(InstrumentView, { id: '1' })));
const { TickerChart, TickerPlot } = await import('../components/ticker-chart.tsx');
const { HoldingsChart } = await import('../components/holdings-chart.tsx');
const { buyMarkerPosition, buyMarkerTone, buyMarkers, closeOnOrBefore, periodChange } = await import('../lib/ticker.ts');
afterEach(() => { cleanup(); window.localStorage.clear(); });
const stamp = day => Date.parse(`${day}T00:00:00Z`);
const text = element => element.textContent.replace(/\u00a0/g, ' ');
const json = (body, status = 200) => new Response(status === 204 ? null : JSON.stringify(body), { status });

function mockView(t, chart, { status = 200, pending, favorite = true, rejectStar = false } = {}) {
  const api = favoritesAPI(favorite ? [1, 2] : [2]);
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const path = new URL(url, 'http://localhost/').pathname;
    if (path === '/auth/me') return json({ name: 'QA Investor', email: 'qa@example.invalid' });
    if (path === '/api/instruments/1/chart') {
      if (pending) await pending.promise;
      return json(status === 404 ? { error: { code: 'NOT_FOUND', message: 'Instrument not found.' } } : chart, status);
    }
    if (rejectStar && path === '/api/favorites/1') return json({ error: { code: 'INTERNAL', message: 'Storage unavailable.' } }, 500);
    const result = api.handle(path, init.method ?? 'GET', init.body ? JSON.parse(init.body) : undefined);
    assert.ok(result, `Unhandled test request: ${init.method ?? 'GET'} ${path}`);
    return json(result.body, result.status);
  });
  return api;
}

test('buy markers compare exact decimals, placing equal prices below in the positive tone', () => {
  for (const [price, close, position, tone] of [
    ['50.0000', '40.0000', 'above', 'negative'],
    ['35.0000', '40.0000', 'below', 'positive'],
    ['40.0000', '40', 'below', 'positive'],
    ['40.0000000000000001', '40', 'above', 'negative'],
  ]) {
    assert.equal(buyMarkerPosition(price, close), position);
    assert.equal(buyMarkerTone(position), tone);
  }
});

test('a buy before available history is never coloured against a future close', () => {
  const chart = tickerChart();
  const earlyBuy = { ...chart.buys[0], day: '2000-01-01' };
  assert.deepEqual(buyMarkers(chart.points, [earlyBuy]), []);
});

test('a visible interval between closes retains the surrounding EUR prices on its y axis', async t => {
  t.mock.method(HTMLElement.prototype, 'getBoundingClientRect', () => ({ x: 0, y: 0, left: 0, top: 0, right: 900, bottom: 340, width: 900, height: 340 }));
  const chart = tickerChart({
    points: [{ day: '2026-11-20', closeEur: '40' }, { day: '2026-11-23', closeEur: '42' }],
    buys: [], from: '2026-11-20', to: '2026-11-23',
  });
  const view = render(React.createElement(TickerPlot, { chart, domain: [stamp('2026-11-21'), stamp('2026-11-22')] }));
  await waitFor(() => {
    const ticks = [...view.container.querySelectorAll('.recharts-yAxis-tick-labels .recharts-cartesian-axis-tick-value')].map(node => Number(node.textContent.replace(/[^0-9,.-]/g, '').replace(',', '.')));
    assert.ok(ticks.some(value => value >= 40 && value <= 42), `Expected price ticks around 40 EUR, got ${ticks.join(', ')}`);
  });
});

test('period figures use closes at the visible boundaries without future data or an invented zero-base percent', () => {
  const points = [{ day: '2026-01-02', closeEur: '37.2000' }, { day: '2026-02-02', closeEur: '40.9500' }, { day: '2026-03-02', closeEur: '42.2900' }];
  assert.equal(closeOnOrBefore(points, '2026-01-01'), null);
  assert.equal(closeOnOrBefore(points, '2026-01-31')?.closeEur, '37.2000');
  const gain = periodChange(points, [stamp('2026-01-02'), stamp('2026-02-28')]);
  assert.equal(Number(gain.amount), 3.75);
  assert.equal(Number(gain.percent), 10.08);
  const loss = periodChange([{ day: '2026-01-02', closeEur: '40' }, { day: '2026-02-02', closeEur: '35' }], [stamp('2026-01-02'), stamp('2026-02-02')]);
  assert.equal(loss.amount, '-5.00');
  assert.equal(loss.percent, '-12.50');
  assert.equal(periodChange(points, [stamp('2026-03-02'), stamp('2026-03-02')]), null);
  assert.equal(periodChange([], [stamp('2026-01-02'), stamp('2026-03-02')]), null);
  const zero = periodChange([{ day: '2026-01-02', closeEur: '0' }, { day: '2026-03-02', closeEur: '5' }], [stamp('2026-01-02'), stamp('2026-03-02')]);
  assert.equal(zero.percent, null);
});

for (const [end, tone, expected] of [
  ['35.0000', 'negative', /[−-]5,00 €.*[−-]12,50 %/],
  ['40.0000', 'neutral', /0,00 €.*0,00 %/],
]) test(`visible period change shows the ${tone} figure and tone`, () => {
  const chart = tickerChart();
  chart.from = chart.points[2].day;
  chart.points = [{ day: chart.from, closeEur: '40.0000' }, { day: chart.to, closeEur: end }];
  chart.buys = [];
  render(React.createElement(TickerChart, { chart }));
  const figure = screen.getByTestId('period-change');
  assert.match(figure.className, new RegExp(`performance-${tone}`));
  assert.match(text(figure), expected);
});

for (const [change, tone, expected] of [
  [{ amount: '1.0880', percent: '2.64' }, 'positive', /\+1,09 €.*\+2,64 %/],
  [{ amount: '-0.6400', percent: '-1.50' }, 'negative', /[−-]0,64 €.*[−-]1,50 %/],
  [{ amount: '0.0000', percent: '0.00' }, 'neutral', /0,00 €.*0,00 %/],
]) test(`instrument header formats EUR and ${tone} daily change`, async t => {
  const chart = tickerChart();
  chart.latest.change = change;
  mockView(t, chart);
  mountView();
  await screen.findByRole('heading', { name: chart.instrument.name, exact: true });
  assert.equal(text(screen.getByTestId('instrument-price')), '42,29 €');
  assert.match(text(screen.getByTestId('instrument-ids')), /IE00B4X9L533.*HMWO\.L/);
  const changeNode = screen.getByTestId('instrument-change');
  assert.match(changeNode.className, new RegExp(`performance-${tone}`));
  assert.match(text(changeNode), expected);
  assert.match(text(changeNode), /Change vs previous close:/);
  assert.equal(screen.getByRole('link', { name: 'All portfolios' }).getAttribute('href'), '/');
});

test('missing latest price/change stays unavailable rather than showing zero', async t => {
  const chart = tickerChart({ latest: { priceEur: null, change: null, asOf: null } });
  mockView(t, chart);
  mountView();
  await screen.findByTestId('instrument-price');
  assert.match(text(screen.getByTestId('instrument-price')), /^[–—]$/);
  assert.match(text(screen.getByTestId('instrument-change')), /^[–—]$/);
});

test('loading waits for the chart and cached-data warning retains the price and usable controls', async t => {
  const pending = Promise.withResolvers();
  mockView(t, tickerChart({ warning: 'YAHOO_UNAVAILABLE' }), { pending });
  mountView();
  assert.match(text(screen.getByRole('status')), /Loading chart/);
  await act(async () => pending.resolve());
  await screen.findByTestId('instrument-warning');
  assert.equal(text(screen.getByTestId('instrument-price')), '42,29 €');
  assert.equal(screen.getByRole('combobox', { name: 'Chart time period' }).disabled, false);
});

test('404 shows the portfolio-scoped empty state and a working back link', async t => {
  mockView(t, tickerChart(), { status: 404 });
  mountView();
  assert.match(text(await screen.findByTestId('instrument-not-found')), /not in your portfolios/i);
  assert.equal(screen.getByRole('link', { name: 'All portfolios' }).getAttribute('href'), '/');
  assert.ok(screen.queryByTestId('instrument-price') === null);
});

test('star controls persist removal and addition through the public favorites API', async t => {
  const api = mockView(t, tickerChart());
  mountView();
  const star = await screen.findByTestId('favorite-toggle');
  await waitFor(() => assert.equal(star.getAttribute('aria-pressed'), 'true'));
  fireEvent.click(star);
  await waitFor(() => assert.equal(api.ids.includes(1), false));
  await waitFor(() => assert.equal(star.getAttribute('aria-pressed'), 'false'));
  fireEvent.click(star);
  await waitFor(() => assert.equal(api.ids.includes(1), true));
  assert.deepEqual(api.calls.filter(call => call.path === '/api/favorites/1').map(call => call.method), ['DELETE', 'PUT']);
  assert.equal(api.calls.filter(call => call.path === '/api/favorites').length, 0);
});

test('an explicit false favorite flag controls the star even when the list has different stale membership', async t => {
  const api = mockView(t, tickerChart({ favorite: false }));
  mountView();
  const star = await screen.findByTestId('favorite-toggle');
  await waitFor(() => assert.equal(star.disabled, false));
  assert.equal(star.getAttribute('aria-pressed'), 'false');
  assert.equal(api.calls.length, 0);
});

test('an older chart response without favorite falls back to the favorites list', async t => {
  const chart = tickerChart();
  delete chart.favorite;
  const api = mockView(t, chart);
  mountView();
  const star = await screen.findByTestId('favorite-toggle');
  await waitFor(() => assert.equal(star.getAttribute('aria-pressed'), 'true'));
  assert.equal(api.calls.filter(call => call.path === '/api/favorites').length, 1);
});

test('a failed star mutation restores the confirmed favorite state and reports the error', async t => {
  mockView(t, tickerChart(), { rejectStar: true });
  mountView();
  const star = await screen.findByTestId('favorite-toggle');
  await waitFor(() => assert.equal(star.getAttribute('aria-pressed'), 'true'));
  fireEvent.click(star);
  await screen.findByRole('alert');
  await waitFor(() => assert.equal(star.getAttribute('aria-pressed'), 'true'));
});

test('ticker and holdings charts restore the same saved preset and retain portfolio navigation controls', () => {
  const chart = tickerChart();
  const ticker = render(React.createElement(TickerChart, { chart }));
  const selector = screen.getByRole('combobox', { name: 'Chart time period' });
  assert.equal(selector.value, '12');
  assert.deepEqual([...selector.options].map(option => option.text), ['Last 3 months', 'Last 6 months', 'Last 12 months', 'Last 18 months', 'Last 24 months', 'Last 48 months', 'Last 60 months', 'Max']);
  fireEvent.change(selector, { target: { value: '6' } });
  assert.equal(window.localStorage.getItem('holdings:chart-period'), '6');
  const before = text(screen.getByLabelText('Visible chart period'));
  fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
  assert.notEqual(text(screen.getByLabelText('Visible chart period')), before);
  assert.equal(selector.value, 'custom');
  fireEvent.click(screen.getByRole('button', { name: 'Reset chart' }));
  assert.equal(selector.value, 'max');
  assert.match(text(screen.getByTestId('period-change')), /\+17,29 €.*\+69,16 %/);
  ticker.unmount();
  render(React.createElement(HoldingsChart, { performance: { dates: [chart.from, chart.to], totals: ['25', '42.29'], invested: '25', current: null, series: [{ instrumentId: 1, name: 'Title', values: ['25', '42.29'] }] } }));
  assert.equal(screen.getByRole('combobox', { name: 'Chart time period' }).value, 'max');
  assert.equal(within(screen.getByRole('group', { name: 'Chart navigation' })).getAllByRole('button').length, 5);
});
