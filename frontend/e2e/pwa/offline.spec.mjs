import { test, expect } from '@playwright/test';
import { themes } from '../fixtures.mjs';
import { assertNoOverflow, ready } from '../helpers.mjs';
import { renderedContrast } from '../rendered-contrast.mjs';
import { assertThemeColor, assertTouchTarget, mockPwaAPI, openControlled } from './helpers.mjs';

for (const theme of themes) {
  test(`${theme}: offline fallback and Retry work on every navigation`, async ({ page, context }, testInfo) => {
    const api = await mockPwaAPI(page, context);
    await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
    await openControlled(page);
    const origin = new URL(page.url()).origin;
    for (const [name, path] of [
      ['home', '/'], ['portfolio', '/portfolios/1/dashboard'], ['unknown', '/pwa-unknown-route'],
    ]) {
      await test.step(path, async () => {
        await context.setOffline(true);
        // A hard navigation must fail at the real network and be answered by
        // the real worker, not by a mocked route or Next's client router.
        const response = await page.goto(path);
        expect(response?.fromServiceWorker()).toBe(true);
        expect(new URL(page.url()).pathname).toBe(path);
        await expect(page.getByRole('heading', { name: "You're offline", exact: true })).toBeVisible();
        await assertThemeColor(page, theme);
        await page.evaluate(() => document.fonts.ready);
        expect(await page.evaluate(async () => {
          const faces = await Promise.all([document.fonts.load('15px Inter'), document.fonts.load('15px "JetBrains Mono"')]);
          return faces.every(matches => matches.length > 0 && matches.every(face => face.status === 'loaded'));
        }), 'Both precached fonts must be usable with the network off').toBe(true);
        const retry = page.getByRole('button', { name: 'Retry', exact: true });
        await assertTouchTarget(retry, page);
        const home = page.getByRole('link', { name: 'Open Holdings', exact: true });
        await expect(home).toHaveAttribute('href', '/');
        await assertTouchTarget(home, page);
        for (const locator of [page.getByRole('heading'), page.getByTestId('offline-page').locator('p').last(), retry, home]) {
          const contrast = await renderedContrast(locator);
          expect(contrast.ratio, `${theme} offline text: ${JSON.stringify(contrast)}`).toBeGreaterThanOrEqual(4.5);
        }
        await assertNoOverflow(page, `${theme}/offline/${name}`);
        await page.screenshot({ path: testInfo.outputPath(`${theme}-offline-${name}.png`), fullPage: true, animations: 'disabled' });
        // Retry is real script behaviour, including a second failed attempt.
        await Promise.all([page.waitForEvent('load'), retry.click()]);
        await expect(page.getByRole('heading', { name: "You're offline", exact: true })).toBeVisible();
        await context.setOffline(false);
        await Promise.all([page.waitForEvent('load'), page.getByTestId('offline-retry').click()]);
        expect(new URL(page.url()).pathname).toBe(path);
        await expect(page.getByTestId('offline-page')).toHaveCount(0);
        if (name === 'home') await ready(page, 'portfolios');
        if (name === 'portfolio') await ready(page, 'dashboard');
        if (name === 'unknown') {
          await expect(page.getByRole('heading', { name: 'Page not found', exact: true })).toBeVisible();
          await expect(page.getByRole('link', { name: 'Back to Holdings', exact: true })).toHaveAttribute('href', '/');
        }
      });
    }
    api.assertPrivatePassThrough(origin);
  });
}

test('offline System resolves the OS theme and storage failures still render', async ({ page, context }) => {
  await mockPwaAPI(page, context);
  await openControlled(page);
  await page.evaluate(() => localStorage.setItem('holdings.theme', 'system'));
  await page.emulateMedia({ colorScheme: 'dark' });
  await context.setOffline(true);
  await page.goto('/offline-system-probe');
  await assertThemeColor(page, 'dark');
  await page.emulateMedia({ colorScheme: 'light' });
  await page.reload();
  await assertThemeColor(page, 'light');
  await page.addInitScript(() => {
    Object.defineProperty(Storage.prototype, 'getItem', { configurable: true, value() { throw new DOMException('Storage denied', 'SecurityError'); } });
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: "You're offline", exact: true })).toBeVisible();
  await assertThemeColor(page, 'light');
});
