import { defineConfig } from '@playwright/test';

// Set PLAYWRIGHT_BASE_URL to use a server you already started. Otherwise the
// harness owns a Next dev server and never needs an API server or Google login.
const externalURL = process.env.PLAYWRIGHT_BASE_URL;
const port = Number(process.env.PLAYWRIGHT_PORT ?? 3100);
const baseURL = externalURL ?? `http://127.0.0.1:${port}`;

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  workers: 2,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  outputDir: './test-results',
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    browserName: 'chromium',
    locale: 'en-GB',
    timezoneId: 'Europe/Berlin',
    colorScheme: 'light',
    reducedMotion: 'reduce',
    serviceWorkers: 'block',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'mobile', testIgnore: ['**/boundary.spec.mjs', '**/pwa/**'], use: { viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true } },
    { name: 'desktop', testIgnore: ['**/boundary.spec.mjs', '**/pwa/**'], use: { viewport: { width: 1280, height: 800 } } },
    { name: 'mobile-narrow', testMatch: ['**/boundary.spec.mjs', '**/portfolio-summaries.spec.mjs'], use: { viewport: { width: 360, height: 812 }, isMobile: true, hasTouch: true } },
    // These require next build/start or Docker, never the owned dev server.
    // Ordinary projects retain serviceWorkers:'block', including with an URL.
    ...(externalURL ? [360, 375, 1280].map(width => ({
      name: `pwa-${width}`,
      testMatch: '**/pwa/*.spec.mjs',
      use: {
        viewport: { width, height: width === 1280 ? 800 : 812 },
        isMobile: width < 640,
        hasTouch: width < 640,
        serviceWorkers: 'allow',
      },
    })) : []),
  ],
  webServer: externalURL ? undefined : {
    command: `npm run dev -- --hostname 127.0.0.1 --port ${port}`,
    url: baseURL,
    reuseExistingServer: false,
    timeout: 120_000,
    env: { NEXT_PUBLIC_API_URL: '', NEXT_TELEMETRY_DISABLED: '1' },
  },
});
