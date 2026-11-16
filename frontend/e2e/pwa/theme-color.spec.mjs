import { test, expect } from '@playwright/test';
import { themes } from '../fixtures.mjs';
import { assertThemeColor, mockPwaAPI, openControlled, selectTheme } from './helpers.mjs';

test('a single theme-color follows all themes, persistence and live System changes', async ({ page, context }) => {
  await mockPwaAPI(page, context);
  await openControlled(page);
  for (const theme of themes) {
    await selectTheme(page, theme);
    await assertThemeColor(page, theme);
    expect(await page.evaluate(() => localStorage.getItem('holdings.theme'))).toBe(theme);
    // Do not re-seed localStorage: the chosen theme must survive a real reload.
    await page.reload();
    await assertThemeColor(page, theme);
  }
  await selectTheme(page, 'system');
  await page.emulateMedia({ colorScheme: 'dark' });
  await assertThemeColor(page, 'dark');
  await page.emulateMedia({ colorScheme: 'light' });
  await assertThemeColor(page, 'light');
  await expect(page.locator('html')).toHaveAttribute('data-theme-pref', 'system');
});
