import { test, expect } from '@playwright/test';
import { ready } from '../helpers.mjs';
import { cacheEntries, mockPwaAPI, openControlled } from './helpers.mjs';

test('controlled OAuth, portfolio use and sign-out never cache private data or navigation HTML', async ({ page, context }) => {
  const api = await mockPwaAPI(page, context, { loggedIn: false });
  await openControlled(page, '/', 'login');
  const origin = new URL(page.url()).origin;
  await page.getByRole('button', { name: /Google/ }).click();
  await ready(page, 'portfolios');
  expect(api.hits).toContain('/auth/google');
  expect(api.hits).toContain('/auth/google/callback');
  await page.goto('/portfolios/1/dashboard');
  await ready(page, 'dashboard');
  // Assert the fixture is still reached *after* service-worker control. A
  // stale worker response must not silently make an API mock ineffective.
  const meBefore = api.hits.filter(path => path === '/auth/me').length;
  expect(await page.evaluate(async () => (await (await fetch('/auth/me')).json()).email)).toBe('qa@example.invalid');
  expect(api.hits.filter(path => path === '/auth/me').length).toBe(meBefore + 1);

  const probes = [];
  const workerOwnedProbes = [];
  context.on('request', request => {
    if (request.url().includes('pwa-bypass-probe') && request.serviceWorker()) workerOwnedProbes.push(request.url());
  });
  await context.route(/pwa-bypass-probe/, async route => {
    probes.push(`${route.request().method()} ${route.request().url()}`);
    return route.fulfill({ headers: { 'access-control-allow-origin': '*', 'cache-control': 'no-store' },
      contentType: 'application/json', body: '{"fixture":"pwa-bypass"}' });
  });
  const bodies = await page.evaluate(async () => {
    const cases = [
      ['/api/pwa-bypass-probe', {}], ['/api/pwa-bypass-probe', { method: 'POST', body: '{}' }],
      ['/auth/pwa-bypass-probe', {}],
      ['/pwa-bypass-probe-rsc', { headers: { RSC: '1' } }],
      ['/pwa-bypass-probe-query?_rsc=qa', {}],
      ['/pwa-bypass-probe-prefetch', { headers: { 'Next-Router-Prefetch': '1' } }],
      ['/_next/data/pwa-bypass-probe.json', {}],
      ['https://example.invalid/pwa-bypass-probe', {}],
    ];
    return Promise.all(cases.map(async ([url, init]) => (await (await fetch(url, init)).json()).fixture));
  });
  expect(bodies).toEqual(Array(8).fill('pwa-bypass'));
  expect(probes).toHaveLength(8);
  expect(workerOwnedProbes, 'Bypassed requests must not use respondWith(fetch(...))').toEqual([]);

  await context.route('**/_next/static/pwa-cache-error-probe.js', route => route.fulfill({
    status: 503, contentType: 'application/javascript', body: '// unavailable',
  }));
  expect(await page.evaluate(async () => (await fetch('/_next/static/pwa-cache-error-probe.js')).status)).toBe(503);

  await page.getByRole('link', { name: 'holdings home', exact: true }).click();
  await ready(page, 'portfolios');
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await ready(page, 'login');
  expect(api.hits).toContain('/auth/logout');
  const entries = await cacheEntries(page);
  expect(entries.length, 'The check must inspect a real populated shell cache').toBeGreaterThan(0);
  expect(entries.some(entry => new URL(entry.url).pathname === '/offline.html')).toBe(true);
  expect(entries.some(entry => entry.name.startsWith('holdings-static-') && new URL(entry.url).pathname.startsWith('/_next/static/')),
    'Production JS/CSS use must exercise the runtime static cache').toBe(true);
  const forbidden = entries.filter(entry => {
    const url = new URL(entry.url);
    const shellFile = /^\/(?:offline\.(?:html|css|js)|manifest\.webmanifest|favicon\.(?:svg|ico)|favicon-32\.png)$/.test(url.pathname);
    const staticFile = /^\/(?:_next\/static|fonts|icons)\//.test(url.pathname);
    return url.origin !== origin || entry.method !== 'GET' || entry.status !== 200 || entry.type !== 'basic'
      || (!shellFile && !staticFile)
      || /^\/(?:api|auth)(?:\/|$)/.test(url.pathname) || url.pathname.includes('pwa-bypass-probe')
      || url.pathname.includes('pwa-cache-error-probe')
      || url.pathname.startsWith('/_next/data/') || url.searchParams.has('_rsc')
      || /text\/x-component/.test(entry.contentType)
      || (/text\/html/.test(entry.contentType) && url.pathname !== '/offline.html');
  });
  expect(forbidden, 'Cache Storage may contain app assets and offline.html only, after logout too').toEqual([]);
  api.assertPrivatePassThrough(origin);
});
