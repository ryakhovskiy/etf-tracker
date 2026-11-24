import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { portfolioSummary } from './fixtures/portfolio-summary.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver', 'sessionStorage']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
const { act, cleanup, fireEvent, render, renderHook, screen } = await import('@testing-library/react');
const { CurrentValueFetch } = await import('../components/current-value-fetch.tsx');
const { PriceFetch } = await import('../components/price-fetch.tsx');
const { usePortfolioSummaries } = await import('../components/use-portfolio-summaries.ts');
const { today } = await import('../lib/transactions.ts');

afterEach(() => { cleanup(); sessionStorage.clear(); });
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const clock = t => t.mock.timers.enable({ apis: ['Date', 'setTimeout', 'setInterval'], now: Date.UTC(2026, 10, 24, 12) });
const advance = async (t, ms) => act(async () => t.mock.timers.tick(ms));
const pending = { status: 'RUNNING', nextRequestAt: '2026-11-24T12:00:09Z', items: [{ instrumentId: 1, status: 'PENDING' }] };
const done = { ...pending, status: 'DONE', items: [{ instrumentId: 1, status: 'OK' }] };

// The second poll fails once. Check both boundaries so a slower success poll or
// a faster error retry fails without relying on wall-clock scheduling.
async function checkCadence(t, polls, interval) {
  assert.equal(polls.length, 1, 'the first poll starts immediately');
  await advance(t, interval - 1);
  assert.equal(polls.length, 1, 'success must not poll early');
  await advance(t, 1);
  assert.equal(polls.length, 2, 'success polls at the provider interval');
  await advance(t, 1999);
  assert.equal(polls.length, 2, 'errors keep the 2-second backoff');
  await advance(t, 1);
  assert.equal(polls.length, 3, 'the failed request is retried at 2 seconds');
  await advance(t, interval - 1);
  assert.equal(polls.length, 3, 'successful recovery must not poll early');
  await advance(t, 1);
  assert.equal(polls.length, 4, 'recovery restores the provider interval');
  await advance(t, 10_000);
  assert.equal(polls.length, 4, 'completion stops polling');
  assert.deepEqual(polls.map(time => time - polls[0]), [0, interval, interval + 2000, interval * 2 + 2000]);
}

function jobReply(polls) {
  polls.push(Date.now());
  if (polls.length === 2) return json({ error: { code: 'UPSTREAM_ERROR', message: 'Temporary outage' } }, 503);
  return json(polls.length >= 4 ? done : pending);
}

for (const surface of ['dashboard', 'transactions']) {
  for (const provider of ['YAHOO', 'ALPHAVANTAGE', 'EULERPOOL']) {
    for (const kind of ['regular', 'realtime']) {
      test(`${surface}: ${provider} primary ${kind} polls at ${kind === 'realtime' || provider === 'YAHOO' ? 500 : 2000} ms, with 2 s error retry`, async t => {
        clock(t);
        const polls = [], submitted = [], unexpected = [];
        let reloads = 0;
        const budget = { primary: provider, globalRemaining: 25, userRemaining: 10, userLimit: 10, nextSlotAt: new Date().toISOString() };
        const estimate = {
          provider, requestsNeeded: 1, fresh: [], noSymbol: [], budget,
          yahoo: { requests: 1, remaining: 25, noSymbol: [] },
          eulerpool: { requests: 1, remaining: 25, noIsin: [] },
          realtime: { requests: 1, remaining: 25, noSymbol: [] },
        };
        t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
          const path = new URL(url, 'http://localhost').pathname;
          if (path === '/api/market-data/budget') return json(budget);
          if (path.endsWith('/price-fetch/estimate')) return json(estimate);
          if (path === '/api/portfolios/1/price-fetch-jobs' && init.method === 'POST') {
            submitted.push(JSON.parse(init.body));
            return json({ jobId: 'poll-job' }, 202);
          }
          if (path === '/api/price-fetch-jobs/poll-job') return jobReply(polls);
          unexpected.push(path);
          throw new Error(`Unexpected request ${path}`);
        });
        const reload = async () => { reloads++; };
        await act(async () => {
          if (surface === 'dashboard') render(React.createElement(CurrentValueFetch, {
            id: '1', reload, performance: { series: [{ instrumentId: 1 }], current: null },
          }));
          else render(React.createElement(PriceFetch, {
            id: '1', date: today(), rows: [{ instrumentId: 1, ticker: 'SAP', name: 'SAP SE', avSymbol: 'SAP.DEX', yahooSymbol: 'SAP.DE' }],
            selected: [1], setRows() {}, reload, busy: false, onFetching() {},
          }));
        });
        const name = kind === 'realtime' ? /^Fetch real-time/ : surface === 'dashboard' ? /^Fetch current value/ : /^Fetch prices/;
        const button = screen.getByRole('button', { name });
        assert.equal(button.disabled, false);
        await act(async () => fireEvent.click(button));
        assert.equal(submitted.length, 1);
        assert.equal(submitted[0].realtime === true, kind === 'realtime');
        await checkCadence(t, polls, kind === 'realtime' || provider === 'YAHOO' ? 500 : 2000);
        assert.equal(sessionStorage.getItem(`${surface === 'dashboard' ? 'current-value-fetch' : 'price-fetch'}:1`), null);
        assert.equal(reloads, surface === 'dashboard' ? 1 : 0);
        assert.deepEqual(unexpected, []);
      });
    }
  }
}

test('portfolio summaries: Yahoo polls at 500 ms, errors retry at 2 s, and completion reloads stored figures', async t => {
  clock(t);
  const polls = [], unexpected = [];
  let summaries = 0, starts = 0;
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const path = new URL(url, 'http://localhost').pathname;
    if (path === '/api/portfolios/summaries') return json(portfolioSummary(++summaries === 1 ? 'auto' : 'updated'));
    if (path === '/api/price-refresh-jobs' && init.method === 'POST') {
      starts++;
      return json({ started: true, reused: false, jobId: 'poll-job', requests: 1 });
    }
    if (path === '/api/price-fetch-jobs/poll-job') return jobReply(polls);
    unexpected.push(path);
    throw new Error(`Unexpected request ${path}`);
  });
  let hook;
  await act(async () => { hook = renderHook(() => usePortfolioSummaries(true)); });
  await checkCadence(t, polls, 500);
  assert.equal(starts, 1);
  assert.equal(summaries, 2);
  assert.equal(hook.result.current.updating, false);
  assert.equal(hook.result.current.data.total.currentValue, '1780.00');
  assert.deepEqual(unexpected, []);
});
