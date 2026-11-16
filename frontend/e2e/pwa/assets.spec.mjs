import { test, expect } from '@playwright/test';
import sharp from 'sharp';
import { countDocuments, documentCount, mockPwaAPI, openControlled, settledFrames } from './helpers.mjs';

test('production manifest, icons, worker headers and Chromium installability', async ({ page, context, request }) => {
  const api = await mockPwaAPI(page, context);
  await countDocuments(page);
  await openControlled(page);
  await settledFrames(page);
  expect(await documentCount(page), 'First install must never reload the document').toBe(1);
  await expect(page.getByTestId('update-notice')).toHaveCount(0);

  const link = page.locator('link[rel="manifest"]');
  await expect(link).toHaveCount(1);
  const manifestURL = new URL(await link.getAttribute('href'), page.url()).href;
  expect(new URL(manifestURL).pathname).toBe('/manifest.webmanifest');
  const response = await request.get(manifestURL);
  expect(response.status()).toBe(200);
  expect(response.headers()['content-type']).toMatch(/application\/(?:manifest\+json|json)/);
  const manifest = await response.json();
  expect(manifest).toMatchObject({ id: '/', name: 'Holdings', short_name: 'Holdings', start_url: '/', scope: '/', display: 'standalone' });
  expect(manifest.description?.length).toBeGreaterThan(0);
  expect(manifest.orientation, 'Do not lock the device orientation').toBeUndefined();
  expect(manifest.theme_color).toMatch(/^#[\da-f]{6}$/i);
  expect(manifest.background_color).toMatch(/^#[\da-f]{6}$/i);
  for (const [size, purpose] of [[192, 'any'], [512, 'any'], [512, 'maskable']]) {
    const icon = manifest.icons.find(icon => icon.sizes === `${size}x${size}` && (icon.purpose || 'any').split(/\s+/).includes(purpose));
    expect(icon, `${size}px ${purpose} icon must be declared`).toBeTruthy();
    expect(icon.type).toBe('image/png');
  }
  for (const icon of manifest.icons) {
    const iconURL = new URL(icon.src, manifestURL);
    expect(iconURL.origin).toBe(new URL(page.url()).origin);
    const iconResponse = await request.get(iconURL.href);
    expect(iconResponse.status(), icon.src).toBe(200);
    expect(iconResponse.headers()['content-type'].split(';')[0]).toBe(icon.type);
    const bytes = await iconResponse.body();
    const metadata = await sharp(bytes).metadata();
    expect(`${metadata.width}x${metadata.height}`, icon.src).toBe(icon.sizes);
    expect(metadata.format).toBe('png');
    if ((icon.purpose || '').includes('maskable')) {
      const { data, info } = await sharp(bytes).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
      const backdrop = [...data.subarray(0, 3)];
      let glyphPixels = 0;
      let outsideSafeCircle = 0;
      let translucentPixels = 0;
      for (let y = 0; y < info.height; y++) for (let x = 0; x < info.width; x++) {
        const offset = (y * info.width + x) * 4;
        if (data[offset + 3] !== 255) translucentPixels++;
        // The generated two-colour brand mark has a full-bleed background.
        // Ignore tiny antialiasing differences; all visible glyph pixels must
        // fit the W3C 80%-diameter maskable safe circle.
        if (backdrop.some((channel, index) => Math.abs(channel - data[offset + index]) > 16)) {
          glyphPixels++;
          if (Math.hypot(x + 0.5 - info.width / 2, y + 0.5 - info.height / 2) > info.width * 0.4) outsideSafeCircle++;
        }
      }
      expect(glyphPixels).toBeGreaterThan(100);
      expect(translucentPixels, 'Maskable icons must be opaque').toBe(0);
      expect(outsideSafeCircle, 'Maskable brand glyph must fit the 80% safe zone').toBe(0);
    }
  }
  const appleLink = page.locator('link[rel="apple-touch-icon"]').first();
  const apple = await request.get(new URL(await appleLink.getAttribute('href'), page.url()).href);
  expect(apple.status()).toBe(200);
  expect(apple.headers()['content-type']).toMatch(/^image\/png/);
  const appleBytes = await apple.body();
  const appleMeta = await sharp(appleBytes).metadata();
  expect([appleMeta.width, appleMeta.height]).toEqual([180, 180]);
  expect((await sharp(appleBytes).stats()).isOpaque).toBe(true);
  for (const [url, type, size] of [['/favicon.svg', 'image/svg+xml', null], ['/favicon-32.png', 'image/png', 32]]) {
    expect(await page.locator('link[rel="icon"]').evaluateAll(links => links.map(link => new URL(link.href).pathname))).toContain(url);
    const icon = await request.get(new URL(url, page.url()).href);
    expect(icon.status()).toBe(200);
    expect(icon.headers()['content-type']).toContain(type);
    if (size) {
      const metadata = await sharp(await icon.body()).metadata();
      expect([metadata.width, metadata.height]).toEqual([size, size]);
    }
  }
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute('content', 'yes');
  await expect(page.locator('meta[name="apple-mobile-web-app-title"]')).toHaveAttribute('content', 'Holdings');
  await expect(page.locator('meta[name="apple-mobile-web-app-status-bar-style"]')).toHaveAttribute('content', /default|black|black-translucent/);
  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', /viewport-fit=cover/);

  const worker = await request.get(new URL('/sw.js', page.url()).href);
  expect(worker.status()).toBe(200);
  expect(worker.headers()['content-type']).toMatch(/(?:application|text)\/javascript/);
  for (const directive of ['no-cache', 'no-store', 'must-revalidate']) expect(worker.headers()['cache-control']).toContain(directive);
  const csp = worker.headers()['content-security-policy'];
  expect(csp).toMatch(/(?:^|;)\s*default-src\s+'self'\s*(?:;|$)/);
  expect(csp).toMatch(/(?:^|;)\s*script-src\s+'self'\s*(?:;|$)/);
  const workerSource = await worker.text();
  const version = /const VERSION\s*=\s*(["'])([^"']*)\1/.exec(workerSource)?.[2];
  expect(version, 'The unmodified production worker must embed a nonempty build identity').toBeTruthy();
  expect(version).not.toMatch(/^(?:dev|development|__BUILD_ID__)$/i);
  if (process.env.PLAYWRIGHT_EXPECTED_BUILD_ID) expect(version).toBe(process.env.PLAYWRIGHT_EXPECTED_BUILD_ID);

  const cdp = await context.newCDPSession(page);
  try {
    await cdp.send('Page.enable');
    await expect.poll(async () => (await cdp.send('Page.getAppManifest')).url).toBe(manifestURL);
    expect((await cdp.send('Page.getAppManifest')).errors).toEqual([]);
    await expect.poll(async () => (await cdp.send('Page.getInstallabilityErrors')).installabilityErrors,
      { message: 'Production must be installable according to Chromium, without ignored error IDs', timeout: 20_000 }).toEqual([]);
  } finally {
    await cdp.detach();
  }
  api.assertPrivatePassThrough(new URL(page.url()).origin);
});
