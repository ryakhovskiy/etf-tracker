import assert from 'node:assert/strict';
import { test } from 'node:test';
import { setImmediate } from 'node:timers/promises';
import { mockAPI } from '../e2e/fixtures.mjs';
import { mockSummaryState } from '../e2e/portfolio-summary-fixtures.mjs';
import { priceJob } from './fixtures/price-job.mjs';

// Exercise the actual browser route handlers without a browser or bound port.
function fixturePage() {
  const routes = [];
  return {
    url: () => 'http://localhost/',
    async route(pattern, handler) { routes.push({ pattern, handler }); },
    request(path) {
      const url = new URL(path, 'http://localhost').href;
      const route = routes.toReversed().find(({ pattern }) => pattern instanceof RegExp ? pattern.test(url) : url.endsWith(pattern.replace('**', '')));
      assert.ok(route, `No fixture route for ${path}`);
      return route.handler({
        request: () => ({ url: () => url, method: () => 'GET' }),
        fulfill: async ({ json }) => json,
      });
    },
  };
}

test('summary browser fixture answers once, holds since, and wakes on fixture completion', async () => {
  const page = fixturePage();
  const fixture = await mockSummaryState(page, 'updating');
  try {
    const first = await page.request('/api/price-fetch-jobs/summary-job');
    assert.equal(first.status, 'RUNNING');
    assert.equal(first.version, 1);
    let settled = false;
    const waiting = page.request('/api/price-fetch-jobs/summary-job?since=1').then(job => { settled = true; return job; });
    await setImmediate();
    assert.equal(settled, false, 'the fixture must not make an unchanged response spin');
    fixture.finishJob();
    const done = await waiting;
    assert.equal(done.status, 'DONE');
    assert.equal(done.version, 2);
    assert.deepEqual(fixture.calls.since, [null, '1']);
    assert.equal((await page.request('/api/price-fetch-jobs/summary-job?since=2')).status, 'DONE');
    assert.deepEqual(fixture.unexpected, []);
  } finally { fixture.dispose(); }
});

test('general browser fixture holds each current version until a controlled job changes', async () => {
  const page = fixturePage();
  const job = priceJob({ status: 'RUNNING', items: [{ instrumentId: 1, status: 'PENDING' }] });
  const unexpected = await mockAPI(page, { priceJobs: new Map([['controlled', job]]) });
  try {
    assert.equal((await page.request('/api/price-fetch-jobs/controlled')).version, 1);
    let settled = false;
    const waiting = page.request('/api/price-fetch-jobs/controlled?since=1').then(value => { settled = true; return value; });
    await setImmediate();
    assert.equal(settled, false);
    job.publish({ items: [{ instrumentId: 1, status: 'OK' }] });
    assert.equal((await waiting).version, 2);
    assert.equal(job.waiting, 0);
    const finished = page.request('/api/price-fetch-jobs/controlled?since=2');
    assert.equal(job.waiting, 1);
    job.finish();
    assert.equal((await finished).status, 'DONE');
    assert.equal((await page.request('/api/price-fetch-jobs/controlled?since=3')).version, 3);
    assert.deepEqual(unexpected, []);
  } finally { job.dispose(); }
});
