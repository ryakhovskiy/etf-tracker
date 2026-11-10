# Holdings

A small Go + Next.js app with Google login/signup, SQLite-backed users and refresh tokens, JWT access tokens, and private portfolios with a search-driven stock/ETF picker.

See [Implemented feature facts](docs/feature-facts.md) for the concise agent reference, current provider roles, and implementation status across the feature specs.

## Setup

Requires Go 1.27.3 or newer and Node.js 22.18 or newer (including native TypeScript support for the frontend tests).

1. Create OAuth credentials in Google Cloud Console. Add `http://localhost:8080/auth/google/callback` as an authorized redirect URI.
2. Copy `.env.example` to `.env` and fill in the Google credentials.
3. Start the API:

   ```sh
   cd backend && go run ./cmd/api
   ```

4. Start the web app in a second terminal:

   ```sh
   cd frontend && npm install && npm run dev
   ```

Open http://localhost:3000. Google OAuth handles both new-user signup and returning-user login. Users and hashed refresh tokens are persisted in `DATABASE_PATH` (default: `data/holdings.db`); access and refresh tokens are issued only as HttpOnly cookies.

## Docker deployment

Install Docker Engine with the Compose plugin on the Linux server. From the repository root, copy `.env.example` to `.env` if needed and set your real credentials and public HTTPS URLs, for example:

```dotenv
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
JWT_SECRET=your-random-secret-at-least-32-characters
FRONTEND_URL=https://holdings.example.com
GOOGLE_REDIRECT_URL=https://holdings.example.com/auth/google/callback
```

Generate `JWT_SECRET` with `openssl rand -hex 32` and register the exact `GOOGLE_REDIRECT_URL` in Google Cloud Console. If the public HTTPS URL includes `:9443`, include that port in both URLs above. Keep the other provider settings in `.env` as needed. Compose sets `APP_ENV=production`, `SECURE_SESSION_COOKIE=true`, `PORT=9000`, and the container database path automatically, overriding local development values.

Build both production images and start both containers with one command:

```sh
docker compose up -d --build --wait
```

To build images without starting containers:

```sh
docker compose build
```

Both Dockerfiles use multiple stages. The backend runtime contains the stripped Go binary, CA certificates, and timezone data; migrations and mock market data are embedded in the binary. The frontend runtime contains Node.js, the [Next.js standalone output](https://nextjs.org/docs/app/api-reference/config/next-config-js/output), static assets, and any `public` assets. Build tools, source trees, local `.env` files, and the full development dependency tree are excluded from the final images. Both services run as unprivileged users.

The two containers join the dedicated `holdings-network` bridge network. The frontend listens on container port **9443**, published only on the server's loopback interface as **`127.0.0.1:9443`**, so it is not reachable from the network directly and Docker's port publishing cannot bypass the host firewall. It forwards `/api/*` and `/auth/*` to **`backend:9000`** over that network, so the browser uses the frontend origin for API requests and cookies. The backend listens on **9000** and is accessible only within the Docker network. `NEXT_PUBLIC_API_URL` is compiled as an empty string in the Docker build; no frontend environment file or server-specific image rebuild is needed.

Port 9443 serves **HTTP inside this setup**. Configure your HTTPS reverse proxy to forward the entire public site to `http://127.0.0.1:9443`; the public URLs in `.env` must match the browser-facing HTTPS origin. A port number alone does not enable TLS. The API requires HTTPS URLs and secure cookies in production.

SQLite files persist in the `holdings_holdings-data` named volume at `/app/data`. The volume is initialized with permissions for the backend service user. Existing host databases are not imported automatically; copy any existing database into that volume while the backend is stopped and preserve ownership `10001:10001`. Back up the volume before upgrades. `docker compose down` preserves it; `docker compose down -v` deletes it.

Check service health and logs with `docker compose ps` and `docker compose logs -f`. The frontend starts after the backend health check passes. After pulling source updates, run the same `docker compose up -d --build --wait` command to rebuild and recreate the services.

## Database migrations

The API applies pending [Goose](https://pressly.github.io/goose/documentation/provider/) migrations before accepting requests. SQL migrations live in [backend/internal/database/migrations](./backend/internal/database/migrations) and are embedded into the binary. Goose records applied versions in `goose_db_version`; restarting the API does not reapply them. Add a new numbered SQL file with `-- +goose Up` and `-- +goose Down` sections for each schema change, then rebuild the API. Do not edit already applied migrations.

The initial migration adopts the previous schema without deleting users. The session migration invalidates existing sessions because legacy refresh tokens do not record their family history; users must sign in again once after upgrading. Back up the database before upgrading. Run only one API instance against a database file and keep its directory writable only by the service owner.

## Sessions and production

Set `APP_ENV=production`, `SECURE_SESSION_COOKIE=true`, HTTPS frontend/callback URLs, Google credentials, and an explicit random `JWT_SECRET` of at least 32 characters. Generate a secret with `openssl rand -hex 32`; the development fallback and example placeholder are rejected in production. Frontend and API must share the same site (for example `app.example.com` and `api.example.com`) because session cookies use `SameSite=Strict`.

Access tokens expire after 15 minutes and are checked against server-side sessions on every request. Sessions expire at most 30 days after login. Logout and refresh-token replay revoke the whole session immediately, including its access tokens. Storage failures return 503; failed logout preserves cookies so the user can retry. The frontend serializes refresh and logout across tabs with Web Locks where available, with a same-tab fallback queue.

Run backend checks from [backend](./backend): `go test ./...`, `go vet ./...`, and `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`. Run `CGO_ENABLED=1 go test -race ./...` where a C compiler is available.

Run frontend checks from [frontend](./frontend): `npm ci`, `npm test`, `npm run typecheck`, `npm run build`, and `npm audit`. The frontend uses patched Next.js 16 and React 19; the obsolete `next lint` command was replaced with a type-check script. See the [Next.js upgrade guide](https://nextjs.org/docs/app/guides/upgrading/version-16) for framework changes.

## Portfolios and instruments

Signed-in users can create, rename, delete, and open their own portfolios. The portfolio picker searches a seeded instrument catalog by name, ticker, ISIN, or WKN after four characters. Select instruments and add them in one atomic batch. Portfolio membership removal is currently available through the API only.

New Goose migrations create the portfolio tables and seed 21 stocks and ETFs. Every SQLite connection enables foreign keys; portfolio deletion cascades to its items. All `/api/portfolios` and `/api/instruments/search` routes require a valid session and hide other users’ portfolios with 404 responses.

Optional positive integer environment variables `MAX_USERS` (10), `MAX_PORTFOLIOS_PER_USER` (10), and `MAX_ITEMS_PER_PORTFOLIO` (30) override the defaults at API startup. Count checks and inserts use `BEGIN IMMEDIATE` transactions. Existing users can still sign in when signups are full. Add-item requests accept at most 30 ids per batch, including when the configured portfolio capacity is higher.

Frontend tests include DOM interaction tests for portfolio limits, search cancellation, keyboard selection, and batch adds. Run them with `npm test` from [frontend](./frontend).

## CSV imports

Each portfolio has an **Import** tab for CSV and text files up to 1 MB and 5,000 data rows. The defaults read German Excel exports: semicolon delimiter, comma decimals, dot thousands separators, euro suffixes, and `DD.MM.YYYY` dates. Encoding detection accepts valid UTF-8 (including BOM) and otherwise uses Windows-1252. Reference files in both encodings live in [testdata/import](./testdata/import).

Configure column mappings and parsing, review and edit the paginated preview, and decide whether to skip or import duplicates. Amount means units; Total € means money invested. Editing units or price recalculates the total; editing total recalculates units. A mismatch from the broker's original total is a warning. Known instruments are added to the portfolio automatically; unknown instruments require a valid ISIN. Imports obey the portfolio item limit and create one CSV transaction per date, preserving each row's units, price, and total.

Configuration is saved per user in SQLite, either explicitly with **Save configuration** or atomically with a successful import. Files are never persisted. The import transaction creates instruments, portfolio items, transactions, and settings together; any failure rolls everything back. Migration 6 adds the settings table and instrument source.

Authenticated endpoints are `GET/PUT /api/users/me/csv-settings`, `POST /api/portfolios/{id}/imports/parse` (multipart file and settings), `/imports/validate` (JSON rows), and `/imports` (JSON rows and settings). Parse and validation responses include a `revision`; send that revision with the final import to confirm the exact validated result. A changed result returns `409 IMPORT_STALE` with a fresh `error.preview` for review. JSON numbers are decimal strings with dot separators; dates use `YYYY-MM-DD`.

## Prices, symbol linking and FX

On the **Transactions** tab, link each instrument to an Alpha Vantage listing, select rows, then use **Fetch prices**. The estimate shows the maximum AV request cost before starting a background job. Fetching is available for today or the previous weekday in Europe/Berlin. Applying a quote preserves the invested total and recalculates units. Quotes fetched today are shared across users; a shared non-EUR quote is re-converted at the ECB rate for each job's transaction date without another AV request, so every line uses the FX of its own date. Saved quote defaults compete with purchase prices by their trading dates.

`MARKETDATA_MODE=mock` (the default) uses embedded AV and FX fixtures without network access, so searches only find the fixture listings (e.g. "SAP", "tesco"). Set `MARKETDATA_MODE=live` to call `https://www.alphavantage.co/query?function=SYMBOL_SEARCH&keywords=…&apikey=…` and `function=GLOBAL_QUOTE&symbol=…`; live mode requires `ALPHAVANTAGE_API_KEY`. The public `demo` key only answers Alpha Vantage's example queries (e.g. keywords `tesco`); other queries return a notice that is reported as `UPSTREAM_ERROR`. The default AV limits are 5/minute, 25 per rolling 24 hours globally, and 10 per user; configure them with `AV_MAX_PER_MINUTE`, `AV_MAX_PER_DAY`, and `AV_MAX_PER_USER_PER_DAY`. SQLite stores reservations and provider backoff across restarts. Calls queue through a single worker; failed calls still consume budget. `AV_LIMIT_BACKOFF` defaults to `1h`. API keys stay on the server and provider errors never expose their request URLs.

Non-EUR quotes use ECB rates from Frankfurter, independently of AV quota. `FRANKFURTER_BASE_URL` defaults to `https://api.frankfurter.dev/v2`; `FX_PROVIDER` defaults to `ECB`. Publications are stored permanently, with the latest available rate on or before the transaction date accepted for at most seven calendar days. London pence listings use GBP rates after dividing prices by 100. Committing an unchanged quoted price preserves its original price, currency, rate, and publication date. Unlinking deletes cached quotes while retaining committed transaction details.

Authenticated routes include `GET /api/market-data/budget`, `GET /api/portfolios/{id}/price-fetch/estimate`, `POST /api/portfolios/{id}/price-fetch-jobs`, and `GET /api/price-fetch-jobs/{jobId}`. Jobs belong to their initiating user and expire after one hour. Successful quotes are saved immediately. If the API restarts, the UI reports the interruption and reloads the prices already saved. Symbol linking uses `GET /api/instruments/{id}/av-symbol/search?q=` and `PUT/DELETE /api/instruments/{id}/av-symbol`. Alpha Vantage searches symbols and names, not ISINs or WKNs, so the Link dialog pre-fills an editable search term: the ticker without a Yahoo-style exchange suffix (`ISPA.DE` → `ISPA`), or the fund name without share-class words when the ticker is an ISIN/WKN. ISIN/WKN terms are rejected before spending a request. In mock mode, searches only match the small embedded fixture. The instrument picker offers an explicit, quota-bearing AV keyword search only after OpenFIGI returns no results or fails.


## Dashboard performance and current value

The Dashboard shows total invested, holdings valued at purchase prices, and purchase-price performance in EUR. **Fetch current value** estimates the AV cost before starting the existing price-fetch job for today in Europe/Berlin. Fresh shared quotes are free; when the daily budget is insufficient, instruments with the highest holdings value at purchase prices are fetched first. Progress and the next available request time appear beside the figures.

Current value and performance are rebuilt from stored quotes on every dashboard load. Each held instrument uses its newest quote unless its latest purchase is newer, in which case it uses that purchase price. A partial badge lists fallback instruments and reasons. No current figures appear until at least one instrument uses a quote. The figures include quote dates and fetch time; a dashed chart extension appears only when the newest quote date is after the last transaction. Fetching from either tab refreshes the mounted views, while transaction fields with edited prices retain those edits.

`GET /api/portfolios/{id}/performance` retains its historical fields and adds `atPurchase` and nullable `current` valuations, including per-instrument sources and FX provenance. Values and percentages use exact arithmetic with half-up rounding; percentages are null for zero investment. Price-fetch job requests accept optional `priority: "HOLDINGS_VALUE"` to order held instruments by their purchase-price value. No new database tables are required.

## Dividend calendar

The **Dividends** tab shows a 12-month calendar per year, from the year of the portfolio's first transaction through next year (`‹ ›`, the year stays in the URL as `?year=`). Every item currently in the portfolio contributes its dividends, placed in the month of their **pay-date**. Yahoo only provides ex-dates, so the pay-date is estimated: ex-date + the instrument's **pay-date delay** (0–90 days, set per user and instrument under **Manage items → In the portfolio**, default 0, one value across all of the user's portfolios), moved forward to the next German working day (weekends and the nine nationwide public holidays; regional holidays are not considered). A payment can therefore land in the next month or the next year. A dividend that goes ex in one month and is paid in a later one also appears, faded, as an ex-date marker in its ex-date month; markers are not counted in any total. Received means the pay-date has passed; everything else is Expected. Amounts are gross and in EUR: the per-share amount in the listing currency times the ECB rate of the latest publication on or before the pay-date (the latest rate while the pay-date is in the future; pence listings use GBP ÷ 100), rounded to 4 decimals; totals multiply that by the units held from lines traded **before** the ex-date and round half-up to cents. Yield is the per-share amount divided by the close of the last trading day before the ex-date. A past event with no units held is shown as "not held" with 0,00 €; a currency the ECB does not publish shows "–" with a reason.

Below the calendar a bar chart shows 24 stacked bars: per month, the dividends going ex (translucent grey, one shade per instrument) and the payments (the dashboard's instrument colors; expected payments hatched), one segment per dividend. Hovering, focusing or tapping a segment highlights it and shows its details. The pay-date logic lives in the backend (`marketdata.PayDate`) so it can be refined without frontend changes.

Future months show forecasts: every actual event of the last 365 days repeats every 12 months, unless the date is not after today or an actual event lies within ±20 days. Forecasts use current units, the latest ECB rate and the latest close, and carry the ex-date they are based on.

Dividend events, closes and the listing currency come from Yahoo Finance (one chart request with `events=div` per instrument, no fallback provider) and are cached in `dividend_events` / `dividend_sync` (migration 13), shared by all users. Opening the tab renders the cache at once and syncs, in the background, instruments never synced, synced more than 7 days ago, or synced from a later date than the portfolio needs (1 January of the year before its first transaction). Later syncs refetch from 400 days before the last sync, upsert corrected amounts and delete events that disappeared from the covered range. **Refresh dividends** syncs every item. A failed instrument keeps its cached data and appears in an error banner. FX publications for all needed dates load with one Frankfurter time-series request per currency.

Authenticated endpoints: `GET /api/portfolios/{id}/dividends?year=` (400 `INVALID_YEAR` outside the bounds), `POST /api/portfolios/{id}/dividend-sync` with optional `{"scope":"ALL"|"STALE"}` → `202 {jobId}` (`409 SYNC_RUNNING` with the running `jobId` while one runs for the portfolio; instruments are processed one at a time, at least 1 s apart), and `GET /api/dividend-sync-jobs/{jobId}` → `{status, done, total, items}`. The UI polls every 2 s and reloads the calendar when the job is done. Each entry carries `exDate`, `payDate`, `payDateShifted`, `payDelayDays` and `paid`; each month has `entries`/`total` (payments) and `exEntries`/`exTotal` (ex-date markers). `GET /api/portfolios/{id}/items` lists the portfolio's items with their latest stored quote (EUR, no provider call) and the user's delay; `PUT /api/users/me/instruments/{instrumentId}/pay-delay` with `{"payDelayDays":0..90}` stores it (`instrument_pay_delays`, migration 14; 404 for instruments not in any of the user's portfolios).
