# AGENTS.md
Holdings is a private stock/ETF portfolio tracker: a Go API (`backend/`) with SQLite, and a Next.js App Router frontend (`frontend/`). Google OAuth is the only login. `docs/feature-facts.md` is the detailed feature reference (behaviour, provider roles, full API map); `README.md` covers setup and deployment. When the docs and the code/tests disagree, the code and tests are correct.

## Commands

Requires Go ≥ 1.27.3 and Node ≥ 22.18. Copy `.env.example` to `.env` at the repo root; the backend loads `.env` or `../.env`.

Backend (run from `backend/`):
```sh
go run ./cmd/api                          # API on :8080 (PORT)
go test ./...                             # all tests
go test ./internal/database -run TestName # single test
go vet ./...
CGO_ENABLED=1 go test -race ./...         # needs a C compiler
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Frontend (run from `frontend/`):
```sh
npm ci && npm run dev                     # http://localhost:3000
npm test                                  # node:test: lib/*.test.mjs (strip-types) + tests/*.test.mjs (tsx + jsdom)
node --import tsx --test tests/dividends.test.mjs                          # single file
node --import tsx --test --test-name-pattern="pattern" tests/x.test.mjs    # single test
npm run typecheck                         # next typegen + tsc. There is no lint script; `next lint` was removed.
npm run build
```
For local dev, set `NEXT_PUBLIC_API_URL=http://localhost:8080` in `frontend/.env.local`.

Docker (from the repo root): `docker compose up -d --build --wait`. `build.sh` builds and pushes the `kryakhovskiy/etracker-*` images.

## Architecture

**Backend packages (`backend/internal/`)**
- `auth`: despite its name, this package holds **all HTTP handlers**, not just OAuth. That covers portfolios, transactions, CSV import, the market-data providers, and dividends. `Handler.RegisterRoutes` wires each feature's `register*Routes`, and routing uses the stdlib `http.ServeMux` method patterns (`"GET /api/..."`). Background workers (price-fetch jobs, dividend sync, the AV queue) are owned by the Handler and stopped in `Handler.Close`.
- `database`: the SQLite store (`modernc.org/sqlite`, pure Go, `CGO_ENABLED=0`), plus domain math and queries. Goose SQL migrations are embedded from `database/migrations/`.
- `marketdata`: provider clients for Yahoo, Eulerpool, OpenFIGI, Alpha Vantage, and Frankfurter/ECB FX. It also has caching, German working-day logic, and `PayDate`. Mock fixtures are embedded from `marketdata/testdata/`.
- `httpapi`: CORS allowlist, security headers, and JSON response helpers.
- `config`: env loading and validation. Production mode rejects weak or missing secrets and non-HTTPS URLs.

**Frontend**
- `frontend/app/portfolios/[id]/*`: tab routes (dashboard, transactions, dividends, import, items). The portfolio tabs stay mounted, so drafts and selection survive tab switches.
- `frontend/components`: the UI. `frontend/lib`: API client (`lib/api.ts` → `authenticatedFetch` in `lib/session.ts`, which handles token refresh with Web Locks), types, and `decimal.js` helpers.
- In Docker, Next.js rewrites `/api/*` and `/auth/*` to `backend:9000` (`API_PROXY_URL`). Browser requests and cookies therefore stay on the frontend origin, and `NEXT_PUBLIC_API_URL` is compiled as `""`.

## Cross-cutting rules

- **Migrations:** add a new numbered `NNNNN_name.sql` file with `-- +goose Up` / `-- +goose Down`. Never edit an applied migration. Migrations run automatically at API startup.
- **Decimals:** never use floats for money.
  - The API sends decimals as dot-separated **strings**. The backend uses `math/big` and the frontend uses `decimal.js`, rounding half-up.
  - Storage scales: units ×10⁶, price ×10⁴, money ×10², FX rate ×10⁸.
  - The UI accepts comma or dot input and displays `de-DE`.
- **Terminology trap:** in the transaction API, `amount` = EUR total and `quantity` = units. In CSV import, the column "Amount" = units and "Total €" = money.
- **Authorization:** every feature route requires a session (`401`). Portfolio, transaction and job queries are user-scoped, and another user's resource returns `404`, not `403`. Instruments, quotes, FX and provider caches are shared across users.
- **SQLite:** foreign keys are enabled on every connection. Capacity checks and writes use `BEGIN IMMEDIATE`, and batch adds, commits and imports are atomic. Only one API instance may run per DB file.
- **Errors:** feature errors use `{error:{code,message,...}}` with stable codes such as `DUPLICATE_TRANSACTION`, `IMPORT_STALE`, `SYNC_RUNNING` and `ITEM_HAS_TRANSACTIONS`.
- **Market data:**
  - `MARKETDATA_MODE=mock` is the default and makes no network calls, serving the embedded fixtures. CI must use mock mode, and config enforces this.
  - `MARKETDATA_PRIMARY` defaults to `YAHOO`. Search falls back Yahoo → Eulerpool → OpenFIGI, and WKN queries go straight to OpenFIGI.
  - Alpha Vantage is never spent automatically: it is an explicit, cost-labelled fallback behind a persisted budget/quota gateway.
  - Live mode currently requires both `ALPHAVANTAGE_API_KEY` and `EULERPOOL_API_KEY`.
- **FX:** always use the quote's listing currency. Minor units (`GBp`/`GBX`/`ZAc`) are converted via the major currency ÷100. Rates are ECB, except real-time Yahoo prices, which use a live Yahoo `{CCY}EUR=X` rate marked `fx_source='YAHOO'`.
- **Dividends:** the pay-date is estimated in the backend (`marketdata.PayDate`) from ex-date + a per-user delay, moved to the next German working day. Yahoo is the only dividend source.
- **Not supported yet:** sells, editing transactions (users delete and re-enter instead), fees, taxes, splits, and dividend bookkeeping.

## Frontend tests

The tests are plain `node:test` files. They set up a `JSDOM` global by hand, dynamically import `.tsx` components through `tsx`, and mock `fetch` with `t.mock.method(globalThis, 'fetch', ...)`. Never pass DOM nodes to `assert.equal`/`deepEqual` (including inside `waitFor`). When the assertion fails, the diff walks the jsdom object graph and can use 9+ GB of memory. Compare primitives instead, such as `textContent` or `getAttribute(...)`, or use `assert.ok(a === b)`.
