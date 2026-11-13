import { test, expect } from '@playwright/test';
import { themes } from './fixtures.mjs';
import { assertNoOverflow, captureBrowserErrors } from './helpers.mjs';
import { mockSummaryState } from './portfolio-summary-fixtures.mjs';

const states = ['fresh', 'partial', 'loading', 'updating', 'auto', 'cooldown'];
for (const theme of themes) {
  for (const state of states) {
    test(`${theme} portfolio list ${state} state fits and remains usable`, async ({ page }, testInfo) => {
      if (state === 'cooldown') await page.clock.install({ time: new Date('2026-11-13T12:00:00Z') });
      else await page.clock.setFixedTime(new Date('2026-11-13T12:00:00Z'));
      await page.addInitScript(value => localStorage.setItem('holdings.theme', value), theme);
      const fixture = await mockSummaryState(page, state);
      const errors = captureBrowserErrors(page);
      try {
        await page.goto('/');
        const strip = page.getByRole('region', { name: 'All portfolios total' });
        const cards = page.getByTestId('portfolio-card');
        await expect(cards).toHaveCount(3);
        if (state === 'loading') {
          await expect(page.getByTestId('portfolio-metrics-skeleton')).toHaveCount(3);
          await expect(strip).toHaveAttribute('aria-busy', 'true');
          await expect(strip).not.toContainText('0,00');
        } else {
          await expect(strip).toContainText(state === 'partial' ? '1.800,00' : '1.750,00');
          await expect(cards.first()).toContainText('▲ +200,00');
          await expect(cards.nth(2)).toContainText('0,00');
          await expect(cards.nth(2)).not.toContainText('▲');
          await expect(cards.nth(2)).not.toContainText('▼');
        }
        if (state === 'partial') {
          const hint = cards.nth(1).getByRole('button', { name: /Partial figures:/ }).first();
          await expect(cards.nth(1)).toContainText('Unavailable');
          await hint.scrollIntoViewIfNeeded();
          if (page.viewportSize().width <= 640) {
            const box = await hint.boundingBox();
            expect(box.width).toBeGreaterThanOrEqual(44);
            expect(box.height).toBeGreaterThanOrEqual(44);
            await hint.tap();
          } else {
            await hint.focus();
            await page.keyboard.press('Enter');
          }
          await expect(hint).toHaveAttribute('aria-expanded', 'true');
          await expect(hint.getByRole('tooltip')).toBeVisible();
          await assertNoOverflow(page, `${theme}/partial tooltip/${page.viewportSize().width}px`);
        }
        if (state === 'auto' || state === 'updating') {
          await expect(strip.getByRole('status')).toContainText('Updating prices');
          await expect(strip.getByRole('button', { name: 'Updating…' })).toBeDisabled();
          expect(fixture.calls.posts).toEqual(state === 'auto' ? [{}] : []);
          await expect.poll(() => fixture.calls.polls).toBe(1);
        }
        if (state === 'fresh') {
          await expect(cards.nth(1)).toContainText('▼ -50,00');
          await expect(strip.getByRole('button', { name: 'Refresh prices (3 Yahoo requests)' })).toBeEnabled();
          expect(fixture.calls.posts).toEqual([]);
        }
        if (state === 'cooldown') {
          await expect(strip).toContainText('Yahoo is cooling down');
          await expect(strip.getByRole('button', { name: /Refresh prices/ })).toBeDisabled();
          expect(fixture.calls.posts).toEqual([]);
        }
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({ path: testInfo.outputPath(`${theme}-list-${state}.png`), fullPage: true, animations: 'disabled' });
        await assertNoOverflow(page, `${theme}/portfolio list/${state}`);
        if (state === 'partial') {
          const hint = cards.nth(1).getByRole('button', { name: /Partial figures:/ }).first();
          await page.keyboard.press('Escape');
          await expect(hint).toHaveAttribute('aria-expanded', 'false');
          await expect(hint.getByRole('tooltip')).toBeHidden();
        }
        if (state === 'loading') {
          fixture.releaseLoading();
          await expect(strip).toContainText('1.750,00');
          await expect(page.getByTestId('portfolio-metrics-skeleton')).toHaveCount(0);
        }
        if (state === 'cooldown') {
          fixture.expireCooldown();
          await page.clock.fastForward(15 * 60 * 1000 + 1000);
          const refresh = strip.getByRole('button', { name: /Refresh prices/ });
          await expect(refresh).toBeEnabled();
          expect(fixture.calls.posts).toEqual([]);
          await refresh.click();
          await expect.poll(() => fixture.calls.posts).toEqual([{ force: true }]);
          fixture.finishJob();
          await expect(strip).toContainText('1.780,00');
        }
        if (state === 'auto' || state === 'updating') {
          fixture.finishJob();
          await expect(strip).toContainText('1.780,00');
          await expect(strip).toHaveAttribute('aria-busy', 'false');
          expect(fixture.calls.posts).toEqual(state === 'auto' ? [{}] : []);
        }
        expect(fixture.unexpected).toEqual([]);
        expect(errors).toEqual([]);
      } finally { fixture.dispose(); }
    });
  }
}
