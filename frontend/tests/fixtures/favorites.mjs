// Decimal strings and a user-level ordered set, shared by component/browser
// fixtures. Mutations model the public API contract, never a component state.
const asOf = { quoteDate: '2026-11-13', quoteTime: '2026-11-13T11:58:00Z', fetchedAt: '2026-11-13T11:59:00Z', providers: ['YAHOO'], realtime: true };
export const favoriteInstruments = [
  { instrumentId: 1, name: 'HSBC MSCI World UCITS ETF USD (Distributing)', ticker: 'HMWO.L', priceEur: '42.0400', change: { amount: '-0.6400', percent: '-1.50' }, asOf, reasons: [] },
  { instrumentId: 2, name: 'Apple Inc.', ticker: 'AAPL', priceEur: '190.1250', change: { amount: '1.2350', percent: '0.65' }, asOf, reasons: [] },
  { instrumentId: 3, name: 'An exceptionally long instrument name without a stored market quote', ticker: 'EMPTY', priceEur: null, change: null, asOf: null, reasons: ['NO_QUOTE'] },
  { instrumentId: 4, name: 'Unchanged EUR stock', ticker: 'FLAT', priceEur: '100.0000', change: { amount: '0.0000', percent: '0.00' }, asOf, reasons: [] },
];

export function favoritesResponse(ids = [1, 2, 3, 4]) {
  return { favorites: ids.map((id, position) => ({ ...structuredClone(favoriteInstruments.find(item => item.instrumentId === id)), position })) };
}

export function favoritesAPI(initialIds = [1, 2]) {
  let ids = [...initialIds];
  const overrides = new Map();
  const calls = [];
  const response = () => ({ favorites: favoritesResponse(ids).favorites.map(item => ({ ...item, ...overrides.get(item.instrumentId) })) });
  return {
    calls, response,
    get ids() { return [...ids]; },
    replace(nextIds) { ids = [...nextIds]; },
    update(id, fields) { overrides.set(id, structuredClone(fields)); },
    handle(path, method = 'GET', body) {
      if (!/^\/api\/favorites(?:\/|$)/.test(path)) return null;
      calls.push({ path, method, ...(body === undefined ? {} : { body: structuredClone(body) }) });
      if (path === '/api/favorites' && method === 'GET') return { status: 200, body: response() };
      if (path === '/api/favorites/order' && method === 'PUT') {
        const next = body?.instrumentIds;
        if (!Array.isArray(next) || next.length !== ids.length || new Set(next).size !== ids.length || !next.every(id => ids.includes(id))) {
          return { status: 409, body: { error: { code: 'FAVORITES_STALE', message: 'Favorites changed. Reload and try again.' } } };
        }
        ids = [...next];
        return { status: 200, body: response() };
      }
      const match = /^\/api\/favorites\/(\d+)$/.exec(path);
      if (match && ['PUT', 'DELETE'].includes(method)) {
        const id = Number(match[1]);
        if (!favoriteInstruments.some(item => item.instrumentId === id)) return { status: 404, body: { error: { code: 'NOT_FOUND', message: 'Instrument not found.' } } };
        if (method === 'PUT' && !ids.includes(id)) ids.push(id);
        if (method === 'DELETE') ids = ids.filter(value => value !== id);
        return method === 'DELETE' ? { status: 204 } : { status: 200, body: { instrumentId: id, position: ids.indexOf(id) } };
      }
      throw new Error(`Unexpected favorites fixture request: ${method} ${path}`);
    },
  };
}
