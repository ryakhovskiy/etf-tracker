// Contract-v1 fixtures shared by node component tests and the offline browser
// harness. Every financial value is an API decimal string, never a JS number.
export const summaryPortfolios = [
  { id: 1, name: 'Long-term growth & income', itemCount: 2, createdAt: '2026-01-01T12:00:00Z', instrumentIds: [1, 2], itemLimit: 30 },
  { id: 2, name: 'Income', itemCount: 1, createdAt: '2026-02-01T12:00:00Z' },
  { id: 3, name: 'Steady', itemCount: 1, createdAt: '2026-03-01T12:00:00Z' },
];

const asOf = { quoteDate: '2026-11-13', quoteTime: '2026-11-13T11:58:00Z', fetchedAt: '2026-11-13T11:59:00Z', providers: ['YAHOO'], realtime: true };
const base = { partial: false, changePartial: false, reasons: [], asOf };
const initial = {
  summaries: [
    { ...base, portfolioId: 1, positionCount: 2, invested: '1000.00', currentValue: '1200.00', performance: { amount: '200.00', percent: '20.00' }, change: { amount: '20.00', percent: '1.69' } },
    { ...base, portfolioId: 2, positionCount: 1, invested: '500.00', currentValue: '450.00', performance: { amount: '-50.00', percent: '-10.00' }, change: { amount: '-10.00', percent: '-2.17' } },
    { ...base, portfolioId: 3, positionCount: 1, invested: '100.00', currentValue: '100.00', performance: { amount: '0.00', percent: '0.00' }, change: { amount: '0.00', percent: '0.00' } },
  ],
  total: { ...base, portfolioCount: 3, positionCount: 4, invested: '1600.00', currentValue: '1750.00', performance: { amount: '150.00', percent: '9.38' }, change: { amount: '10.00', percent: '0.57' } },
  refresh: { stale: false, newestFetchedAt: '2026-11-13T11:59:00Z', maxAgeSeconds: 900, autoFetch: false, runningJobId: null, lastAttemptAt: null, yahoo: { available: true, blockedUntil: null }, estimate: { requests: 3, remaining: 950, noSymbol: [] } },
};

export function portfolioSummary(state = 'fresh') {
  const response = structuredClone(initial);
  if (state === 'partial') {
    response.summaries[1] = { ...response.summaries[1], currentValue: null, performance: null, change: null, partial: true, changePartial: true, reasons: ['NO_QUOTE'], asOf: null };
    response.total = { ...response.total, currentValue: '1800.00', performance: { amount: '200.00', percent: '12.50' }, change: { amount: '20.00', percent: '1.56' }, partial: true, changePartial: true, reasons: ['NO_QUOTE'] };
  }
  if (['auto', 'updating', 'cooldown'].includes(state)) {
    response.refresh.stale = true;
    response.refresh.newestFetchedAt = '2026-11-13T10:00:00Z';
    for (const summary of [...response.summaries, response.total]) summary.asOf = { ...summary.asOf, quoteTime: '2026-11-13T09:59:00Z', fetchedAt: '2026-11-13T10:00:00Z' };
  }
  if (state === 'auto') response.refresh.autoFetch = true;
  if (state === 'updating') {
    response.refresh.runningJobId = 'summary-job';
    response.refresh.lastAttemptAt = '2026-11-13T12:00:00Z';
  }
  if (state === 'cooldown') response.refresh.yahoo = { available: false, blockedUntil: '2026-11-13T12:15:00Z' };
  if (state === 'updated') {
    response.summaries[0] = { ...response.summaries[0], currentValue: '1220.00', performance: { amount: '220.00', percent: '22.00' }, change: { amount: '40.00', percent: '3.39' } };
    response.summaries[1] = { ...response.summaries[1], currentValue: '460.00', performance: { amount: '-40.00', percent: '-8.00' }, change: { amount: '0.00', percent: '0.00' } };
    response.total = { ...response.total, currentValue: '1780.00', performance: { amount: '180.00', percent: '11.25' }, change: { amount: '40.00', percent: '2.30' } };
    response.refresh.newestFetchedAt = '2026-11-13T12:00:00Z';
    for (const summary of [...response.summaries, response.total]) summary.asOf = { ...summary.asOf, quoteTime: '2026-11-13T12:00:00Z', fetchedAt: '2026-11-13T12:00:00Z' };
  }
  return response;
}
