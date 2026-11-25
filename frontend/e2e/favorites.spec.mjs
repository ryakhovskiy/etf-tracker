import { test, expect } from '@playwright/test';
import { mockFavorites } from './favorites-fixtures.mjs';
import { favoriteInstruments } from '../tests/fixtures/favorites.mjs';
import { assertNoOverflow, captureBrowserErrors, ready } from './helpers.mjs';

const tiles = page => page.getByTestId('favorite-tile');
const tileOrder = page => tiles(page).evaluateAll(elements => elements.map(element => Number(element.getAttribute('data-instrument-id'))));
const expectOrder = (page, ids) => expect.poll(() => tileOrder(page)).toEqual(ids);

async function dragFavorite(page, { touch }) {
  const source = tiles(page).nth(0);
  await source.scrollIntoViewIfNeeded();
  const from = await source.boundingBox();
  const to = await tiles(page).nth(1).boundingBox();
  const start = { x: from.x + from.width / 2, y: from.y + from.height / 2 };
  const end = { x: to.x + to.width / 2, y: to.y + to.height / 2 };
  if (touch) {
    // Send real Chromium touch input. Dispatching synthetic DOM events would
    // miss native scrolling and PointerSensor activation/cancellation.
    const client = await page.context().newCDPSession(page);
    try {
      await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ ...start, id: 1 }] });
      await expect(source).toHaveAttribute('aria-pressed', 'true');
      for (let step = 1; step <= 10; step++) await client.send('Input.dispatchTouchEvent', {
        type: 'touchMove', touchPoints: [{ x: start.x + (end.x - start.x) * step / 10, y: start.y + (end.y - start.y) * step / 10, id: 1 }],
      });
      await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    } finally { await client.detach(); }
  } else {
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await expect(source).toHaveAttribute('aria-pressed', 'true');
    await page.mouse.move(end.x, end.y, { steps: 10 });
    await page.mouse.up();
  }
}

// Check painted numeric text inside each actual card, not just page bounds.
// A clipped number inside a fitting tile is still a broken compact layout.
async function assertNumbersFit(page) {
  const violations = await page.locator('[data-testid="favorite-tile"], [data-testid="portfolio-card"], [aria-label="Portfolio figures"], [aria-label="All portfolios total"], .kpi-grid, .summary-grid').evaluateAll(roots => {
    const issues = [];
    for (const root of roots) for (const element of root.querySelectorAll('.num, [data-testid="favorite-price"], [data-testid="favorite-change"], .metric-value, .kpi-value, .summary-value')) {
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      if (!rect.width || !rect.height || style.display === 'none' || style.visibility === 'hidden') continue;
      if (element.scrollWidth > element.clientWidth + 1 && element.clientWidth > 0) issues.push(`${element.className}: ${element.textContent} (${element.scrollWidth}>${element.clientWidth})`);
      const card = element.closest('[data-testid="favorite-tile"], .metric, .kpi, .summary-card, .summary-tile, [data-testid="portfolio-card"], [aria-label="Portfolio figures"], [aria-label="All portfolios total"]') ?? root;
      const bounds = card.getBoundingClientRect();
      if (rect.left < bounds.left - 1 || rect.right > bounds.right + 1) issues.push(`${element.className}: ${element.textContent} escapes card`);
    }
    return issues;
  });
  expect(violations, 'All displayed amounts must fit their cards').toEqual([]);
}

test('star order, native long-press drag, reload persistence and unstar work across pages', async ({ page }, testInfo) => {
  const fixture = await mockFavorites(page, { ids: [] });
  const errors = captureBrowserErrors(page);
  try {
    await page.goto('/portfolios/1/items');
    await ready(page, 'items');
    for (const item of [favoriteInstruments[1], favoriteInstruments[0]]) {
      const button = page.getByRole('button', { name: `Add ${item.name} to favorites`, exact: true });
      await button.click();
      await expect(button).toHaveAttribute('aria-pressed', 'true');
      await expect.poll(() => fixture.api.ids).toContain(item.instrumentId);
    }
    await page.goto('/');
    await expectOrder(page, [2, 1]);
    await dragFavorite(page, { touch: testInfo.project.use.hasTouch === true });
    await expectOrder(page, [1, 2]);
    await expect.poll(() => fixture.api.ids).toEqual([1, 2]);
    expect(fixture.api.calls.filter(call => call.path.endsWith('/order')).map(call => call.body)).toEqual([{ instrumentIds: [1, 2] }]);
    await page.reload();
    await expectOrder(page, [1, 2]);
    await page.goto('/portfolios/1/items');
    const remove = page.getByRole('button', { name: `Remove ${favoriteInstruments[0].name} from favorites`, exact: true });
    await remove.click();
    await expect(remove).toHaveAttribute('aria-pressed', 'false');
    await expect.poll(() => fixture.api.ids).toEqual([2]);
    await page.goto('/');
    await expectOrder(page, [2]);
    expect(fixture.unexpected).toEqual([]);
    expect(errors).toEqual([]);
  } finally { fixture.dispose(); }
});

test('keyboard reorder announces positions and persists the resulting order', async ({ page }) => {
  const fixture = await mockFavorites(page);
  try {
    await page.goto('/');
    await expectOrder(page, [1, 2]);
    await tiles(page).first().focus();
    await page.keyboard.press('Space');
    await expect(page.getByText(/Picked up favorite .*Position 1 of 2/)).toBeAttached();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByText(/Moved .*position 2 of 2/)).toBeAttached();
    await page.keyboard.press('Space');
    await expectOrder(page, [2, 1]);
    await expect.poll(() => fixture.api.ids).toEqual([2, 1]);
    await page.reload();
    await expectOrder(page, [2, 1]);
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('a quick touch swipe on a favorite scrolls without reordering', async ({ page }, testInfo) => {
  test.skip(testInfo.project.use.hasTouch !== true, 'Native touch scrolling requires a mobile project.');
  const fixture = await mockFavorites(page, { ids: [1, 2, 3, 4] });
  try {
    await page.goto('/');
    await expectOrder(page, [1, 2, 3, 4]);
    await tiles(page).first().evaluate(element => element.scrollIntoView({ block: 'center', behavior: 'instant' }));
    const before = await page.evaluate(() => scrollY);
    expect(before).toBeGreaterThan(0);
    const box = await tiles(page).first().boundingBox();
    const x = box.x + box.width / 2;
    const y = box.y + box.height / 2;
    const client = await page.context().newCDPSession(page);
    try {
      await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y, id: 1 }] });
      for (let step = 1; step <= 6; step++) await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + step * 20, id: 1 }] });
      await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    } finally { await client.detach(); }
    await expect.poll(() => page.evaluate(() => scrollY)).toBeLessThan(before - 20);
    await expectOrder(page, [1, 2, 3, 4]);
    expect(fixture.api.calls.filter(call => call.method === 'PUT')).toEqual([]);
  } finally { fixture.dispose(); }
});

test('favorites refresh after the start-page realtime job finishes', async ({ page }) => {
  const fixture = await mockFavorites(page, { refresh: true });
  try {
    await page.goto('/');
    await expectOrder(page, [1, 2]);
    const favorite = tiles(page).filter({ hasText: favoriteInstruments[0].name });
    await expect(favorite.getByTestId('favorite-price')).toHaveText('42,04 €');
    await page.getByRole('button', { name: /Refresh prices/ }).click();
    await expect(page.getByRole('status').filter({ hasText: 'Updating prices' })).toBeVisible();
    fixture.finishRefresh();
    await expect(favorite.getByTestId('favorite-price')).toHaveText('44,04 €');
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('compact portfolio and dashboard amounts fit at the configured viewport', async ({ page }) => {
  const fixture = await mockFavorites(page, { ids: [1, 2, 3, 4], largeValues: true });
  const errors = captureBrowserErrors(page);
  try {
    await page.goto('/');
    await expectOrder(page, [1, 2, 3, 4]);
    await page.evaluate(() => document.fonts.ready);
    if (page.viewportSize().width <= 640) {
      const first = await tiles(page).nth(0).boundingBox();
      const second = await tiles(page).nth(1).boundingBox();
      const third = await tiles(page).nth(2).boundingBox();
      expect(Math.abs(first.y - second.y)).toBeLessThan(2);
      expect(third.y).toBeGreaterThan(first.y);
    }
    await assertNumbersFit(page);
    await assertNoOverflow(page, 'compact portfolios and favorites');
    await page.goto('/portfolios/1/dashboard');
    await ready(page, 'dashboard');
    const figures = page.getByLabel('Portfolio figures');
    await expect(figures).toContainText('7.472,51');
    if (page.viewportSize().width <= 640) {
      await expect(page.getByTestId('figures-asof')).toBeVisible();
      await expect(figures.locator('.figure-meta').filter({ visible: true })).toHaveCount(0);
      const labelLines = await figures.locator('.kpi-label').evaluateAll(labels => labels.map(label => {
        const style = getComputedStyle(label);
        return label.getBoundingClientRect().height / parseFloat(style.lineHeight);
      }));
      expect(labelLines.every(lines => lines <= 2.1), `KPI label line counts: ${labelLines.join(', ')}`).toBe(true);
    }
    await assertNumbersFit(page);
    await assertNoOverflow(page, 'compact dashboard');
    expect(fixture.unexpected).toEqual([]);
    expect(errors).toEqual([]);
  } finally { fixture.dispose(); }
});
