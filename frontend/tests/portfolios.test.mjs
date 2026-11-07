import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver', 'HTMLDialogElement']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
HTMLDialogElement.prototype.showModal = function () { this.open = true; };
HTMLDialogElement.prototype.close = function () { this.open = false; };
const { render, screen, fireEvent, waitFor, cleanup, act } = await import('@testing-library/react');
const { InstrumentPicker } = await import('../components/instrument-picker.tsx');
const { Portfolios } = await import('../components/portfolios.tsx');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
afterEach(() => cleanup());
const apple = { id: 1, ticker: 'AAPL', name: 'Apple Inc.', type: 'STOCK', isin: 'US0378331005', wkn: '865985', inPortfolio: false };
const other = { ...apple, id: 2, ticker: 'MSFT', name: 'Microsoft Corp.', isin: 'US5949181045', wkn: '870747' };
const initial = { id: 1, name: 'Growth', instrumentIds: [], itemLimit: 30 };
const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const search = value => fireEvent.change(screen.getByRole('combobox'), { target: { value } });
const mountList = () => render(React.createElement(AppRouterContext.Provider, { value: { push() {} } }, React.createElement(Portfolios)));

test('empty portfolio list exposes only the create action and reports server errors inline', async t => {
  t.mock.method(globalThis, 'fetch', async (url, init) => init.method === 'POST'
    ? json({ error: { code: 'PORTFOLIO_LIMIT_REACHED', message: 'Portfolio limit reached.' } }, 409)
    : json({ portfolios: [], limit: 10 }));
  mountList();
  const create = await screen.findByRole('button', { name: 'Create portfolio' });
  assert.equal(screen.getAllByRole('button').length, 1);
  fireEvent.click(create);
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Growth' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  assert.match((await screen.findByRole('alert')).textContent, /Portfolio limit reached/);
});

test('create action is disabled at the portfolio limit', async t => {
  t.mock.method(globalThis, 'fetch', async () => json({ portfolios: [{ id: 1, name: 'Growth', itemCount: 0 }], limit: 1 }));
  mountList();
  assert.equal((await screen.findByRole('button', { name: 'Create portfolio' })).disabled, true);
  assert.match(screen.getByText(/Portfolio limit reached/).textContent, /Delete a portfolio/);
});

test('search debounces, widens on deletion, and ignores aborted stale results below the minimum', async t => {
  const requests = [];
  t.mock.method(globalThis, 'fetch', (url, init) => new Promise(resolve => { requests.push({ url, signal: init.signal, resolve }); }));
  render(React.createElement(InstrumentPicker, { initial }));
  search('app');
  await act(() => delay(330)); assert.equal(requests.length, 0);
  search('apple'); await act(() => delay(330)); assert.equal(requests.length, 1);
  search('appl'); assert.equal(requests[0].signal.aborted, true);
  await act(() => delay(330)); assert.equal(requests.length, 2);
  await act(async () => requests[1].resolve(json([apple])));
  assert.equal(screen.getAllByRole('option').length, 1);
  await act(async () => requests[0].resolve(json([other])));
  assert.ok(screen.queryByText('Microsoft Corp.') === null);
  search('app'); assert.equal(requests[1].signal.aborted, true);
  assert.ok(screen.queryByRole('listbox') === null);
  assert.equal(requests.length, 2);
  assert.match(requests[1].url, /q=appl&/);
});

test('disabled rows cannot be picked; successful add clears selection and marks membership', async t => {
  let added = false;
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    if (url.includes('/search')) return json([apple, { ...other, inPortfolio: true }]);
    if (init.method === 'POST') { added = true; assert.deepEqual(JSON.parse(init.body).instrumentIds, [1]); return json({ added: [1], skipped: [] }); }
    return json({ ...initial, instrumentIds: [1, 2] });
  });
  render(React.createElement(InstrumentPicker, { initial: { ...initial, instrumentIds: [2] } }));
  assert.equal(screen.getByRole('button', { name: 'Add to portfolio' }).disabled, true);
  search('apple');
  const row = await screen.findByRole('option', { name: /AAPL/ });
  fireEvent.click(screen.getByRole('option', { name: /MSFT/ }));
  assert.ok(screen.queryByRole('table') === null);
  fireEvent.click(row); fireEvent.click(row);
  assert.equal(screen.getAllByRole('row').length, 2);
  assert.match(row.textContent, /Selected/);
  fireEvent.click(screen.getByRole('button', { name: 'Add to portfolio' }));
  await screen.findByText('1 added'); assert.equal(added, true);
  assert.ok(screen.queryByRole('table') === null);
  assert.match(screen.getByRole('option', { name: /AAPL/ }).textContent, /In portfolio/);
  assert.equal(screen.getByRole('button', { name: 'Add to portfolio' }).disabled, true);
});

test('overflow disables Add and failed adds preserve selection', async t => {
  t.mock.method(globalThis, 'fetch', async url => url.includes('/search') ? json([apple, other]) : json({ error: { code: 'ITEM_LIMIT_REACHED', message: 'Item limit reached.' } }, 409));
  render(React.createElement(InstrumentPicker, { initial: { ...initial, itemLimit: 1 } }));
  search('apple');
  fireEvent.click(await screen.findByRole('option', { name: /AAPL/ }));
  fireEvent.click(screen.getByRole('option', { name: /MSFT/ }));
  assert.equal(screen.getByRole('button', { name: 'Add to portfolio' }).disabled, true);
  assert.match(screen.getByText(/Only 1 more/).textContent, /Remove some/);
  fireEvent.click(screen.getByRole('button', { name: 'Remove MSFT from selection' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add to portfolio' }));
  assert.match((await screen.findByRole('alert')).textContent, /Item limit reached/);
  assert.ok(screen.getByRole('table'));
  assert.ok(screen.getByRole('button', { name: 'Remove AAPL from selection' }));
});

test('keyboard navigation skips disabled rows and Escape and outside clicks close results', async t => {
  t.mock.method(globalThis, 'fetch', async () => json([{ ...other, inPortfolio: true }, apple]));
  render(React.createElement(InstrumentPicker, { initial }));
  search('apple'); await screen.findByRole('option', { name: /AAPL/ });
  const input = screen.getByRole('combobox');
  fireEvent.keyDown(input, { key: 'ArrowDown' });
  assert.equal(input.getAttribute('aria-activedescendant'), 'instrument-1');
  fireEvent.keyDown(input, { key: 'Enter' }); assert.ok(screen.getByRole('table'));
  fireEvent.keyDown(input, { key: 'Escape' }); assert.ok(screen.queryByRole('listbox') === null);
  fireEvent.focus(input); assert.ok(screen.getByRole('listbox'));
  fireEvent.pointerDown(document.body); assert.ok(screen.queryByRole('listbox') === null);
});
