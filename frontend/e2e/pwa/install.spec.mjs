import { test, expect } from '@playwright/test';
import { themes } from '../fixtures.mjs';
import { assertNoOverflow } from '../helpers.mjs';
import { renderedContrast } from '../rendered-contrast.mjs';
import { assertTouchTarget, mockPwaAPI, openControlled, selectTheme, settledFrames } from './helpers.mjs';

// Native Chrome installability events can arrive at any time. These focused UI
// tests admit only their own simulated event; installability itself is checked
// separately through CDP without this interception.
async function syntheticEventsOnly(page) {
  await page.addInitScript(() => {
    window.addEventListener('beforeinstallprompt', event => {
      if (event.pwaFixture) return;
      event.preventDefault();
      event.stopImmediatePropagation();
    }, true);
  });
}

async function offerInstall(page) {
  return page.evaluate(() => {
    const event = new Event('beforeinstallprompt', { cancelable: true });
    Object.assign(event, {
      pwaFixture: true,
      prompt: async () => { window.pwaPromptCalls = (window.pwaPromptCalls || 0) + 1; },
      userChoice: Promise.resolve({ outcome: 'dismissed', platform: 'web' }),
    });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  });
}

test('Install app uses the offered prompt and appinstalled removes the control', async ({ page, context }, testInfo) => {
  await syntheticEventsOnly(page);
  await mockPwaAPI(page, context);
  await openControlled(page);
  await expect(page.getByTestId('install-app')).toHaveCount(0);
  expect(await offerInstall(page), 'The app must prevent the browser default install prompt').toBe(true);
  const install = page.getByRole('button', { name: 'Install app', exact: true });
  for (const theme of themes) {
    await selectTheme(page, theme);
    await expect(install).toBeVisible();
    await assertTouchTarget(install, page);
    await assertNoOverflow(page, `${theme}/install-control`);
    expect((await renderedContrast(install)).ratio).toBeGreaterThanOrEqual(4.5);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-install-control.png`), fullPage: true });
    // The extra Install control moves the picker trigger left in the header.
    // Its open panel must still fit, as well as the closed header.
    await page.getByTestId('theme-trigger').click();
    await expect(page.getByTestId('theme-switcher')).toBeVisible();
    await assertNoOverflow(page, `${theme}/install-with-open-theme-picker`);
    await page.keyboard.press('Escape');
  }
  await install.click();
  await expect.poll(() => page.evaluate(() => window.pwaPromptCalls || 0)).toBe(1);
  await offerInstall(page);
  await expect(install).toBeVisible();
  await page.evaluate(() => window.dispatchEvent(new Event('appinstalled')));
  await expect(install).toHaveCount(0);
  await offerInstall(page);
  await settledFrames(page);
  await expect(install).toHaveCount(0);
  expect(await page.evaluate(() => window.pwaPromptCalls)).toBe(1);
});

for (const standalone of ['display-mode', 'navigator.standalone']) {
  test(`Install app remains hidden in ${standalone}`, async ({ page, context }) => {
    await syntheticEventsOnly(page);
    await page.addInitScript(mode => {
      if (mode === 'navigator.standalone') Object.defineProperty(navigator, 'standalone', { configurable: true, value: true });
      else {
        const original = window.matchMedia.bind(window);
        window.matchMedia = query => {
          const media = original(query);
          if (query === '(display-mode: standalone)') Object.defineProperty(media, 'matches', { value: true });
          return media;
        };
      }
    }, standalone);
    await mockPwaAPI(page, context);
    await openControlled(page);
    await offerInstall(page);
    await settledFrames(page);
    await expect(page.getByTestId('install-app')).toHaveCount(0);
    await expect(page.getByTestId('ios-install-hint')).toHaveCount(0);
  });
}

test.describe('iOS Safari', () => {
  test.use({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' });

  test('Install app opens the accessible Share / Add to Home Screen hint', async ({ page, context }, testInfo) => {
    await syntheticEventsOnly(page);
    await mockPwaAPI(page, context);
    await openControlled(page);
    const install = page.getByRole('button', { name: 'Install app', exact: true });
    for (const theme of themes) {
      await selectTheme(page, theme);
      await assertTouchTarget(install, page);
      await install.click();
      const hint = page.getByRole('dialog', { name: 'Add to Home Screen', exact: true });
      await expect(hint).toBeVisible();
      await expect(hint).toContainText('Tap Share, then Add to Home Screen');
      const close = hint.getByRole('button', { name: 'Close', exact: true });
      await assertTouchTarget(close, page);
      await assertNoOverflow(page, `${theme}/ios-install-hint`);
      for (const locator of [hint.locator('p').first(), hint.locator('p').last(), close]) {
        expect((await renderedContrast(locator)).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await page.screenshot({ path: testInfo.outputPath(`${theme}-ios-install-hint.png`), fullPage: true });
      await close.click();
      await expect(hint).toHaveCount(0);
    }
    await page.evaluate(() => window.dispatchEvent(new Event('appinstalled')));
    await expect(install).toHaveCount(0);
  });
});

test.describe('iPhone WKWebView', () => {
  test.use({ userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148' });

  test('a webview without Safari and Version tokens does not offer the Safari Share hint', async ({ page, context }) => {
    await syntheticEventsOnly(page);
    await mockPwaAPI(page, context);
    await openControlled(page);
    await settledFrames(page);
    await expect(page.getByTestId('install-app')).toHaveCount(0);
    await expect(page.getByRole('dialog', { name: 'Add to Home Screen', exact: true })).toHaveCount(0);
  });
});
