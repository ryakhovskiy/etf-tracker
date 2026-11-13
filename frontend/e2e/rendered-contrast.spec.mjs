import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { captureBrowserErrors, ready } from './helpers.mjs';
import { renderedContrast } from './rendered-contrast.mjs';

for (const theme of themes) {
  test(`${theme} rendered dividend chart and ex-marker text meet AA after opacity`, async ({ page }) => {
    await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
    await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
    const unexpected = await mockAPI(page);
    const errors = captureBrowserErrors(page);
    await page.goto('/portfolios/1/dividends');
    await ready(page, 'dividends');
    const chartSegment = page.getByRole('img', { name: /^HMWO\.L goes ex:/ }).locator('path, rect').last();
    const mark = await renderedContrast(chartSegment, 'fill');
    expect.soft(mark.ratio, `${theme}: faded chart mark ${JSON.stringify(mark)}`).toBeGreaterThanOrEqual(3);
    const markerText = page.getByRole('button', { name: /^HMWO\.L goes ex / }).getByText('HMWO.L', { exact: true });
    const text = await renderedContrast(markerText);
    expect.soft(text.ratio, `${theme}: ex-marker text ${JSON.stringify(text)}`).toBeGreaterThanOrEqual(4.5);
    for (const [month, state] of [['Dezember', 'forecast'], ['März', 'not held']]) {
      const card = page.getByRole('listitem', { name: `${month} 2026`, exact: true });
      const name = card.getByText('Apple Inc.', { exact: true });
      const contrast = await renderedContrast(name);
      expect.soft(contrast.ratio, `${theme}: ${state} text ${JSON.stringify(contrast)}`).toBeGreaterThanOrEqual(4.5);
    }
    const empty = page.getByRole('listitem', { name: 'Januar 2026', exact: true }).getByText(/^No (?:payments|dividends)$/);
    const emptyContrast = await renderedContrast(empty);
    expect.soft(emptyContrast.ratio, `${theme}: empty-month text ${JSON.stringify(emptyContrast)}`).toBeGreaterThanOrEqual(4.5);
    expect(unexpected).toEqual([]);
    expect(errors).toEqual([]);
  });
}
