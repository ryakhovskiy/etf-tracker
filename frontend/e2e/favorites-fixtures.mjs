import { mockAPI, performance } from './fixtures.mjs';
import { favoritesAPI, favoriteInstruments } from '../tests/fixtures/favorites.mjs';
import { portfolioSummary } from '../tests/fixtures/portfolio-summary.mjs';
import { priceJob } from '../tests/fixtures/price-job.mjs';

export async function mockFavorites(page, { ids = [1, 2], refresh = false, largeValues = false } = {}) {
  const unexpected = await mockAPI(page);
  const api = favoritesAPI(ids);
  const job = priceJob({ kind: 'USER_REALTIME', status: 'RUNNING', items: [{ instrumentId: 1, status: 'PENDING' }, { instrumentId: 2, status: 'PENDING' }] });
  let refreshed = false;
  await page.route(/\/api\/favorites(?:\/|\?|$)/, async route => {
    const request = route.request();
    const result = api.handle(new URL(request.url()).pathname, request.method(), request.postData() ? request.postDataJSON() : undefined);
    return route.fulfill({ status: result.status, json: result.body });
  });
  await page.route('**/api/portfolios/1/items', route => route.fulfill({ json: favoriteInstruments.slice(0, 2).map(item => ({
    instrumentId: item.instrumentId, ticker: item.ticker, name: item.name, isin: item.instrumentId === 1 ? 'IE00B4X9L533' : 'US0378331005',
    type: item.instrumentId === 1 ? 'ETF' : 'STOCK', payDelayDays: 30, favorite: api.ids.includes(item.instrumentId),
    latestQuote: { priceEur: item.priceEur, quoteDate: '2026-11-13', provider: 'YAHOO' },
  })) }));
  await page.route('**/api/portfolios/summaries', route => {
    const result = portfolioSummary(refreshed ? 'updated' : 'fresh');
    if (largeValues) for (const value of [...result.summaries, result.total]) {
      value.currentValue = '1234567890.12';
      value.invested = '123456789.12';
      value.performance = { amount: '1111111101.00', percent: '900.00' };
      value.change = { amount: '-1234567.89', percent: '-12.34' };
    }
    return route.fulfill({ json: result });
  });
  if (largeValues) await page.route(/\/api\/portfolios\/1\/performance(?:\?|$)/, route => {
    const result = structuredClone(performance);
    // Reproduce the amounts from debug/current.jpg: these used to run out of
    // the Current performance tile at phone widths.
    result.invested = '36553.92';
    result.atPurchase = { value: '45446.26', performance: '8892.34', performancePct: '24.33' };
    result.current = { ...result.current, value: '44026.43', performance: '7472.51', performancePct: '20.44' };
    return route.fulfill({ json: result });
  });
  if (refresh) {
    await page.route('**/api/price-refresh-jobs', route => route.fulfill({ status: 202, json: { started: true, reused: false, jobId: 'favorites-refresh', requests: 2 } }));
    await page.route(/\/api\/price-fetch-jobs\/favorites-refresh(?:\?|$)/, async route => route.fulfill({ json: await job.read(new URL(route.request().url()).searchParams.get('since')) }));
  }
  return {
    api, unexpected,
    finishRefresh() {
      refreshed = true;
      api.update(1, { priceEur: '44.0400', change: { amount: '1.3600', percent: '3.19' }, asOf: { ...favoriteInstruments[0].asOf, quoteTime: '2026-11-13T12:00:00Z', fetchedAt: '2026-11-13T12:00:00Z' } });
      job.finish();
    },
    dispose() { job.dispose(); },
  };
}
