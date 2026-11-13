import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { captureBrowserErrors, ready } from './helpers.mjs';
import { renderedContrast } from './rendered-contrast.mjs';

for (const theme of themes) {
  test(`${theme} retained position text stays readable after a legend toggle`, async ({ page }, testInfo) => {
    await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
    await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
    const unexpected = await mockAPI(page);
    const errors = captureBrowserErrors(page);
    await page.goto('/portfolios/1/dashboard');
    await ready(page, 'dashboard');
    const name = 'HSBC MSCI World UCITS ETF USD (Distributing)';
    const legend = page.getByRole('group', { name: 'Chart positions' }).getByRole('button', { name, exact: true });
    await legend.click();
    await expect(legend).toHaveAttribute('aria-pressed', 'false');
    // The mock honors instrumentIds: hiding World leaves only Apple's 90 EUR.
    const value = page.getByLabel('Portfolio figures').getByText('Current value', { exact: true }).locator('..').locator('strong');
    await expect(value).toHaveText('90,00\u00a0€');
    const tile = page.getByTestId('holding-tile').filter({ has: page.getByRole('heading', { name, exact: true }) });
    await expect(tile.getByText('hidden in chart', { exact: true })).toBeVisible();
    const metadata = tile.getByText(/^2 units ·/);
    const contrast = await renderedContrast(metadata);
    expect(contrast.ratio, `${theme}: retained holding metadata ${JSON.stringify(contrast)}`).toBeGreaterThanOrEqual(4.5);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-hidden-position.png`), fullPage: true, animations: 'disabled' });
    expect(unexpected).toEqual([]);
    expect(errors).toEqual([]);
  });
}
