import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';

const dom = new JSDOM('<!doctype html><html lang="en"><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver', 'HTMLDialogElement', 'sessionStorage', 'Element']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
HTMLDialogElement.prototype.showModal = function () { this.open = true; };
HTMLDialogElement.prototype.close = function () { this.open = false; };
const { render, screen, fireEvent, waitFor, cleanup, act } = await import('@testing-library/react');
const { InstrumentPicker } = await import('../components/instrument-picker.tsx');
const { Transactions } = await import('../components/transactions.tsx');
const { TransactionDraftProvider } = await import('../components/transaction-draft.tsx');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const { formatAum, formatPercent, formatDate, highlightParts } = await import('../lib/etf.ts');
const { today } = await import('../lib/transactions.ts');
const axe = (await import('axe-core')).default;
afterEach(() => { cleanup(); sessionStorage.clear(); });

const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const initial = { id: 1, name: 'Test', itemCount: 0, instrumentIds: [], itemLimit: 30, createdAt: '' };
const hsbcLocal = { id: 17, ticker: 'IE00B4X9L533', name: 'HSBC MSCI World UCITS ETF USD', type: 'ETF', isin: 'IE00B4X9L533', wkn: 'A1C9KK', inPortfolio: false };
const apple = { id: 1, ticker: 'AAPL', name: 'Apple Inc.', type: 'STOCK', isin: 'US0378331005', wkn: '865985', inPortfolio: false };
const euler = (rows, extra = {}) => ({ provider: 'EULERPOOL', results: rows.map(r => ({ source: 'EULERPOOL', inPortfolio: false, ...r })), ...extra });
const online = [
  { isin: 'IE000UQND7H4', ticker: 'HMWA.L', name: 'HSBC MSCI WORLD UCITS ETF', type: 'ETF', currency: 'USD' },
  { isin: 'DE0007164600', ticker: 'SAP.DE', name: 'SAP SE', type: 'STOCK', currency: 'EUR', existingInstrumentId: 7, inPortfolio: true },
];
const type = value => fireEvent.change(screen.getByRole('combobox'), { target: { value } });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
function mockFetch(t, handler) {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, init) => { calls.push({ url, init }); return handler(url, init) ?? json([]); });
  return calls;
}
const mountPicker = (props = {}) => render(React.createElement(InstrumentPicker, { initial: { ...initial, ...props } }));
const isOnline = url => url.includes('/search/online');

test('formatters follow the German display conventions', () => {
  assert.equal(formatAum('121400000000'), '121,4 Mrd. €');
  assert.equal(formatAum('950000000'), '950,0 Mio. €');
  assert.equal(formatAum(''), '—');
  assert.equal(formatPercent('0.2'), '0,20 %');
  assert.equal(formatDate('2010-12-08'), '08.12.2010');
  assert.deepEqual(highlightParts('HSBC MSCI World', 'msci'), [{ text: 'HSBC ', match: false }, { text: 'MSCI', match: true }, { text: ' World', match: false }]);
  assert.deepEqual(highlightParts('Apple', ''), [{ text: 'Apple', match: false }]);
});

test('Search button sits right of the input, is disabled below 4 characters, and shows a spinner without changing its label', async t => {
  let release;
  mockFetch(t, url => isOnline(url) ? new Promise(resolve => { release = () => resolve(json(euler(online))); }) : json([]));
  mountPicker();
  const input = screen.getByRole('combobox');
  const button = screen.getByRole('button', { name: 'Search' });
  assert.ok(button.parentElement.classList.contains('search-bar'));
  assert.ok(button.previousElementSibling.contains(input), 'button follows the input in the same row');
  assert.equal(input.getAttribute('placeholder'), 'Name, ticker, ISIN or WKN');
  assert.equal(button.disabled, true);
  type('hsb'); assert.equal(button.disabled, true);
  type('hsbc'); assert.equal(button.disabled, false);
  assert.ok(button.querySelector('.icon') !== null && button.querySelector('.spin') === null);
  fireEvent.click(button);
  await waitFor(() => assert.equal(button.getAttribute('aria-busy'), 'true'));
  assert.ok(button.querySelector('.spin') !== null);
  assert.ok(button.textContent.includes('Search'), 'label stays while searching');
  await act(async () => release());
  await waitFor(() => assert.equal(button.getAttribute('aria-busy'), 'false'));
  assert.ok(button.querySelector('.spin') === null);
});

test('Enter runs the online search, Enter on a highlighted row selects it, and the clear button resets', async t => {
  const calls = mockFetch(t, url => isOnline(url) ? json(euler(online)) : url.includes('/instruments/search') ? json([hsbcLocal]) : url.endsWith('/api/instruments') ? json({ ...apple, id: 99 }) : json([]));
  mountPicker();
  const input = screen.getByRole('combobox');
  assert.ok(screen.queryByRole('button', { name: 'Clear search' }) === null);
  type('hsbc');
  await screen.findByRole('option', { name: /IE00B4X9L533/ });
  assert.equal(calls.filter(c => isOnline(c.url)).length, 0, 'typing alone never searches online');
  fireEvent.keyDown(input, { key: 'Enter' });
  await screen.findByRole('option', { name: /HMWA\.L/ });
  assert.equal(calls.filter(c => isOnline(c.url)).length, 1);
  assert.ok(calls.find(c => isOnline(c.url)).url.includes('q=hsbc'));
  fireEvent.click(screen.getByRole('button', { name: 'Clear search' }));
  assert.equal(input.value, '');
  assert.ok(screen.queryByRole('listbox') === null);
  assert.ok(document.activeElement === input);
});

test('dropdown groups local and online results with pills, highlight, currency chip, info icon and attribution', async t => {
  mockFetch(t, url => isOnline(url) ? json(euler(online)) : url.includes('/instruments/search') ? json([hsbcLocal]) : json([]));
  mountPicker();
  type('hsbc');
  await screen.findByRole('option', { name: /IE00B4X9L533/ });
  assert.ok(screen.queryByText('Online · Eulerpool') === null, 'online section is hidden until Search');
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText('Online · Eulerpool');
  assert.ok(screen.getByText('In your database') !== null);
  const groups = screen.getAllByRole('group');
  assert.deepEqual(groups.map(g => g.getAttribute('aria-labelledby') !== null), [true, true]);
  const local = screen.getByRole('option', { name: /IE00B4X9L533/ });
  assert.ok(local.textContent.includes('ETF'));
  assert.ok(local.querySelector('mark') !== null && local.querySelector('mark').textContent.toLowerCase() === 'hsbc');
  assert.ok(/IE00B4X9L533 · A1C9KK/.test(local.querySelector('.option-meta').textContent));
  const row = screen.getByRole('option', { name: /HMWA\.L/ });
  assert.ok(row.querySelector('.chip').textContent === 'USD');
  assert.ok(row.querySelector('.pill-etf') !== null);
  // The in-portfolio stock is disabled and says why.
  const sap = screen.getByRole('option', { name: /SAP\.DE/ });
  assert.equal(sap.getAttribute('aria-disabled'), 'true');
  assert.ok(sap.textContent.includes('✓ In portfolio'));
  assert.ok(sap.querySelector('.pill-stock').textContent === 'Stock');
  // ⓘ only for ETFs that exist in the database (they have a profile endpoint).
  assert.ok(local.querySelector('.info-button') !== null);
  assert.ok(row.querySelector('.info-button') === null);
  assert.ok(sap.querySelector('.info-button') === null);
  assert.ok(screen.getByText('Data by Eulerpool') !== null);
});

test('disabled rows cannot be clicked, keyboard navigation skips them, and multiple picks keep the dropdown open', async t => {
  let posts = 0;
  const calls = mockFetch(t, (url, init) => isOnline(url) ? json(euler(online)) : url.includes('/instruments/search') ? json([hsbcLocal]) : init?.method === 'POST' && url.endsWith('/api/instruments') ? json({ ...hsbcLocal, wkn: null, id: 50 + posts++, isin: JSON.parse(init.body).isin, name: JSON.parse(init.body).name }) : json([]));
  mountPicker();
  type('hsbc'); await screen.findByRole('option', { name: /IE00B4X9L533/ });
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByRole('option', { name: /HMWA\.L/ });
  fireEvent.click(screen.getByRole('option', { name: /SAP\.DE/ }));
  assert.equal(calls.filter(c => c.init?.method === 'POST').length, 0);
  const input = screen.getByRole('combobox');
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  assert.equal(input.getAttribute('aria-activedescendant'), 'instrument-17');
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  assert.ok(input.getAttribute('aria-activedescendant').startsWith('instrument-online--'), 'skips the disabled SAP row');
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  assert.equal(input.getAttribute('aria-activedescendant'), 'instrument-17', 'wraps around');
  fireEvent.keyDown(input, { key: 'Enter' });
  await screen.findByTestId('selected-instrument');
  fireEvent.click(screen.getByRole('option', { name: /HMWA\.L/ }));
  await waitFor(() => assert.equal(screen.getAllByTestId('selected-instrument').length, 2));
  assert.ok(screen.getByRole('listbox') !== null, 'dropdown stays open');
  const post = calls.find(c => c.init?.method === 'POST' && c.url.endsWith('/api/instruments'));
  assert.equal(JSON.parse(post.init.body).source, 'EULERPOOL');
});

test('loading skeleton, empty state with the Alpha Vantage offer, error with retry and warning banner', async t => {
  let mode = 'pending', release;
  const calls = mockFetch(t, url => {
    if (!isOnline(url)) return json([]);
    if (url.includes('provider=av')) return json({ results: [] });
    if (mode === 'pending') return new Promise(resolve => { release = () => resolve(json({ provider: 'OPENFIGI', results: [] })); });
    if (mode === 'error') return json({ error: { code: 'UPSTREAM_ERROR', message: 'x' } }, 502);
    if (mode === 'warn') return json({ provider: 'OPENFIGI', warnings: ['EULERPOOL_UNAVAILABLE'], results: [{ figi: 'BBG000', ticker: 'HSBC', name: 'HSBC Holdings', type: 'STOCK', currency: 'EUR', exchCode: 'GR', isin: 'GB0005405286', inPortfolio: false }] });
    return json(euler(online));
  });
  mountPicker();
  type('zzzz'); await screen.findByText(/No results for/);
  assert.ok(screen.queryByRole('button', { name: 'Try Alpha Vantage (1 request)' }) === null, 'no AV offer before any online search');
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await waitFor(() => assert.equal(document.querySelectorAll('.skeleton-row').length, 3));
  assert.equal(document.querySelector('.skeleton').getAttribute('aria-hidden'), 'true');
  await act(async () => release());
  const av = await screen.findByRole('button', { name: 'Try Alpha Vantage (1 request)' });
  assert.ok(screen.getByText(/No results for/) !== null);
  assert.equal(calls.filter(c => c.url.includes('provider=av')).length, 0, 'AV is only offered, never called');
  fireEvent.click(av);
  await waitFor(() => assert.equal(calls.filter(c => c.url.includes('provider=av')).length, 1));
  // Error state with Retry.
  mode = 'error';
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  const alert = await screen.findByRole('alert');
  assert.ok(/unavailable/.test(alert.textContent));
  mode = 'ok';
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByRole('option', { name: /HMWA\.L/ });
  // Warning banner above fallback results.
  mode = 'warn';
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText('Eulerpool unavailable, showing OpenFIGI results');
  assert.ok(screen.getByText('Online · OpenFIGI') !== null);
  assert.ok(screen.queryByText('Data by Eulerpool') === null, 'attribution only with Eulerpool data');
  assert.ok(screen.queryByRole('button', { name: 'Try Alpha Vantage (1 request)' }) === null, 'AV is not offered while results exist');
});

test('the Alpha Vantage offer is withheld for ISIN and WKN queries and when Eulerpool answered', async t => {
  mockFetch(t, url => isOnline(url) ? json({ provider: 'OPENFIGI', results: [] }) : json([]));
  mountPicker();
  type('IE00B4X9L533'); fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText(/No results for/); await act(() => delay(20));
  assert.ok(screen.queryByRole('button', { name: 'Try Alpha Vantage (1 request)' }) === null);
  cleanup();
  mockFetch(t, url => isOnline(url) ? json({ provider: 'EULERPOOL', results: [] }) : json([]));
  mountPicker();
  type('nothing'); fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText(/No results for/); await act(() => delay(20));
  assert.ok(screen.queryByRole('button', { name: 'Try Alpha Vantage (1 request)' }) === null);
});

test('Escape and outside clicks close the dropdown and focus returns to the input', async t => {
  mockFetch(t, url => url.includes('/instruments/search') ? json([apple]) : json([]));
  mountPicker();
  const input = screen.getByRole('combobox');
  type('appl'); await screen.findByRole('option', { name: /AAPL/ });
  assert.equal(input.getAttribute('aria-expanded'), 'true');
  screen.getByRole('button', { name: 'Search' }).focus();
  fireEvent.keyDown(input, { key: 'Escape' });
  assert.ok(screen.queryByRole('listbox') === null);
  assert.ok(document.activeElement === input);
  assert.equal(input.getAttribute('aria-expanded'), 'false');
  fireEvent.focus(input); assert.ok(screen.getByRole('listbox') !== null);
  fireEvent.pointerDown(document.body); assert.ok(screen.queryByRole('listbox') === null);
});

test('combobox semantics: controls, activedescendant, groups, live announcement, and no axe violations', async t => {
  mockFetch(t, url => isOnline(url) ? json(euler(online)) : url.includes('/instruments/search') ? json([hsbcLocal]) : json([]));
  const { container } = mountPicker();
  const input = screen.getByRole('combobox');
  assert.equal(input.getAttribute('aria-autocomplete'), 'list');
  type('hsbc'); await screen.findByRole('option', { name: /IE00B4X9L533/ });
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByRole('option', { name: /HMWA\.L/ });
  const list = screen.getByRole('listbox');
  assert.equal(input.getAttribute('aria-controls'), list.id);
  assert.ok(list.id.length > 0);
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  const active = document.getElementById(input.getAttribute('aria-activedescendant'));
  assert.ok(active !== null && active.getAttribute('role') === 'option');
  const live = [...document.querySelectorAll('[aria-live="polite"]')].map(n => n.textContent);
  assert.ok(live.includes('3 results'), `announced: ${live.join('|')}`);
  const result = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } });
  assert.deepEqual(result.violations.map(v => `${v.id}: ${v.nodes.map(n => n.failureSummary).join(' ; ')}`), []);
});

test('ETF info panel loads or refreshes the profile and credits Eulerpool', async t => {
  const profile = { instrumentId: 17, isin: 'IE00B4X9L533', etfCompany: 'HSBC', trackingIndex: 'MSCI World Index', assetClass: 'Equity', domicile: 'IE', expenseRatio: '0.15', aumEur: '121400000000', dividendYield: '', inceptionDate: '2010-12-08', currency: 'GBP', website: 'https://www.assetmanagement.hsbc.co.uk/en/funds/x', fetchedAt: '2026-11-04T10:00:00Z', stale: false };
  const calls = mockFetch(t, (url, init) => {
    if (url.includes('/instruments/search')) return json([hsbcLocal]);
    if (url.endsWith('/instruments/17/etf-profile')) return json({ error: { code: 'NOT_FOUND', message: 'none' } }, 404);
    if (url.endsWith('/instruments/17/etf-profile/refresh') && init.method === 'POST') return json(profile);
    return json([]);
  });
  mountPicker();
  type('hsbc'); await screen.findByRole('option', { name: /IE00B4X9L533/ });
  fireEvent.click(screen.getByRole('option', { name: /IE00B4X9L533/ }).querySelector('.info-button'));
  await screen.findByText('121,4 Mrd. €');
  assert.ok(screen.getByText('0,15 %') !== null && screen.getByText('MSCI World Index') !== null && screen.getByText('08.12.2010') !== null);
  assert.ok(screen.getByText('Updated 04.11.2026 · Data by Eulerpool') !== null);
  assert.ok(calls.some(c => c.url.endsWith('/etf-profile/refresh')), 'missing profile is fetched on demand');
  assert.equal(screen.getByRole('link', { name: 'assetmanagement.hsbc.co.uk' }).getAttribute('rel'), 'noopener noreferrer');
  assert.ok(screen.queryByRole('option', { name: /IE00B4X9L533/ }) !== null, 'opening the panel does not close the dropdown');
  assert.ok(screen.getByRole('dialog', { hidden: true }) !== null);
});

// --- price fetching ---
const stock = { instrumentId: 1, isin: 'US0378331005', type: 'STOCK', ticker: 'AAPL', name: 'Apple Inc.', defaultPrice: '10.0000', defaultAmount: '20.00', priceSource: 'None' };
const etf = { instrumentId: 2, isin: 'IE00B4X9L533', type: 'ETF', ticker: 'HMWD', name: 'HSBC MSCI World', defaultPrice: '', defaultAmount: '20.00', priceSource: 'None' };
const budget = () => ({ globalRemaining: 25, userRemaining: 10, userLimit: 10, nextSlotAt: new Date().toISOString() });
const mountTx = () => render(React.createElement(AppRouterContext.Provider, { value: { push() {} } }, React.createElement(TransactionDraftProvider, null, React.createElement(Transactions, { id: '1' }))));

const estimateOf = (provider, extra = {}) => ({ requestsNeeded: 0, fresh: [], noSymbol: [], budget: budget(), provider, ...extra });
const jobDone = items => ({ status: 'DONE', nextRequestAt: new Date().toISOString(), items });

test('prices (Eulerpool primary): Eulerpool cost on the main button, and the Alpha Vantage backup only for NEEDS_BACKUP items, only on click', async t => {
  let backupJob = false;
  const calls = mockFetch(t, (url, init) => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, avSymbol: undefined }, { ...etf, avSymbol: 'HMWD.LON', avCurrency: 'USD' }]);
    if (url.includes('/market-data/budget')) return json(budget());
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('EULERPOOL', { requestsNeeded: 1, eulerpool: { requests: 3, remaining: 400 }, alphavantage: { requests: 1, remaining: 9 } }));
    if (url.endsWith('/price-fetch-jobs') && init.method === 'POST') { const body = JSON.parse(init.body); if (body.provider === 'ALPHAVANTAGE') { backupJob = body; return json({ jobId: 'av' }, 202); } return json({ jobId: 'ep' }, 202); }
    if (url.includes('/price-fetch-jobs/ep')) return json(jobDone([{ instrumentId: 1, status: 'OK', priceEur: '300.0000', currency: 'USD', quoteDate: '2026-11-04', provider: 'EULERPOOL', quoteId: 5 }, { instrumentId: 2, status: 'NEEDS_BACKUP', reason: 'NOT_FOUND' }]));
    if (url.includes('/price-fetch-jobs/av')) return json(jobDone([{ instrumentId: 2, status: 'OK', priceEur: '40.0000', currency: 'USD', quoteDate: '2026-11-04', quoteId: 6 }]));
  });
  mountTx();
  const main = await screen.findByRole('button', { name: 'Fetch prices (3 Eulerpool requests)' });
  assert.ok(screen.getAllByText('Data by Eulerpool').length > 0);
  assert.equal(screen.getByLabelText('Fetch AAPL').disabled, false);
  assert.ok(screen.queryByRole('button', { name: /remaining via/ }) === null);
  fireEvent.click(main);
  const backup = await screen.findByRole('button', { name: 'Fetch 1 remaining via Alpha Vantage (uses 1 request)' });
  assert.equal(screen.getByLabelText('AAPL price').value, '300,0000');
  assert.ok(screen.getByText(/Fetched 04\.11\.2026 · Eulerpool/) !== null);
  assert.equal(backupJob, false, 'the backup never starts by itself');
  assert.ok(calls.some(c => c.url.includes('/estimate') && c.url.includes('instrumentIds=2')));
  fireEvent.click(backup);
  await waitFor(() => assert.equal(screen.getByLabelText('HMWD price').value, '40,0000'));
  assert.deepEqual(backupJob, { date: backupJob.date, instrumentIds: [2], provider: 'ALPHAVANTAGE' });
  await waitFor(() => assert.ok(screen.queryByRole('button', { name: /remaining via/ }) === null), { timeout: 3000 });
});

test('prices (Yahoo primary): Yahoo cost on the main button, items Yahoo could not price get Eulerpool first, then Alpha Vantage, each only on click', async t => {
  const posted = [];
  mockFetch(t, (url, init) => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, yahooSymbol: 'AAPL', avSymbol: undefined }, { ...etf, avSymbol: 'HMWD.LON', avCurrency: 'USD' }]);
    if (url.includes('/market-data/budget')) return json({ ...budget(), primary: 'YAHOO' });
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('YAHOO', { requestsNeeded: 1, yahoo: { requests: 2, remaining: 900, noSymbol: [] }, eulerpool: { requests: 1, remaining: 300, noIsin: [] }, alphavantage: { requests: 1, remaining: 9 } }));
    if (url.endsWith('/price-fetch-jobs') && init.method === 'POST') { posted.push(JSON.parse(init.body)); return json({ jobId: posted.length === 1 ? 'yh' : 'ep' }, 202); }
    if (url.includes('/price-fetch-jobs/yh')) return json(jobDone([{ instrumentId: 1, status: 'OK', priceEur: '300.0000', currency: 'USD', quoteDate: '2026-11-04', provider: 'YAHOO', quoteId: 5 }, { instrumentId: 2, status: 'NO_SYMBOL' }]));
    if (url.includes('/price-fetch-jobs/ep')) return json(jobDone([{ instrumentId: 2, status: 'OK', priceEur: '41.0000', currency: 'GBP', quoteDate: '2026-11-04', provider: 'EULERPOOL', quoteId: 6 }]));
  });
  mountTx();
  const main = await screen.findByRole('button', { name: 'Fetch prices (2 Yahoo requests)' });
  assert.ok(screen.getAllByText('Data by Yahoo Finance').length > 0);
  assert.ok(screen.queryByText(/AV: /) === null, 'the Alpha Vantage badge is only for Alpha Vantage as main source');
  fireEvent.click(main);
  const eulerpool = await screen.findByRole('button', { name: 'Fetch 1 remaining via Eulerpool (uses 1 request)' });
  const alpha = screen.getByRole('button', { name: 'Fetch 1 remaining via Alpha Vantage (uses 1 request)' });
  assert.ok(screen.getByText(/could not be priced by Yahoo Finance: HMWD/) !== null);
  assert.ok(eulerpool.compareDocumentPosition(alpha) === 4, 'Eulerpool comes before Alpha Vantage');
  assert.ok(screen.queryByRole('button', { name: /via Yahoo Finance/ }) === null, 'the primary source is not offered as its own backup');
  assert.equal(screen.getByLabelText('AAPL price').value, '300,0000');
  assert.equal(posted.length, 1, 'no fallback before a click');
  assert.ok(posted[0].provider === undefined, 'the main button uses the configured primary provider');
  fireEvent.click(eulerpool);
  await waitFor(() => assert.equal(screen.getByLabelText('HMWD price').value, '41,0000'));
  assert.deepEqual(posted[1].provider, 'EULERPOOL');
  assert.deepEqual(posted[1].instrumentIds, [2]);
  await waitFor(() => assert.ok(screen.queryByRole('button', { name: /remaining via/ }) === null), { timeout: 3000 });
});

test('prices (Eulerpool primary): Yahoo is offered before Alpha Vantage as backup', async t => {
  mockFetch(t, (url, init) => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, avSymbol: undefined }]);
    if (url.includes('/market-data/budget')) return json(budget());
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('EULERPOOL', { eulerpool: { requests: 1, remaining: 400 }, yahoo: { requests: 2, remaining: 900, noSymbol: [] }, alphavantage: { requests: 1, remaining: 9 } }));
    if (url.endsWith('/price-fetch-jobs') && init.method === 'POST') return json({ jobId: 'ep' }, 202);
    if (url.includes('/price-fetch-jobs/ep')) return json(jobDone([{ instrumentId: 1, status: 'NEEDS_BACKUP', reason: 'NOT_FOUND' }]));
  });
  mountTx();
  fireEvent.click(await screen.findByRole('button', { name: 'Fetch prices (1 Eulerpool request)' }));
  const yahoo = await screen.findByRole('button', { name: 'Fetch 2 remaining via Yahoo Finance (uses 2 requests)' });
  assert.ok(yahoo.compareDocumentPosition(screen.getByRole('button', { name: /via Alpha Vantage/ })) === 4);
  assert.ok(screen.queryByRole('button', { name: /via Eulerpool/ }) === null);
});

test('prices: a past date is fetched with the main provider and shows the closing-price note', async t => {
  let posted = null;
  mockFetch(t, (url, init) => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, yahooSymbol: 'AAPL', avSymbol: undefined }]);
    if (url.includes('/market-data/budget')) return json(budget());
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('YAHOO', { yahoo: { requests: 1, remaining: 900, noSymbol: [], historical: true } }));
    if (url.endsWith('/price-fetch-jobs') && init.method === 'POST') { posted = JSON.parse(init.body); return json({ jobId: 'yh' }, 202); }
    if (url.includes('/price-fetch-jobs/yh')) return json(jobDone([{ instrumentId: 1, status: 'OK', priceEur: '250.0000', currency: 'USD', quoteDate: '2025-03-14', provider: 'YAHOO', quoteId: 9 }]));
  });
  mountTx();
  await screen.findByLabelText('Fetch AAPL');
  fireEvent.change(screen.getByLabelText('Transaction date'), { target: { value: '2025-03-15' } });
  const button = await screen.findByRole('button', { name: 'Fetch prices (1 Yahoo request)' });
  assert.equal(button.disabled, false, 'past dates are not special any more');
  assert.ok(screen.getByText(/Closing price on or before 15\.03\.2025 · Data by Yahoo Finance/) !== null);
  fireEvent.click(button);
  await waitFor(() => assert.equal(screen.getByLabelText('AAPL price').value, '250,0000'));
  assert.deepEqual(posted, { date: '2025-03-15', instrumentIds: [1] });
  assert.ok(screen.getByText(/Fetched 14\.03\.2025 · Yahoo Finance/) !== null);
});

test('search (Yahoo): section title, exchange and sector, attribution, no Alpha Vantage offer, and a rate-limit banner naming the fallback', async t => {
  let mode = 'yahoo';
  const posts = [];
  mockFetch(t, (url, init) => {
    if (init?.method === 'POST' && url.endsWith('/api/instruments')) { posts.push(JSON.parse(init.body)); return json({ ...apple, id: 70, isin: 'DE0007164600', ticker: 'SAP.DE', name: 'SAP SE' }); }
    if (!isOnline(url)) return json([]);
    if (mode === 'empty') return json({ provider: 'YAHOO', results: [] });
    if (mode === 'limited') return json({ provider: 'EULERPOOL', warnings: ['YAHOO_LIMIT_REACHED'], results: [{ source: 'EULERPOOL', isin: 'DE0007164600', ticker: 'SAP.DE', name: 'SAP SE', type: 'STOCK', currency: 'EUR', inPortfolio: false }] });
    return json({ provider: 'YAHOO', results: [{ source: 'YAHOO', yahooSymbol: 'SAP.DE', ticker: 'SAP.DE', name: 'SAP SE', type: 'STOCK', exchangeName: 'XETRA', sector: 'Technology', inPortfolio: false }] });
  });
  mountPicker();
  type('sap se');
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText('Online · Yahoo Finance');
  const row = screen.getByRole('option', { name: /SAP\.DE/ });
  assert.ok(row.querySelector('.option-meta').textContent === 'SAP.DE · no ISIN · XETRA · Technology', row.querySelector('.option-meta').textContent);
  assert.ok(screen.getByText('Data by Yahoo Finance') !== null);
  fireEvent.click(row);
  await waitFor(() => assert.equal(posts.length, 1));
  assert.equal(posts[0].source, 'YAHOO');
  assert.equal(posts[0].yahooSymbol, 'SAP.DE');
  mode = 'empty';
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText(/No results for/); await act(() => delay(20));
  assert.ok(screen.queryByRole('button', { name: 'Try Alpha Vantage (1 request)' }) === null, 'Yahoo answered: nothing more is offered');
  mode = 'limited';
  fireEvent.click(screen.getByRole('button', { name: 'Search' }));
  await screen.findByText('Yahoo Finance is rate limited, showing Eulerpool results');
  assert.ok(screen.getByText('Online · Eulerpool') !== null);
});

test('prices: the real-time button fetches Yahoo now with live FX, whatever the primary, and only for today', async t => {
  let posted = null, realtime = { requests: 2, remaining: 900, noSymbol: [] };
  mockFetch(t, (url, init) => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, avSymbol: undefined }]);
    if (url.includes('/market-data/budget')) return json(budget());
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('EULERPOOL', { eulerpool: { requests: 1, remaining: 400 }, realtime: url.includes(`date=${today()}`) ? realtime : null }));
    if (url.endsWith('/price-fetch-jobs') && init.method === 'POST') { posted = JSON.parse(init.body); return json({ jobId: 'rt' }, 202); }
    if (url.includes('/price-fetch-jobs/rt')) return json(jobDone([{ instrumentId: 1, status: 'OK', priceEur: '313.2000', origPrice: '349.7150', currency: 'USD', fxRate: '0.89560000', fxRateDate: '2026-11-10', fxSource: 'YAHOO', quoteDate: '2026-11-10', quoteTime: '2026-11-10T20:52:55Z', provider: 'YAHOO', quoteId: 11 }]));
  });
  mountTx();
  const button = await screen.findByRole('button', { name: 'Fetch real-time prices (2 Yahoo requests)' });
  assert.match(button.title, /live Yahoo FX rate/);
  assert.match(button.title, /delayed by about 15 minutes/);
  fireEvent.click(button);
  await waitFor(() => assert.equal(screen.getByLabelText('AAPL price').value, '313,2000'));
  assert.deepEqual(posted, { date: today(), instrumentIds: [1], realtime: true });
  assert.ok(screen.getByText('Fetched 10.11.2026 21:52 · Yahoo Finance real-time · 1 USD = 0,8956 € (Yahoo live)') !== null);
  assert.equal(sessionStorage.getItem('price-fetch:1:kind'), null, 'the finished run forgets its kind');

  // No real-time price for a past date.
  fireEvent.change(screen.getByLabelText('Transaction date'), { target: { value: '2025-03-15' } });
  await screen.findByRole('button', { name: 'Fetch prices (1 Eulerpool request)' });
  await waitFor(() => assert.ok(screen.queryByRole('button', { name: /real-time/ }) === null));
});

test('prices: the real-time button is disabled while Yahoo is rate limited', async t => {
  mockFetch(t, url => {
    if (url.endsWith('/saving-plan')) return json([{ ...stock, yahooSymbol: 'AAPL', avSymbol: undefined }]);
    if (url.includes('/market-data/budget')) return json({ ...budget(), primary: 'YAHOO' });
    if (url.includes('/price-fetch/estimate')) return json(estimateOf('YAHOO', { yahoo: { requests: 0, remaining: 0, noSymbol: [] }, realtime: { requests: 1, remaining: 0, noSymbol: [] } }));
  });
  mountTx();
  const button = await screen.findByRole('button', { name: 'Fetch real-time prices (1 Yahoo request)' });
  assert.equal(button.disabled, true);
  assert.equal(screen.getByRole('button', { name: 'Fetch prices (0 Yahoo requests)' }).disabled, false, 'cached prices stay available');
});
