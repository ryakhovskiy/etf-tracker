// Deliberately realistic populated responses: long instrument names, an ETF,
// positive and negative performance, foreign currency dividends, CSV edits.
// Unhandled API/auth requests fail the test instead of reaching a real backend.
import { portfolioSummary, summaryPortfolios } from '../tests/fixtures/portfolio-summary.mjs';

export const themes = ['light', 'dark', 'tech-green', 'tech-cappuccino', 'tech-red'];
export const portfolio = summaryPortfolios[0];

const instruments = [
  { id: 1, ticker: 'HMWO.L', name: 'HSBC MSCI World UCITS ETF USD (Distributing)', type: 'ETF', isin: 'IE00B4X9L533', wkn: 'A1C9KK', inPortfolio: true },
  { id: 2, ticker: 'AAPL', name: 'Apple Inc.', type: 'STOCK', isin: 'US0378331005', wkn: '865985', inPortfolio: true },
];
const performance = {
  dates: ['2026-09-01', '2026-10-01', '2026-11-02'],
  series: [
    { instrumentId: 1, name: instruments[0].name, values: ['100.00', '105.00', '110.00'] },
    { instrumentId: 2, name: instruments[1].name, values: ['100.00', '95.00', '90.00'] },
  ],
  totals: ['200.00', '200.00', '200.00'], invested: '200.00',
  atPurchase: { value: '200.00', performance: '0.00', performancePct: '0.00' },
  current: {
    value: '210.00', performance: '10.00', performancePct: '5.00',
    asOfFrom: '2026-11-12', asOfTo: '2026-11-12', lastFetchedAt: '2026-11-12T15:00:00Z', partial: false, chartPoint: true,
    items: [
      { instrumentId: 1, units: '2', priceEur: '60.00', value: '120.00', source: 'QUOTE', quoteDate: '2026-11-12', fxRate: '1.15000000', fxRateDate: '2026-11-12' },
      { instrumentId: 2, units: '1', priceEur: '90.00', value: '90.00', source: 'QUOTE', quoteDate: '2026-11-12', fxRate: '0.93000000', fxRateDate: '2026-11-12' },
    ],
  },
};
function selectedPerformance(url) {
  if (!url.searchParams.has('instrumentIds')) return performance;
  const ids = new Set(url.searchParams.get('instrumentIds').split(',').filter(Boolean).map(Number));
  const series = performance.series.filter(item => ids.has(item.instrumentId));
  if (series.length === performance.series.length) return performance;
  if (!series.length) return { ...performance, dates: [], series: [], totals: [], invested: '0.00', atPurchase: { value: '0.00', performance: '0.00', performancePct: null }, current: null };
  const positive = series[0].instrumentId === 1;
  return {
    ...performance, series, totals: series[0].values, invested: '100.00',
    atPurchase: { value: positive ? '110.00' : '90.00', performance: positive ? '10.00' : '-10.00', performancePct: positive ? '10.00' : '-10.00' },
    current: {
      ...performance.current, value: positive ? '120.00' : '90.00', performance: positive ? '20.00' : '-10.00', performancePct: positive ? '20.00' : '-10.00',
      items: performance.current.items.filter(item => ids.has(item.instrumentId)),
    },
  };
}
const transactions = {
  page: 1, total: 2,
  lines: instruments.map((item, index) => ({
    id: index + 1, transactionId: 1, instrumentId: item.id, date: '2026-09-01',
    ticker: item.ticker, name: item.name, amount: '100.00', price: index ? '100.0000' : '50.0000', quantity: index ? '1.000000' : '2.000000', source: 'MANUAL',
    performance: { currentPrice: index ? '90.00' : '60.00', quoteDate: '2026-11-12', change: index ? '-10.00' : '20.00', changePct: index ? '-10.00' : '20.00', priceChange: index ? '-10.00' : '10.00', priceChangePct: index ? '-10.00' : '20.00' },
  })),
  groups: { 1: { change: '10.00', changePct: '5.00', pricedLines: 2, totalLines: 2 } },
};
const settings = {
  encoding: 'auto', delimiter: ';', quoteChar: '"', headerRow: true, skipLines: 0,
  decimalSeparator: ',', thousandsSeparator: '.', dateFormat: 'DD.MM.YYYY', stripEuro: true,
  mapping: Object.fromEntries(Object.entries({ isin: 'ISIN', wkn: 'WKN', ticker: 'Symbol', name: 'Name', quantity: 'Amount', price: 'Open Price', date: 'Date', total: 'Total Invest' }).map(([field, header]) => [field, { header }])),
};
const importRow = {
  rowNumber: 2, include: true, status: 'OK', errors: {}, warnings: [], instrument: instruments[0],
  fields: { isin: instruments[0].isin, wkn: instruments[0].wkn, ticker: instruments[0].ticker, name: instruments[0].name, type: 'ETF', quantity: '2.000000', price: '50.0000', total: '100.00', date: '2026-11-02' },
};
function preview(rows = [importRow]) {
  return {
    headers: ['ISIN', 'Symbol', 'Name', 'Amount', 'Open Price', 'Total Invest', 'Date'], rows, revision: 'smoke-revision',
    summary: { rows: rows.length, included: rows.length, errors: 0, undecidedDuplicates: 0, newInstruments: 0, newItems: 0, transactions: 1, lines: rows.length, itemsAfter: 2, itemLimit: 30 },
  };
}
export const csvFile = 'ISIN;Symbol;Name;Amount;Open Price;Total Invest;Date\nIE00B4X9L533;HMWO.L;HSBC MSCI World UCITS ETF;2;50;100;02.11.2026\n';

function dividends(year) {
  const entry = {
    instrumentId: 1, ticker: 'HMWO.L', isin: instruments[0].isin, shortName: instruments[0].name,
    kind: 'ACTUAL', exDate: `${year}-10-15`, basisExDate: null, payDate: `${year}-11-16`,
    payDateShifted: true, payDelayDays: 30, paid: false, perUnitOrig: '0.4500', currency: 'USD',
    perUnitEur: '0.4185', fxRate: '0.93000000', fxRateDate: `${year}-10-15`,
    yieldPct: '0.75', close: '60.00', closeDate: `${year}-10-14`, units: '2.000000', total: '0.84', held: true, warning: null,
  };
  const entries = [entry, {
    ...entry, instrumentId: 2, ticker: 'AAPL', isin: instruments[1].isin, shortName: instruments[1].name,
    kind: 'FORECAST', exDate: `${year}-12-01`, basisExDate: `${year - 1}-12-01`, payDate: `${year}-12-31`, payDateShifted: false,
    perUnitOrig: '0.2500', perUnitEur: '0.2325', units: '1.000000', total: '0.23',
  }, {
    ...entry, instrumentId: 2, ticker: 'AAPL', isin: instruments[1].isin, shortName: instruments[1].name,
    exDate: `${year}-02-12`, payDate: `${year}-03-16`, paid: true, held: false, units: '0.000000', total: '0.00',
  }];
  return {
    year, bounds: { minYear: 2025, maxYear: 2027 }, totals: { received: '0.00', expected: '1.07', total: '1.07' },
    months: Array.from({ length: 12 }, (_, i) => ({
      month: i + 1, total: i === 10 ? '0.84' : i === 11 ? '0.23' : '0.00',
      entries: entries.filter(item => Number(item.payDate.slice(5, 7)) === i + 1),
      exTotal: i === 9 ? '0.84' : i === 11 ? '0.23' : '0.00',
      exEntries: entries.filter(item => Number(item.exDate.slice(5, 7)) === i + 1),
    })),
    sync: { provider: 'YAHOO', lastSyncedAt: '2026-11-13T12:00:00Z', stale: [], errors: [], jobId: null },
  };
}

export async function mockAPI(page, { loggedIn = true } = {}) {
  const unexpected = [];
  await page.route(/\/(?:api|auth)(?:\/|\?|$)/, async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const headers = { 'access-control-allow-origin': new URL(page.url() === 'about:blank' ? request.url() : page.url()).origin, 'access-control-allow-credentials': 'true', 'access-control-allow-headers': 'content-type', 'access-control-allow-methods': 'GET, POST, PUT, DELETE, OPTIONS' };
    const json = (body, status = 200) => route.fulfill({ status, headers, json: body });
    if (method === 'OPTIONS') return route.fulfill({ status: 204, headers });
    if (path === '/auth/me') return loggedIn ? json({ name: 'QA Investor', email: 'qa@example.invalid' }) : json({ error: { code: 'UNAUTHORIZED', message: 'Please sign in.' } }, 401);
    if (path === '/auth/refresh') return json({ error: { code: 'UNAUTHORIZED', message: 'Please sign in.' } }, 401);
    if (path === '/auth/logout') return json({ ok: true });
    if (method === 'GET') {
      if (path === '/api/portfolios') return json({ portfolios: summaryPortfolios, limit: 10 });
      if (path === '/api/portfolios/summaries') return json(portfolioSummary());
      if (path === '/api/portfolios/1') return json(portfolio);
      if (path === '/api/portfolios/1/performance') return json(selectedPerformance(url));
      if (path === '/api/portfolios/1/transactions') return json(transactions);
      if (path === '/api/portfolios/1/items') return json(instruments.map(item => ({ instrumentId: item.id, ticker: item.ticker, name: item.name, isin: item.isin, type: item.type, payDelayDays: 30, latestQuote: { priceEur: item.id === 1 ? '60.0000' : '90.0000', quoteDate: '2026-11-12', provider: 'YAHOO' } })));
      if (path === '/api/portfolios/1/saving-plan') return json(instruments.map(item => ({ instrumentId: item.id, ticker: item.ticker, name: item.name, isin: item.isin, type: item.type, yahooSymbol: item.ticker, defaultAmount: '100.00', defaultPrice: '50.0000', priceSource: 'Last purchase' })));
      if (path === '/api/portfolios/1/dividends') return json(dividends(Number(url.searchParams.get('year') ?? 2026)));
      if (path === '/api/users/me/csv-settings') return json({ settings, saved: false });
      if (path === '/api/market-data/budget') return json({ globalRemaining: 25, userRemaining: 10, userLimit: 10, nextSlotAt: '', primary: 'YAHOO' });
      if (path.endsWith('/price-fetch/estimate')) return json({ requestsNeeded: 2, fresh: [], noSymbol: [], budget: { globalRemaining: 25, userRemaining: 10, nextSlotAt: '' }, provider: 'YAHOO', yahoo: { requests: 2, remaining: 10, noSymbol: [] }, realtime: { requests: 2, remaining: 10, noSymbol: [] }, eulerpool: { requests: 2, remaining: 10, noIsin: [] } });
      if (path === '/api/instruments/1/etf-profile') return json({ instrumentId: 1, isin: instruments[0].isin, etfCompany: 'HSBC Asset Management', trackingIndex: 'MSCI World', assetClass: 'Equity', domicile: 'Ireland', expenseRatio: '0.15', aumEur: '12500000000', dividendYield: '1.75', inceptionDate: '2010-12-08', currency: 'USD', website: 'https://example.invalid/etf', fetchedAt: '2026-11-12T12:00:00Z', stale: false });
      if (path === '/api/instruments/search') return json([{ id: 3, ticker: 'SAP.DE', name: 'SAP SE', type: 'STOCK', isin: 'DE0007164600', wkn: '716460', inPortfolio: false }]);
    }
    if (method === 'POST' && path === '/api/price-refresh-jobs') return json({ started: false, reason: 'FRESH' });
    if (method === 'POST' && path === '/api/portfolios/1/imports/parse') return json(preview());
    if (method === 'POST' && path === '/api/portfolios/1/imports/validate') return json(preview(request.postDataJSON().rows.map(row => ({ ...importRow, ...row }))));
    unexpected.push(`${method} ${path}${url.search}`);
    return json({ error: { code: 'UNMOCKED_ROUTE', message: `Unmocked smoke fixture: ${method} ${path}` } }, 500);
  });
  return unexpected;
}
