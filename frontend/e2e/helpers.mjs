import { expect } from '@playwright/test';
import { csvFile, portfolio } from './fixtures.mjs';

export const screens = [
  { name: 'login', path: '/', loggedIn: false },
  { name: 'portfolios', path: '/' },
  ...['dashboard', 'transactions', 'dividends', 'import', 'items'].map(tab => ({ name: tab, path: `/portfolios/1/${tab}` })),
];

export async function ready(page, name) {
  if (name === 'login') await expect(page.getByRole('button', { name: /Google/ })).toBeVisible();
  else if (name === 'portfolios') await expect(page.getByRole('link', { name: new RegExp(portfolio.name) })).toBeVisible();
  else {
    await expect(page.getByRole('heading', { name: portfolio.name, exact: true })).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Portfolio', exact: true }).locator('[aria-current="page"]')).toHaveAttribute('href', `/portfolios/1/${name}`);
    if (name === 'dashboard') await expect(page.getByLabel('Portfolio figures')).toBeVisible();
    if (name === 'transactions') await expect(page.getByRole('button', { name: 'Commit transaction' })).toBeVisible();
    if (name === 'dividends') await expect(page.getByRole('button', { name: 'Previous year' })).toBeEnabled();
    if (name === 'import') {
      await page.getByLabel('CSV file').setInputFiles({ name: 'holdings-smoke.csv', mimeType: 'text/csv', buffer: Buffer.from(csvFile) });
      await expect(page.getByRole('checkbox', { name: /Include row 2/ })).toBeVisible();
    }
    if (name === 'items') await expect(page.getByLabel('Pay-date delay for HMWO.L in days')).toHaveValue('30');
  }
  await page.evaluate(() => document.fonts.ready);
}

export function captureBrowserErrors(page) {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() !== 'error') return;
    const text = message.text();
    const url = message.location().url;
    // A logged-out /auth/me and /auth/refresh intentionally return 401. Only
    // ignore that exact resource error; hydration failures must remain visible.
    if (/^Failed to load resource: the server responded with a status of 401\b/.test(text)
      && /\/auth\/(?:me|refresh)(?:\?|$)/.test(url)) return;
    errors.push(`${text}${url ? ` (${url})` : ''}`);
  });
  return errors;
}

export async function assertNoOverflow(page, label) {
  // Mobile Chrome can enlarge innerWidth to fit overflowing content. Compare
  // against the configured device width, which cannot grow with that content.
  const width = page.viewportSize()?.width;
  expect(width, 'The smoke harness must configure a viewport').toBeGreaterThan(0);
  const dimensions = await page.evaluate(viewportWidth => {
    const selector = element => {
      const tag = element.tagName.toLowerCase();
      if (element.id) return `${tag}#${CSS.escape(element.id)}`;
      if (element.getAttribute('data-testid')) return `${tag}[data-testid=${JSON.stringify(element.getAttribute('data-testid'))}]`;
      if (element.getAttribute('aria-label')) return `${tag}[aria-label=${JSON.stringify(element.getAttribute('aria-label'))}]`;
      const classes = [...element.classList].slice(0, 3).map(name => `.${CSS.escape(name)}`).join('');
      return `${tag}${classes}`;
    };
    const offenders = [];
    for (const element of document.querySelectorAll('body *')) {
      const rect = element.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0 || (rect.left >= -1 && rect.right <= viewportWidth + 1)) continue;
      let hidden = false;
      let scrollsInsideViewport = false;
      for (let ancestor = element; ancestor; ancestor = ancestor.parentElement) {
        const style = getComputedStyle(ancestor);
        if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse' || Number(style.opacity) === 0) hidden = true;
        // The scroll container itself must fit. An oversized scroll container
        // is page overflow and is reported in its own iteration.
        const ancestorBounds = ancestor.getBoundingClientRect();
        if (ancestor !== element && /^(auto|scroll)$/.test(style.overflowX)
          && ancestorBounds.left >= -1 && ancestorBounds.right <= viewportWidth + 1) scrollsInsideViewport = true;
      }
      if (!hidden && !scrollsInsideViewport) {
        offenders.push({ selector: `${element.parentElement ? `${selector(element.parentElement)} > ` : ''}${selector(element)}`, left: Math.round(rect.left * 100) / 100, right: Math.round(rect.right * 100) / 100, width: Math.round(rect.width * 100) / 100 });
      }
    }
    return { scroll: document.documentElement.scrollWidth, inner: innerWidth, offenders };
  }, width);
  const details = `${label}: configured width ${width}px, scrollWidth ${dimensions.scroll}px, innerWidth ${dimensions.inner}px; overflowing elements:\n${JSON.stringify(dimensions.offenders.slice(0, 30), null, 2)}`;
  expect(dimensions.scroll <= width && dimensions.offenders.length === 0, details).toBe(true);
}
