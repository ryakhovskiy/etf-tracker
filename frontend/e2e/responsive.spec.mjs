import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { assertNoOverflow, captureBrowserErrors, ready, screens } from './helpers.mjs';

for (const theme of themes) {
  test.describe(theme, () => {
    for (const screen of screens) {
      test(`${screen.name} is populated and fits the viewport`, async ({ page }, testInfo) => {
        await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
        await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
        const unexpected = await mockAPI(page, screen);
        const pageErrors = captureBrowserErrors(page);
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
        await assertNoOverflow(page, `${theme}/${screen.name}`);
        expect(unexpected, 'Every API/auth request must have an explicit fixture').toEqual([]);
        expect(pageErrors, 'The populated page must not throw browser errors').toEqual([]);
      });
    }
  });
}
