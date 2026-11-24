import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { portfolioSummary } from './fixtures/portfolio-summary.mjs';
import { priceJob } from './fixtures/price-job.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver', 'sessionStorage']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
const { act, cleanup, fireEvent, render, renderHook, screen } = await import('@testing-library/react');
const { CurrentValueFetch } = await import('../components/current-value-fetch.tsx');
const { PriceFetch } = await import('../components/price-fetch.tsx');
const { BackupFetch } = await import('../components/backup-fetch.tsx');
const { usePortfolioSummaries } = await import('../components/use-portfolio-summaries.ts');
const { today } = await import('../lib/transactions.ts');

afterEach(() => { cleanup(); sessionStorage.clear(); });
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const clock = t => t.mock.timers.enable({ apis: ['Date', 'setTimeout', 'setInterval'], now: Date.UTC(2026, 10, 24, 12) });
const advance = async (t, ms) => act(async () => t.mock.timers.tick(ms));
const pending = () => ({ version: 1, status: 'RUNNING', nextRequestAt: new Date(Date.now() + 9000).toISOString(), items: [{ instrumentId: 1, status: 'PENDING' }] });
const completedItems = [{ instrumentId: 1, status: 'OK' }];
const surfaces = ['summaries', 'dashboard', 'transactions', 'backup'];

async function mountJob(t, surface, { provider = 'YAHOO', initial = pending(), reply } = {}) {
  const job = priceJob(initial);
  const jobs = new Map([['poll-job', job]]);
  const polls = [], submitted = [], unexpected = [];
  let reloads = 0, summaryLoads = 0;
  const budget = { primary: provider, globalRemaining: 25, userRemaining: 10, userLimit: 10, nextSlotAt: new Date().toISOString() };
  const estimate = {
    provider, requestsNeeded: 1, fresh: [], noSymbol: [], budget,
    yahoo: { requests: 1, remaining: 25, noSymbol: [] },
    eulerpool: { requests: 1, remaining: 25, noIsin: [] },
    alphavantage: { requests: 1, remaining: 10 },
    realtime: { requests: 1, remaining: 25, noSymbol: [] },
  };
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const parsed = new URL(url, 'http://localhost');
    const path = parsed.pathname;
    if (path === '/api/portfolios/summaries') return json(portfolioSummary(++summaryLoads === 1 ? 'auto' : 'updated'));
    if (path === '/api/market-data/budget') return json(budget);
    if (path.endsWith('/price-fetch/estimate')) return json(estimate);
    if ((path === '/api/price-refresh-jobs' || path === '/api/portfolios/1/price-fetch-jobs') && init.method === 'POST') {
      submitted.push(JSON.parse(init.body));
      return json({ started: true, reused: false, jobId: 'poll-job', requests: 1 }, 202);
    }
    if (path.startsWith('/api/price-fetch-jobs/') && jobs.has(path.split('/').at(-1))) {
      const jobId = path.split('/').at(-1);
      const currentJob = jobs.get(jobId);
      const request = { jobId, since: parsed.searchParams.get('since'), at: Date.now(), signal: init.signal };
      polls.push(request);
      const overridden = reply?.(polls.length, request, currentJob);
      return overridden ?? json(await currentJob.read(request.since, { signal: init.signal }));
    }
    unexpected.push(path);
    throw new Error(`Unexpected request ${path}`);
  });
  const reload = async () => { reloads++; };
  const element = id => surface === 'dashboard' ? React.createElement(CurrentValueFetch, {
    id, reload, performance: { series: [{ instrumentId: 1 }], current: null },
  }) : surface === 'transactions' ? React.createElement(PriceFetch, {
    id, date: today(), rows: [{ instrumentId: 1, ticker: 'SAP', name: 'SAP SE', avSymbol: 'SAP.DEX', yahooSymbol: 'SAP.DE' }],
    selected: [1], setRows() {}, reload, busy: false, onFetching() {},
  }) : React.createElement(BackupFetch, {
    portfolioId: id, date: today(), primary: provider === 'YAHOO' ? 'EULERPOOL' : 'YAHOO', ids: [1], names: { 1: 'SAP' }, onItems() {}, onDone: reload,
  });
  let view;
  await act(async () => {
    if (surface === 'summaries') view = renderHook(() => usePortfolioSummaries(true));
    else view = render(element('1'));
  });
  if (surface !== 'summaries') {
    const name = surface === 'dashboard' ? /^Fetch current value/ : surface === 'transactions' ? /^Fetch prices/ : new RegExp(`via ${provider === 'ALPHAVANTAGE' ? 'Alpha Vantage' : 'Yahoo Finance'}`);
    const button = screen.getByRole('button', { name });
    assert.equal(button.disabled, false);
    await act(async () => fireEvent.click(button));
  }
  t.after(() => { for (const current of jobs.values()) current.dispose(); });
  const switchPortfolio = async () => {
    const replacement = priceJob(pending());
    jobs.set('replacement', replacement);
    sessionStorage.setItem(`${surface === 'dashboard' ? 'current-value-fetch' : 'price-fetch'}:2`, 'replacement');
    await act(async () => view.rerender(element('2')));
    return replacement;
  };
  return { job, polls, submitted, unexpected, view, switchPortfolio, get reloads() { return reloads; }, get summaryLoads() { return summaryLoads; } };
}

for (const surface of surfaces) {
  test(`${surface}: immediately long-polls with since, publishes changes and stops on DONE`, async t => {
    clock(t);
    const h = await mountJob(t, surface);
    assert.deepEqual(h.polls.map(poll => poll.since), [null, '1']);
    assert.equal(h.polls[1].at, h.polls[0].at, 'no fixed delay between changed versions');
    assert.equal(h.job.waiting, 1, 'the current-version request stays pending');
    await act(async () => h.job.publish({ items: completedItems }));
    assert.deepEqual(h.polls.map(poll => poll.since), [null, '1', '2']);
    assert.equal(h.polls[2].at, h.polls[1].at);
    await act(async () => h.job.finish(completedItems));
    await advance(t, 20_000);
    assert.equal(h.polls.length, 3, 'DONE must stop the loop');
    assert.equal(h.job.waiting, 0);
    assert.equal(h.submitted.length, 1);
    assert.deepEqual(h.unexpected, []);
    if (surface === 'summaries') assert.equal(h.summaryLoads, 2);
    else assert.equal(h.reloads, surface === 'transactions' ? 0 : 1);
  });

  test(`${surface}: an unchanged version waits exactly 1 second before retrying`, async t => {
    clock(t);
    const h = await mountJob(t, surface, { reply: (number, _, job) => number === 2 ? json(job.snapshot()) : undefined });
    assert.deepEqual(h.polls.map(poll => poll.since), [null, '1']);
    await advance(t, 999);
    assert.equal(h.polls.length, 2);
    await advance(t, 1);
    assert.equal(h.polls.length, 3);
    assert.equal(h.polls[2].since, '1');
    assert.equal(h.polls[2].at - h.polls[1].at, 1000);
    await act(async () => h.job.finish(completedItems));
  });

  test(`${surface}: errors retry at 2 seconds and retain the last version`, async t => {
    clock(t);
    const h = await mountJob(t, surface, { reply: number => number === 2 ? json({ error: { code: 'UPSTREAM_ERROR', message: 'Temporary outage' } }, 503) : undefined });
    assert.equal(h.polls.length, 2);
    await advance(t, 1999);
    assert.equal(h.polls.length, 2);
    await advance(t, 1);
    assert.equal(h.polls.length, 3);
    assert.equal(h.polls[2].since, '1');
    assert.equal(h.polls[2].at - h.polls[1].at, 2000);
    await act(async () => h.job.finish(completedItems));
  });

  test(`${surface}: unmount aborts the pending long poll without retrying`, async t => {
    clock(t);
    const h = await mountJob(t, surface);
    assert.equal(h.polls.length, 2);
    const signal = h.polls[1].signal;
    assert.ok(signal, 'the poll must pass an AbortSignal to fetch');
    assert.equal(signal.aborted, false);
    await act(async () => h.view.unmount());
    assert.equal(signal.aborted, true);
    assert.equal(h.job.waiting, 0);
    await advance(t, 20_000);
    assert.equal(h.polls.length, 2);
  });

  test(`${surface}: a job already DONE returns once without a follow-up`, async t => {
    clock(t);
    const h = await mountJob(t, surface, { initial: { ...pending(), status: 'DONE', items: completedItems } });
    await advance(t, 20_000);
    assert.deepEqual(h.polls.map(poll => poll.since), [null]);
    assert.equal(h.job.waiting, 0);
  });
}

for (const surface of ['dashboard', 'transactions']) {
  test(`${surface}: switching to another job aborts the old poll and starts without its since`, async t => {
    clock(t);
    const h = await mountJob(t, surface);
    const previous = h.polls.at(-1).signal;
    const replacement = await h.switchPortfolio();
    assert.equal(previous.aborted, true);
    assert.equal(h.job.waiting, 0);
    assert.deepEqual(h.polls.filter(poll => poll.jobId === 'replacement').map(poll => poll.since), [null, '1']);
    await act(async () => replacement.finish(completedItems));
  });
}

for (const surface of ['dashboard', 'transactions', 'backup']) {
  test(`${surface}: Alpha Vantage countdown ticks while the long poll stays pending`, async t => {
    clock(t);
    const h = await mountJob(t, surface, { provider: 'ALPHAVANTAGE' });
    assert.equal(h.polls.length, 2);
    assert.match(screen.getByRole('status').textContent, /next request in 9 s/);
    await advance(t, 3000);
    assert.match(screen.getByRole('status').textContent, /next request in 6 s/);
    assert.equal(h.polls.length, 2, 'countdown must not need a poll response');
    await act(async () => h.job.finish(completedItems));
  });
}

test('an older backend without versions is throttled by the 1 second safety guard', async t => {
  clock(t);
  const legacy = { ...pending(), version: undefined };
  const h = await mountJob(t, 'dashboard', { reply: number => json(number === 1 ? legacy : { ...legacy, status: 'DONE', items: completedItems }) });
  assert.equal(h.polls.length, 1);
  await advance(t, 999);
  assert.equal(h.polls.length, 1);
  await advance(t, 1);
  assert.deepEqual(h.polls.map(poll => poll.since), [null, null]);
});

test('portfolio summaries stop after five consecutive failed long polls and reload stored figures', async t => {
  clock(t);
  const h = await mountJob(t, 'summaries', { reply: () => json({ error: { code: 'UPSTREAM_ERROR', message: 'Temporary outage' } }, 503) });
  for (let error = 1; error < 5; error++) await advance(t, 2000);
  assert.equal(h.polls.length, 5);
  assert.equal(h.summaryLoads, 2);
  assert.equal(h.view.result.current.updating, false);
  await advance(t, 20_000);
  assert.equal(h.polls.length, 5);
});
