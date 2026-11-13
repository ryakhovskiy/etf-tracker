import assert from 'node:assert/strict';
import { afterEach, beforeEach, test } from 'node:test';
import { JSDOM } from 'jsdom';
import React from 'react';

const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/', runScripts: 'outside-only' });
for (const key of ['window', 'document', 'HTMLElement', 'Node', 'MutationObserver']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
const { render, screen, fireEvent, cleanup, act, waitFor } = await import('@testing-library/react');
const { ThemeSwitcher } = await import('../components/theme-switcher.tsx');
const { ThemeSync } = await import('../components/theme-sync.tsx');
const { THEME_STORAGE_KEY, themeInitScript, setThemePreference, readStoredPreference } = await import('../lib/theme.ts');
const choices = ['system', 'light', 'dark', 'tech-green', 'tech-cappuccino', 'tech-red'];
let media;

beforeEach(() => {
  window.localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  document.documentElement.removeAttribute('data-theme-pref');
  const listeners = new Set();
  media = {
    matches: false,
    media: '(prefers-color-scheme: dark)',
    addEventListener: (type, listener) => { if (type === 'change') listeners.add(listener); },
    removeEventListener: (type, listener) => { if (type === 'change') listeners.delete(listener); },
    change(dark) { this.matches = dark; for (const listener of listeners) listener({ matches: dark }); },
  };
  window.matchMedia = () => media;
});
afterEach(() => cleanup());

const init = () => window.eval(themeInitScript);
function applied(theme, preference = theme) {
  assert.equal(document.documentElement.getAttribute('data-theme'), theme);
  assert.equal(document.documentElement.getAttribute('data-theme-pref'), preference);
}
function mount() {
  return render(React.createElement(React.Fragment, null, React.createElement(ThemeSync), React.createElement(ThemeSwitcher)));
}

test('the pre-paint script defaults to System and resolves both OS colour schemes', () => {
  for (const dark of [false, true]) {
    media.matches = dark;
    init();
    applied(dark ? 'dark' : 'light', 'system');
    assert.equal(window.localStorage.getItem(THEME_STORAGE_KEY), null);
  }
});

test('all five explicit themes survive a new page initialization regardless of OS preference', () => {
  media.matches = true;
  for (const preference of choices.slice(1)) {
    setThemePreference(preference);
    assert.equal(window.localStorage.getItem(THEME_STORAGE_KEY), preference);
    document.documentElement.removeAttribute('data-theme');
    document.documentElement.removeAttribute('data-theme-pref');
    init();
    applied(preference);
    assert.equal(readStoredPreference(), preference);
  }
});

test('invalid or empty stored preferences fall back to System in the head script and runtime', () => {
  media.matches = true;
  for (const invalid of ['', 'sepia', 'DARK', 'null', 'constructor', '__proto__']) {
    window.localStorage.setItem(THEME_STORAGE_KEY, invalid);
    assert.equal(readStoredPreference(), 'system');
    init();
    applied('dark', 'system');
  }
});

test('denied storage and a throwing OS query do not prevent initial rendering or selection', async t => {
  t.mock.getter(window, 'localStorage', () => { throw new window.DOMException('Denied', 'SecurityError'); });
  t.mock.method(window, 'matchMedia', () => { throw new Error('No media query API'); });
  assert.doesNotThrow(init);
  applied('light', 'system');
  assert.equal(readStoredPreference(), 'system');
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Theme: System' }));
  fireEvent.click(screen.getByRole('radio', { name: 'Tech Green', exact: true }));
  await waitFor(() => assert.equal(screen.getByRole('radio', { name: 'Tech Green', exact: true }).getAttribute('aria-checked'), 'true'));
  applied('tech-green');
});

test('a failed storage write still applies the chosen theme and updates radio state', async t => {
  init();
  t.mock.method(window.Storage.prototype, 'setItem', () => { throw new window.DOMException('Quota full', 'QuotaExceededError'); });
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Theme: System' }));
  fireEvent.click(screen.getByRole('radio', { name: 'Tech Red', exact: true }));
  await waitFor(() => assert.equal(screen.getByRole('radio', { name: 'Tech Red', exact: true }).getAttribute('aria-checked'), 'true'));
  applied('tech-red');
});

test('System follows live OS changes, while an explicit theme ignores them', async () => {
  init();
  const view = mount();
  act(() => media.change(true));
  applied('dark', 'system');
  act(() => media.change(false));
  applied('light', 'system');
  act(() => setThemePreference('tech-cappuccino'));
  act(() => media.change(true));
  applied('tech-cappuccino');
  act(() => setThemePreference('system'));
  applied('dark', 'system');
  view.unmount();
  media.change(false);
  applied('dark', 'system');
});

test('preferences changed or cleared in another tab update the HTML theme and switcher', async () => {
  init();
  mount();
  act(() => {
    window.localStorage.setItem(THEME_STORAGE_KEY, 'tech-green');
    window.dispatchEvent(new window.StorageEvent('storage', { key: THEME_STORAGE_KEY, newValue: 'tech-green' }));
  });
  applied('tech-green');
  await screen.findByRole('button', { name: 'Theme: Tech Green' });
  act(() => {
    window.localStorage.clear();
    window.dispatchEvent(new window.StorageEvent('storage', { key: null }));
  });
  applied('light', 'system');
  await screen.findByRole('button', { name: 'Theme: System' });
});

test('the switcher exposes six exclusive radios and supports arrows, Home, End, Escape and focus return', async () => {
  init();
  mount();
  const trigger = screen.getByRole('button', { name: 'Theme: System' });
  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
  fireEvent.click(trigger);
  const group = screen.getByRole('radiogroup', { name: 'Theme', exact: true });
  const radios = screen.getAllByRole('radio');
  assert.equal(radios.length, 6);
  assert.ok(document.activeElement === radios[0]);
  assert.deepEqual(radios.map(radio => radio.getAttribute('aria-checked')), ['true', 'false', 'false', 'false', 'false', 'false']);
  async function key(value, index) {
    fireEvent.keyDown(group, { key: value });
    await waitFor(() => assert.equal(radios[index].getAttribute('aria-checked'), 'true'));
    assert.equal(radios.filter(radio => radio.getAttribute('aria-checked') === 'true').length, 1);
    assert.equal(radios[index].tabIndex, 0);
    assert.ok(document.activeElement === radios[index]);
    assert.equal(window.localStorage.getItem(THEME_STORAGE_KEY), choices[index]);
    applied(choices[index] === 'system' ? 'light' : choices[index], choices[index]);
  }
  await key('ArrowRight', 1);
  await key('ArrowDown', 2);
  await key('ArrowLeft', 1);
  await key('ArrowUp', 0);
  await key('ArrowUp', 5);
  await key('Home', 0);
  await key('End', 5);
  fireEvent.keyDown(group, { key: 'Escape' });
  assert.ok(screen.queryByRole('radiogroup', { name: 'Theme' }) === null);
  assert.equal(trigger.getAttribute('aria-expanded'), 'false');
  assert.ok(document.activeElement === trigger);
});
