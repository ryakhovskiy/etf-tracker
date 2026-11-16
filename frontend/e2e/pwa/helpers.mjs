import { expect } from '@playwright/test';
import { mockAPI } from '../fixtures.mjs';
import { ready } from '../helpers.mjs';

const privatePath = /^\/(?:api|auth)(?:\/|$)/;

/** Context routes, rather than page routes, also see worker-owned network calls.
 * We assert those calls never occur for private endpoints: they must bypass the
 * worker entirely. OAuth steps are local fixtures, never Google. */
export async function mockPwaAPI(page, context, { loggedIn = true } = {}) {
  const unexpected = await mockAPI(page, { loggedIn, routingContext: context });
  const requests = [];
  const hits = [];
  context.on('request', request => {
    if (privatePath.test(new URL(request.url()).pathname)) {
      requests.push({ url: request.url(), worker: !!request.serviceWorker() });
    }
  });
  await context.route(/\/auth(?:\/|\?|$)/, async route => {
    const path = new URL(route.request().url()).pathname;
    hits.push(path);
    const headers = { 'cache-control': 'no-store' };
    // Playwright routes only the first request in an HTTP redirect chain.
    // Separate document navigations keep both auth steps mocked and observable
    // with the worker enabled, without allowing the callback to hit a backend.
    const navigate = destination => route.fulfill({ headers, contentType: 'text/html',
      body: `<!doctype html><title>Mock OAuth</title><script>location.replace(${JSON.stringify(destination)})</script>` });
    if (path === '/auth/google') {
      return navigate('/auth/google/callback?code=pwa-fixture');
    }
    if (path === '/auth/google/callback') {
      loggedIn = true;
      return navigate('/');
    }
    if (path === '/auth/logout') {
      loggedIn = false;
      return route.fulfill({ headers, json: { ok: true } });
    }
    if (path === '/auth/me' || path === '/auth/refresh') {
      const ok = loggedIn && path === '/auth/me';
      return route.fulfill({ status: ok ? 200 : 401, headers,
        json: ok ? { name: 'QA Investor', email: 'qa@example.invalid' } : { error: { code: 'UNAUTHORIZED', message: 'Please sign in.' } } });
    }
    return route.fallback();
  });
  return {
    requests, hits, unexpected,
    assertPrivatePassThrough(origin) {
      expect(requests.length, 'Private API/auth fixtures must actually be exercised').toBeGreaterThan(0);
      expect(requests.filter(request => request.worker), 'API/auth must bypass respondWith, not be fetched by the SW').toEqual([]);
      expect(requests.filter(request => new URL(request.url).origin !== origin),
        'Build production with NEXT_PUBLIC_API_URL="" so all private calls stay on this origin').toEqual([]);
      expect(unexpected, 'Every private request must have a fixture; no backend is needed').toEqual([]);
    },
  };
}

export async function openControlled(page, path = '/', screen = 'portfolios') {
  await page.goto(path);
  if (screen) await ready(page, screen);
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker?.controller
    ? new URL(navigator.serviceWorker.controller.scriptURL).pathname : null),
  { timeout: 30_000, message: 'A production service worker must control the first loaded page without reloading' }).toBe('/sw.js');
  const registration = await page.evaluate(async () => {
    const value = await navigator.serviceWorker.ready;
    return { scope: value.scope, state: value.active?.state };
  });
  expect(registration.scope).toBe(`${new URL(page.url()).origin}/`);
  expect(registration.state).toBe('activated');
}

export async function settledFrames(page) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
}

export async function countDocuments(page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('pwa-document-count', String(Number(sessionStorage.getItem('pwa-document-count') || 0) + 1));
  });
}

export const documentCount = page => page.evaluate(() => Number(sessionStorage.getItem('pwa-document-count')));

export async function assertThemeColor(page, theme) {
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
  await expect(page.locator('meta[name="theme-color"]')).toHaveCount(1);
  await expect.poll(() => page.evaluate(() => {
    const normalize = color => {
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = 1;
      const context = canvas.getContext('2d');
      context.fillStyle = color;
      context.fillRect(0, 0, 1, 1);
      return [...context.getImageData(0, 0, 1, 1).data].join(',');
    };
    const meta = document.querySelector('meta[name="theme-color"]')?.content;
    const background = getComputedStyle(document.documentElement).getPropertyValue('--bg').trim();
    return !!meta && !!background && normalize(meta) === normalize(background);
  }), { message: `theme-color must equal the rendered ${theme} background token` }).toBe(true);
}

export async function assertTouchTarget(locator, page) {
  if (page.viewportSize().width > 640) return;
  const rect = await locator.boundingBox();
  expect(rect?.width, 'Mobile control width').toBeGreaterThanOrEqual(44);
  expect(rect?.height, 'Mobile control height').toBeGreaterThanOrEqual(44);
}

export async function selectTheme(page, preference) {
  await page.getByTestId('theme-trigger').click();
  await page.getByTestId(`theme-option-${preference}`).click();
  // The picker intentionally stays open after a radio choice. Close it before
  // the next interaction; clicking its trigger again would otherwise close it.
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('theme-trigger')).toHaveAttribute('aria-expanded', 'false');
}

export async function cacheEntries(page) {
  return page.evaluate(async () => {
    const entries = [];
    for (const name of await caches.keys()) {
      const cache = await caches.open(name);
      for (const request of await cache.keys()) {
        const response = await cache.match(request);
        entries.push({ name, url: request.url, method: request.method, status: response.status,
          responseURL: response.url, redirected: response.redirected,
          type: response.type, contentType: response.headers.get('content-type') || '' });
      }
    }
    return entries;
  });
}
