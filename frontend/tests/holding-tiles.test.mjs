import assert from 'node:assert/strict';
import { afterEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
const { render, screen, cleanup, within } = await import('@testing-library/react');
const { HoldingTiles } = await import('../components/holding-tiles.tsx');
afterEach(() => cleanup());

const base = {
  dates: ['2026-11-02'],
  series: [{ instrumentId: 1, name: 'World ETF', values: ['100.00'] }, { instrumentId: 2, name: 'Apple', values: ['100.00'] }],
  totals: ['200.00'], invested: '200.00',
  atPurchase: { value: '200.00', performance: '0.00', performancePct: '0.00' }, current: null,
};
const current = {
  value: '200.00', performance: '0.00', performancePct: '0.00', asOfFrom: '2026-11-12', asOfTo: '2026-11-12', lastFetchedAt: '2026-11-12T15:00:00Z', partial: false, chartPoint: true,
  items: [
    { instrumentId: 1, units: '2.000000', priceEur: '60.0000', value: '120.00', source: 'QUOTE', quoteDate: '2026-11-12', fxRate: null, fxRateDate: null },
    { instrumentId: 2, units: '1.000000', priceEur: '80.0000', value: '80.00', source: 'QUOTE', quoteDate: '2026-11-12', fxRate: null, fxRateDate: null },
  ],
};
const text = node => node.textContent.replaceAll('\u00a0', ' ');
const tiles = () => within(screen.getByRole('region', { name: 'Positions' })).getAllByRole('listitem');

test('position tiles show current values, unit information, portfolio shares, signs and arrows', () => {
  render(React.createElement(HoldingTiles, { performance: { ...base, current } }));
  const [world, apple] = tiles();
  assert.ok(within(world).getByRole('heading', { name: 'World ETF' }));
  assert.match(text(world), /120,00 €/);
  assert.match(text(world), /▲ \+20,00 € \(\+20,00 %\)/);
  assert.match(text(world), /2 units · 60,00 € each/);
  assert.match(text(world), /60,0 %/);
  assert.match(text(apple), /▼ -20,00 € \(-20,00 %\)/);
  assert.match(text(apple), /1 unit · 80,00 € each/);
  assert.match(text(apple), /40,0 %/);
  assert.equal(world.style.getPropertyValue('--stripe'), 'var(--series-1)');
  assert.equal(apple.style.getPropertyValue('--stripe'), 'var(--series-2)');
});

test('missing current quotes preserve purchase values and a partial purchase-price fallback is explicit', () => {
  const view = render(React.createElement(HoldingTiles, { performance: base }));
  for (const tile of tiles()) {
    assert.match(text(tile), /100,00 €/);
    assert.match(text(tile), /Value at purchase prices/);
    assert.match(text(tile), /50,0 %/);
  }
  view.rerender(React.createElement(HoldingTiles, { performance: { ...base, current: { ...current, partial: true, items: [{ ...current.items[0], source: 'PURCHASE', reason: 'NO_QUOTE' }] } } }));
  assert.match(text(tiles()[0]), /\(purchase price\)/);
  assert.match(text(tiles()[1]), /Value at purchase prices/);
});

test('zero and empty positions avoid invalid percentages, while hidden chart positions keep a text badge', () => {
  const zero = { ...base, series: [{ instrumentId: 8, name: 'New position', values: ['0.00'] }], totals: ['0.00'], invested: '0.00' };
  const view = render(React.createElement(HoldingTiles, { performance: zero, hiddenIds: [8] }));
  const [tile] = tiles();
  assert.ok(within(tile).getByText('hidden in chart'));
  assert.match(text(tile), /0,0 %/);
  assert.doesNotMatch(text(tile), /NaN|Infinity/);
  assert.equal(tile.style.getPropertyValue('--stripe'), 'var(--series-1)');
  view.rerender(React.createElement(HoldingTiles, { performance: { ...zero, series: [] } }));
  assert.ok(screen.queryByRole('region', { name: 'Positions' }) === null);
});
