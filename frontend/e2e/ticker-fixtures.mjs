import { mockFavorites } from './favorites-fixtures.mjs';
import { tickerChart } from '../tests/fixtures/ticker.mjs';

export async function mockTicker(page, { chart = tickerChart(), status = 200, errorCode = 'NOT_FOUND', deferred = false } = {}) {
  const favorites = await mockFavorites(page);
  const calls = [];
  const pending = new Set();
  await page.route(/\/api\/instruments\/\d+\/chart(?:\?|$)/, async route => {
    calls.push({ path: new URL(route.request().url()).pathname, method: route.request().method() });
    if (deferred) await new Promise(resolve => pending.add(resolve));
    return route.fulfill({ status, json: status === 404
      ? { error: { code: errorCode, message: errorCode === 'NO_PRICE_HISTORY' ? 'There is no price history for this instrument.' : 'Instrument not found.' } }
      : { ...chart, ...(typeof chart.favorite === 'boolean' ? { favorite: favorites.api.ids.includes(chart.instrument.id) } : {}) } });
  });
  return {
    ...favorites, chart, calls,
    release() { deferred = false; for (const resolve of pending) resolve(); pending.clear(); },
    dispose() { this.release(); favorites.dispose(); },
  };
}
