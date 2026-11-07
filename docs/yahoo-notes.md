# Yahoo Finance (unofficial API) - verified 2026-11-07

Yahoo Finance is the second backup provider next to Alpha Vantage. It never runs by itself: a user
clicks "Fetch N remaining via Yahoo Finance (uses N requests)", or "Fetch historical prices via Yahoo
Finance" for a past transaction date. There is no API key, but the API needs a cookie and a crumb.
Fixtures: `testdata/marketdata/yahoo/`. The API is unofficial and may change without notice.

## Session: cookie, then crumb
1. `GET https://fc.yahoo.com/` answers **404** (expected) and sets the `A3` cookie. A cookie jar keeps it.
2. `GET https://query1.finance.yahoo.com/v1/test/getcrumb` with that cookie answers 200 and the crumb as
   plain text (e.g. `wHQsnXGY9b3`). Without the cookie: 401 `Invalid Cookie`. `query2` returns the same crumb
   and is the fallback host. A body with spaces or markup (e.g. "Too Many Requests") is rejected as a crumb.
3. The crumb is sent as `crumb=` on every request. `v7/finance/quote` **requires** it (401 without or with a
   wrong crumb). `v8/finance/chart` and `v1/finance/search` currently work without it; it is sent anyway.
4. 401/403 means cookie or crumb expired: the session (jar + crumb) is rebuilt once and the request retried once.
5. A **browser User-Agent is mandatory**: without one every endpoint answers `429 Edge: Too Many Requests`.
6. A real 429 starts a cool-down (`YAHOO_COOLDOWN`, default 15m) during which no requests are sent.
`finance.yahoo.com` sets no usable cookie from here; `fc.yahoo.com` is the working cookie host.

## Endpoints used
| Purpose | Request | Notes |
|---|---|---|
| ISIN to symbol | `GET query2/v1/finance/search?q=<ISIN>&quotesCount=5` | Returns the best listing, e.g. IE00B8GKDB10 -> `VHYD.L`. WKN queries find nothing. |
| Latest prices | `GET query1/v7/finance/quote?symbols=A,B,C&crumb=` | Batched (40 per request). Unknown symbols are simply absent. `currency`, `regularMarketPrice`, `regularMarketTime` (unix s). |
| History | `GET query1/v8/finance/chart/<SYM>?period1=&period2=&interval=1d&crumb=` | unix seconds, `period2` exclusive. `indicators.quote[0].close` aligned with `timestamp`; `null` = no bar. Unknown symbol: 404 `{"chart":{"error":{"code":"Not Found"}}}`. |

Currency comes with every response (`meta.currency` / `currency`); it is the **listing** currency, e.g. USD for
VHYD.L. Minor-unit codes (GBp, GBX, ZAc) are divided by 100 at conversion. Day labels use the exchange time zone
(`meta.exchangeTimezoneName`); the unadjusted `close` is used, not `adjclose`.

## How it is used
- **Symbols** are cached on the instrument (`yahoo_symbol`); "Yahoo does not know this ISIN" is remembered for 7 days.
- **Today**: one batched quote request. **Any earlier date** (1990-01-01 up to today): one chart request per
  instrument for [date-10 days, date]; the last close on or before the date is used, so weekends and holidays
  resolve to the previous trading day. Exact-day closes are stored in `price_history` and reused.
- `GET /api/instruments/{id}/price-history?from=&to=` returns daily closes (up to 40 years) and stores them.
- Every request (including cookie and crumb) is recorded in `provider_requests` with `provider='YAHOO'`;
  `YAHOO_SOFT_DAILY_LIMIT` caps requests per 24h and `YAHOO_MIN_INTERVAL` spaces them.
- A historical close is never reused as a "fresh" current price (only quotes dated today or the previous trading day are).
