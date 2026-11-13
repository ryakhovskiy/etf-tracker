import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { checkThemeContrast, parseThemes, ratio } from '../scripts/check-theme-contrast.mjs';

const css = readFileSync(new URL('../app/styles/tokens.css', import.meta.url), 'utf8');

test('all five themes meet WCAG AA text (4.5:1) and UI (3:1) contrast', () => {
  const themes = [...css.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/html\[data-theme="([a-z-]+)"\]\s*(?:,[^{]*)?\{([^}]*)\}/g)].map(match => match[1]).sort();
  // The checker must never pass vacuously when a selector or token file changes.
  assert.deepEqual(themes, ['dark', 'light', 'tech-cappuccino', 'tech-green', 'tech-red']);
  // One authoritative parser/rule list, shared with the frontend's verifier.
  // Covers body/muted/link/status text, button text, borders, focus and charts.
  const result = checkThemeContrast(css);
  assert.equal(result.themeCount, 5);
  assert.deepEqual([...result.themes].sort(), themes);
  assert.deepEqual(result.failures, []);
});

test('the contrast verifier rejects unreadable text and an invisible focus ring', () => {
  // Exercise the actual checker against deliberately broken token fixtures.
  const unreadable = css.replace(/--text:\s*#[a-f\d]{6}/i, '--text: #ffffff');
  assert.ok(checkThemeContrast(unreadable).failures.some(failure => /--text on --surface/.test(failure)));
  const invisibleFocus = css.replace(/--focus-ring:\s*#[a-f\d]{6}/i, '--focus-ring: #ffffff');
  assert.ok(checkThemeContrast(invisibleFocus).failures.some(failure => /--focus-ring on --surface/.test(failure)));
  assert.equal(ratio('#000000', '#ffffff'), 21);
  assert.equal(ratio('#ffffff', '#ffffff'), 1);
});

test('dividend chart ex-date shades retain 3:1 contrast after their configured opacity', () => {
  const themes = parseThemes(css);
  const failures = [];
  for (const match of css.matchAll(/html\[data-theme="([a-z-]+)"\]\s*(?:,[^{]*)?\{([^}]*)\}/g)) {
    const [, name, block] = match;
    const alpha = Number(/--div-ex-opacity:\s*([\d.]+)/.exec(block)?.[1]);
    assert.ok(Number.isFinite(alpha) && alpha >= 0 && alpha <= 1, `${name}: ex-date opacity is defined`);
    const background = themes[name]['--surface'];
    for (const shade of 'abcde') {
      const foreground = themes[name][`--div-ex-${shade}`];
      const composite = '#' + [1, 3, 5].map(index => Math.round(parseInt(foreground.slice(index, index + 2), 16) * alpha + parseInt(background.slice(index, index + 2), 16) * (1 - alpha)).toString(16).padStart(2, '0')).join('');
      const contrast = ratio(composite, background);
      if (contrast < 3) failures.push(`${name} --div-ex-${shade} at opacity ${alpha}: ${contrast.toFixed(2)}:1 on --surface`);
    }
  }
  assert.deepEqual(failures, [], 'BUG: faded dividend chart marks must meet UI contrast after compositing');
});
