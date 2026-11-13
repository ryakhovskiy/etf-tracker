import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'SVGElement', 'Node', 'MutationObserver', 'HTMLDialogElement', 'sessionStorage', 'KeyboardEvent', 'MouseEvent']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
const { render, screen, fireEvent, waitFor, cleanup, within } = await import('@testing-library/react');
const { PortfolioUpdatesProvider, usePortfolioUpdates } = await import('../components/portfolio-updates.tsx');
const { PortfolioHoldings, delayError } = await import('../components/portfolio-holdings.tsx');
afterEach(() => cleanup());

const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const path = url => new URL(url, 'http://x').pathname; // API_URL differs per environment
const text = node => node.textContent.replace(/ /g, ' ');
const items = (over = {}) => [
 { instrumentId: 3, ticker: 'HMWO.L', isin: 'IE00B4X9L533', name: 'HSBC MSCI World UCITS ETF', type: 'ETF', latestQuote: { priceEur: '38.4100', quoteDate: '2026-11-06', provider: 'YAHOO' }, payDelayDays: 30, ...over },
 { instrumentId: 4, ticker: 'SAP.DE', isin: null, name: 'SAP SE', type: 'STOCK', latestQuote: null, payDelayDays: 0 },
];
// Shows the revision so tests can see that a save announces a portfolio change.
function Revision() { const { revision } = usePortfolioUpdates(); return React.createElement('output', { 'data-testid': 'revision' }, String(revision)); }
const tree = (tab = 'items') => React.createElement(PortfolioUpdatesProvider, { activeTab: tab }, React.createElement(PortfolioHoldings, { id: 1 }), React.createElement(Revision));

test('validates whole days from 0 to 90', () => {
 for (const ok of ['0', '7', '30', '90', ' 14 ']) assert.equal(delayError(ok), '', ok);
 for (const bad of ['', '-1', '91', '1.5', 'abc', '100']) assert.equal(delayError(bad), 'Enter a whole number of days from 0 to 90.', bad);
});

test('lists the portfolio items with ISIN, name, latest stored quote and delay; loads once the tab opens', async t => {
 const requested = [];
 t.mock.method(globalThis, 'fetch', async url => { requested.push(path(url)); return json(items()); });
 const view = render(tree('dashboard'));
 await new Promise(resolve => setTimeout(resolve, 20));
 assert.equal(requested.length, 0);
 view.rerender(tree('items'));
 const section = await screen.findByRole('region', { name: 'In the portfolio' });
 await within(section).findByText('HMWO.L');
 assert.deepEqual(requested, ['/api/portfolios/1/items']);
 const hsbc = within(section).getByRole('listitem', { name: 'HMWO.L HSBC MSCI World UCITS ETF' });
 const sap = within(section).getByRole('listitem', { name: 'SAP.DE SAP SE' });
 assert.equal(within(section).getAllByTestId('portfolio-item').length, 2);
 assert.ok(within(hsbc).getByText('HSBC MSCI World UCITS ETF'));
 assert.deepEqual(within(hsbc).getAllByRole('term').map(text), ['ISIN', 'Current price']);
 assert.deepEqual(within(hsbc).getAllByRole('definition').map(text), ['IE00B4X9L533', '38,41 € (06.11.2026)']);
 assert.deepEqual(within(sap).getAllByRole('definition').map(text), ['—', '–']);
 assert.equal(within(section).getByLabelText('Pay-date delay for HMWO.L in days').value, '30');
 assert.equal(within(section).getByLabelText('Pay-date delay for SAP.DE in days').value, '0');
 // Switching away and back does not reload.
 view.rerender(tree('dashboard'));
 view.rerender(tree('items'));
 await new Promise(resolve => setTimeout(resolve, 20));
 assert.equal(requested.length, 1);
});

test('saves a delay on blur or Enter, rejects values outside 0–90 and shows server errors', async t => {
 const puts = [];
 let fail = false;
 t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
  if (init.method === 'PUT') {
   puts.push([path(url), JSON.parse(init.body)]);
   return fail ? json({ error: { code: 'NOT_FOUND', message: 'Portfolio or item not found.' } }, 404) : json(JSON.parse(init.body));
  }
  return json(items());
 });
 render(tree());
 const input = await screen.findByLabelText('Pay-date delay for SAP.DE in days');
 // Out of range: an error, no request.
 fireEvent.change(input, { target: { value: '91' } });
 fireEvent.blur(input);
 assert.equal((await screen.findByRole('alert')).textContent, 'Enter a whole number of days from 0 to 90.');
 assert.equal(input.getAttribute('aria-invalid'), 'true');
 assert.equal(puts.length, 0);
 // A valid value saves on blur and announces the change (the Dividends tab reloads).
 fireEvent.change(input, { target: { value: '21' } });
 fireEvent.blur(input);
 await screen.findByText('Saved');
 assert.deepEqual(puts, [['/api/users/me/instruments/4/pay-delay', { payDelayDays: 21 }]]);
 assert.ok(screen.queryByRole('alert') === null);
 await waitFor(() => assert.equal(screen.getByTestId('revision').textContent, '1'));
 // Unchanged: no request. Enter saves.
 fireEvent.blur(input);
 const hsbc = screen.getByLabelText('Pay-date delay for HMWO.L in days');
 fireEvent.change(hsbc, { target: { value: '35' } });
 fireEvent.keyDown(hsbc, { key: 'Enter' });
 await waitFor(() => assert.equal(puts.length, 2));
 assert.deepEqual(puts[1], ['/api/users/me/instruments/3/pay-delay', { payDelayDays: 35 }]);
 // A server error is shown at the field.
 fail = true;
 fireEvent.change(hsbc, { target: { value: '40' } });
 fireEvent.blur(hsbc);
 assert.equal((await screen.findByRole('alert')).textContent, 'Portfolio or item not found.');
});

test('an empty portfolio says so', async t => {
 t.mock.method(globalThis, 'fetch', async () => json([]));
 render(tree());
 assert.ok(await screen.findByText('No items in this portfolio yet.'));
});
