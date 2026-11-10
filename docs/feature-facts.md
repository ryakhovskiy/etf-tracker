# Holdings: implemented feature facts

Snapshot: 2026-11-08. Agent reference for the current checkout, consolidating
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
| `/` | Login or portfolio list; create, rename, confirm deletion; counts/limit hints |
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

- Price fetch starts only on user action after estimate. Primary quote failures
  become `NEEDS_BACKUP`; alternative providers require separate clicks. Default
  UI offers Eulerpool then AV after Yahoo; Eulerpool-primary UI can offer Yahoo
  before AV. No automatic AV spend.
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
| Portfolios/items | `GET/POST /api/portfolios`; `GET/PATCH/DELETE /api/portfolios/{p}`; `GET/POST .../{p}/items`; `DELETE .../{p}/items/{i}`; `PUT /api/users/me/instruments/{i}/pay-delay` |
| Instruments | `GET /api/instruments/search`; `GET .../search/online` (`provider=av` explicit fallback); `POST /api/instruments` |
| Transactions | `GET /api/portfolios/{p}/saving-plan`; `GET/POST .../{p}/transactions`; `DELETE .../{p}/transactions/{txId}`; `GET .../{p}/performance` |
| CSV | `GET/PUT /api/users/me/csv-settings`; `POST /api/portfolios/{p}/imports/parse`, `/imports/validate`, `/imports` |
| Prices | `GET /api/market-data/budget`; `GET /api/portfolios/{p}/price-fetch/estimate`; `POST .../{p}/price-fetch-jobs`; `GET /api/price-fetch-jobs/{jobId}` |
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
  Migrations currently run through **00013**; add migrations rather than editing applied ones.
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
