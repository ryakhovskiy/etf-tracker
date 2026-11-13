# Holdings: implemented feature facts

Snapshot: 2026-11-08; updated 2026-11-13 for the frontend redesign (themes, responsive tile UI). Agent reference for the current checkout, consolidating
`f1-portfolio-core.md`, `f2.1`–`f2.5`, and `f3-eulerpool.md`.
**Implemented** means present in code; it does not assert merge/deployment status
or live-provider availability. Code and tests take precedence over historical specs.

## Implementation map

| Area / original spec | Status | Current capability |
|---|---|---|
| Portfolios and instrument picker / f1 | Implemented | Private portfolio CRUD, limits, local search, batch item adds, removal API |
| Transactions / f2.1 | Implemented | Manual buys, defaults, exact decimal math, duplicate decisions, history and deletion |
| CSV import / f2.2 | Implemented | Configurable parser, editable preview, resolution, atomic import, per-user saved settings |
| OpenFIGI / f2.3 | Implemented; provider role amended | German-listing identifier/keyword search, cached results, instrument persistence |
| AV and FX / f2.4 | Implemented; provider/date rules amended | Budgeted AV gateway, symbol linking, quote jobs, ECB conversions and provenance |
| Dashboard performance / f2.5 | Implemented | Purchase/current valuation, partial coverage, budget-aware fetching, chart extension |
| Eulerpool / f3 | Implemented; primary-provider default superseded | Search, stock bulk quotes, ETF history/profiles, quota accounting, modern picker |
| Yahoo and historical prices / later code | Implemented extension | Default primary provider, symbol linking, current quotes and daily history |
| Dividend calendar / f4, beyond the supplied specs | Implemented extension | Cached gross dividends, EUR totals, forecasts and background sync |
| Themes and responsive tile UI / later frontend work | Implemented extension | System + 5 per-browser themes, tokenized colours, 360px–desktop tiles/cards, bottom tab bar on phones, Playwright smoke suite |
| Docker deployment / later infrastructure | Implemented | Separate multi-stage backend/frontend images, dedicated network, persistent SQLite volume, health checks |

## Platform, security and shared rules

- Backend: Go, standard `net/http` routing, SQLite, embedded Goose migrations.
  Frontend: Next.js App Router + React, existing CSS tokens, `decimal.js`, Recharts.
- Google OAuth login/signup; HttpOnly access/refresh cookies, server-checked
  sessions, refresh rotation and logout/replay revocation. Frontend handles session
  refresh and returns unauthenticated users to login.
- Feature APIs require authentication (`401`). Portfolio queries are user-scoped;
  another user's portfolio/transaction/job is hidden with `404`. Instruments,
  quotes, profiles and provider caches are shared across users.
- Foreign keys are enabled per SQLite connection. Capacity checks and writes use
  `BEGIN IMMEDIATE`; batch additions, commits and imports are atomic.
- Defaults: **10 users**, **10 portfolios/user**, **30 items/portfolio**; positive
  overrides: `MAX_USERS`, `MAX_PORTFOLIOS_PER_USER`, `MAX_ITEMS_PER_PORTFOLIO`.
  Existing users can log in at signup capacity; a blocked signup creates no user
  or session and redirects to `/?error=signups_closed`.
- API decimal values are dot-separated **strings**; UI accepts comma or dot and
  displays `de-DE`. Backend financial math uses `math/big`, frontend `decimal.js`.
  Storage scales: units ×10⁶, price ×10⁴, money total ×10², FX rate ×10⁸.
  Rounding is half-up. Positive quantity/price/total ≤1,000,000; dates span
  1990-01-01 through today in Europe/Berlin.
- Terminology differs by surface: transaction API `amount` = **EUR total** and
  `quantity` = **units**; CSV column **Amount** maps to units, **Total €** to money.

## Docker deployment

- From the repository root, `docker compose build` builds both images;
  `docker compose up -d --build --wait` builds and starts both services.
  Requires Docker Engine with the Compose plugin and a configured root `.env`.
- Backend: Go 1.27.3 build stage, `CGO_ENABLED=0`, stripped binary with trimmed
  paths; Alpine runtime with CA roots and Go timezone data. Goose migrations and
  mock fixtures are embedded. Runs as UID/GID `10001:10001`.
- Frontend: Node 22 Alpine build/runtime, `npm ci`, Next.js standalone output,
  compiled static assets and `public` assets; runs as user `node`. Final images
  exclude source trees, local `.env` files and the full development dependencies.
- Both services join the dedicated **`holdings-network`** bridge. Frontend
  publishes **`127.0.0.1:9443:9443`** (loopback only); backend listens on **9000** within that network,
  without publishing a host port. Docker builds set `NEXT_PUBLIC_API_URL=""`
  and `API_PROXY_URL=http://backend:9000`; Next.js forwards `/api/*` and `/auth/*`
  to the backend, keeping browser requests and cookies on the frontend origin.
  These values are compiled at build time; local development retains its direct API URL.
- **9443 serves HTTP**; an external HTTPS reverse proxy must forward the public
  site to it. Set `FRONTEND_URL` and `GOOGLE_REDIRECT_URL` to the public HTTPS
  origin/callback, including any public port, and register that callback with Google.
  Supply real Google credentials and a random `JWT_SECRET` of at least 32 characters.
  Compose overrides `APP_ENV=production`, `SECURE_SESSION_COOKIE=true`,
  `PORT=9000` and `DATABASE_PATH=/app/data/holdings.db`.
- SQLite persists in the **`holdings_holdings-data`** named volume at `/app/data`,
  initialized writable by the backend user. Existing host databases require explicit
  migration into it. `docker compose down` preserves data; `down -v` deletes it.
- Both services have health checks, init processes and `unless-stopped` restart
  policies. Frontend startup waits for backend `/health` to pass. Operational
  commands: `docker compose ps` and `docker compose logs -f`.

## Pages and workflows

| Route | Purpose |
|---|---|
| `/` | Login or portfolio list; create, rename, confirm deletion; counts/limit hints; per-portfolio value, performance and change vs yesterday plus a total strip (see "Portfolio list summaries") |
| `/portfolios/:id` | Redirect to dashboard |
| `/portfolios/:id/dashboard` | Figures, stacked chart, paginated transaction history and deletion |
| `/portfolios/:id/transactions` | Buy-entry form, quote fetching and symbol linking |
| `/portfolios/:id/saving-plan` | Compatibility redirect to Transactions; API keeps `saving-plan` name |
| `/portfolios/:id/dividends` | Annual dividend calendar |
| `/portfolios/:id/import` | CSV configuration, preview and import |
| `/portfolios/:id/items` | Search-driven item selection and batch add; items in the portfolio with latest stored quote and pay-date delay |

Portfolio tabs stay mounted: tab switches preserve drafts, selection and pagination;
successful mutations refresh related panels. Portfolio names are trimmed, 1–100
characters, case-insensitively unique per user. Empty portfolios list exposes only
Create; Create disables at capacity. Portfolio deletion cascades items and transactions.
Item removal is API-only and rejects items with transactions (`ITEM_HAS_TRANSACTIONS`).

### Instrument search and selection

- Local search: trimmed query ≥4 characters, 300ms debounce, cancellation and stale
  response protection; ≤10 results. Case-insensitive contains match on ticker,
  ISIN, WKN, name and FIGI; exact identifiers rank before prefixes, then contains,
  then name. SQL wildcards are escaped.
- Online search requires Search/Enter. Default chain: **Yahoo → Eulerpool →
  OpenFIGI**; WKN queries go straight to OpenFIGI. AV keyword search is offered
  only as an explicit cost-labeled fallback. Selecting an online hit persists it,
  reuses matching instruments and fills empty fields without overwriting values.
- OpenFIGI: valid ISIN/check digit or WKN mapping, otherwise keyword search;
  default German exchange preference `GR,GY`. Mapping batches jobs; keyword
  requests run per exchange in parallel. Keep stock/ETF types, dedupe by ticker,
  retain successful partial results with warnings. OpenFIGI does not supply ISINs.
- Eulerpool search merges listings by ISIN; search currency is informational.
  Yahoo picks use unique Yahoo symbols and may attach an unambiguous ISIN via
  Eulerpool. ISIN and provider-symbol linking are also available on Transactions.
- Picker has local/online sections, match highlights, type/exchange/currency
  badges, ETF info, loading/empty/error/retry states and provider attribution.
  Combobox/listbox semantics, arrows/Enter/Esc, outside-click dismissal, Alt+I for
  ETF info, dark-mode tokens, reduced motion and mobile layout are implemented.
- Existing members and selected rows are disabled. Multiple picks keep the
  dropdown open. Selected rows can be removed locally; Add shows counts and
  capacity hints. Server accepts 1–30 IDs per batch, rejects unknown IDs and
  over-capacity batches entirely, and returns `added`/`skipped` for idempotency.

### Transactions

- One form row per portfolio item; latest purchase supplies amount and price
  defaults (trade date, creation time, then IDs break ties). A newer stored quote
  can supply the price default. No history: zero amount, empty price.
- Amount/price edits recompute units while retaining the invested amount;
  quantity edits recompute amount. Clearing/invalid input preserves other fields.
  Without a positive price, quantity is disabled. Grand total updates live.
- Commit ignores zero-amount rows, validates all positive rows and portfolio
  membership, and writes one BUY transaction. Server checks rounded amount ÷
  price against submitted quantity within 0.000001, storing validated inputs.
- Duplicate = same portfolio/date/instrument/quantity/price. `409
  DUPLICATE_TRANSACTION` opens Skip matching lines / Commit anyway (`force`).
  Successful commit clears the draft and navigates to Dashboard.
- History groups lines by transaction, newest first, default 50 lines/page with
  configurable page size/columns. Confirmed transaction deletion refreshes figures
  and chart. Editing transactions uses delete and re-enter.
- The Dashboard history's Performance column shows the newest stored EUR quote
  on or after each purchase, its date, the per-unit EUR/percentage difference
  from the buy price, and the difference multiplied by the purchased quantity.
  Transaction headers sum these quantity-weighted differences across all lines,
  including lines on other pages; percentages use the priced lines' purchase
  price × quantity as the base. Unpriced lines stay unavailable and make totals
  partial. Gains are green, losses red, and zero neutral. No provider fetch is
  triggered by displaying performance.

### CSV import

- File picker only; ≤1 MB, ≤5,000 rows, ≤100 new instruments. Defaults accept the
  German reference exports: `;`, comma decimal, dot thousands, euro stripping,
  `DD.MM.YYYY`, headers and UTF-8/Windows-1252 auto-detection (including BOM).
- Configurable encoding, delimiter, quote (`"`, `'`, none), headers, skipped lines,
  numeric separators, date format and header/index/unmapped column mapping.
  Server validates settings and mapping conflicts. Per-user versioned SQLite
  settings support load/save/reset/discard, dirty state and saved timestamp;
  successful import saves settings in the same transaction. Files are not stored.
- Resolve ISIN first, then WKN. Valid unknown ISINs create one shared instrument;
  unknown WKN-only rows and conflicting identifiers fail. Known items can be
  auto-added within capacity. Missing WKNs can be filled; differing WKN/name and
  total mismatches produce warnings. New rows allow name/ticker/type edits.
- Editable paginated preview supports Include, duplicate Skip/Import, deletion,
  cell highlights/revert, filters and bulk decisions. Quantity/price edits update
  total; total edits update quantity. Settings/file changes reparse after 300ms;
  edits revalidate after 500ms. Guards warn before discarding preview edits.
- Statuses: `OK`, `WILL_ADD_TO_PORTFOLIO`, `NEW_INSTRUMENT`, `DUPLICATE`, `ERROR`.
  Duplicates compare database lines and earlier file rows. Included errors,
  undecided duplicates or capacity overflow block import.
- Confirmation uses server summary and validation `revision`. Import revalidates
  atomically, creates one CSV transaction per date and preserves broker units,
  price and total. Changed results return `409 IMPORT_STALE` with fresh preview.

### Dashboard and valuation

- Total invested = sum of saved EUR totals. Purchase-price chart values cumulative
  units at each transaction date using that date's quantity-weighted purchase price,
  carrying the last price forward between purchases.
- Purchase/current performance = value − invested; percentage divides by invested
  (null at zero). Display uses rounded sign/color, ▲/▼, readable dark tokens.
- Current valuation uses newest shared quote by quote date/fetch time, unless a
  purchase is later. Fallback reasons: `NO_SYMBOL`, `NO_QUOTE`, `PURCHASE_NEWER`.
  Any fallback makes it partial; no usable quote makes `current=null`.
- Quote date range/fetch time and item sources persist across reloads. Fetch current
  value estimates cost first and reuses price jobs; AV budget-limited requests use
  `priority: "HOLDINGS_VALUE"` to prioritize larger holdings.
- Stacked time-scale chart uses stable instrument colors and exact tooltip values;
  a later quote date adds a dashed current-value extension, marker and source-aware
  tooltip. Older quotes show a hint. Y-axis labels omit cents and size automatically.
- The chart defaults to 12 months and remembers the last explicitly selected
  preset in browser local storage across visits and portfolios. Custom zoom/pan
  ranges never overwrite that preference; reopening restores the last preset,
  or 12 months if none was selected. Max runs from the first transaction through
  today. The top-right selector offers the last 3, 6, 12, 18, 24, 48, or 60 calendar
  months. Users can zoom
  with +/− or Ctrl/⌘ + scroll, and pan by dragging, the left/right buttons, or arrow
  keys; Home/Reset returns to Max. Navigation stays inside the portfolio history,
  with a minimum one-day window. Prior holdings remain in the plot when zooming;
  the last stored value continues through today with its original tooltip date.
- Each position's legend button toggles it in the chart and all five summary
  figures. The API accepts optional `instrumentIds=1,2` on the performance route
  and recalculates selected positions from the complete user-scoped transaction
  history and stored quotes before rounding; an empty list selects no positions.
  Chart periods stay in place when toggling. Show all restores the full portfolio;
  hiding every position shows zero totals and an empty chart. Selected positions
  without usable quotes show unavailable current figures. This view selection
  persists through tab switches and data refreshes; market-price fetches still
  cover the full portfolio.

### Portfolio list summaries

Backend contract (frontend UI facts below). Everything is session-scoped (`401`)
and covers only the caller's portfolios; `GET /api/portfolios` is unchanged.

- `GET /api/portfolios/summaries` returns `summaries[]` (one per portfolio, by
  `portfolioId`), `total` (all portfolios, with `portfolioCount`) and `refresh`.
  Figures: `positionCount` (instruments with units held), `invested` (EUR sum of
  transaction totals), `currentValue`, `performance` and `change`
  (`{amount, percent}`), `partial`, `changePartial`, `reasons[]` and `asOf`.
  Decimals are strings, rounded half-up once at the end; the total sums the exact
  amounts of all portfolios before rounding, so tiles may differ from it by a cent.
- **Current value** = units held × the instrument's latest stored quote in EUR (the
  same quote as the dashboard). A position with no quote (`NO_QUOTE`) or a quote
  older than its latest purchase (`PURCHASE_NEWER`) is valued at its latest
  purchase price and `partial` is set. `currentValue`, `performance` and `asOf`
  are `null` when no position has a usable quote; `percent` is `null` when
  invested is 0. Zeros are never invented.
- **Change vs yesterday**, per instrument with a usable quote. The session is the
  quote's own date (Berlin day of its market time), so on a weekend it is
  Friday against Thursday. Units held before the session (trade date < session)
  contribute units × (price − previous close in EUR); units bought on the session
  date contribute units × (price − purchase price). `percent` = change ÷ the value
  of the units held before the session at the previous close (`null` when that is
  0, e.g. everything was bought in the session). Previous close in EUR = stored
  previous close × the FX rate of that previous session when one is stored
  (÷100 for `GBp`/`GBX`/`ZAc`), rounded to 4 dp like a quote; EUR listings need no
  rate. Today no previous-session rate is stored, so EVERY automatically
  fetched non-EUR position's change uses the quote's own rate and carries
  `FX_PREVIOUS_UNAVAILABLE` (and `changePartial`); only EUR listings are exact. Missing pieces make the change
  partial (`changePartial`) without hiding it: `NO_PREVIOUS_CLOSE` (units held
  before but no stored previous close: that position's before-session part is
  omitted), `FX_PREVIOUS_UNAVAILABLE` (no dated previous-session FX rate: the
  previous close is converted at the quote's own rate, an approximation), and
  the `NO_QUOTE`/`PURCHASE_NEWER` positions contribute 0. `change` is `null` only
  when no position produced a computable change.
- `asOf` describes the OLDEST quote used: `quoteDate`, `quoteTime` (market time,
  `null` unless every used quote has one), `fetchedAt`, sorted `providers`, and
  `realtime` (all used quotes are Yahoo real-time).
- **Automatic Yahoo refresh (amended rule).** `POST /api/price-refresh-jobs` with
  body `{}` (the frontend sends it on first load when `refresh.autoFetch` is true)
  or `{"force":true}` (manual "Refresh prices": skips only the freshness and
  throttle checks) starts ONE job of kind `USER_REALTIME` over the de-duplicated
  union of the user's held instruments across all portfolios (instruments without
  units are excluded). It runs the existing real-time Yahoo runner: Yahoo only,
  never Alpha Vantage, never Eulerpool, no fallback. Poll it with
  `GET /api/price-fetch-jobs/{jobId}`; when `DONE`, reload the summaries.
  - `202 {started:true, reused:false, jobId, requests}` when started.
  - `200 {started:false, reused:true, jobId}` when the user already has a running
    price job (one running job per user, any kind): poll that id.
  - `200 {started:false, reason}` with `FRESH` (not stale), `THROTTLED` (unforced
    attempt less than or exactly 15 minutes after the last one; re-checked under the
    lock that registers the job, so concurrent requests cannot start two), `YAHOO_UNAVAILABLE`
    (cooling down or budget used up; `blockedUntil` may be set; wins over `force`)
    or `NOTHING_TO_PRICE`. These are not errors: show the stored figures.
  - `refresh` in the summaries response: `stale` (no quote for any held
    instrument, or the NEWEST fetch time among them is strictly older than
    `maxAgeSeconds` = 900; other instruments being old or unpriced does not make it
    stale), `newestFetchedAt`, `autoFetch` (a POST would start a job now),
    `runningJobId`, `lastAttemptAt`, `yahoo {available, blockedUntil}` and
    `estimate {requests, remaining, noSymbol}` for the cost label.
  - Time source: `Handler.clock` (default `time.Now`) drives the decisions, so
    tests can fix it.

- **UI (frontend):**
  - Portfolio tile (`li[data-testid=portfolio-card]`): name link, item count, then a
    `dl` with Current value (hero figure), Performance and Change vs yesterday, then
    "as of HH:MM · Yahoo real-time" (the oldest used quote; date only, e.g.
    "as of 12.11.2026 · Yahoo", when the quote has no time or is not today).
    Rename/Delete are kept. No positions shows "No positions yet"; no usable quote
    shows "Unavailable".
  - Total strip above the grid (`section[data-testid=portfolio-summary-strip]`,
    `aria-label` "All portfolios total"): Total value, Performance and Change vs
    yesterday of all portfolios, "{n} portfolios · {m} positions", the as-of line and
    the refresh status.
  - Gain/loss text always carries its sign and ▲/▼ ("▲ +204,00 € (+18,28 %)",
    "▼ -30,00 € (-6,00 %)"; zero is neutral, no arrow); colour only reinforces it via
    the theme's positive/negative tokens (amber loss in Tech Red). Amounts are
    de-DE formatted from the API's decimal strings; "–" when the API sends `null`.
  - Partial: a "partial" badge (tooltip, focusable, `aria-label` "Partial figures: …")
    next to the value when `partial` and next to the change when `changePartial`;
    `NO_QUOTE`, `PURCHASE_NEWER`, `NO_PREVIOUS_CLOSE` and `FX_PREVIOUS_UNAVAILABLE`
    map to plain sentences. Zeros are never faked.
  - Loading: while the summaries request is pending the strip and tiles show skeleton
    lines (grid `aria-busy`). If it fails, the strip says "Portfolio figures are
    unavailable right now." with "Try again" and the tiles stay usable. During a
    refresh job the stored figures stay visible with "Updating prices… done/total"
    (`role=status`) and the button disabled; values update in place when the job is
    `DONE` (summaries are fetched again).
  - First load: after the list and summaries arrive, if `refresh.runningJobId` is set
    the UI only polls that job; else if `refresh.autoFetch` it POSTs
    `/api/price-refresh-jobs` with `{}` exactly once per visit. The job is polled via
    `GET /api/price-fetch-jobs/{id}` every 2 s (it stops after 5 consecutive poll
    failures and shows the stored figures). `FRESH`, `THROTTLED` and
    `NOTHING_TO_PRICE` are silent; `YAHOO_UNAVAILABLE` (or `refresh.yahoo` blocked)
    shows "Yahoo is cooling down until HH:MM. Showing stored prices." and disables
    the button.
  - Manual control: the button "Refresh prices (N Yahoo requests)"
    (`refresh.estimate.requests`; just "Refresh prices" at 0) POSTs `{force:true}`;
    hidden when there are no positions. Deleting a portfolio fetches the summaries again.
  - Layout/a11y: all colours from tokens, 44px targets, no overflow at 360px, the strip
    becomes a 2-column grid at ≤900px. Stable test ids: `portfolio-summary-strip`,
    `portfolio-card`, `portfolio-metrics`, `portfolio-metrics-skeleton`.

## Appearance: themes and responsive layout

Frontend only: no backend, API, database or Docker involvement.

- **Choices.** Six: System plus five themes. Theme IDs: `light`, `dark`, `tech-green`,
  `tech-cappuccino`, `tech-red` (Tech Cappuccino is the Catppuccin Mocha palette).
  `html[data-theme]` always holds the *resolved* theme; `html[data-theme-pref]` holds
  the choice (`system` or a theme ID). New visitors get System.
- **System** resolves to `light`/`dark` from `prefers-color-scheme` and follows OS
  changes live, without a reload.
- **Persistence.** Per browser, `localStorage` key `holdings.theme` (no account or
  backend). A missing or invalid value means System. If storage throws, the choice
  still applies for that page view and nothing breaks. A change in another tab is
  adopted through the `storage` event.
- **No flash.** An inline script in `<head>` of `app/layout.tsx` (`themeInitScript` in
  `frontend/lib/theme.ts`) sets both attributes before first paint; everything in it
  is wrapped in try/catch. `ThemeSync` re-resolves on mount and keeps System/tabs in step.
- **Switcher.** In the sticky top bar of every page, including login: a trigger
  (`data-testid="theme-trigger"`) opens a radio group named "Theme" with six `role="radio"`
  options (`aria-checked`, colour swatches). Arrow keys/Home/End move and select, Escape
  closes and returns focus to the trigger.
- **Tokens.** [`frontend/app/styles/tokens.css`](../frontend/app/styles/tokens.css) is the
  only file that contains colours (the Google logo colours are tokens there too).
  Component CSS and TSX use semantic tokens only: `--bg`, `--surface`, `--surface-raised`,
  `--surface-sunken`, `--border`, `--border-strong`, `--text`, `--text-muted`, `--link`,
  `--accent`, `--accent-soft`, `--on-accent`, `--positive`, `--negative`, `--warning`,
  `--info` (+ `-soft` tints), `--focus-ring`, `--shadow`, `--glow`, radii, font tokens.
  Chart tokens: `--chart-grid`, `--chart-axis`, `--series-1`…`--series-7`, and the
  dividend ex-date shades `--div-ex-a`…`e` with `--div-ex-opacity` (0.9). An instrument
  keeps one colour slot everywhere: `instrumentColor(id)` is `var(--series-N)` with
  N = ((id−1) mod 7)+1 (`frontend/lib/colors.ts`), so Recharts and the dividend chart
  restyle on a theme switch. `scripts/check-theme-contrast.mjs` verifies WCAG AA for
  every theme (text 4.5:1, controls/graphics 3:1, ex-date shades after opacity);
  `tests/theme-contrast.test.mjs` runs it in `npm test`.
- **Fonts.** Inter (UI) and JetBrains Mono (figures; also headings and labels in the
  three tech themes), self-hosted variable woff2 under `frontend/app/fonts`, loaded with
  `next/font/local` (no network at build or run time). Tech themes also use sharper radii.
- **Gain/loss** never relies on colour: the text always carries the sign and ▲/▼
  (`performanceDisplay`). Each theme defines its own `--positive`/`--negative`; in Tech Red
  losses are amber-orange so they are not confused with the crimson accent.
- **Responsive.** Works from 360px with no horizontal page scroll (checked at 360, 375
  and 1280px); the page max-width is 1360px. The portfolio tab nav is one `<nav>`: segmented
  pills on desktop, a fixed bottom tab bar with inline SVG icons at ≤640px. On phones and
  touch devices (`≤640px` or `pointer: coarse`) buttons, selects, text inputs and summaries
  are ≥44px tall, and buttons/summaries ≥44px wide (`app/styles/touch.css`, imported last);
  checkbox labels are the hit area. Dialogs are bottom sheets at ≤640px; instrument search results
  and the dividend details popover also open as sheets there. Motion is a subtle hover lift
  and transitions; `prefers-reduced-motion` turns animation off.
- **Tiles and cards instead of tables:** portfolio list (card grid), dashboard figures (KPI
  tiles; current value and performance lead on phones), one tile per position (chart-colour
  stripe, value, change vs purchase prices, share), dashboard history (one card per
  transaction with a field chooser), Transactions entry (one tile per instrument),
  Manage items and Selected instruments (tiles), dividend month cards. The CSV review stays a
  grid above 800px and becomes stacked, labelled row cards at ≤800px (cells carry
  `data-label`; the header row stays available to assistive technology).
- CSS lives in [`frontend/app/styles`](../frontend/app/styles) (`tokens`, `base`, `layout`,
  `touch`, `components/*`), imported by `app/globals.css`.

## Market data: current behavior

**Superseded spec rules:** Eulerpool is no longer the default primary; fetching is
no longer limited to today/previous weekday. Current code accepts any valid past
date and fetches a daily close on or before it. Default primary is **YAHOO**;
`MARKETDATA_PRIMARY` also accepts `EULERPOOL` or `ALPHAVANTAGE`.

| Provider | Implemented role and protections |
|---|---|
| Yahoo | Default search/current/historical prices; current quotes batched by 40; ISIN-to-symbol resolution and cached daily history. Cookie/crumb session, refresh-on-auth-failure, request spacing, persistent soft limit and cooldown. |
| Eulerpool | Search, ETF profiles, stocks in bulk ≤50, one history request/ETF plus profile if currency missing. Ledger and monthly rate-limit headers constrain local daily cap. Auth failure disables it until restart. |
| OpenFIGI | Identifier/keyword search fallback for German listings; concurrent-safe LRU, default 1,000 entries/24h; successful responses only. |
| Alpha Vantage | Explicit cost-labeled search/quote backup through one gateway; persisted atomic reservations, rolling global/user limits, worker queue and provider backoff. Failed reserved calls count. Historical daily series is compact (~100 trading days). |
| Frankfurter / ECB | Persistent FX outside AV budget; latest publication ≤ requested date, ≤7 calendar days old. Currency-range requests store all returned days; shared currencies reuse rates. |

- Price fetch starts on user action after estimate, with ONE exception: the
  portfolio list (`/`) refreshes Yahoo real-time prices automatically on first
  load (see "Portfolio list summaries"; Yahoo only, throttled to 15 minutes).
  Primary quote failures
  become `NEEDS_BACKUP`; alternative providers require separate clicks. Default
  UI offers Eulerpool then AV after Yahoo; Eulerpool-primary UI can offer Yahoo
  before AV. No automatic AV spend.
- **Real-time prices** (Transactions "Fetch real-time prices", Dashboard "Fetch
  real-time value"; today only, always Yahoo whatever the primary): a price job
  with `"realtime": true`. It ignores today's cached quote and calls
  `v8/finance/chart/{symbol}?interval=1d&range=1d` once per instrument, reading
  `meta.regularMarketPrice`/`regularMarketTime`. The chart endpoint needs only
  the browser User-Agent, so no cookie or crumb is spent (a crumb already held is
  attached). Foreign currencies use a live Yahoo rate `{CCY}EUR=X` (1 request per
  currency, same EUR-per-unit orientation as ECB). If none can be had, the ECB
  rate is used. Quotes are saved with `quote_time` and `fx_source='YAHOO'`, and
  labelled "HH:MM · Yahoo Finance real-time · … (Yahoo live)". Yahoo's own exchange
  delay still applies: US listings are real time, XETRA/LSE are ~15 min delayed.
  The estimate's `realtime` field (null for past dates) counts searches +
  instruments + currencies of earlier quotes. A regular fetch that reuses a
  real-time quote as fresh converts it at the ECB rate again.
  The same chart meta also yields the previous session's close
  (`chartPreviousClose`, else `previousClose`), stored with the quote as
  `price_quotes.prev_close_e4` (listing currency, ×10⁴). `prev_fx_rate_e8` (the FX
  rate at that same previous session) exists but is NOT filled today: Yahoo's chart
  meta dates neither previous close, so a live pair's previous close cannot be
  proven to belong to the same session (exchange-only holidays), and is not used.
- Shared fresh quotes avoid provider calls. Eulerpool stock TTL is 15m; Eulerpool
  ETF/AV quotes use calendar-day freshness; Yahoo also checks trading-date recency.
  Historical closes are cached separately and are not fresh current quotes.
- Jobs: one running price job/user; poll every 2s; successful quotes saved
  immediately; in-memory jobs expire after 1h and disappear on restart. UI reports
  interruption and reloads saved quotes. Outcomes include fresh/success, backup,
  symbol, quota, FX and upstream failures.
- FX uses the quote's listing currency, never a guessed search currency. Minor
  units `GBp`/`GBX`/`ZAc` convert via major currency ÷100. Eulerpool ETF currency
  comes from profile; NAV reconciliation guards against 100× series scaling errors.
- Applying quotes keeps invested EUR total and recalculates units. Unchanged
  committed `quoteId` copies original price/currency/FX provenance; edited prices
  ignore it. AV unlink removes quotes and preserves committed lines.
- ETF info panels show issuer/index/TER/AUM/domicile/yield/inception/website and
  attribution. Profiles refresh in background on relevant creation/add paths or
  explicitly; default TTL 30 days. AUM is converted to EUR when currency is known.

### Important configuration defaults

| Environment variables | Defaults / constraints |
|---|---|
| `MARKETDATA_MODE`, `MARKETDATA_PRIMARY` | `mock`, `YAHOO`; CI rejects live mode; mock fixtures make no market-data network calls |
| `OPENFIGI_EXCH_CODES`, `EXTERNAL_CACHE_SIZE`, `EXTERNAL_CACHE_TTL` | `GR,GY`, `1000`, `24h`; 1–3 allowed German exchange codes |
| `AV_MAX_PER_MINUTE`, `AV_MAX_PER_DAY`, `AV_MAX_PER_USER_PER_DAY`, `AV_LIMIT_BACKOFF` | `5`, `25`, `10`, `1h` |
| `EULERPOOL_SOFT_DAILY_LIMIT`, `EULERPOOL_QUOTE_TTL`, `EULERPOOL_PROFILE_TTL` | `500`, `15m`, `720h` |
| `YAHOO_SOFT_DAILY_LIMIT`, `YAHOO_MIN_INTERVAL`, `YAHOO_COOLDOWN` | `1000`, `300ms`, `15m` |
| `FRANKFURTER_BASE_URL`, `FX_PROVIDER` | `https://api.frankfurter.dev/v2`, `ECB` |
| Live credentials | Current startup requires both `ALPHAVANTAGE_API_KEY` and `EULERPOOL_API_KEY`, even with Yahoo primary; `OPENFIGI_API_KEY` is optional. Keys remain server-side. |

## API map

All paths below require auth. `{p}` = portfolio ID, `{i}` = instrument ID.
Feature errors use `{error:{code,message,...}}`, with field errors, capacity counts,
duplicate lines or stale preview where applicable.

| Area | Routes |
|---|---|
| Portfolios/items | `GET/POST /api/portfolios`; `GET /api/portfolios/summaries`; `GET/PATCH/DELETE /api/portfolios/{p}`; `GET/POST .../{p}/items`; `DELETE .../{p}/items/{i}`; `PUT /api/users/me/instruments/{i}/pay-delay` |
| Instruments | `GET /api/instruments/search`; `GET .../search/online` (`provider=av` explicit fallback); `POST /api/instruments` |
| Transactions | `GET /api/portfolios/{p}/saving-plan`; `GET/POST .../{p}/transactions`; `DELETE .../{p}/transactions/{txId}`; `GET .../{p}/performance` |
| CSV | `GET/PUT /api/users/me/csv-settings`; `POST /api/portfolios/{p}/imports/parse`, `/imports/validate`, `/imports` |
| Prices | `GET /api/market-data/budget`; `GET /api/portfolios/{p}/price-fetch/estimate`; `POST .../{p}/price-fetch-jobs`; `GET /api/price-fetch-jobs/{jobId}`; `POST /api/price-refresh-jobs` (user-level Yahoo real-time refresh, see below) |
| Linking/history | `GET /api/instruments/{i}/av-symbol/search`, `/yahoo-symbol/search`, `/isin/search`; `PUT .../{i}/av-symbol`, `/yahoo-symbol`, `/isin`; `DELETE .../{i}/av-symbol`; `GET .../{i}/price-history` |
| ETF profiles | `GET /api/instruments/{i}/etf-profile`; `POST .../{i}/etf-profile/refresh` |
| Dividends | `GET /api/portfolios/{p}/dividends?year=`; `POST .../{p}/dividend-sync`; `GET /api/dividend-sync-jobs/{jobId}` |

## Additional implemented feature: dividends

Annual 12-month calendar grouped by estimated pay-date (ex-date + per-user,
per-instrument delay of 0–90 days, moved to the next German working day; nationwide
holidays only), faded ex-date markers in the ex-date month (not counted), gross
per-share EUR amounts at the pay-date ECB rate, units held strictly before ex-date,
yield from the prior close, and totals (received = pay-date passed). A 24-bar stacked
chart (ex-dates grey, payments in instrument colors, expected hatched) sits below. Forecasts repeat
recent actual events annually using current units; show forecast/not-held/unsupported
currency states. Yahoo is the sole dividend provider. Shared cache renders first;
stale/never-synced data refreshes in background, with manual refresh, progress,
per-item errors and ECB historical FX. This does **not** create dividend transactions.

## Source map, verification and boundaries

- Handlers/auth: [`backend/internal/auth`](../backend/internal/auth).
  Persistence/math/migrations: [`backend/internal/database`](../backend/internal/database).
  Provider clients/gateways: [`backend/internal/marketdata`](../backend/internal/marketdata).
  Config: [`config.go`](../backend/internal/config/config.go).
- UI: [`frontend/components`](../frontend/components), routes in
  [`frontend/app`](../frontend/app), decimal/helpers in [`frontend/lib`](../frontend/lib).
  Themes/CSS: [`frontend/app/styles`](../frontend/app/styles), theme engine
  [`frontend/lib/theme.ts`](../frontend/lib/theme.ts), contrast check
  [`frontend/scripts/check-theme-contrast.mjs`](../frontend/scripts/check-theme-contrast.mjs).
  Migrations currently run through **00016** (`00016_previous_close.sql`); add migrations rather than editing applied ones.
- Deployment: [`compose.yaml`](../compose.yaml),
  [`backend/Dockerfile`](../backend/Dockerfile),
  [`frontend/Dockerfile`](../frontend/Dockerfile), per-service `.dockerignore`
  files and [`next.config.mjs`](../frontend/next.config.mjs).
  Setup and HTTPS requirements: [Docker deployment](../README.md#docker-deployment).
- Backend tests cover ownership, concurrency/limits, decimals, imports, valuation,
  providers/jobs/FX and dividends. Frontend tests cover interactions, coupling,
  stale requests, limits, accessibility, charts, imports and dividends.
  Snapshot checks: `go test ./...` and `npm test` both passed on 2026-11-08.
  Run `go test ./...` in `backend`; `npm test` and `npm run typecheck` in `frontend`.
  Mock fixtures live under `testdata/` and embedded provider testdata.
- Browser suite (Playwright, not part of `npm test`): `npm run test:e2e` in `frontend`
  starts its own Next dev server and fulfils every API/auth call in the browser (no backend
  or Google login). 131 cases in 8 spec files: 5 themes × login, portfolios and the five
  tabs at 375×812 and 1280×800, plus 360px boundary, theme-engine, touch-target, overflow
  and rendered-contrast checks. Needs Chromium (`npx playwright install chromium`) and its
  system libraries; see [`frontend/e2e/README.md`](../frontend/e2e/README.md).
- At `7040330`, `npm test`, `npm run typecheck`, `npm run build` and the 131-case
  `npm run test:e2e` suite passed.
- Docker checks passed on 2026-11-08: Compose validation, both image builds,
  frontend tests/type checking and isolated container smoke tests. Verified healthy
  non-root services, dedicated networking, internal-only backend port, writable
  SQLite storage, static assets, API forwarding and secure OAuth cookies/callback.
  Local Go dependency downloads required the host TLS proxy CA as a temporary
  build secret; it is not included in the deployment configuration or runtime images.
- This snapshot is based on source/test inspection and local tests; live-provider
  behavior and every original acceptance checkbox have not been independently audited.
  Provider observations: [`yahoo-notes.md`](yahoo-notes.md),
  [`eulerpool-notes.md`](eulerpool-notes.md). Historical specs describe intent;
  unchecked boxes alone do not indicate missing implementation.
- Still unsupported: sells, transaction editing, fees, taxes, splits, dividend
  bookkeeping/reinvestment and a portfolio-item removal UI.
