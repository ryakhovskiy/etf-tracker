import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';
import { favoriteInstruments, favoritesAPI, favoritesResponse } from './fixtures/favorites.mjs';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/', pretendToBeVisual: true });
for (const key of ['window', 'document', 'HTMLElement', 'SVGElement', 'Element', 'Node', 'MutationObserver', 'KeyboardEvent', 'MouseEvent', 'sessionStorage']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
HTMLElement.prototype.scrollIntoView = function () {};
const { render, screen, fireEvent, waitFor, cleanup, within, act, configure } = await import('@testing-library/react');
// The full suite runs many JSDOM files in parallel. Poll for the observable UI
// result without treating a loaded event loop as a failed save or reload.
configure({ asyncUtilTimeout: 5000 });
const { Favorites } = await import('../components/favorites.tsx');
const { PortfolioHoldings } = await import('../components/portfolio-holdings.tsx');
const { PortfolioUpdatesProvider } = await import('../components/portfolio-updates.tsx');
afterEach(() => cleanup());

const json = (body, status = 200) => new Response(status === 204 ? null : JSON.stringify(body), { status });
const pathOf = url => new URL(url, 'http://localhost/').pathname;
const text = element => element.textContent.replace(/\u00a0/g, ' ');
const order = () => screen.getAllByTestId('favorite-tile').map(node => Number(node.getAttribute('data-instrument-id')));
const mount = (props = {}) => render(React.createElement(Favorites, { updating: false, ...props }));
function mockFavorites(t, api, intercept) {
  t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
    const path = pathOf(url);
    const method = init.method ?? 'GET';
    const body = init.body ? JSON.parse(init.body) : undefined;
    const overridden = await intercept?.({ path, method, body });
    if (overridden) return overridden;
    const result = api.handle(path, method, body);
    assert.ok(result, `Unhandled test request: ${method} ${path}`);
    return json(result.body, result.status);
  });
}

// JSDOM does not lay out elements. Give the real sortable nodes the same 2x2
// geometry as the mobile grid so the real KeyboardSensor can find neighbours.
function gridGeometry(t) {
  const original = HTMLElement.prototype.getBoundingClientRect;
  t.mock.method(HTMLElement.prototype, 'getBoundingClientRect', function () {
    const tile = this.getAttribute('data-testid') === 'favorite-tile' ? this : this.classList.contains('favorite-cell') ? this.querySelector('[data-testid="favorite-tile"]') : null;
    if (!tile) return original.call(this);
    const index = [...document.querySelectorAll('[data-testid="favorite-tile"]')].indexOf(tile);
    return { x: index % 2 * 180, y: Math.floor(index / 2) * 110, left: index % 2 * 180, top: Math.floor(index / 2) * 110, right: index % 2 * 180 + 170, bottom: Math.floor(index / 2) * 110 + 100, width: 170, height: 100 };
  });
}
async function keyboardMove({ drop = true } = {}) {
  const tile = screen.getAllByTestId('favorite-tile')[0];
  tile.focus();
  fireEvent.keyDown(tile, { key: ' ', code: 'Space' });
  await waitFor(() => assert.match(document.body.textContent, /Picked up favorite .*Position 1 of/));
  fireEvent.keyDown(document, { key: 'ArrowRight', code: 'ArrowRight' });
  await waitFor(() => assert.match(document.body.textContent, /Moved .*position 2/));
  fireEvent.keyDown(document, { key: drop ? ' ' : 'Escape', code: drop ? 'Space' : 'Escape' });
}

test('favorites format decimal strings with de-DE rounding, tones, accessible change and one shared timestamp', async t => {
  mockFavorites(t, favoritesAPI([1, 2, 4]));
  mount();
  await screen.findByTestId('favorites-list');
  assert.deepEqual(order(), [1, 2, 4]);
  const [negative, positive, flat] = screen.getAllByTestId('favorite-tile');
  assert.equal(text(within(negative).getByTestId('favorite-price')), '42,04 €');
  assert.match(text(within(negative).getByTestId('favorite-change')), /[−-]0,64 €.*[−-]1,50 %/);
  assert.match(within(negative).getByTestId('favorite-change').className, /performance-negative/);
  assert.equal(text(within(positive).getByTestId('favorite-price')), '190,13 €');
  assert.match(text(within(positive).getByTestId('favorite-change')), /\+1,24 €.*\+0,65 %/);
  assert.match(within(positive).getByTestId('favorite-change').className, /performance-positive/);
  assert.match(within(flat).getByTestId('favorite-change').className, /performance-neutral/);
  assert.doesNotMatch(text(within(flat).getByTestId('favorite-change')), /[▲▼+−]/);
  for (const tile of [negative, positive, flat]) {
    assert.match(text(within(tile).getByTestId('favorite-change')), /Change vs previous close:/);
    assert.equal(tile.tagName, 'A');
    assert.equal(tile.getAttribute('href'), `/instruments/${tile.getAttribute('data-instrument-id')}`);
    assert.equal(tile.getAttribute('aria-roledescription'), 'sortable');
    assert.equal(tile.tabIndex, 0);
  }
  assert.equal(screen.getAllByTestId('favorites-asof').length, 1);
});

test('missing quote, missing previous close and unavailable previous FX remain unavailable without invented zero changes', async t => {
  const api = favoritesAPI([1, 2, 3]);
  api.update(1, { change: null, reasons: ['NO_PREVIOUS_CLOSE'] });
  api.update(2, { change: null, reasons: ['FX_PREVIOUS_UNAVAILABLE'] });
  mockFavorites(t, api);
  mount();
  await screen.findByTestId('favorites-list');
  for (const tile of screen.getAllByTestId('favorite-tile')) {
    assert.match(text(within(tile).getByTestId('favorite-change')), /[–—]/);
    assert.doesNotMatch(text(within(tile).getByTestId('favorite-change')), /0,00/);
    assert.doesNotMatch(within(tile).getByTestId('favorite-change').className, /performance-(positive|negative)/);
  }
  const missing = screen.getAllByTestId('favorite-tile')[2];
  assert.match(text(within(missing).getByTestId('favorite-price')), /^[–—]$/);
  assert.match(missing.getAttribute('aria-label'), /no price/i);
});

test('an empty favorites section points to Manage items', async t => {
  mockFavorites(t, favoritesAPI([]));
  mount();
  assert.match(text(await screen.findByTestId('favorites-empty')), /Star items under Manage items/);
  assert.equal(screen.queryAllByTestId('favorite-tile').length, 0);
});

test('favorites reload after a price refresh completes and retain values while updating', async t => {
  const api = favoritesAPI([1]);
  mockFavorites(t, api);
  const view = mount();
  await screen.findByTestId('favorite-tile');
  view.rerender(React.createElement(Favorites, { updating: true }));
  assert.equal(text(screen.getByTestId('favorite-price')), '42,04 €');
  api.update(1, { priceEur: '44.0400' });
  view.rerender(React.createElement(Favorites, { updating: false }));
  await waitFor(() => assert.equal(text(screen.getByTestId('favorite-price')), '44,04 €'));
  assert.equal(api.calls.filter(call => call.method === 'GET').length, 2);
});

test('keyboard sensor moves a favorite, announces the position and persists the optimistic order', async t => {
  gridGeometry(t);
  const api = favoritesAPI([1, 2]);
  const pending = Promise.withResolvers();
  const requests = [];
  mockFavorites(t, api, ({ path, method, body }) => {
    if (path === '/api/favorites/order') { requests.push({ method, body }); return pending.promise; }
  });
  mount();
  await screen.findByTestId('favorites-list');
  await keyboardMove();
  await waitFor(() => assert.deepEqual(order(), [2, 1]));
  assert.deepEqual(requests, [{ method: 'PUT', body: { instrumentIds: [2, 1] } }]);
  await act(async () => pending.resolve(json(favoritesResponse([2, 1]))));
  assert.match(document.body.textContent, /Dropped .*position 2/);
  assert.deepEqual(order(), [2, 1]);
});

test('lifting a favorite keeps the pickup announcement until it moves', async t => {
  gridGeometry(t);
  mockFavorites(t, favoritesAPI([1, 2]));
  mount();
  await screen.findByTestId('favorites-list');
  const tile = screen.getAllByTestId('favorite-tile')[0];
  tile.focus();
  fireEvent.keyDown(tile, { key: ' ', code: 'Space' });
  await waitFor(() => assert.match(document.body.textContent, /Picked up favorite .*Position 1 of 2/));
  fireEvent.keyDown(document, { key: 'Escape', code: 'Escape' });
});

test('a failed reorder reverts the displayed order and shows a recoverable error', async t => {
  gridGeometry(t);
  mockFavorites(t, favoritesAPI([1, 2]), ({ path }) => path === '/api/favorites/order' ? json({ error: { code: 'INTERNAL', message: 'Storage unavailable.' } }, 500) : undefined);
  mount();
  await screen.findByTestId('favorites-list');
  await keyboardMove();
  assert.match(text(await screen.findByRole('alert')), /Could not save the new order/);
  assert.deepEqual(order(), [1, 2]);
});

test('a 409 reorder reloads the authoritative favorites set', async t => {
  gridGeometry(t);
  const api = favoritesAPI([1, 2]);
  mockFavorites(t, api, ({ path }) => {
    if (path !== '/api/favorites/order') return;
    api.replace([2, 3, 1]);
    return json({ error: { code: 'FAVORITES_STALE', message: 'Favorites changed.' } }, 409);
  });
  mount();
  await screen.findByTestId('favorites-list');
  await keyboardMove();
  await waitFor(() => assert.deepEqual(order(), [2, 3, 1]));
  assert.equal(api.calls.filter(call => call.method === 'GET').length, 2);
});

test('Escape cancels keyboard reordering without writing an order', async t => {
  gridGeometry(t);
  const api = favoritesAPI([1, 2]);
  mockFavorites(t, api);
  mount();
  await screen.findByTestId('favorites-list');
  await keyboardMove({ drop: false });
  await waitFor(() => assert.match(document.body.textContent, /Reordering cancelled/));
  assert.deepEqual(order(), [1, 2]);
  assert.equal(api.calls.filter(call => call.method === 'PUT').length, 0);
});

test('a stale reorder followed by a failed reload restores the last confirmed order', async t => {
  gridGeometry(t);
  let stale = false;
  mockFavorites(t, favoritesAPI([1, 2]), ({ path }) => {
    if (path === '/api/favorites/order') {
      stale = true;
      return json({ error: { code: 'FAVORITES_STALE', message: 'Favorites changed.' } }, 409);
    }
    if (path === '/api/favorites' && stale) return json({ error: { code: 'INTERNAL', message: 'Reload unavailable.' } }, 500);
  });
  mount();
  await screen.findByTestId('favorites-list');
  await keyboardMove();
  await screen.findByRole('alert');
  await waitFor(() => assert.deepEqual(order(), [1, 2]));
});

for (const starred of [false, true]) {
  test(`${starred ? 'unstar' : 'star'} changes aria-pressed optimistically and reverts a rejected save`, async t => {
    const item = favoriteInstruments[0];
    const pending = Promise.withResolvers();
    const mutations = [];
    t.mock.method(globalThis, 'fetch', async (url, init = {}) => {
      const path = pathOf(url);
      if (path === '/api/portfolios/1/items') return json([{ ...item, favorite: starred, payDelayDays: 30, latestQuote: null, isin: null, type: 'ETF' }]);
      assert.equal(path, '/api/favorites/1');
      mutations.push(init.method);
      return pending.promise;
    });
    render(React.createElement(PortfolioUpdatesProvider, { activeTab: 'items' }, React.createElement(PortfolioHoldings, { id: 1 })));
    const button = await screen.findByRole('button', { name: `${starred ? 'Remove' : 'Add'} ${item.name} ${starred ? 'from' : 'to'} favorites` });
    assert.equal(button.getAttribute('aria-pressed'), String(starred));
    fireEvent.click(button);
    assert.equal(button.getAttribute('aria-pressed'), String(!starred));
    await waitFor(() => assert.deepEqual(mutations, [starred ? 'DELETE' : 'PUT']));
    await act(async () => pending.resolve(json({ error: { code: 'INTERNAL', message: 'Storage unavailable.' } }, 500)));
    await screen.findByRole('alert');
    assert.equal(button.getAttribute('aria-pressed'), String(starred));
  });
}
