import { test, expect } from '@playwright/test';
import { mockTicker } from './ticker-fixtures.mjs';
import { tickerChart } from '../tests/fixtures/ticker.mjs';
import { favoriteInstruments } from '../tests/fixtures/favorites.mjs';
import { assertNoOverflow, captureBrowserErrors, ready } from './helpers.mjs';

const selector = page => page.getByRole('combobox', { name: 'Chart time period' });
const visiblePeriod = page => page.getByLabel('Visible chart period');
const markers = page => page.getByTestId('buy-marker');
const open = async page => {
  await page.goto('/instruments/1');
  await expect(page.getByRole('heading', { name: favoriteInstruments[0].name, exact: true })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Price chart', exact: true }).locator('.recharts-area-area')).toBeAttached();
};

test('a quick favorite tap opens the EUR ticker view; star and back persist across pages', async ({ page }, testInfo) => {
  const fixture = await mockTicker(page);
  const errors = captureBrowserErrors(page);
  try {
    await page.goto('/');
    const tile = page.getByTestId('favorite-tile').filter({ hasText: favoriteInstruments[0].name });
    await expect(tile).toHaveAttribute('href', '/instruments/1');
    if (testInfo.project.use.hasTouch) await tile.tap(); else await tile.click();
    await expect(page).toHaveURL(/\/instruments\/1$/);
    await expect(page.getByTestId('instrument-price')).toHaveText('42,29 €');
    await expect(page.getByTestId('instrument-change')).toHaveClass(/performance-positive/);
    await expect(page.getByTestId('instrument-ids')).toContainText('IE00B4X9L533');
    const star = page.getByTestId('favorite-toggle');
    await expect(star).toHaveAttribute('aria-pressed', 'true');
    await star.click();
    await expect(star).toHaveAttribute('aria-pressed', 'false');
    await expect.poll(() => fixture.api.ids).toEqual([2]);
    await star.click();
    await expect(star).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(() => fixture.api.ids).toEqual([2, 1]);
    await assertNoOverflow(page, 'ticker header and controls');
    await page.getByRole('link', { name: 'All portfolios', exact: true }).click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByTestId('favorite-tile')).toHaveCount(2);
    expect(fixture.unexpected).toEqual([]);
    expect(errors).toEqual([]);
  } finally { fixture.dispose(); }
});

test('Enter opens a favorite after keyboard reordering with Space remains available', async ({ page }) => {
  const fixture = await mockTicker(page);
  try {
    await page.goto('/');
    const tiles = page.getByTestId('favorite-tile');
    await expect(tiles).toHaveCount(2);
    await tiles.first().focus();
    await page.keyboard.press('Space');
    await expect(page.getByText(/Picked up favorite .*Position 1 of 2/)).toBeAttached();
    await page.keyboard.press('ArrowRight');
    await expect(page.getByText(/Moved .*position 2 of 2/)).toBeAttached();
    await page.keyboard.press('Space');
    await expect.poll(() => fixture.api.ids).toEqual([2, 1]);
    await expect(page).toHaveURL(/\/$/);
    const tile = tiles.filter({ hasText: favoriteInstruments[0].name });
    await tile.focus();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/instruments\/1$/);
    await expect(page.getByTestId('instrument-price')).toBeVisible();
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('every visible buy renders at its paid price with position, gain/loss colour and an accessible tooltip', async ({ page }) => {
  const fixture = await mockTicker(page);
  try {
    await open(page);
    await expect(markers(page)).toHaveCount(3);
    // Position and tone are public attributes on the marker itself.
    expect(await markers(page).evaluateAll(elements => elements.map(element => ({
      position: element.dataset.position, tone: element.dataset.tone,
    })))).toEqual([
      { position: 'above', tone: 'negative' },
      { position: 'below', tone: 'positive' },
      { position: 'below', tone: 'positive' },
    ]);
    for (let index = 0; index < 3; index++) {
      const marker = markers(page).nth(index);
      const paint = await marker.evaluate(element => {
        const shape = element.querySelector('path, polygon');
        const context = document.createElement('canvas').getContext('2d');
        const normalize = color => { context.fillStyle = color; return context.fillStyle; };
        return {
          actual: shape ? normalize(getComputedStyle(shape).fill) : null,
          expected: normalize(getComputedStyle(document.documentElement).getPropertyValue(element.dataset.tone === 'negative' ? '--loss' : '--gain').trim()),
          label: element.getAttribute('aria-label'),
        };
      });
      expect(paint.actual).toBe(paint.expected);
      expect(paint.label).toContain(index === 0 ? 'Retirement portfolio' : 'Family savings');
      await marker.focus();
      const tooltip = page.getByTestId('buy-tooltip');
      await expect(tooltip).toBeVisible();
      await expect(tooltip).toContainText(index === 0 ? 'Retirement portfolio' : 'Family savings');
      await expect(tooltip).toContainText(index === 0 ? '50,00' : index === 1 ? '35,00' : '40,00');
      await expect(tooltip).toContainText('2,5 units');
      await expect(tooltip).toContainText('0,75 units');
      const buyDate = new Date(`${fixture.chart.buys.at(-1).day}T00:00:00Z`).toLocaleDateString('de-DE', { timeZone: 'UTC', day: '2-digit', month: '2-digit', year: 'numeric' });
      await expect(tooltip).toContainText(buyDate);
    }
    // A domain based only on closes would clip the 50-EUR marker above the
    // 42.29-EUR peak. All three markers must remain inside the plot.
    const plotBox = await page.getByRole('group', { name: 'Price chart', exact: true }).boundingBox();
    for (const marker of await markers(page).all()) {
      const box = await marker.boundingBox();
      expect(box.y).toBeGreaterThanOrEqual(plotBox.y);
      expect(box.y + box.height).toBeLessThanOrEqual(plotBox.y + plotBox.height);
    }
    await assertNoOverflow(page, 'ticker markers and tooltip');
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('chart presets are shared with portfolio charts; zoom, pan, keyboard and Reset change the visible period', async ({ page }) => {
  const fixture = await mockTicker(page);
  try {
    await page.goto('/portfolios/1/dashboard');
    await ready(page, 'dashboard');
    await selector(page).selectOption('6');
    await open(page);
    await expect(selector(page)).toHaveValue('6');
    await expect(selector(page).locator('option')).toHaveText(['Last 3 months', 'Last 6 months', 'Last 12 months', 'Last 18 months', 'Last 24 months', 'Last 48 months', 'Last 60 months', 'Max']);
    await selector(page).selectOption('12');
    const initial = await visiblePeriod(page).textContent();
    await page.getByRole('button', { name: 'Zoom in', exact: true }).click();
    await expect(selector(page)).toHaveValue('custom');
    await expect(visiblePeriod(page)).not.toHaveText(initial);
    const zoomed = await visiblePeriod(page).textContent();
    await page.getByRole('button', { name: 'Pan left', exact: true }).click();
    await expect(visiblePeriod(page)).not.toHaveText(zoomed);
    const plot = page.getByRole('group', { name: 'Price chart', exact: true });
    await plot.focus();
    await page.keyboard.press('Home');
    await expect(selector(page)).toHaveValue('max');
    await expect(page.getByTestId('period-change')).toContainText('+17,29');
    await expect(page.getByTestId('period-change')).toContainText('+69,16');
    await page.keyboard.press('+');
    await expect(selector(page)).toHaveValue('custom');
    await page.getByRole('button', { name: 'Reset chart', exact: true }).click();
    await expect(selector(page)).toHaveValue('max');
    await selector(page).selectOption('3');
    await page.goto('/portfolios/1/dashboard');
    await ready(page, 'dashboard');
    await expect(selector(page)).toHaveValue('3');
    await assertNoOverflow(page, 'shared chart shell');
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('loading resolves to cached chart points with a quiet Yahoo warning', async ({ page }) => {
  const fixture = await mockTicker(page, { deferred: true, chart: tickerChart({ warning: 'YAHOO_UNAVAILABLE' }) });
  try {
    await page.goto('/instruments/1');
    await expect(page.getByRole('status').filter({ hasText: 'Loading chart' })).toBeVisible();
    fixture.release();
    await expect(page.getByTestId('instrument-warning')).toBeVisible();
    await expect(page.getByTestId('instrument-price')).toHaveText('42,29 €');
    await expect(markers(page)).toHaveCount(3);
    await expect(selector(page)).toBeEnabled();
    await assertNoOverflow(page, 'cached ticker warning');
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('a title outside the user portfolios shows a 404 state with a back link', async ({ page }) => {
  const fixture = await mockTicker(page, { status: 404 });
  try {
    await page.goto('/instruments/1');
    await expect(page.getByTestId('instrument-not-found')).toHaveText(/not in your portfolios/i);
    await expect(page.getByTestId('instrument-price')).toHaveCount(0);
    await expect(markers(page)).toHaveCount(0);
    await assertNoOverflow(page, 'ticker 404');
    await page.getByRole('link', { name: 'All portfolios', exact: true }).click();
    await expect(page).toHaveURL(/\/$/);
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});

test('an owned title with no price history has its own empty state and back link', async ({ page }) => {
  const fixture = await mockTicker(page, { status: 404, errorCode: 'NO_PRICE_HISTORY' });
  try {
    await page.goto('/instruments/1');
    await expect(page.getByTestId('instrument-no-history')).toHaveText('There is no price history for this instrument.');
    await expect(page.getByTestId('instrument-no-history')).toHaveAttribute('role', 'status');
    await expect(page.getByTestId('instrument-not-found')).toHaveCount(0);
    await expect(page.getByTestId('instrument-price')).toHaveCount(0);
    await expect(page.getByTestId('favorite-toggle')).toHaveCount(0);
    await assertNoOverflow(page, 'owned ticker without history');
    await page.getByRole('link', { name: 'All portfolios', exact: true }).click();
    await expect(page).toHaveURL(/\/$/);
    expect(fixture.unexpected).toEqual([]);
  } finally { fixture.dispose(); }
});
