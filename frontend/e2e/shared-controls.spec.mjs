import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { assertNoOverflow, captureBrowserErrors, ready } from './helpers.mjs';

for (const theme of themes) {
  test(`${theme} page-size and field chooser controls remain usable at this viewport`, async ({ page }) => {
    await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
    await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
    const unexpected = await mockAPI(page);
    const errors = captureBrowserErrors(page);
    await page.goto('/portfolios/1/dashboard');
    await ready(page, 'dashboard');
    const size = page.getByRole('combobox', { name: /(?:Rows|Items) per page/ });
    await size.selectOption('10');
    await expect(size).toHaveValue('10');
    // Chromium exposes native <summary> as generic in this build; use its
    // semantic element and text, without depending on a presentation class.
    const chooser = page.locator('summary').filter({ hasText: /^(?:Columns|Fields(?: shown)?)$/i });
    await expect(chooser).toBeVisible();
    await chooser.click();
    const reset = page.getByRole('button', { name: /^Reset (?:columns|fields)$/i });
    await expect(reset).toBeVisible();
    const move = page.getByRole('button', { name: /^Move / });
    expect(await move.count()).toBeGreaterThan(0);
    if (page.viewportSize().width <= 640) {
      const boxes = [
        { name: 'page size', ...await size.boundingBox() },
        { name: 'field chooser', ...await chooser.boundingBox() },
        { name: 'reset fields', ...await reset.boundingBox() },
        ...await move.evaluateAll(elements => elements.filter(element => !element.disabled).map(element => ({ name: element.getAttribute('aria-label'), width: element.getBoundingClientRect().width, height: element.getBoundingClientRect().height }))),
      ];
      for (const box of boxes) {
        expect.soft(box.width, `${theme}: ${box.name} touch width`).toBeGreaterThanOrEqual(44);
        expect.soft(box.height, `${theme}: ${box.name} touch height`).toBeGreaterThanOrEqual(44);
      }
    }
    const date = page.getByRole('checkbox', { name: 'Date', exact: true });
    await date.uncheck();
    await expect(date).not.toBeChecked();
    await reset.click();
    await expect(date).toBeChecked();
    await assertNoOverflow(page, `${theme}/field chooser open`);
    expect(unexpected).toEqual([]);
    expect(errors).toEqual([]);
  });
}
