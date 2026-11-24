import { mockAPI } from './fixtures.mjs';
import { portfolioSummary } from '../tests/fixtures/portfolio-summary.mjs';
import { priceJob } from '../tests/fixtures/price-job.mjs';

// Page routes retain the same data contract while tests control *when* pending
// data/jobs complete. No sleeps, backend, provider or Google connection needed.
export async function mockSummaryState(page, initialState) {
  const unexpected = await mockAPI(page);
  let state = initialState === 'loading' ? 'fresh' : initialState;
  let complete = false;
  const loadGate = Promise.withResolvers();
  const job = priceJob({ status: 'RUNNING', kind: 'USER_REALTIME', items: [{ instrumentId: 1, status: 'PENDING' }, { instrumentId: 2, status: 'PENDING' }] });
  const calls = { summary: 0, posts: [], polls: 0, since: [] };
  if (initialState !== 'loading') loadGate.resolve();
  await page.route('**/api/portfolios/summaries', async route => {
    calls.summary++;
    await loadGate.promise;
    return route.fulfill({ json: portfolioSummary(complete ? 'updated' : state) });
  });
  await page.route('**/api/price-refresh-jobs', async route => {
    calls.posts.push(route.request().postDataJSON());
    state = 'updating';
    return route.fulfill({ status: 202, json: { started: true, reused: false, jobId: 'summary-job', requests: 3 } });
  });
  await page.route(/\/api\/price-fetch-jobs\/summary-job(?:\?|$)/, async route => {
    calls.polls++;
    const since = new URL(route.request().url()).searchParams.get('since');
    calls.since.push(since);
    return route.fulfill({ json: await job.read(since) });
  });
  return {
    unexpected, calls,
    releaseLoading() { loadGate.resolve(); },
    finishJob() { complete = true; job.finish([{ instrumentId: 1, status: 'OK' }, { instrumentId: 2, status: 'OK' }]); },
    expireCooldown() { state = 'auto'; },
    dispose() { loadGate.resolve(); job.dispose(); },
  };
}
