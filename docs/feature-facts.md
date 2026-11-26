# Holdings: implemented feature facts

Snapshot: 2026-11-08; updated 2026-11-13 for the frontend redesign (themes, responsive tile UI) and 2026-11-16 for the PWA. Agent reference for the current checkout, consolidating
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
| Progressive web app / later frontend work | Implemented extension | Installable (manifest, icons, in-app Install control); service worker caches only app code/fonts/icons and shows an offline page; non-forcing update notice; never caches `/api` or `/auth` |
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
- Identical consecutive quotes are not stored again: when every stored price field
  equals the instrument's newest row (NULL-safe), that row's fetch time is updated
  instead. Migration 00019 removed existing runs of duplicates once, keeping the newest
  fetch of each run.
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

### Favorites

Backend contract. A user stars a **title (instrument)**, not a portfolio item: one
star per user and instrument however many of their portfolios hold it. Stars are
ordered; tiles on the portfolio list page follow `position`.

- Table `favorites(user_id, instrument_id, position, created_at)`, primary key
  `(user_id, instrument_id)` (migration `00018_favorites.sql`). `position` is
  contiguous from 0 per user: appending uses the count, and every removal and
  reorder renumbers inside the same `BEGIN IMMEDIATE` transaction.
- Only an instrument in one of the caller's portfolios can be starred, otherwise
  `404 NOT_FOUND`. Removing an item from the caller's last portfolio holding it,
  or deleting such a portfolio, deletes the favorite and compacts the positions
  in the same transaction as that removal.
- `GET /api/favorites` → `{favorites:[{instrumentId, name, ticker, position,
  priceEur, change:{amount, percent}|null, asOf, reasons[]}]}` in order. It reads
  stored quotes only (no provider call). `priceEur` is the latest stored quote in
  EUR as stored (4 dp), `asOf` the same object as a summary's (`quoteDate`,
  `quoteTime`, `fetchedAt`, `providers`, `realtime`); both are `null` without a
  quote. `change` is price − previous close, both in EUR (`amount` signed, 2 dp,
  `percent` against the previous close), with the previous close converted by the
  summaries' rules (`previousClose()`: the previous day's `{CCY}EUR=X` close,
  minor units through the major currency). It is `null`, with a reason, when
  data is missing: `NO_QUOTE`, `NO_PREVIOUS_CLOSE`, or `FX_PREVIOUS_UNAVAILABLE`
  (the previous close could only be converted at the quote's own rate; unlike a
  summary, a tile has no partial marker, so no approximate figure is shown).
  `reasons` is always an array.
- `PUT /api/favorites/{i}` stars and appends (idempotent, keeps the place when
  already starred) → `200 {instrumentId, position}`. `DELETE /api/favorites/{i}`
  → `204`, compacts; `404` when the caller has not starred it.
- `PUT /api/favorites/order` with `{instrumentIds:[…]}` must list exactly the
  caller's current favorites, each once, else `409 FAVORITES_STALE` and nothing
  changes. It is atomic and returns `{favorites:[…]}` like the GET. A missing or
  malformed body is `400 INVALID_BODY`.
- `GET /api/portfolios/{p}/items` adds `favorite: boolean` per item (the caller's
  own star).
- Another user's data is never visible: a title the caller does not hold or has
  not starred is `404`, never `403`.

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
  previous close × the daily close of the `{CCY}EUR=X` pair dated exactly like that
  previous close (÷100 for `GBp`/`GBX`/`ZAc`, through the major currency), rounded
  to 4 dp like a quote; EUR listings need no rate. The stored
  `prev_close_date` and `prev_fx_rate_e8` come from the dated daily bars of the
  same real-time chart requests (see Market data), so the daily FX move is part of
  the change and an exchange-only holiday gap (stock previous session Wednesday,
  FX Thursday) still pairs the right dates. Missing pieces make the change
  partial (`changePartial`) without hiding it: `NO_PREVIOUS_CLOSE` (units held
  before but no stored previous close: that position's before-session part is
  omitted), `FX_PREVIOUS_UNAVAILABLE` (the FX bars hold no close dated like the
  instrument's previous close, or the quote was converted at an ECB fallback rate,
  or it is an older quote without a stored date: the previous close is converted
  at the quote's own rate, an approximation), and
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
  units are excluded) **plus the user's favorites** (starred titles need not be
  held; see Favorites). The request `estimate`, `stale`, `newestFetchedAt` and
  `NOTHING_TO_PRICE` use the same set. It runs the existing real-time Yahoo runner: Yahoo only,
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
  - `refresh` in the summaries response: `stale` (no quote for any held or
    favorite instrument, or the NEWEST fetch time among them is strictly older than
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
    `GET /api/price-fetch-jobs/{id}` by long polling (see Jobs; it stops after 5
    consecutive poll failures and shows the stored figures). `FRESH`, `THROTTLED` and
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

## Progressive web app

Frontend only: no backend, API or database involvement. Offline means **an offline page only**.

- **Installable.** `/manifest.webmanifest` comes from `frontend/app/manifest.ts`: `id`/`scope`/`start_url`
  `/`, name and short name "Holdings", `display: standalone` (orientation is not locked), icons 192 and 512
  `any` plus 512 `maskable`. Production builds pass Chromium's installability check. Production needs
  HTTPS for the worker (`localhost` is exempt); config already requires HTTPS URLs in production.
- **Icons** derive from the brand mark (an "h" in a rounded tile). `npm run pwa:assets` (tsx running
  `frontend/scripts/generate-pwa-assets.mjs`, using the `sharp` that Next already installs; no network, no
  new dependency) regenerates the committed files in `frontend/public`: `icons/` (192, 512, maskable 512,
  `apple-touch-icon` 180 px opaque), `favicon.svg`, `favicon-32.png`, and `offline.html`/`offline.css` plus
  font copies for the offline page. Colours are read from `tokens.css`. `appleWebApp` metadata
  (capable, title "Holdings", default status bar) and `viewport-fit=cover` are set in `app/layout.tsx`.
- **Colours.** `lib/pwa-colors.ts` is the one place with literal colours outside `tokens.css`: each
  theme's `--bg` for `<meta name="theme-color">`, and the Light `--bg` for the manifest's `theme_color` and
  `background_color`. `tests/pwa-colors.test.mjs` fails if they drift from `tokens.css`. The meta tag follows
  the active theme (all five plus System): the head script sets it before paint and `applyTheme` keeps it in step.
- **Service worker.** Source `frontend/pwa/sw.js` (plain JS, scope `/`), **served at `/sw.js` by
  `app/sw.js/route.ts`**, which embeds the build id into the script at build time. A static file would be
  byte-identical across deploys, and an already-open tab would never see an update. Consequence: **every
  redeploy is a new worker and shows the update notice, even when nothing visible changed.**
  `/sw.js` is served with `Cache-Control: no-cache, no-store, must-revalidate`, `Content-Type:
  application/javascript; charset=utf-8`, `Content-Security-Policy: default-src 'self'; script-src 'self'` and
  `Service-Worker-Allowed: /` (route response and `next.config.mjs` headers). It is registered in
  **production builds only** (`next dev` never registers one) and checks for updates on load, when the tab becomes
  visible again, and hourly.

| Request | What the worker does | Stored on the device |
|---|---|---|
| `/offline.html`, `/offline.css`, `/offline.js`, `/fonts/*` (two woff2), `/icons/*`, `/favicon.svg`, `/favicon-32.png`, `/manifest.webmanifest` | Precached at install; served from cache | `holdings-shell-<build>` |
| `/_next/static/*` (hashed, immutable) | Cache-first; stored only after the admission check below | `holdings-static-<build>` |
| Page navigations | Network-first; on **network failure** the cached offline page; online HTML is never stored | nothing |
| `/api/*`, `/auth/*` (OAuth redirects included), non-GET, cross-origin, RSC/data fetches (`RSC`, `Next-Router-Prefetch` header or `_rsc`), `/sw.js` | Not intercepted (no `respondWith`) | **never** |

  **Admission:** every Cache Storage write (precache and runtime) passes one validator: exactly status 200,
  same-origin (`basic`), not redirected, final URL equal to the requested URL, and a content type that matches
  the file extension; HTML is accepted only for `/offline.html`. A failed precache check fails the install. Cache
  Storage errors (quota, eviction) never drop a successful network response.
  **Portfolio data and login responses are never stored on the device:** nothing from `/api/*` or `/auth/*`
  is ever cached, so sign-out needs to clear nothing. Cache names are versioned by build; `activate` deletes older
  `holdings-*` caches.
- **Offline page.** `/offline.html` is static (no React): "You're offline", a **Retry** button (reloads the
  current URL) and an "Open Holdings" link. It is themed from the stored `holdings.theme` by the app's own
  inline theme script, so all five themes and System work with zero network. It answers any failed
  navigation (`/`, a portfolio route, an unknown route).
- **Update notice.** When a new worker is waiting while an old one controls the page, a status notice "New
  version available" appears with **Reload** and **Later** (`data-testid` `update-notice`, `update-reload`,
  `update-dismiss`). Nothing reloads by itself, so drafts and CSV imports in progress are safe. Reload posts
  `SKIP_WAITING` (the only way a worker activates early) and the page reloads once when the new worker takes over.
  The first install shows no notice. If another tab already activated the update, Reload in this tab
  reloads that document once; a tab never reloads without its own click.
- **Install control.** A button "Install app" (`data-testid="install-app"`) in the header's account area, next to
  Sign out (icon-only at 480 px and below, name kept). It shows only after the browser fires `beforeinstallprompt`
  (Chrome, Edge, Android) and its click calls `prompt()`. On iPhone/iPad Safari that is not already standalone
  it opens the hint dialog "Add to Home Screen": "Tap Share, then Add to Home Screen" (Safari is
  identified positively by its `Version/` and `Safari/` tokens; other iOS browsers and in-app web views get no hint). It is hidden when the
  app runs standalone and after `appinstalled`.
- **Standalone polish.** The header, page, login and bottom tab bar pad with `env(safe-area-inset-*)`; the
  sticky header clears the notch. Unknown URLs render `app/not-found.tsx` (header plus a link home), so every
  page can be left without a browser back button. The update notice, install hint and offline page use the same
  tokens, 44 px targets and `prefers-reduced-motion` rules as the rest of the UI.

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
  `v8/finance/chart/{symbol}?interval=1d&range=5d` once per instrument (the same
  one request as before; five daily bars cover a long weekend or holiday), reading
  `meta.regularMarketPrice`/`regularMarketTime` for the current price. The chart endpoint needs only
  the browser User-Agent, so no cookie or crumb is spent (a crumb already held is
  attached). Foreign currencies use a live Yahoo rate `{CCY}EUR=X` (1 request per
  currency, same EUR-per-unit orientation as ECB). If none can be had, the ECB
  rate is used. Quotes are saved with `quote_time` and `fx_source='YAHOO'`, and
  labelled "HH:MM · Yahoo Finance real-time · … (Yahoo live)". Yahoo's own exchange
  delay still applies: US listings are real time, XETRA/LSE are ~15 min delayed.
  **Parallel:** the instruments of one job are fetched at the same time, not one
  after another, so a 5-title refresh takes about one Yahoo round trip. The
  `YahooGateway` allows at most `YAHOO_MAX_CONCURRENCY` requests in flight (default
  10) and starts them at least `YAHOO_MIN_INTERVAL` apart (default 0s). Each foreign
  currency is still fetched **once** per job, however many rows use it: the first
  row asks Yahoo, the others wait for its answer (or the ECB fallback). The request
  count and the estimate are unchanged. Past-date fetches (`runYahooPrices`) fetch
  the History of their instruments in parallel the same way; today's batched quote
  request and Alpha Vantage / Eulerpool jobs are unchanged. The cookie + crumb
  session is built once, however many requests need it, and rebuilt once after a
  401/403. A 429 starts the cool-down at once: requests that have not started are
  refused (`RATE_LIMITED`), requests already in flight finish with their own
  result. The soft daily limit is checked per request, so a parallel run can exceed
  it by at most `YAHOO_MAX_CONCURRENCY - 1` requests. `Handler.Close` cancels running
  Yahoo jobs and the session build and waits for them before the store is closed.
  **FX prefetch:** FX is never on the critical path. When a Yahoo job starts,
  before any price request, it predicts the foreign currencies its rows will use
  from stored data only (the currency of the instrument's latest stored quote, else
  its stored listing currency: Eulerpool listing, then ETF profile; pence listings
  count as their major currency) and starts one FX lookup per currency at once, in
  parallel with each other and with the price requests, ahead of the price workers.
  Real-time jobs prefetch the live Yahoo rate (`{CCY}EUR=X`, with its dated closes
  for the previous-close conversion); today and past-date jobs prefetch the ECB rate
  of the job's date. The rows use the same per-currency memo as the prefetch, so the
  currency is looked up once per job whichever side asks first, and a failed
  prefetch is kept for the job rather than repeated after the price. A currency that
  was not predicted (a title never priced) is fetched on demand through the same
  memo, once, and logged ("not predicted"). A prediction no row uses costs one
  request and is ignored. Once every row has its answer, or the run stopped early
  on a 429, the job cancels any prefetch still running and waits for it to end
  before it is DONE, so an unused lookup never delays the job; `Handler.Close`
  cancels the prefetch goroutines with the job. If the live FX request is itself
  rate limited, the rows fall back to the ECB rate as before. Independent
  currencies' ECB (Frankfurter) requests also run in parallel: `FXService` locks per
  currency, not across currencies; the cache rules are unchanged.
  The estimate's `realtime` field (null for past dates) counts searches +
  instruments + currencies of earlier quotes. A regular fetch that reuses a
  real-time quote as fresh converts it at the ECB rate again.
  The same response's dated daily bars (`timestamp[]`, `indicators.quote[0].close[]`,
  dated in `meta.exchangeTimezoneName`; null closes skipped) give the previous
  session: the last bar dated before the session of `regularMarketTime`. Its close
  and date are stored with the quote as `price_quotes.prev_close_e4` (listing
  currency, ×10⁴) and `prev_close_date`. `meta.chartPreviousClose` is NOT used: with
  `range=5d` it is the close before the first bar. Each `{CCY}EUR=X` request returns
  the pair's bars the same way; the FX close dated exactly like the instrument's
  previous close is stored as `prev_fx_rate_e8` (no neighbouring day is substituted;
  none for EUR, ECB fallback rates, or when the bars lack that date).
- Shared fresh quotes avoid provider calls. Eulerpool stock TTL is 15m; Eulerpool
  ETF/AV quotes use calendar-day freshness; Yahoo also checks trading-date recency.
  Historical closes are cached separately and are not fresh current quotes.
- Jobs: one running price job/user; successful quotes saved
  immediately; in-memory jobs expire after 1h and disappear on restart.
  **Long polling:** every price job (Yahoo real-time, Yahoo prices, Alpha Vantage,
  Eulerpool, `USER_REALTIME`) carries an integer `version`, 1 when queued, bumped
  under the job lock on every change (status change or any item set). `GET
  /api/price-fetch-jobs/{id}` answers at once. With `?since=N` it answers at once
  if `version > N` or the job is `DONE`; otherwise it waits until the job changes
  and answers with the latest state (several changes coalesce into one answer). The
  wait ends after 10 s (`jobWaitMax`; the server `WriteTimeout` is 15 s), when the
  client disconnects, or when graceful shutdown begins, and then answers `200` with
  the current state, same shape. A malformed `since` (not a non-negative integer,
  including empty and overflow) is `400 VALIDATION_ERROR`; 404 rules are unchanged
  (unknown job, another user's job, `DONE` job older than 1 h, with or without
  `since`). Clients poll immediately after each answer with the last `version`
  (waiting 1 s if the version did not change, 2 s after an error). `nextRequestAt`
  (Alpha Vantage queue) is read from the budget outside the job lock on each answer
  and is not part of the version, so clients tick that countdown locally. The
  budget read is skipped, or cancelled if already running, once the client is gone
  or shutdown has begun: those answers are the captured snapshot. `Handler.Drain`
  (registered with `http.Server.RegisterOnShutdown` in `cmd/api`) releases every
  held request the moment `Shutdown` starts, so shutdown never waits for a hold;
  `Handler.Close` then cancels the jobs before the store is closed. UI reports
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
| `YAHOO_SOFT_DAILY_LIMIT`, `YAHOO_MAX_CONCURRENCY`, `YAHOO_MIN_INTERVAL`, `YAHOO_COOLDOWN` | `1000`, `10`, `0s`, `15m`; concurrency ≥ 1; the interval is the minimum gap between request *starts*, and `1` with `300ms` restores one request at a time |
| `FRANKFURTER_BASE_URL`, `FX_PROVIDER` | `https://api.frankfurter.dev/v2`, `ECB` |
| Live credentials | Current startup requires both `ALPHAVANTAGE_API_KEY` and `EULERPOOL_API_KEY`, even with Yahoo primary; `OPENFIGI_API_KEY` is optional. Keys remain server-side. |

## API map

All paths below require auth. `{p}` = portfolio ID, `{i}` = instrument ID.
Feature errors use `{error:{code,message,...}}`, with field errors, capacity counts,
duplicate lines or stale preview where applicable.

| Area | Routes |
|---|---|
| Favorites | `GET /api/favorites`; `PUT/DELETE /api/favorites/{i}`; `PUT /api/favorites/order` |
| Portfolios/items | `GET/POST /api/portfolios`; `GET /api/portfolios/summaries`; `GET/PATCH/DELETE /api/portfolios/{p}`; `GET/POST .../{p}/items`; `DELETE .../{p}/items/{i}`; `PUT /api/users/me/instruments/{i}/pay-delay` |
| Instruments | `GET /api/instruments/search`; `GET .../search/online` (`provider=av` explicit fallback); `POST /api/instruments` |
| Transactions | `GET /api/portfolios/{p}/saving-plan`; `GET/POST .../{p}/transactions`; `DELETE .../{p}/transactions/{txId}`; `GET .../{p}/performance` |
| CSV | `GET/PUT /api/users/me/csv-settings`; `POST /api/portfolios/{p}/imports/parse`, `/imports/validate`, `/imports` |
| Prices | `GET /api/market-data/budget`; `GET /api/portfolios/{p}/price-fetch/estimate`; `POST .../{p}/price-fetch-jobs`; `GET /api/price-fetch-jobs/{jobId}[?since=<version>]` (long poll, see Jobs); `POST /api/price-refresh-jobs` (user-level Yahoo real-time refresh, see below) |
| Linking/history | `GET /api/instruments/{i}/av-symbol/search`, `/yahoo-symbol/search`, `/isin/search`; `PUT .../{i}/av-symbol`, `/yahoo-symbol`, `/isin`; `DELETE .../{i}/av-symbol`; `GET .../{i}/price-history` |
| ETF profiles | `GET /api/instruments/{i}/etf-profile`; `POST .../{i}/etf-profile/refresh` |
| Dividends | `GET /api/portfolios/{p}/dividends?year=`; `POST .../{p}/dividend-sync`; `GET /api/dividend-sync-jobs/{jobId}[?since=<version>]` (long poll, see dividends) |

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

**Parallel sync and long polling.** A dividend sync fetches its instruments in
parallel through the same runner as the price jobs (`forEachParallel`), at most
`YAHOO_MAX_CONCURRENCY` (default 10) requests in flight; the Yahoo gateway still
enforces the daily budget and the 429 cool-down, and there is no extra spacing.
Items finish in any order: each item's status/error is set by its index and `done`
counts the finished ones. A `429` lets the requests in flight finish with their own
results; items that have not started fail with the rate-limit message without a
request (and are recorded like any failed fetch). Unchanged: one running sync per
portfolio (`409 SYNC_RUNNING`), the 30-minute timeout, the `ALL`/`STALE` scope and
the per-item messages. `Handler.Close` cancels the dividend sync before closing the
Yahoo gateway, waits for it, and records nothing for items interrupted by the
shutdown (they report "The sync was interrupted."). The job carries an integer
`version` (1 when queued, bumped on every item and on `DONE`) and
`GET /api/dividend-sync-jobs/{jobId}?since=N` has the contract of the price jobs
(see Jobs above: it answers at once if `version > N` or the job is `DONE`, else
waits at most 10 s and ends early on a change, client disconnect or shutdown; always
`200` with the same shape, `items` always an array; malformed `since` →
`400 VALIDATION_ERROR`; 404 rules unchanged). Both job kinds share one wake-up
mechanism (`jobSignal`, `awaitChange`); no lock is held while waiting. In the
calendar, the FX rates of all currencies it needs are requested concurrently
(`FXService` locks per currency); results and cache semantics are unchanged.

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
  PWA: [`frontend/pwa/sw.js`](../frontend/pwa/sw.js), [`frontend/app/sw.js/route.ts`](../frontend/app/sw.js/route.ts),
  [`frontend/app/manifest.ts`](../frontend/app/manifest.ts), [`frontend/lib/pwa.ts`](../frontend/lib/pwa.ts),
  [`frontend/lib/pwa-colors.ts`](../frontend/lib/pwa-colors.ts), [`frontend/scripts/generate-pwa-assets.mjs`](../frontend/scripts/generate-pwa-assets.mjs),
  generated static files in [`frontend/public`](../frontend/public).
  Migrations currently run through **00018** (`00016_previous_close.sql`, `00017_previous_close_date.sql`, `00018_favorites.sql`); add migrations rather than editing applied ones.
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
- At `1a15a5f`, the 102-case production-only PWA Playwright projects (`pwa-360`, `pwa-375`, `pwa-1280`, against
  `next build` + `next start` with `NEXT_PUBLIC_API_URL=""`) passed, as did the 221-case regular suite.
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
