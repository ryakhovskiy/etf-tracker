# Holdings

A small Go + Next.js app with Google login/signup, SQLite-backed users and refresh tokens, JWT access tokens, and private portfolios with a search-driven stock/ETF picker.

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
