import { test, expect } from '@playwright/test';
import { mockAPI } from './fixtures.mjs';
import { captureBrowserErrors, ready } from './helpers.mjs';

async function applied(page, theme, preference = theme) {
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
  await expect(page.locator('html')).toHaveAttribute('data-theme-pref', preference);
}

test('keyboard selection persists through reload and System follows live OS changes', async ({ page }) => {
  const unexpected = await mockAPI(page, { loggedIn: false });
  const errors = captureBrowserErrors(page);
  // A new browser context has no preference. No init script re-seeds storage,
  // including after reload, so this exercises the actual persistence path.
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('/');
  await ready(page, 'login');
  await applied(page, 'light', 'system');
  const trigger = page.getByTestId('theme-trigger');
  await trigger.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('radio', { name: 'System', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('radio', { name: 'Dark', exact: true })).toHaveAttribute('aria-checked', 'true');
  await applied(page, 'dark');
  expect(await page.evaluate(() => localStorage.getItem('holdings.theme'))).toBe('dark');
  await page.keyboard.press('Escape');
  await expect(trigger).toBeFocused();
  await page.reload();
  await ready(page, 'login');
  await applied(page, 'dark');
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.emulateMedia({ colorScheme: 'light' });
  await applied(page, 'dark');
  await trigger.click();
  await page.keyboard.press('Home');
  await expect(page.getByRole('radio', { name: 'System', exact: true })).toHaveAttribute('aria-checked', 'true');
  await applied(page, 'light', 'system');
  await page.emulateMedia({ colorScheme: 'dark' });
  await applied(page, 'dark', 'system');
  await page.emulateMedia({ colorScheme: 'light' });
  await applied(page, 'light', 'system');
  expect(unexpected).toEqual([]);
  expect(errors).toEqual([]);
});

test('denied localStorage still renders, switches themes and falls back on reload', async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(window, 'localStorage', {
    configurable: true,
    get() { throw new DOMException('Storage denied by browser', 'SecurityError'); },
  }));
  const unexpected = await mockAPI(page, { loggedIn: false });
  const errors = captureBrowserErrors(page);
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto('/');
  await ready(page, 'login');
  await applied(page, 'dark', 'system');
  await page.getByTestId('theme-trigger').click();
  const green = page.getByRole('radio', { name: 'Tech Green', exact: true });
  await green.focus();
  await page.keyboard.press('Space');
  await expect(green).toHaveAttribute('aria-checked', 'true');
  await applied(page, 'tech-green');
  await page.reload();
  await ready(page, 'login');
  await applied(page, 'dark', 'system');
  expect(unexpected).toEqual([]);
  expect(errors).toEqual([]);
});
