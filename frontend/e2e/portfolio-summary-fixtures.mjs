import { mockAPI } from './fixtures.mjs';
import { portfolioSummary } from '../tests/fixtures/portfolio-summary.mjs';

// Page routes retain the same data contract while tests control *when* pending
// data/jobs complete. No sleeps, backend, provider or Google connection needed.
export async function mockSummaryState(page, initialState) {
  const unexpected = await mockAPI(page);
  let state = initialState === 'loading' ? 'fresh' : initialState;
  let complete = false;
  const loadGate = Promise.withResolvers();
  const jobGate = Promise.withResolvers();
  const calls = { summary: 0, posts: [], polls: 0 };
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
  await page.route('**/api/price-fetch-jobs/summary-job', async route => {
    calls.polls++;
    await jobGate.promise;
    return route.fulfill({ json: { status: 'DONE', kind: 'USER_REALTIME', items: [{ instrumentId: 1, status: 'OK' }, { instrumentId: 2, status: 'OK' }] } });
  });
  return {
    unexpected, calls,
    releaseLoading() { loadGate.resolve(); },
    finishJob() { complete = true; jobGate.resolve(); },
    expireCooldown() { state = 'auto'; },
    dispose() { loadGate.resolve(); jobGate.resolve(); },
  };
}
