import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { MANIFEST_BACKGROUND_COLOR, MANIFEST_THEME_COLOR, THEME_COLORS } from '../lib/pwa-colors.ts';
import { THEME_PREFERENCES, themeInitScript } from '../lib/theme.ts';

const rawTokens = readFileSync(new URL('../app/styles/tokens.css', import.meta.url), 'utf8');
const tokens = rawTokens.replace(/\/\*[\s\S]*?\*\//g, '');
const bg = theme => {
  const block = new RegExp(`html\\[data-theme="${theme}"\\][^{]*\\{([^}]*)\\}`).exec(tokens)?.[1] ?? '';
  return /--bg:\s*(#[0-9a-fA-F]{6})/.exec(block)?.[1].toLowerCase();
};

test('theme-color values equal each theme\'s --bg token in tokens.css', () => {
  const themes = THEME_PREFERENCES.filter(theme => theme !== 'system');
  assert.deepEqual(Object.keys(THEME_COLORS).sort(), [...themes].sort());
  for (const theme of themes) assert.equal(THEME_COLORS[theme].toLowerCase(), bg(theme), `${theme} theme-color matches --bg`);
});

test('manifest colours are the Light theme\'s --bg and are derived from the same table', () => {
  assert.equal(MANIFEST_THEME_COLOR.toLowerCase(), bg('light'));
  assert.equal(MANIFEST_BACKGROUND_COLOR.toLowerCase(), bg('light'));
});

test('the no-flash head script writes the same per-theme colours into the theme-color meta', () => {
  for (const [theme, color] of Object.entries(THEME_COLORS)) assert.ok(themeInitScript.includes(`"${theme}":"${color}"`), `${theme} colour embedded in the init script`);
  assert.ok(themeInitScript.includes('meta[name="theme-color"]'));
});

test('generated public assets use the same init script and light colour', () => {
  const html = readFileSync(new URL('../public/offline.html', import.meta.url), 'utf8');
  assert.ok(html.includes(themeInitScript), 'public/offline.html is stale: run npm run pwa:assets');
  assert.ok(html.includes(`<meta name="theme-color" content="${MANIFEST_THEME_COLOR}">`));
  const css = readFileSync(new URL('../public/offline.css', import.meta.url), 'utf8');
  assert.ok(css.includes(rawTokens), 'public/offline.css is stale (tokens changed): run npm run pwa:assets');
});
