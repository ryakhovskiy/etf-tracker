import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { portfolioSummary, summaryPortfolios } from './fixtures/portfolio-summary.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver', 'HTMLDialogElement']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
HTMLDialogElement.prototype.showModal = function () { this.open = true; };
HTMLDialogElement.prototype.close = function () { this.open = false; };
const { render, screen, fireEvent, waitFor, cleanup, act, within } = await import('@testing-library/react');
const { Portfolios } = await import('../components/portfolios.tsx');
const { PortfolioMetrics, timeLabel } = await import('../components/portfolio-metrics.tsx');
const { SummaryStrip } = await import('../components/summary-strip.tsx');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
afterEach(() => cleanup());

const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const deferred = () => Promise.withResolvers();
const text = node => node.textContent.replaceAll('\u00a0', ' ');
const doneJob = { status: 'DONE', kind: 'USER_REALTIME', items: [{ instrumentId: 1, status: 'OK' }, { instrumentId: 2, status: 'OK' }] };
function apiFixture(t, { summary = portfolioSummary(), refresh = { started: false, reason: 'FRESH' }, job = doneJob } = {}) {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const path = new URL(url, 'http://localhost').pathname;
    const method = init.method ?? 'GET';
    calls.push({ path, method, body: init.body ? JSON.parse(init.body) : null });
    let value;
    if (path === '/api/portfolios' && method === 'GET') value = { portfolios: summaryPortfolios, limit: 10 };
    else if (path === '/api/portfolios/summaries') value = typeof summary === 'function' ? await summary() : summary;
    else if (path === '/api/price-refresh-jobs') value = typeof refresh === 'function' ? await refresh() : refresh;
    else if (path.startsWith('/api/price-fetch-jobs/')) value = typeof job === 'function' ? await job() : job;
    else throw new Error(`Unexpected request ${method} ${path}`);
    return value instanceof Response ? value : json(value);
  });
  return calls;
}
const mount = () => render(React.createElement(AppRouterContext.Provider, { value: { push() {} } }, React.createElement(Portfolios)));
const posts = calls => calls.filter(call => call.method === 'POST');
const strip = () => screen.getByRole('region', { name: 'All portfolios total' });
const card = name => screen.getAllByTestId('portfolio-card').find(node => within(node).queryByRole('link', { name: new RegExp(name) }));
async function valueLoaded(value = '1.750,00 €') { await waitFor(() => assert.match(text(strip()), new RegExp(value.replace('.', '\\.')))); }

test('list tiles and total strip show API values with gain/loss/neutral cues and do not refresh fresh quotes', async t => {
  const calls = apiFixture(t); mount(); await valueLoaded();
  assert.equal(screen.getAllByTestId('portfolio-card').length, 3);
  assert.match(text(card('Long-term')), /1\.200,00 €.*▲ \+200,00 € \(\+20,00 %\).*▲ \+20,00 € \(\+1,69 %\)/);
  assert.match(text(card('Income')), /450,00 €.*▼ -50,00 € \(-10,00 %\).*▼ -10,00 € \(-2,17 %\)/);
  assert.match(text(card('Steady')), /0,00 € \(0,00 %\)/);
  assert.doesNotMatch(text(card('Steady')), /▲|▼/);
  assert.match(text(strip()), /▲ \+150,00 € \(\+9,38 %\)/);
  assert.match(text(strip()), /3 portfolios · 4 positions/);
  assert.match(text(strip()), /Yahoo real-time/);
  assert.ok(screen.getByRole('button', { name: 'Refresh prices (3 Yahoo requests)' }));
  assert.ok(screen.getByRole('button', { name: 'Rename Income' }));
  assert.ok(screen.getByRole('button', { name: 'Delete Income' }));
  assert.equal(posts(calls).length, 0);
});

test('summaries loading uses skeletons and aria-busy until real figures arrive', async t => {
  const gate = deferred(); apiFixture(t, { summary: () => gate.promise }); mount();
  await screen.findByRole('link', { name: /Long-term/ });
  assert.equal(screen.getAllByTestId('portfolio-metrics-skeleton').length, 3);
  assert.equal(strip().getAttribute('aria-busy'), 'true');
  assert.match(screen.getByRole('status').textContent, /Loading totals/);
  assert.doesNotMatch(text(strip()), /0,00/);
  await act(async () => gate.resolve(portfolioSummary()));
  await valueLoaded();
  assert.equal(screen.queryAllByTestId('portfolio-metrics-skeleton').length, 0);
  assert.equal(strip().getAttribute('aria-busy'), 'false');
});

test('one automatic user-level job keeps stored values visible and updates them in place on completion', async t => {
  const gate = deferred(); let reads = 0;
  const calls = apiFixture(t, { summary: () => portfolioSummary(++reads === 1 ? 'auto' : 'updated'), refresh: { started: true, reused: false, jobId: 'summary-job', requests: 3 }, job: () => gate.promise });
  mount(); await screen.findByText(/Updating prices/);
  assert.match(text(strip()), /1\.750,00 €/);
  assert.equal(screen.getByRole('button', { name: 'Updating…' }).disabled, true);
  assert.deepEqual(posts(calls), [{ path: '/api/price-refresh-jobs', method: 'POST', body: {} }]);
  const original = card('Long-term');
  await act(async () => gate.resolve(doneJob));
  await valueLoaded('1.780,00 €');
  assert.ok(card('Long-term') === original);
  assert.ok(screen.queryByText(/Updating prices/) === null);
  assert.equal(posts(calls).length, 1);
  assert.equal(reads, 2);
});

test('a running job is joined without starting a second one', async t => {
  const gate = deferred(); let reads = 0;
  const calls = apiFixture(t, { summary: () => portfolioSummary(++reads === 1 ? 'updating' : 'updated'), job: () => gate.promise });
  mount(); await screen.findByText(/Updating prices/);
  assert.equal(posts(calls).length, 0);
  assert.equal(calls.filter(call => call.path === '/api/price-fetch-jobs/summary-job').length, 1);
  await act(async () => gate.resolve(doneJob)); await valueLoaded('1.780,00 €');
  assert.equal(posts(calls).length, 0);
});

test('manual refresh uses force and no provider override', async t => {
  const calls = apiFixture(t); mount(); await valueLoaded();
  fireEvent.click(screen.getByRole('button', { name: 'Refresh prices (3 Yahoo requests)' }));
  await waitFor(() => assert.equal(posts(calls).length, 1));
  assert.deepEqual(posts(calls)[0].body, { force: true });
});

test('cooldown preserves stored figures quietly and disables refreshing', async t => {
  t.mock.timers.enable({ apis: ['Date'], now: Date.UTC(2026, 10, 13, 12) });
  const calls = apiFixture(t, { summary: portfolioSummary('cooldown') }); mount(); await valueLoaded();
  assert.match(text(strip()), /Yahoo is cooling down.*Showing stored prices/);
  assert.equal(screen.getByRole('button', { name: /Refresh prices/ }).disabled, true);
  assert.ok(screen.queryByRole('alert') === null);
  assert.equal(posts(calls).length, 0);
});

test('partial and null figures stay unavailable rather than displaying fake zeros; reasons remain operable', async t => {
  apiFixture(t, { summary: portfolioSummary('partial') }); mount(); await valueLoaded('1.800,00 €');
  const income = card('Income');
  assert.match(text(income), /Unavailable/);
  assert.ok(within(income).getByLabelText('Performance: not available'));
  assert.ok(within(income).getByLabelText('Change vs yesterday: not available'));
  assert.match(text(income), /No quotes yet/);
  const hint = within(income).getAllByRole('button', { name: /Partial figures: no quote/ })[0];
  hint.focus(); assert.ok(document.activeElement === hint);
  assert.match(hint.getAttribute('aria-label'), /valued at purchase price/);
  fireEvent.click(hint);
  assert.equal(hint.getAttribute('aria-expanded'), 'true');
  assert.ok(within(hint).getByRole('tooltip'));
  fireEvent.keyDown(document, { key: 'Escape' });
  assert.equal(hint.getAttribute('aria-expanded'), 'false');
  assert.ok(within(hint).queryByRole('tooltip') === null);
  assert.ok(document.activeElement === hint);
});

test('expired Yahoo cooldown permits a safe manual retry without automatic repeated jobs or navigation', async t => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: Date.UTC(2026, 10, 13, 12) });
  const initial = portfolioSummary('cooldown');
  initial.refresh.yahoo.blockedUntil = new Date(Date.now() + 1000).toISOString();
  let reads = 0;
  const calls = apiFixture(t, { summary: () => ++reads === 1 ? initial : portfolioSummary('auto') });
  await act(async () => mount());
  assert.equal(screen.getByRole('button', { name: /Refresh prices/ }).disabled, true);
  await act(async () => t.mock.timers.tick(2000));
  assert.equal(screen.getByRole('button', { name: /Refresh prices/ }).disabled, false);
  assert.equal(posts(calls).length, 0, 'expiry must not start another automatic job');
  await act(async () => fireEvent.click(screen.getByRole('button', { name: /Refresh prices/ })));
  assert.deepEqual(posts(calls).map(call => call.body), [{ force: true }]);
});

test('Yahoo unavailable without a deadline still permits a server-gated manual retry quietly', async t => {
  const data = portfolioSummary(); data.refresh.yahoo = { available: false, blockedUntil: null };
  const calls = apiFixture(t, { summary: data, refresh: { started: false, reason: 'YAHOO_UNAVAILABLE' } });
  mount(); await valueLoaded();
  assert.equal(screen.getByRole('button', { name: /Refresh prices/ }).disabled, false);
  assert.equal(posts(calls).length, 0);
  fireEvent.click(screen.getByRole('button', { name: /Refresh prices/ }));
  await waitFor(() => assert.equal(posts(calls).length, 1));
  assert.deepEqual(posts(calls)[0].body, { force: true });
  await waitFor(() => assert.equal(calls.filter(call => call.path === '/api/portfolios/summaries').length, 2));
  assert.match(text(strip()), /1\.750,00 €/);
  assert.ok(screen.queryByRole('alert') === null);
});

test('an auto FRESH response after another tab finishes reloads the latest summaries', async t => {
  let reads = 0;
  const calls = apiFixture(t, { summary: () => portfolioSummary(++reads === 1 ? 'auto' : 'updated'), refresh: { started: false, reason: 'FRESH' } });
  mount(); await valueLoaded('1.780,00 €');
  assert.equal(reads, 2); assert.equal(posts(calls).length, 1);
  assert.equal(calls.filter(call => call.path.startsWith('/api/price-fetch-jobs/')).length, 0);
});

for (const state of ['auto', 'updating']) test(`retry after initial GET failure bootstraps ${state} rather than leaving stale data`, async t => {
  let reads = 0; const gate = deferred();
  const calls = apiFixture(t, {
    summary: () => ++reads === 1 ? json({ error: { code: 'UPSTREAM_ERROR', message: 'Unavailable' } }, 503) : portfolioSummary(reads === 2 ? state : 'updated'),
    refresh: { started: true, reused: false, jobId: 'summary-job', requests: 3 }, job: () => gate.promise,
  });
  mount(); fireEvent.click(await screen.findByRole('button', { name: 'Try again' }));
  await screen.findByText(/Updating prices/);
  assert.equal(posts(calls).length, state === 'auto' ? 1 : 0);
  await act(async () => gate.resolve(doneJob)); await valueLoaded('1.780,00 €');
  assert.equal(reads, 3);
});

test('summary display handles null percentage bases, empty holdings and nullable estimate', () => {
  const data = portfolioSummary(); data.total.positionCount = 0; data.total.currentValue = null; data.refresh.estimate = null;
  const view = render(React.createElement(SummaryStrip, { data, loading: false, failed: false, updating: false, progress: null, notice: '', onRefresh() {}, onRetry() {} }));
  assert.ok(screen.queryByRole('button', { name: /Refresh prices/ }) === null);
  view.unmount();
  const summary = { ...portfolioSummary().summaries[0], performance: { amount: '1.00', percent: null }, change: { amount: '0.00', percent: null } };
  const metrics = render(React.createElement(PortfolioMetrics, { summary, updating: false }));
  assert.match(text(metrics.container), /▲ \+1,00 € \(–\)/);
  assert.match(text(metrics.container), /0,00 € \(–\)/);
  assert.doesNotMatch(text(metrics.container), /NaN|Infinity/);
  assert.equal(timeLabel('2026-11-12T23:30:00Z', new Date('2026-11-13T12:00:00Z')), '00:30');
});
