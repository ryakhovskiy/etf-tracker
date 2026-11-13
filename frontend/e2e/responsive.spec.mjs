import { test, expect } from '@playwright/test';
import { csvFile, mockAPI, portfolio, themes } from './fixtures.mjs';

const screens = [
  { name: 'login', path: '/', loggedIn: false },
  { name: 'portfolios', path: '/' },
  ...['dashboard', 'transactions', 'dividends', 'import', 'items'].map(tab => ({ name: tab, path: `/portfolios/1/${tab}` })),
];

async function ready(page, name) {
  if (name === 'login') await expect(page.getByRole('button', { name: /Google/ })).toBeVisible();
  else if (name === 'portfolios') await expect(page.getByRole('link', { name: new RegExp(portfolio.name) })).toBeVisible();
  else {
    await expect(page.getByRole('heading', { name: portfolio.name, exact: true })).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Portfolio', exact: true }).locator('[aria-current="page"]')).toHaveAttribute('href', `/portfolios/1/${name}`);
    if (name === 'dashboard') await expect(page.getByLabel('Portfolio figures')).toBeVisible();
    if (name === 'transactions') await expect(page.getByRole('button', { name: 'Commit transaction' })).toBeVisible();
    if (name === 'dividends') await expect(page.getByRole('button', { name: 'Previous year' })).toBeEnabled();
    if (name === 'import') {
      await page.getByLabel('CSV file').setInputFiles({ name: 'holdings-smoke.csv', mimeType: 'text/csv', buffer: Buffer.from(csvFile) });
      await expect(page.getByRole('checkbox', { name: /Include row 2/ })).toBeVisible();
    }
    if (name === 'items') await expect(page.getByLabel('Pay-date delay for HMWO.L in days')).toHaveValue('30');
  }
  await page.evaluate(() => document.fonts.ready);
}

for (const theme of themes) {
  test.describe(theme, () => {
    for (const screen of screens) {
      test(`${screen.name} is populated and fits the viewport`, async ({ page }, testInfo) => {
        await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
        await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
        const unexpected = await mockAPI(page, screen);
        const pageErrors = [];
        page.on('pageerror', error => pageErrors.push(error.message));
        await page.goto(screen.path);
        await ready(page, screen.name);
        // Save every settled screen, including an overflowing one, as evidence.
        await page.screenshot({ path: testInfo.outputPath(`${theme}-${screen.name}.png`), fullPage: true, animations: 'disabled' });
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
        await expect(page.getByTestId('theme-trigger')).toBeVisible();
        await page.getByTestId('theme-trigger').click();
        await expect(page.getByRole('radiogroup', { name: 'Theme', exact: true })).toBeVisible();
        await expect(page.getByRole('radio')).toHaveCount(6);
        await page.keyboard.press('Escape');
        const dimensions = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
        expect(dimensions.scroll, `${theme}/${screen.name}: horizontal page overflow`).toBeLessThanOrEqual(dimensions.width);
        expect(unexpected, 'Every API/auth request must have an explicit fixture').toEqual([]);
        expect(pageErrors, 'The populated page must not throw browser errors').toEqual([]);
      });
    }
  });
}
