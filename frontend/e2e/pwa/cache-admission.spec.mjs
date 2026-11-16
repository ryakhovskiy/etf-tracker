import { test, expect } from '@playwright/test';
import { cacheEntries, mockPwaAPI, openControlled } from './helpers.mjs';
import { updateServer } from './update-server.mjs';

const icon = '/icons/icon-192.png';
const privateHTML = '<!doctype html><title>Private login</title><p>private-pwa-marker</p>';
const badResponses = [
  { name: '201 basic PNG', response: { status: 201 } },
  { name: '200 HTML instead of PNG', response: { contentType: 'text/html', body: privateHTML } },
  { name: '200 JSON instead of PNG', response: { contentType: 'application/json', body: '{"private":"pwa-marker"}' } },
  { name: '200 RSC instead of PNG', response: { contentType: 'text/x-component', body: '0:"private-pwa-marker"' } },
  { name: 'redirected login HTML', destination: '/auth/pwa-cache-target', final: { contentType: 'text/html', body: privateHTML } },
  { name: 'redirected API JSON', destination: '/api/pwa-cache-target', final: { contentType: 'application/json', body: '{"private":"pwa-marker"}' } },
  { name: 'redirected valid PNG', destination: '/icons/pwa-cache-target.png', final: { forwardPath: '/icons/icon-512.png' } },
];

async function registerWorker(page) {
  return page.evaluate(async () => {
    let registration;
    try { registration = await navigator.serviceWorker.register('/sw.js', { scope: '/', updateViaCache: 'none' }); }
    catch { return 'rejected'; }
    const worker = registration.installing ?? registration.waiting ?? registration.active;
    if (!worker) return 'unregistered';
    const done = () => ['installed', 'activated', 'redundant'].includes(worker.state);
    if (!done()) await new Promise(resolve => worker.addEventListener('statechange', () => { if (done()) resolve(); }));
    return worker.state;
  });
}

test('valid typed 200 assets are admitted to shell and runtime caches', async ({ page, baseURL }) => {
  const deployment = await updateServer(baseURL);
  try {
    const probe = '/_next/static/pwa-valid-admission-probe.png';
    deployment.stub('/pwa-cache-probe.html', { contentType: 'text/html', body: '<!doctype html><title>Valid cache admission probe</title>' });
    deployment.stub(probe, { forwardPath: icon });
    await page.goto(`${deployment.url}/pwa-cache-probe.html`);
    await registerWorker(page);
    await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);
    expect(await page.evaluate(async path => (await fetch(path)).status, probe)).toBe(200);
    await expect.poll(async () => (await cacheEntries(page)).some(entry => new URL(entry.url).pathname === probe)).toBe(true);
    const entries = await cacheEntries(page);
    for (const path of [icon, '/icons/icon-512.png', '/icons/icon-maskable-512.png', '/icons/apple-touch-icon.png', probe]) {
      const entry = entries.find(entry => new URL(entry.url).pathname === path);
      expect(entry, `A valid PNG must be cached: ${path}`).toBeTruthy();
      expect(entry).toMatchObject({ status: 200, type: 'basic', redirected: false, responseURL: `${deployment.url}${path}` });
      expect(entry.contentType).toMatch(/^image\/png(?:;|$)/);
    }
    expect(deployment.failures).toEqual([]);
  } finally {
    await deployment.close();
  }
});

for (const phase of ['precache', 'runtime']) for (const fixture of badResponses) {
  test(`${phase} rejects ${fixture.name}`, async ({ page, baseURL }) => {
    const deployment = await updateServer(baseURL);
    try {
      // A plain controlled document avoids bootstrapping React/API calls. Every
      // malformed endpoint, including auth/API redirect targets, is served by
      // this local fixture; none reaches the app's real backend. Real HTTP
      // redirects retain Response.redirected and the final Response.url.
      deployment.stub('/pwa-cache-probe.html', { contentType: 'text/html', body: '<!doctype html><title>Cache admission probe</title>' });
      const asset = phase === 'precache' ? icon : '/_next/static/pwa-cache-admission-probe.png';
      if (fixture.destination) {
        deployment.stub(asset, { status: 302, headers: { location: fixture.destination }, body: '' });
        deployment.stub(fixture.destination, fixture.final);
      } else deployment.stub(asset, { forwardPath: icon, ...fixture.response });
      await page.goto(`${deployment.url}/pwa-cache-probe.html`);
      const terminal = await registerWorker(page);
      expect(['installed', 'activated', 'redundant', 'rejected', 'unregistered']).toContain(terminal);
      if (phase === 'runtime') {
        await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true);
        const status = await page.evaluate(async path => (await fetch(path)).status, asset);
        expect(status, 'Reject caching without changing the response returned to the online client').toBe(fixture.response?.status ?? 200);
      }
      expect(deployment.workerRequests).toContain(1);
      expect(deployment.requests.some(request => request.path === asset && request.status === (fixture.destination ? 302 : fixture.response.status ?? 200)),
        'The malformed asset response must actually be fetched').toBe(true);
      if (fixture.destination) expect(deployment.requests.some(request => request.path === fixture.destination && request.status === 200)).toBe(true);
      const entries = await cacheEntries(page);
      const forbidden = entries.filter(entry => {
        const path = new URL(entry.url).pathname;
        const finalPath = entry.responseURL ? new URL(entry.responseURL).pathname : '';
        return path === asset || /^\/(?:api|auth)\//.test(path) || /^\/(?:api|auth)\//.test(finalPath)
          || entry.status !== 200 || entry.type !== 'basic' || entry.redirected
          || (/text\/html/.test(entry.contentType) && path !== '/offline.html')
          || (/json|text\/x-component/.test(entry.contentType) && path !== '/manifest.webmanifest');
      });
      expect(forbidden, `${fixture.name} must never be persisted under an innocent icon key or private URL`).toEqual([]);
      expect(deployment.failures).toEqual([]);
    } finally {
      await deployment.close();
    }
  });
}

for (const operation of ['open', 'match', 'put']) {
  test(`a runtime cache ${operation} failure preserves a successful online asset`, async ({ page, context, baseURL }) => {
    const deployment = await updateServer(baseURL);
    try {
      const api = await mockPwaAPI(page, context);
      const probe = '/_next/static/pwa-runtime-cache-probe.js';
      deployment.stub(probe, { contentType: 'application/javascript', body: '/* pwa-network-success */' });
      await openControlled(page, `${deployment.url}/`);
      const worker = context.serviceWorkers().find(worker => new URL(worker.url()).origin === deployment.url);
      expect(worker, 'Inject the storage failure into the actual controlling production worker').toBeTruthy();
      await worker.evaluate(({ operation, probe }) => {
        self.pwaCacheFaultHits = 0;
        const failure = () => { self.pwaCacheFaultHits++; throw new DOMException('QA simulated storage failure', 'QuotaExceededError'); };
        if (operation === 'open') {
          const original = CacheStorage.prototype.open;
          CacheStorage.prototype.open = function (name) {
            if (name.startsWith('holdings-static-')) return failure();
            return original.call(this, name);
          };
        } else {
          const original = Cache.prototype[operation];
          Cache.prototype[operation] = function (request, ...args) {
            const url = typeof request === 'string' ? request : request.url;
            if (new URL(url, self.location.origin).pathname === probe) return failure();
            return original.call(this, request, ...args);
          };
        }
      }, { operation, probe });
      const result = await page.evaluate(async path => {
        try {
          const response = await fetch(path);
          return { status: response.status, body: await response.text() };
        } catch (error) { return { error: String(error) }; }
      }, probe);
      expect(await worker.evaluate(() => self.pwaCacheFaultHits), 'The targeted Cache Storage operation must actually fail').toBeGreaterThan(0);
      expect(result, 'Cache Storage is best effort; a good network response must remain usable').toEqual({ status: 200, body: '/* pwa-network-success */' });
      expect(deployment.requests.some(request => request.path === probe && request.status === 200)).toBe(true);
      api.assertPrivatePassThrough(deployment.url);
      expect(deployment.failures).toEqual([]);
    } finally {
      await deployment.close();
    }
  });
}
