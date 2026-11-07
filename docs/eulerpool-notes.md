# Eulerpool live API verification

Verified 2026-11-06 against `https://api.eulerpool.com/api/1` (an earlier attempt
the same day was blocked by Cloudflare error 1010; it works now). Fixtures are in
`testdata/marketdata/eulerpool/`. The key is not stored anywhere.

## Auth
- `?token=KEY` works. `Authorization: Bearer KEY` also works (HTTP 200).
- Bad key: HTTP 401 `{"error":"Invalid API key. ..."}`.

## Limits (headers on every counted response)
`X-RateLimit-Limit: 100000`, `X-RateLimit-Remaining`, `X-RateLimit-Reset: 1796083200`
(unix **seconds**), `X-RateLimit-Window: month`. So the limit is **monthly**, not daily.
404 responses carry no rate-limit headers (not counted). Search responses are CDN-cached
(`cf-cache-status: HIT`).

## Response shapes
- `GET /equity/search?q=` → `{"count":N,"results":[{name,isin,ticker,type,currency}]}`.
  `type` is lowercase (`etf`, `stock`). **One row per listing** (same ISIN repeated with
  different tickers, e.g. H4ZJ.DE / H4ZJ.SG / HMWD.L) → dedupe by ISIN needed.
  `currency` can be `null` (ISIN queries) and for keyword queries seems to be the fund's
  base currency, not necessarily the listing currency.
  No match → `{"count":0,"results":[]}` (HTTP 200).
- **WKN:** `equity/search?q=A1C9KK` → empty; `GET /screener/search/A1C9KK` → `[]`.
  WKN is **not supported** by either endpoint (`A1C9KK` is the HSBC MSCI World ETF).
- `GET /etf/profile/{isin}` → object: etfCompany, expenseRatio (number, percent, 0.15),
  aum (number), currency ("GBP"), exchangeRate, nav, inceptionDate, domicile,
  trackingIndex, dividendYield (null), website, updated_at, tickers[]. The profile
  `currency` is ambiguous (nav 36.67 GBP vs a USD-denominated fund).
- `POST /market/quotes/bulk`, body `{"isins":["..."]}` (max 50) →
  `{"quotes":[{isin,ticker,name,price,currency,timestamp,change,changePct}],"errors":[{isin,error:"ISIN not found"}]}`.
  `timestamp` is unix **seconds** (the OpenAPI spec says ms – wrong). HTTP 200 even when
  every ISIN failed.
- `GET /market/quotes/latest/{isin}` → **404, endpoint does not exist.** The real
  `GET /market/quotes/latest?stocks=TICK1,TICK2` is keyed by ticker, returns `{}` for
  unknown tickers and has no currency.
- `GET /etf/quotes/{isin}` → daily history `[[ms_timestamp, price], ...]`, no currency.

## Blocker: ETFs cannot be priced via bulk
Bulk returned quotes for stocks only (AAPL, SAP). **All ETF ISINs tested returned
"ISIN not found"**: IE00B4X9L533, IE00B4L5Y983, IE00BK5BQT80, DE000A0F5UH1 (ticker
lookups via `latest?stocks=` also return `{}`). The spec's quote flow (bulk → NEEDS_BACKUP)
would send every ETF to Alpha Vantage. The only ETF price source is `/etf/quotes/{isin}`
(daily history, last point = latest close, one request per ETF, **no currency**).

## Fixtures
equity-search-{keyword,isin,wkn,empty}, screener-search-wkn, etf-profile (full),
quotes-bulk, quotes-bulk-mixed, quotes-bulk-unknown-isin, quotes-latest-by-ticker,
quotes-latest-isin-404, etf-quotes (trimmed to last 10 points).

## Decision: ETF prices (option A)
ETF prices come from `GET /etf/quotes/{isin}?startdate=<ms>` (last point = latest close, one request per ETF).
The series has no currency; it is in `profile.currency` (the profile's `latestQuotes` equals the last
series point). If the profile is missing it is fetched first (one extra request); with no currency
the item ends `NEEDS_BACKUP` (`NO_CURRENCY`). Unknown ETF ISINs: `404 {"error":"Security not found"}`.
