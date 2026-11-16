import { test, expect } from '@playwright/test';
import { themes } from '../fixtures.mjs';
import { assertNoOverflow, ready } from '../helpers.mjs';
import { renderedContrast } from '../rendered-contrast.mjs';
import { assertTouchTarget, cacheEntries, countDocuments, documentCount, mockPwaAPI, openControlled, selectTheme, settledFrames } from './helpers.mjs';
import { updateServer, workerVersion } from './update-server.mjs';

async function prepareWaitingUpdate(page, context, deployment) {
  const api = await mockPwaAPI(page, context);
  await countDocuments(page);
  await openControlled(page, `${deployment.url}/portfolios/1/transactions`, 'transactions');
  await expect.poll(() => workerVersion(page)).toBe(1);
  await settledFrames(page);
  expect(await documentCount(page)).toBe(1);
  await expect(page.getByTestId('update-notice')).toHaveCount(0);
  const draft = page.getByLabel('HMWO.L amount', { exact: true });
  await draft.fill('123,45');
  deployment.deploy(2);
  // Exercise the production visibility update check, not a fake registration
  // object or synthetic controller. Actual changed JS installs and waits.
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
  await expect.poll(() => workerVersion(page, 'waiting'), { timeout: 30_000 }).toBe(2);
  await expect(page.getByTestId('update-notice')).toContainText('New version available');
  await expect.poll(() => workerVersion(page)).toBe(1);
  return { api, draft };
}

test('a real changed worker waits for Reload, preserves drafts and reloads exactly once', async ({ page, context, baseURL }, testInfo) => {
  const deployment = await updateServer(baseURL);
  try {
    const { api, draft } = await prepareWaitingUpdate(page, context, deployment);
    await page.evaluate(() => caches.open('holdings-shell-qa-obsolete'));
    const notice = page.getByTestId('update-notice');
    for (const theme of themes) {
      await selectTheme(page, theme);
      await assertTouchTarget(page.getByTestId('update-reload'), page);
      await assertTouchTarget(page.getByTestId('update-dismiss'), page);
      await assertNoOverflow(page, `${theme}/update-notice`);
      for (const locator of [notice.locator('.update-notice-text'), page.getByTestId('update-reload'), page.getByTestId('update-dismiss')]) {
        expect((await renderedContrast(locator)).ratio).toBeGreaterThanOrEqual(4.5);
      }
      await expect(draft).toHaveValue('123,45');
      await page.screenshot({ path: testInfo.outputPath(`${theme}-update-notice.png`), fullPage: true });
    }
    await page.waitForLoadState('networkidle');
    expect(await documentCount(page), 'A waiting update must not reload without a click').toBe(1);
    expect(await workerVersion(page)).toBe(1);
    expect(deployment.workerRequests).toContain(2);
    await Promise.all([page.waitForEvent('load'), page.getByTestId('update-reload').click()]);
    await ready(page, 'transactions');
    await expect.poll(() => workerVersion(page)).toBe(2);
    await expect(page.getByTestId('update-notice')).toHaveCount(0);
    await expect.poll(() => page.evaluate(async () => (await caches.keys()).filter(name => name.startsWith('holdings-') && !name.endsWith('-qa-2')))).toEqual([]);
    const entries = await cacheEntries(page);
    const newShell = entries.filter(entry => entry.name.startsWith('holdings-shell-') && entry.name.endsWith('-qa-2'));
    for (const path of ['/offline.html', '/offline.css', '/offline.js', '/fonts/Inter-latin-variable.woff2', '/fonts/JetBrainsMono-latin-variable.woff2']) {
      expect(newShell.some(entry => new URL(entry.url).pathname === path && entry.status === 200),
        `Activation must retain the new shell's ${path}, not merely delete old caches`).toBe(true);
    }
    // A duplicate controllerchange must not cause a second reload, including
    // after the new document has mounted its production listeners.
    await page.evaluate(() => navigator.serviceWorker.dispatchEvent(new Event('controllerchange')));
    await settledFrames(page);
    await page.waitForLoadState('networkidle');
    expect(await documentCount(page)).toBe(2);
    expect(await page.evaluate(async () => (await navigator.serviceWorker.ready).waiting === null)).toBe(true);
    api.assertPrivatePassThrough(deployment.url);
    expect(deployment.failures).toEqual([]);
  } finally {
    await deployment.close();
  }
});

test('Later dismisses a real waiting update while preserving the active worker and draft', async ({ page, context, baseURL }) => {
  const deployment = await updateServer(baseURL);
  try {
    const { api, draft } = await prepareWaitingUpdate(page, context, deployment);
    await page.getByTestId('update-dismiss').click();
    await expect(page.getByTestId('update-notice')).toHaveCount(0);
    await page.waitForLoadState('networkidle');
    expect(await workerVersion(page)).toBe(1);
    expect(await workerVersion(page, 'waiting')).toBe(2);
    expect(await documentCount(page)).toBe(1);
    await expect(draft).toHaveValue('123,45');
    api.assertPrivatePassThrough(deployment.url);
    expect(deployment.failures).toEqual([]);
  } finally {
    await deployment.close();
  }
});

test('each tab reloads once only after its own click, even when another tab already activated v2', async ({ page, context, baseURL }) => {
  const deployment = await updateServer(baseURL);
  let other;
  try {
    const api = await mockPwaAPI(page, context);
    await countDocuments(page);
    await openControlled(page, `${deployment.url}/portfolios/1/transactions`, 'transactions');
    await expect.poll(() => workerVersion(page)).toBe(1);
    other = await context.newPage();
    await countDocuments(other);
    await openControlled(other, `${deployment.url}/portfolios/1/transactions`, 'transactions');
    await expect.poll(() => workerVersion(other)).toBe(1);
    const otherDraft = other.getByLabel('HMWO.L amount', { exact: true });
    await otherDraft.fill('456,78');
    expect(await documentCount(page)).toBe(1);
    expect(await documentCount(other)).toBe(1);
    await expect(page.getByTestId('update-notice')).toHaveCount(0);
    await expect(other.getByTestId('update-notice')).toHaveCount(0);

    await page.bringToFront();
    deployment.deploy(2);
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await expect.poll(() => workerVersion(page, 'waiting'), { timeout: 30_000 }).toBe(2);
    await expect(page.getByTestId('update-notice')).toBeVisible();
    await expect(other.getByTestId('update-notice')).toBeVisible();
    await Promise.all([page.waitForEvent('load'), page.getByTestId('update-reload').click()]);
    await ready(page, 'transactions');
    await expect.poll(() => workerVersion(page)).toBe(2);
    await expect.poll(() => workerVersion(other)).toBe(2);
    await settledFrames(other);
    expect(await documentCount(page)).toBe(2);
    expect(await documentCount(other), 'Activation in tab A must not reload tab B').toBe(1);
    await expect(otherDraft).toHaveValue('456,78');
    // The second document still runs its old JS with a captured worker that
    // has already activated. Its explicit Reload must remain operative.
    await expect(other.getByTestId('update-notice')).toBeVisible();
    await other.bringToFront();
    await Promise.all([other.waitForEvent('load'), other.getByTestId('update-reload').click()]);
    await ready(other, 'transactions');
    await expect.poll(() => workerVersion(other)).toBe(2);
    await expect(other.getByTestId('update-notice')).toHaveCount(0);
    for (const document of [page, other]) {
      await document.evaluate(() => navigator.serviceWorker.dispatchEvent(new Event('controllerchange')));
      await settledFrames(document);
      await document.waitForLoadState('networkidle');
      expect(await documentCount(document), 'Each tab must reload exactly once for its own explicit choice').toBe(2);
    }
    api.assertPrivatePassThrough(deployment.url);
    expect(deployment.failures).toEqual([]);
  } finally {
    if (other && !other.isClosed()) await other.close();
    await deployment.close();
  }
});
