import assert from 'node:assert/strict';
import { test, afterEach } from 'node:test';
import { readFileSync } from 'node:fs';
import { JSDOM } from 'jsdom';
import React from 'react';
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'http://localhost/' });
for (const key of ['window', 'document', 'HTMLElement', 'SVGElement', 'Node', 'MutationObserver', 'HTMLDialogElement', 'sessionStorage', 'KeyboardEvent', 'MouseEvent']) globalThis[key] = dom.window[key];
globalThis.self = dom.window;
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
const { render, screen, fireEvent, waitFor, cleanup, within } = await import('@testing-library/react');
const { AppRouterContext } = await import('next/dist/shared/lib/app-router-context.shared-runtime.js');
const { SearchParamsContext, PathnameContext } = await import('next/dist/shared/lib/hooks-client-context.shared-runtime.js');
const { RouterContext } = await import('next/dist/shared/lib/router-context.shared-runtime.js');
const { PortfolioUpdatesProvider } = await import('../components/portfolio-updates.tsx');
const { Dividends } = await import('../components/dividends.tsx');
const { PortfolioWorkspace } = await import('../components/portfolio-workspace.tsx');
const { monthName, perUnit, percent, listingAmount, fxLabel, exLabel } = await import('../lib/dividends.ts');
afterEach(() => cleanup());

const json = (body, status = 200) => new Response(JSON.stringify(body), { status });
const berlin = options => new Intl.DateTimeFormat('sv-SE', { timeZone: 'Europe/Berlin', ...options }).format(new Date());
const Y = Number(berlin({ year: 'numeric' }));
const M = Number(berlin({ month: '2-digit' }));
const css = readFileSync(new URL('../app/globals.css', import.meta.url), 'utf8');

// Without a pay-date delay the pay-date is the ex-date; actual events are paid, forecasts expected.
const entry = (over = {}) => {
 const e = {
  instrumentId: 3, ticker: 'ISPA.DE', isin: 'DE000A0F5UH1', shortName: 'iShares STOXX Global Select Dividend 100 UCITS ETF (DE)',
  kind: 'ACTUAL', exDate: `${Y}-05-14`, basisExDate: null, perUnitOrig: '0.412300', currency: 'EUR', perUnitEur: '0.4123',
  fxRate: null, fxRateDate: null, yieldPct: '1.38', close: '29.8800', closeDate: null, units: '120.5', total: '49.68', held: true, warning: null,
  payDateShifted: false, payDelayDays: 0, ...over,
 };
 return { payDate: e.exDate, paid: e.kind === 'ACTUAL', ...e };
};
// Payments go to the pay-date month, every dividend to its ex-date month's markers.
function calendar(year = Y, entries = [], over = {}) {
 const months = Array.from({ length: 12 }, (_, i) => ({ month: i + 1, total: '0.00', entries: [], exTotal: '0.00', exEntries: [] }));
 for (const e of entries) {
  if (e.payDate.startsWith(String(year))) months[Number(e.payDate.slice(5, 7)) - 1].entries.push(e);
  if (e.exDate.startsWith(String(year))) months[Number(e.exDate.slice(5, 7)) - 1].exEntries.push(e);
 }
 return { year, bounds: { minYear: Y - 2, maxYear: Y + 1 }, totals: { received: '412.18', expected: '198.40', total: '610.58' }, months,
  sync: { provider: 'YAHOO', lastSyncedAt: '2026-11-04T13:32:00Z', stale: [], errors: [], jobId: null }, ...over };
}
const tree = ({ search = '', router = { push() {} }, tab = 'dividends' } = {}) => React.createElement(AppRouterContext.Provider, { value: router },
 React.createElement(SearchParamsContext.Provider, { value: new URLSearchParams(search) },
  React.createElement(PortfolioUpdatesProvider, { activeTab: tab }, React.createElement(Dividends, { id: '1' }))));
const yearOf = url => Number(new URL(url, 'http://x').searchParams.get('year'));
// Amounts use no-break spaces before € and %; compare with plain spaces.
const text = node => node.textContent.replace(/\u00a0/g, ' ');
// The chart repeats tickers and month names: look inside the calendar grid only.
const gridHas = value => waitFor(() => assert.ok(document.querySelector('.dividend-grid')?.textContent.includes(value), `${value} in the calendar`), { timeout: 5000 });

test('formats amounts, dates and month names in de-DE', () => {
 assert.deepEqual(Array.from({ length: 12 }, (_, i) => monthName(i + 1)), ['Januar', 'Februar', 'März', 'April', 'Mai', 'Juni', 'Juli', 'August', 'September', 'Oktober', 'November', 'Dezember']);
 assert.equal(perUnit('1234.5'), '1.234,5000\u00a0€');
 assert.equal(percent('1.38'), '1,38\u00a0%');
 assert.equal(listingAmount('0.412300', 'EUR'), '0,4123 EUR');
 assert.equal(listingAmount('39.820000', 'GBp'), '39,82 GBp');
 assert.equal(fxLabel('1.18340000', 'GBp'), '1 GBP = 1,1834 €');
 assert.equal(exLabel('2026-05-14'), 'Ex 14.05.');
});

test('loads only when the tab is open, then navigates years within the bounds and keeps the year in the URL', async t => {
 const requested = []; const pushes = [];
 t.mock.method(globalThis, 'fetch', async url => { requested.push(yearOf(url)); return json(calendar(yearOf(url))); });
 const router = { push: path => pushes.push(path) };
 const view = render(tree({ tab: 'dashboard', router, search: `year=${Y - 2}` }));
 await new Promise(resolve => setTimeout(resolve, 20));
 assert.equal(requested.length, 0);
 view.rerender(tree({ router, search: `year=${Y - 2}` }));
 await screen.findByRole('heading', { name: String(Y - 2) });
 await waitFor(() => assert.equal(screen.getByRole('button', { name: 'Previous year' }).disabled, true));
 assert.equal(screen.getByRole('button', { name: 'Next year' }).disabled, false);
 assert.deepEqual(requested, [Y - 2]);
 fireEvent.click(screen.getByRole('button', { name: 'Next year' }));
 assert.deepEqual(pushes, [`/portfolios/1/dividends?year=${Y - 1}`]);
 await screen.findByRole('heading', { name: String(Y - 1) });
 await waitFor(() => assert.equal(requested.at(-1), Y - 1));
 // A reload or link with the last year: next is disabled at the upper bound.
 view.rerender(tree({ router, search: `year=${Y + 1}` }));
 await screen.findByRole('heading', { name: String(Y + 1) });
 await waitFor(() => assert.equal(screen.getByRole('button', { name: 'Next year' }).disabled, true));
 assert.equal(screen.getByRole('button', { name: 'Previous year' }).disabled, false);
 // Back navigation changes the URL back: the year follows.
 view.rerender(tree({ router, search: `year=${Y - 1}` }));
 await screen.findByRole('heading', { name: String(Y - 1) });
 await waitFor(() => assert.equal(requested.at(-1), Y - 1));
 // Without a year in the URL, the current year is shown.
 view.rerender(tree({ router }));
 await screen.findByRole('heading', { name: String(Y) });
});

test('12 month cards in a 4/2/1 column grid with the current month highlighted', async t => {
 t.mock.method(globalThis, 'fetch', async url => json(calendar(yearOf(url))));
 const view = render(tree());
 await screen.findByText('Januar');
 const cards = view.container.querySelectorAll('.dividend-grid > li.month-card');
 assert.equal(cards.length, 12);
 assert.deepEqual([...cards].map(c => c.querySelector('h3').textContent), Array.from({ length: 12 }, (_, i) => monthName(i + 1)));
 const current = view.container.querySelectorAll('.month-card.current');
 assert.equal(current.length, 1);
 assert.equal(current[0].getAttribute('aria-current'), 'date');
 assert.equal(current[0].querySelector('h3').textContent, monthName(M));
 assert.match(css, /\.dividend-grid\{[^}]*grid-template-columns:repeat\(4,minmax\(0,1fr\)\)/);
 assert.match(css, /@media\(max-width:1000px\)\{\.dividend-grid\{grid-template-columns:repeat\(2,minmax\(0,1fr\)\)\}\}/);
 assert.match(css, /@media\(max-width:600px\)\{\.dividend-grid\{grid-template-columns:minmax\(0,1fr\)\}/);
 assert.match(css, /\.month-card\.current\{border:2px solid var\(--div-accent\)/);
 // Another year: no highlight.
 cleanup();
 const other = render(tree({ search: `year=${Y - 1}` }));
 await screen.findByText('Januar');
 assert.equal(other.container.querySelectorAll('.month-card.current').length, 0);
});

test('entries show ticker or ISIN, short name, ex date, yield, per-unit value, units and total in de-DE', async t => {
 const entries = [
  entry(),
  entry({ instrumentId: 4, ticker: 'IE00B8GKDB10', isin: 'IE00B8GKDB10', shortName: 'Vanguard FTSE All-World High Dividend Yield', exDate: `${Y}-05-20`, currency: 'USD', perUnitOrig: '0.519077', perUnitEur: '0.4792', fxRate: '0.92310000', fxRateDate: `${Y}-05-20`, yieldPct: null, units: '1234.5', total: '1591.57' }),
  entry({ instrumentId: 5, ticker: 'XYZ', exDate: `${Y}-05-21`, currency: 'XYZ', perUnitEur: null, total: null, warning: 'FX_UNSUPPORTED_CURRENCY' }),
 ];
 const cal = calendar(Y, entries);
 cal.months[4].total = '1641.25';
 t.mock.method(globalThis, 'fetch', async () => json(cal));
 render(tree());
 const may = await screen.findByRole('listitem', { name: `Mai ${Y}` });
 assert.equal(text(within(may).getByRole('heading').parentElement), 'Mai1.641,25 €');
 const [ispa, vanguard, xyz] = may.querySelectorAll('.dividend-entry');
 assert.equal(ispa.querySelector('.entry-ticker').textContent, 'ISPA.DE');
 assert.equal(ispa.querySelector('.entry-name').textContent, 'iShares STOXX Global Select Dividend 100 UCITS ETF (DE)');
 assert.equal(ispa.querySelector('.entry-name').getAttribute('title'), 'iShares STOXX Global Select Dividend 100 UCITS ETF (DE)');
 assert.equal(text(ispa.querySelector('.entry-figures')), 'Ex 14.05. · Pay 14.05.1,38 %0,4123 €× 120,549,68 €');
 assert.equal(vanguard.querySelector('.entry-ticker').textContent, 'IE00B8GKDB10');
 assert.match(text(vanguard.querySelector('.entry-figures')), /^Ex 20\.05\. · Pay 20\.05\.–.*0,4792 €× 1\.234,51\.591,57 €$/);
 assert.equal(vanguard.querySelector('.missing').getAttribute('title'), 'No price before the ex-date.');
 // € values that cannot be computed: "–" with the reason as tooltip.
 const missing = xyz.querySelectorAll('.missing');
 assert.equal(missing.length, 2);
 assert.equal(missing[1].getAttribute('title'), 'The ECB publishes no exchange rate for XYZ.');
 // Year summary.
 const summary = text(document.querySelector('.dividend-summary'));
 assert.equal(summary, 'Received412,18 €Expected198,40 €Total610,58 €');
 assert.ok(screen.getByText('Dividend data updated 04.11.2026 · Yahoo Finance'));
});

test('forecast, not-held and empty months are distinguishable without color', async t => {
 t.mock.method(globalThis, 'fetch', async () => json(calendar(Y, [
  entry({ exDate: `${Y}-03-10`, held: false, units: '0', total: '0.00' }),
  entry({ kind: 'FORECAST', exDate: `${Y}-07-15`, basisExDate: `${Y - 1}-07-15`, closeDate: `${Y}-11-06` }),
 ])));
 const view = render(tree());
 const march = await screen.findByRole('listitem', { name: `März ${Y}` });
 const notHeld = march.querySelector('.dividend-entry');
 assert.ok(notHeld.classList.contains('not-held'));
 assert.equal(notHeld.querySelector('.state-pill').textContent, 'not held');
 assert.match(notHeld.textContent, /0,00\s€$/);
 const forecast = screen.getByRole('listitem', { name: `Juli ${Y}` }).querySelector('.dividend-entry');
 assert.ok(forecast.classList.contains('forecast'));
 assert.equal(forecast.querySelector('.state-pill').textContent, 'expected');
 assert.equal(view.container.querySelectorAll('.state-pill').length, 2);
 assert.equal(screen.getAllByText('No dividends').length, 10);
 // Without color: the forecast border is dashed, both states carry a text pill.
 assert.match(css, /\.dividend-entry\.forecast \.entry-button\{border-style:dashed/);
 assert.match(css, /\.month-empty\{[^}]*color:var\(--div-muted\)/);
});

test('clicking an entry opens a popover with its details', async t => {
 t.mock.method(globalThis, 'fetch', async () => json(calendar(Y, [
  entry({ currency: 'USD', perUnitOrig: '0.519077', fxRate: '0.92310000', fxRateDate: `${Y}-05-14`, close: '93.6250' }),
  entry({ instrumentId: 4, kind: 'FORECAST', exDate: `${Y}-12-18`, basisExDate: `${Y - 1}-12-18`, close: '91.7850', closeDate: `${Y}-11-06`, currency: 'GBp', perUnitOrig: '39.820000', fxRate: '1.18340000', fxRateDate: `${Y}-11-06` }),
 ])));
 render(tree());
 await screen.findByRole('listitem', { name: `Mai ${Y}` });
 const button = document.querySelector('.dividend-entry .entry-button');
 fireEvent.click(button);
 const details = text(screen.getByRole('dialog', { name: 'ISPA.DE dividend details' }));
 assert.equal(button.getAttribute('aria-expanded'), 'true');
 for (const part of [`Ex-date14.05.${Y}`, `Pay-date14.05.${Y}`, 'Estimated asex-date', 'StatusPaid', 'Per share0,519077 USD', `FX rate1 USD = 0,9231 € (ECB 14.05.${Y})`, 'Price before ex-date93,625 USD', 'Units held120,5', 'SourceYahoo Finance']) assert.ok(details.includes(part), `${part} in ${details}`);
 assert.ok(!details.includes('based on'));
 fireEvent.keyDown(document, { key: 'Escape' });
 assert.ok(screen.queryByRole('dialog') === null);
 // Forecasts show their basis and the latest close.
 fireEvent.click(document.querySelectorAll('.dividend-entry .entry-button')[1]);
 const forecast = text(screen.getByRole('dialog'));
 assert.ok(forecast.includes(`based on 18.12.${Y - 1}`), forecast);
 assert.ok(forecast.includes(`Latest close (06.11.${Y})91,785 GBp`), forecast);
 assert.ok(forecast.includes('1 GBP = 1,1834 €'), forecast);
 fireEvent.click(screen.getByRole('button', { name: 'Close details' }));
 assert.ok(screen.queryByRole('dialog') === null);
});

test('stale data syncs in the background: cached data shows at once, progress is inline, the calendar reloads after the job', async t => {
 let gets = 0; const posted = []; let polls = 0;
 t.mock.method(globalThis, 'fetch', async (url, init) => {
  if (url.includes('/dividends?')) { gets++; return json(calendar(Y, gets === 1 ? [entry()] : [entry(), entry({ instrumentId: 9, ticker: 'NEW.DE', exDate: `${Y}-06-02` })], { sync: { provider: 'YAHOO', lastSyncedAt: '2026-11-04T13:32:00Z', stale: [9], errors: [], jobId: null } })); }
  if (url.endsWith('/dividend-sync')) { posted.push(JSON.parse(init.body)); return json({ jobId: 'j1' }, 202); }
  if (url.endsWith('/dividend-sync-jobs/j1')) { polls++; return json(polls === 1 ? { status: 'RUNNING', done: 1, total: 2, items: [] } : { status: 'DONE', done: 2, total: 2, items: [] }); }
  throw Error(`Unexpected ${url}`);
 });
 render(tree());
 await gridHas('ISPA.DE');
 await screen.findByText('1/2 instruments');
 await gridHas('ISPA.DE'); // cached entries stay visible during the sync
 assert.equal(screen.getByRole('button', { name: 'Refreshing…' }).disabled, true);
 assert.deepEqual(posted, [{ scope: 'STALE' }]);
 await gridHas('NEW.DE');
 assert.equal(gets, 2);
 await waitFor(() => assert.equal(screen.getByRole('button', { name: 'Refresh dividends' }).disabled, false));
 // Still stale after the job (e.g. a failing instrument): no automatic loop.
 assert.equal(posted.length, 1);
});

test('refresh queues every item, joins a running sync and shows the error banner', async t => {
 const posted = []; let polls = 0;
 t.mock.method(globalThis, 'fetch', async (url, init) => {
  if (url.includes('/dividends?')) return json(calendar(Y, [entry()], { sync: { provider: 'YAHOO', lastSyncedAt: '2026-11-04T13:32:00Z', stale: [], jobId: null, errors: [
   { instrumentId: 7, label: 'SAP.DE', message: 'Yahoo Finance is unavailable.', at: '2026-11-05T09:00:00Z', lastSyncedAt: '2026-10-01T10:00:00Z' },
   { instrumentId: 2, label: 'MSFT', message: 'Yahoo Finance has no listing for this instrument.', at: '2026-11-05T09:00:00Z', lastSyncedAt: null },
  ] } }));
  if (url.endsWith('/dividend-sync')) { posted.push(JSON.parse(init.body)); return json({ error: { code: 'SYNC_RUNNING', message: 'A dividend sync is already running for this portfolio.', jobId: 'other' } }, 409); }
  if (url.endsWith('/dividend-sync-jobs/other')) { polls++; return json({ status: 'DONE', done: 3, total: 3, items: [] }); }
  throw Error(`Unexpected ${url}`);
 });
 render(tree());
 const banner = await screen.findByRole('alert');
 assert.match(banner.textContent, /Couldn't load dividends for SAP\.DE\. Showing data from 01\.10\.2026\./);
 assert.match(banner.textContent, /Couldn't load dividends for MSFT\. No cached data yet\./);
 const refresh = await screen.findByRole('button', { name: 'Refresh dividends' });
 await waitFor(() => assert.equal(refresh.disabled, false));
 fireEvent.click(refresh);
 await waitFor(() => assert.deepEqual(posted, [{ scope: 'ALL' }]));
 await waitFor(() => assert.equal(polls, 1));
 assert.ok(screen.queryByText('A dividend sync is already running for this portfolio.') === null);
});

test('a sync already running when the tab opens is followed instead of starting another', async t => {
 let posts = 0, polls = 0;
 t.mock.method(globalThis, 'fetch', async (url, init) => {
  if (url.includes('/dividends?')) return json(calendar(Y, [], { sync: { provider: 'YAHOO', lastSyncedAt: null, stale: [1, 2], errors: [], jobId: 'running' } }));
  if (url.endsWith('/dividend-sync')) { posts++; return json({ jobId: 'x' }, 202); }
  if (url.endsWith('/dividend-sync-jobs/running')) { polls++; return json({ status: 'RUNNING', done: 0, total: 2, items: [] }); }
  throw Error(`Unexpected ${url}`);
 });
 render(tree());
 await screen.findByText('0/2 instruments');
 assert.ok(screen.getByText('Dividend data not loaded yet · Yahoo Finance'));
 assert.equal(posts, 0);
 assert.ok(polls >= 1);
});

test('skeleton cards while loading; the tab sits between Transactions and Import', async t => {
 let release;
 t.mock.method(globalThis, 'fetch', async url => {
  if (url.includes('/dividends?')) { await new Promise(resolve => { release = resolve; }); return json(calendar()); }
  if (url.includes('/saving-plan')) return json([]);
  return json({ id: 1, name: 'Growth', itemCount: 0, instrumentIds: [], itemLimit: 30, dates: [], series: [], totals: [], invested: '0.00', lines: [], total: 0, page: 1 });
 });
 const portfolio = { id: 1, name: 'Growth', itemCount: 0, instrumentIds: [], itemLimit: 30, createdAt: '2025-01-01' };
 const router = { push() {}, replace() {}, prefetch() {}, pathname: '/portfolios/1/dividends', asPath: '/portfolios/1/dividends' };
 const view = render(React.createElement(AppRouterContext.Provider, { value: router }, React.createElement(RouterContext.Provider, { value: router },
  React.createElement(PathnameContext.Provider, { value: '/portfolios/1/dividends' }, React.createElement(PortfolioWorkspace, { portfolio })))));
 const tabs = [...view.container.querySelectorAll('.portfolio-tabs a')].map(a => a.textContent);
 assert.deepEqual(tabs, ['Dashboard', 'Transactions', 'Dividends', 'Import', 'Manage items']);
 assert.equal(screen.getByRole('link', { name: 'Dividends' }).getAttribute('aria-current'), 'page');
 const panel = view.container.querySelector('[aria-label="Dividends view"]');
 assert.equal(panel.hidden, false);
 await waitFor(() => assert.equal(panel.querySelectorAll('.skeleton-card').length, 12));
 assert.ok(within(panel).getByRole('status', { name: 'Loading dividends' }));
 release();
 await within(panel).findByText('Januar');
 assert.equal(panel.querySelectorAll('.skeleton-card').length, 0);
});

test('dark mode: every dividend color is a token with a dark value', () => {
 const section = css.slice(css.indexOf('/* Dividends tab.'));
 assert.match(section, /@media\(prefers-color-scheme:dark\)\{\.dividends\{--div-surface:[^;]+;--div-surface-muted:[^;]+;--div-border:[^;]+;--div-ink:[^;]+;--div-muted:[^;]+;--div-accent:/);
 // Hex colors appear only as token values (and the dark error color), never in rules.
 const rules = section.replace(/--[a-z-]+:#[0-9a-f]{3,8}/gi, '').replace(/\.dividends \.error\{color:#[0-9a-f]+\}/, '');
 assert.doesNotMatch(rules, /#[0-9a-f]{3,8}\b/i);
 assert.match(section, /\.pill-expected\{background:var\(--div-pill-expected-bg\);color:var\(--div-pill-expected-fg\)\}/);
});

test('payments sit in the pay-date month; ex-dates paid later are faded markers in their own month', async t => {
 const shifted = entry({ exDate: `${Y}-10-02`, payDate: `${Y}-11-02`, payDateShifted: true, payDelayDays: 30 });
 const sameMonth = entry({ instrumentId: 4, ticker: 'VHYL.L', exDate: `${Y}-05-04`, payDate: `${Y}-05-22`, payDelayDays: 18 });
 const unpaid = entry({ instrumentId: 5, ticker: 'HMWO.L', exDate: `${Y}-12-10`, payDate: `${Y + 1}-01-11`, payDateShifted: true, payDelayDays: 30, paid: false });
 t.mock.method(globalThis, 'fetch', async () => json(calendar(Y, [shifted, sameMonth, unpaid])));
 render(tree());
 const november = await screen.findByRole('listitem', { name: `November ${Y}` });
 const payment = november.querySelector('.dividend-entry');
 assert.equal(text(payment.querySelector('.entry-dates')), 'Ex 02.10. · Pay 02.11.');
 assert.equal(payment.querySelector('.state-pill'), null, 'paid: no expected pill');
 // October: no payment, the ex-date as a marker that is not counted.
 const october = screen.getByRole('listitem', { name: `Oktober ${Y}` });
 assert.equal(october.querySelectorAll('.dividend-entry').length, 0);
 assert.ok(within(october).getByText('No payments'));
 const marker = october.querySelector('.ex-marker');
 assert.equal(text(marker), 'Ex 02.10.ISPA.DE→ paid 02.11.49,68 €');
 assert.equal(marker.querySelector('button').getAttribute('aria-label').replace(/\u00a0/g, ' '), `ISPA.DE goes ex 02.10.${Y}, paid 02.11.${Y}, 49,68 €`);
 // Ex and pay in the same month: the payment only, no marker.
 const may = screen.getByRole('listitem', { name: `Mai ${Y}` });
 assert.equal(may.querySelectorAll('.dividend-entry').length, 1);
 assert.equal(may.querySelectorAll('.ex-marker').length, 0);
 // Paid next year: the marker shows the full pay-date; nothing is paid in December.
 const december = screen.getByRole('listitem', { name: `Dezember ${Y}` });
 assert.equal(text(december.querySelector('.ex-pays')), `→ pays 11.01.${Y + 1}`);
 // Faded until hovered or focused.
 assert.match(css, /\.ex-marker\{[^}]*opacity:\.4/);
 assert.match(css, /\.ex-marker:hover,\.ex-marker:focus-within,\.ex-marker\.open\{opacity:1\}/);
 // Its popover explains the estimate.
 fireEvent.click(marker.querySelector('button'));
 const details = text(screen.getByRole('dialog', { name: 'ISPA.DE dividend details' }));
 for (const part of [`Ex-date02.10.${Y}`, `Pay-date02.11.${Y}`, 'Estimated asex-date + 30 days, moved to the next working day', 'StatusPaid']) assert.ok(details.includes(part), `${part} in ${details}`);
 fireEvent.click(document.body);
 assert.ok(screen.queryByRole('dialog') === null);
 // An unpaid payment carries the expected pill (here in next year's January).
 cleanup();
 t.mock.method(globalThis, 'fetch', async () => json(calendar(Y + 1, [unpaid])));
 render(tree({ search: `year=${Y + 1}` }));
 const january = await screen.findByRole('listitem', { name: `Januar ${Y + 1}` });
 assert.equal(january.querySelector('.state-pill').textContent, 'expected');
});

// jsdom normalizes colors; compare through the same parser.
const cssColor = value => { const el = document.createElement('i'); el.style.color = value; return el.style.color; };

test('the chart stacks one segment per dividend in 24 bars and explains a segment on hover, focus or tap', async t => {
 const { instrumentColor } = await import('../lib/colors.ts');
 const a = entry({ exDate: `${Y}-03-02`, payDate: `${Y}-04-01`, payDelayDays: 30, total: '20.00' });
 const b = entry({ instrumentId: 4, ticker: 'VHYL.L', exDate: `${Y}-03-10`, payDate: `${Y}-03-25`, payDelayDays: 15, total: '10.00' });
 const c = entry({ instrumentId: 5, ticker: 'HMWO.L', kind: 'FORECAST', exDate: `${Y}-11-20`, payDate: `${Y}-12-21`, payDelayDays: 30, total: '5.00', paid: false });
 const zero = entry({ instrumentId: 6, ticker: 'NH', exDate: `${Y}-06-02`, held: false, units: '0', total: '0.00' });
 const cal = calendar(Y, [a, b, c, zero]);
 cal.months[2].total = '10.00'; cal.months[2].exTotal = '30.00'; cal.months[3].total = '20.00';
 cal.months[10].exTotal = '5.00'; cal.months[11].total = '5.00';
 t.mock.method(globalThis, 'fetch', async () => json(cal));
 render(tree());
 const chart = await screen.findByRole('figure', { name: `Dividends by month, ${Y}: ex-dates and payments` });
 assert.deepEqual([...chart.querySelectorAll('.month-label')].map(l => l.textContent).length, 12);
 // Ex bars: March holds two dividends, November one; zero totals draw nothing.
 const ex = [...chart.querySelectorAll('.seg-ex')];
 assert.deepEqual(ex.map(s => s.getAttribute('data-key')), [`ex-ACTUAL-3-${Y}-03-02`, `ex-ACTUAL-4-${Y}-03-10`, `ex-FORECAST-5-${Y}-11-20`]);
 assert.equal(chart.querySelectorAll('.seg').length, 6);
 // Gray translucent shades per instrument for ex-dates; instrument colors for payments; expected payments hatched.
 assert.equal(ex[0].querySelector('.seg-mark').style.fill, 'var(--div-ex-c)');
 assert.equal(ex[1].querySelector('.seg-mark').style.fill, 'var(--div-ex-d)');
 const paid = chart.querySelector(`[data-key="pay-ACTUAL-3-${Y}-03-02"]`);
 assert.ok(paid.classList.contains('seg-paid'));
 assert.equal(cssColor(paid.querySelector('.seg-mark').style.fill), cssColor(instrumentColor(3)));
 const expected = chart.querySelector(`[data-key="pay-FORECAST-5-${Y}-11-20"]`);
 assert.ok(expected.classList.contains('seg-expected'));
 assert.equal(expected.querySelector('.seg-mark').style.fill, 'url(#div-hatch-5)');
 assert.ok(chart.querySelector('#div-hatch-5'));
 assert.match(css, /\.seg-ex \.seg-mark\{fill-opacity:var\(--div-ex-opacity\)\}/);
 // The top segment of a bar has the rounded data end; the one below it is a plain rectangle.
 assert.ok(chart.querySelector(`[data-key="pay-ACTUAL-3-${Y}-03-02"] path.seg-mark`));
 assert.ok(chart.querySelector(`[data-key="ex-ACTUAL-3-${Y}-03-02"] rect.seg-mark`));
 assert.ok(chart.querySelector(`[data-key="ex-ACTUAL-4-${Y}-03-10"] path.seg-mark`));
 // Hover: the segment is highlighted, the others fade, the tooltip explains it.
 fireEvent.mouseEnter(paid);
 assert.ok(paid.classList.contains('active'));
 assert.ok(chart.querySelector('svg').classList.contains('has-active'));
 const tip = text(screen.getByRole('tooltip'));
 for (const part of ['ISPA.DE · Paid', `Ex-date02.03.${Y}`, `Pay-date01.04.${Y}`, 'Estimated asex-date + 30 days', 'Total20,00 €', 'April payments20,00 €']) assert.ok(tip.includes(part), `${part} in ${tip}`);
 fireEvent.mouseLeave(chart.querySelector('.dividend-chart-plot'));
 assert.ok(screen.queryByRole('tooltip') === null);
 // Keyboard focus shows it too; Escape hides it.
 fireEvent.focus(ex[1]);
 assert.ok(text(screen.getByRole('tooltip')).includes('VHYL.L · Ex-date'));
 assert.ok(text(screen.getByRole('tooltip')).includes('März ex-dates30,00 €'));
 fireEvent.keyDown(document, { key: 'Escape' });
 assert.ok(screen.queryByRole('tooltip') === null);
 // Tap toggles.
 fireEvent.click(expected);
 assert.ok(text(screen.getByRole('tooltip')).includes('HMWO.L · Expected payment'));
 fireEvent.click(expected);
 assert.ok(screen.queryByRole('tooltip') === null);
 // Every segment has an accessible name; the legend names kinds and instruments.
 assert.equal(paid.getAttribute('aria-label').replace(/\u00a0/g, ' '), `ISPA.DE paid: 20,00 €, ex-date 02.03.${Y}, pay-date 01.04.${Y}`);
 assert.equal(paid.getAttribute('tabindex'), '0');
 const legend = text(chart.querySelector('.dividend-chart-legend'));
 for (const part of ['Ex-date (left bar)', 'Paid (right bar)', 'Expected payment', 'ISPA.DE', 'VHYL.L', 'HMWO.L']) assert.ok(legend.includes(part), legend);
});

test('the chart says so when a year has no dividends', async t => {
 t.mock.method(globalThis, 'fetch', async () => json(calendar(Y)));
 render(tree());
 const chart = await screen.findByRole('figure');
 assert.ok(within(chart).getByText(`No dividends in ${Y}.`));
 assert.equal(chart.querySelectorAll('.seg').length, 0);
});
