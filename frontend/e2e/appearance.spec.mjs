import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { assertNoOverflow, captureBrowserErrors, ready } from './helpers.mjs';

for (const theme of themes) {
  test(`${theme} picker fits with reduced motion and dividend cards form a responsive grid`, async ({ page }, testInfo) => {
    await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
    await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
    const unexpected = await mockAPI(page);
    const errors = captureBrowserErrors(page);
    await page.goto('/portfolios/1/items');
    await ready(page, 'items');
    const search = page.getByRole('combobox');
    await search.fill('SAP SE');
    const option = page.getByRole('option', { name: /SAP/ });
    await expect(option).toBeVisible();
    const searchBox = await page.getByRole('button', { name: 'Search', exact: true }).boundingBox();
    expect(searchBox.height, 'Search remains a 44px touch target').toBeGreaterThanOrEqual(44);
    const durations = await page.getByRole('listbox').evaluate(element => {
      const style = getComputedStyle(element);
      return [style.animationDuration, style.transitionDuration].flatMap(value => value.split(',').map(duration => parseFloat(duration) * (duration.trim().endsWith('ms') ? 1 : 1000)));
    });
    expect(Math.max(...durations), 'Reduced motion removes dropdown animation').toBeLessThanOrEqual(0.01);
    await assertNoOverflow(page, `${theme}/picker dropdown`);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-picker.png`), fullPage: true, animations: 'disabled' });
    await option.click();
    await expect(page.getByRole('button', { name: 'Remove SAP.DE from selection' })).toBeVisible();
    await page.goto('/portfolios/1/dividends');
    await ready(page, 'dividends');
    const cards = page.getByRole('listitem', { name: / 2026$/ });
    await expect(cards).toHaveCount(12);
    const bounds = await cards.evaluateAll(elements => elements.map(element => ({ x: element.getBoundingClientRect().x, width: element.getBoundingClientRect().width })));
    const columns = new Set(bounds.map(rect => Math.round(rect.x)));
    if (page.viewportSize().width <= 640) expect(columns.size, 'Month cards stack on mobile').toBe(1);
    else expect(columns.size, 'Month cards use a grid on desktop').toBeGreaterThan(1);
    await expect(page.getByRole('listitem', { name: 'November 2026' })).toHaveAttribute('aria-current', 'date');
    expect(unexpected).toEqual([]);
    expect(errors).toEqual([]);
  });
}
