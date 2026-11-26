import { test, expect } from '@playwright/test';
import { assertNoOverflow } from './helpers.mjs';

test('overflow guard rejects zoomed or clipped overflow and allows a contained horizontal scroller', async ({ page }) => {
  const width = page.viewportSize().width;
  const fixture = style => `<meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0} .wide{width:${width + 160}px;height:50px}${style}</style><div class="container"><div class="wide">Oversized content</div></div>`;
  await page.setContent(fixture(''));
  await expect(assertNoOverflow(page, 'expanded viewport fixture')).rejects.toThrow(/div\.wide/);
  // Even if overflow:hidden keeps scrollWidth small, the bounds scan must fail.
  await page.setContent(fixture(`body{overflow-x:hidden}.container{width:${width}px;overflow:hidden}`));
  await expect(assertNoOverflow(page, 'clipped overflow fixture')).rejects.toThrow(/div\.wide/);
  await page.setContent(fixture('.container{position:relative;left:-40px;width:100px;overflow:hidden}.wide{width:100px}'));
  await expect(assertNoOverflow(page, 'left-clipped overflow fixture')).rejects.toThrow(/div\.wide/);
  await page.setContent(fixture(`.container{width:${width}px;margin-left:-40px;overflow-x:auto}`));
  await expect(assertNoOverflow(page, 'left-shifted scroller fixture')).rejects.toThrow(/div\.container/);
  // A user can scroll content inside this fitting container without page scroll.
  await page.setContent(fixture(`.container{width:${width}px;overflow-x:auto}`));
  await assertNoOverflow(page, 'contained scroller fixture');
  // Screen-reader-only headers have no painted area, including descendants.
  await page.setContent(fixture('.container{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0,0,0,0)}'));
  await assertNoOverflow(page, 'zero-area accessibility clip fixture');
  await page.setContent(fixture('.container{position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%)}'));
  await assertNoOverflow(page, 'zero-area clip-path fixture');
});

test('overflow guard respects SVG viewport clipping while rejecting visible paths and oversized SVG roots', async ({ page }) => {
  const width = page.viewportSize().width;
  const fixture = (overflow, svgWidth = width) => `<meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0}</style><svg width="${svgWidth}" height="100" style="overflow:${overflow}"><g><rect x="-160" y="0" width="${width + 320}" height="50"/></g></svg>`;
  await page.setContent(fixture('hidden'));
  await assertNoOverflow(page, 'clipped SVG chart fixture');
  await page.setContent(fixture('clip'));
  await assertNoOverflow(page, 'clip SVG chart fixture');
  await page.setContent(fixture('visible'));
  await expect(assertNoOverflow(page, 'unclipped SVG chart fixture')).rejects.toThrow(/svg|rect/);
  await page.setContent(fixture('hidden', width + 160));
  await expect(assertNoOverflow(page, 'oversized SVG root fixture')).rejects.toThrow(/svg/);
});
