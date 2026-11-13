import { test, expect } from '@playwright/test';
import { mockAPI, themes } from './fixtures.mjs';
import { assertNoOverflow, captureBrowserErrors, ready, screens } from './helpers.mjs';

// Compact acceptance-boundary coverage for the densest populated screens.
for (const theme of themes) {
  for (const screen of screens.filter(({ name }) => ['dashboard', 'transactions', 'import'].includes(name))) {
    test(`${theme} ${screen.name} fits at 360px`, async ({ page }, testInfo) => {
      await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
      await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
      const unexpected = await mockAPI(page);
      const errors = captureBrowserErrors(page);
      await page.goto(screen.path);
      await ready(page, screen.name);
      await page.screenshot({ path: testInfo.outputPath(`${theme}-${screen.name}-360.png`), fullPage: true, animations: 'disabled' });
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
      await assertNoOverflow(page, `${theme}/${screen.name}/360px`);
      expect(unexpected).toEqual([]);
      expect(errors).toEqual([]);
    });
  }
}
