import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { priceJob as dividendJob } from './fixtures/price-job.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'SVGElement', 'Node', 'MutationObserver', 'sessionStorage']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
const { act, cleanup, render, screen } = await import('@testing-library/react');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const { SearchParamsContext } = await import('next/dist/shared/lib/hooks-client-context.shared-runtime.js');
const { PortfolioUpdatesProvider } = await import('../components/portfolio-updates.tsx');
const { Dividends } = await import('../components/dividends.tsx');

afterEach(() => cleanup());
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const clock = t => t.mock.timers.enable({ apis: ['Date', 'setTimeout', 'setInterval'], now: Date.UTC(2026, 10, 25, 12) });
const advance = async (t, ms) => act(async () => t.mock.timers.tick(ms));
const pending = { version: 1, status: 'RUNNING', done: 0, total: 2, items: [{ instrumentId: 1, status: 'PENDING' }, { instrumentId: 2, status: 'PENDING' }] };
const doneItems = pending.items.map(item => ({ ...item, status: 'OK' }));
const tree = () => React.createElement(AppRouterContext.Provider, { value: { push() {} } },
  React.createElement(SearchParamsContext.Provider, { value: new URLSearchParams('year=2026') },
    React.createElement(PortfolioUpdatesProvider, { activeTab: 'dividends' }, React.createElement(Dividends, { id: '1' }))));

async function mountSync(t, { initial = pending, reply } = {}) {
  const job = dividendJob(initial);
  const polls = [], unexpected = [];
  let calendars = 0, posts = 0;
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const parsed = new URL(url, 'http://localhost');
    if (parsed.pathname === '/api/portfolios/1/dividends') {
      calendars++;
      return json({
        year: 2026, bounds: { minYear: 2025, maxYear: 2027 },
        totals: { received: '0.00', expected: '0.00', total: '0.00' },
        months: Array.from({ length: 12 }, (_, index) => ({ month: index + 1, total: '0.00', exTotal: '0.00', entries: [], exEntries: [] })),
        sync: { provider: 'YAHOO', lastSyncedAt: null, stale: [], errors: [], jobId: calendars === 1 ? 'dividend-job' : null },
      });
    }
    if (parsed.pathname === '/api/dividend-sync-jobs/dividend-job') {
      const request = { since: parsed.searchParams.get('since'), at: Date.now(), signal: init.signal };
      polls.push(request);
      return reply?.(polls.length, request, job) ?? json(await job.read(request.since, { signal: init.signal }));
    }
    if (parsed.pathname.endsWith('/dividend-sync')) posts++;
    unexpected.push(`${init.method ?? 'GET'} ${parsed.pathname}`);
    throw new Error(`Unexpected request ${parsed.pathname}`);
  });
  let view;
  await act(async () => { view = render(tree()); });
  t.after(() => job.dispose());
  return {
    job, polls, unexpected, view,
    finish: () => job.publish({ status: 'DONE', done: 2, items: doneItems }),
    get calendars() { return calendars; },
    get posts() { return posts; },
  };
}

test('dividend sync immediately follows with since and updates progress without a fixed delay', async t => {
  clock(t);
  const h = await mountSync(t);
  assert.deepEqual(h.polls.map(poll => poll.since), [null, '1']);
  assert.equal(h.polls[1].at, h.polls[0].at);
  assert.equal(h.job.waiting, 1);
  assert.equal(screen.getByText('0/2 instruments').textContent, '0/2 instruments');
  await advance(t, 20_000);
  assert.equal(h.polls.length, 2, 'a held request needs no repeated GET');
  await act(async () => h.job.publish({ done: 1, items: [{ instrumentId: 1, status: 'PENDING' }, { instrumentId: 2, status: 'OK' }] }));
  assert.equal(screen.getByText('1/2 instruments').textContent, '1/2 instruments');
  assert.deepEqual(h.polls.map(poll => poll.since), [null, '1', '2']);
  assert.equal(h.polls[2].at - h.polls[1].at, 20_000, 'the changed response is followed immediately');
  await act(async () => h.finish());
  assert.equal(h.calendars, 2, 'DONE reloads the calendar');
  assert.equal(h.posts, 0, 'a running job is joined');
  assert.equal(h.job.waiting, 0);
  await advance(t, 20_000);
  assert.equal(h.polls.length, 3, 'DONE stops polling');
  assert.deepEqual(h.unexpected, []);
});

test('dividend sync waits exactly 1 second after an unchanged version', async t => {
  clock(t);
  const h = await mountSync(t, { reply: (number, _, job) => number === 2 ? json(job.snapshot()) : undefined });
  assert.equal(h.polls.length, 2);
  await advance(t, 999);
  assert.equal(h.polls.length, 2);
  await advance(t, 1);
  assert.equal(h.polls.length, 3);
  assert.equal(h.polls[2].since, '1');
  assert.equal(h.polls[2].at - h.polls[1].at, 1000);
  await act(async () => h.finish());
});

test('dividend sync retries errors at 2 seconds, retaining the last version and showing its error', async t => {
  clock(t);
  const h = await mountSync(t, { reply: number => number === 2 ? json({ error: { code: 'UPSTREAM_ERROR', message: 'Dividend sync temporarily unavailable' } }, 503) : undefined });
  assert.match(screen.getByRole('alert').textContent, /Dividend sync temporarily unavailable/);
  await advance(t, 1999);
  assert.equal(h.polls.length, 2);
  await advance(t, 1);
  assert.equal(h.polls.length, 3);
  assert.equal(h.polls[2].since, '1');
  assert.equal(h.polls[2].at - h.polls[1].at, 2000);
  await act(async () => h.finish());
});

test('unmount aborts the held dividend request and removes its waiter without retrying', async t => {
  clock(t);
  const h = await mountSync(t);
  const signal = h.polls[1].signal;
  assert.ok(signal);
  assert.equal(signal.aborted, false);
  await act(async () => h.view.unmount());
  assert.equal(signal.aborted, true);
  assert.equal(h.job.waiting, 0);
  await advance(t, 20_000);
  assert.equal(h.polls.length, 2);
});

test('an already DONE dividend job returns once and reloads the calendar', async t => {
  clock(t);
  const h = await mountSync(t, { initial: { ...pending, status: 'DONE', done: 2, items: doneItems } });
  await advance(t, 20_000);
  assert.deepEqual(h.polls.map(poll => poll.since), [null]);
  assert.equal(h.calendars, 2);
  assert.equal(h.job.waiting, 0);
});

test('a dividend job 404 ends polling and reloads the calendar', async t => {
  clock(t);
  const h = await mountSync(t, { reply: number => number === 2 ? json({ error: { code: 'NOT_FOUND', message: 'Missing dividend job' } }, 404) : undefined });
  await advance(t, 20_000);
  assert.deepEqual(h.polls.map(poll => poll.since), [null, '1']);
  assert.equal(h.calendars, 2);
  assert.ok(screen.queryByRole('alert') === null);
});

test('a legacy dividend response without a version cannot create an immediate request loop', async t => {
  clock(t);
  const h = await mountSync(t, { reply: number => json(number === 1 ? { ...pending, version: undefined } : { ...pending, version: undefined, status: 'DONE', done: 2, items: doneItems }) });
  assert.equal(h.polls.length, 1);
  await advance(t, 999);
  assert.equal(h.polls.length, 1);
  await advance(t, 1);
  assert.deepEqual(h.polls.map(poll => poll.since), [null, null]);
  assert.equal(h.calendars, 2);
});

test('unmount during a dividend error retry cancels the scheduled next request', async t => {
  clock(t);
  const h = await mountSync(t, { reply: number => number === 2 ? json({ error: { code: 'UPSTREAM_ERROR', message: 'Temporary failure' } }, 503) : undefined });
  assert.equal(h.polls.length, 2);
  const signal = h.polls[1].signal;
  await act(async () => h.view.unmount());
  assert.equal(signal.aborted, true);
  await advance(t, 20_000);
  assert.equal(h.polls.length, 2);
});
