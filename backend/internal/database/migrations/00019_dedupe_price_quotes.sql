-- +goose Up
-- One-time cleanup: SaveQuote used to append a row on every refresh, even when
-- the provider returned exactly what was stored. Per instrument, in the order
-- latestQuotes reads them (quote_date, fetched_at, id), delete a row when the
-- next row is identical in every price field (NULL-safe), so the newest fetch of
-- each run of identical consecutive rows is kept. Non-consecutive repeats
-- (A, B, A) are kept: they are real changes. Transactions copy prices into
-- transaction_lines and do not reference price_quotes.
DELETE FROM price_quotes WHERE id IN (
 SELECT id FROM (
  SELECT id,
   provider IS LEAD(provider) OVER w
   AND orig_currency IS LEAD(orig_currency) OVER w
   AND orig_price_e4 IS LEAD(orig_price_e4) OVER w
   AND price_eur_e4 IS LEAD(price_eur_e4) OVER w
   AND fx_rate_e8 IS LEAD(fx_rate_e8) OVER w
   AND fx_rate_date IS LEAD(fx_rate_date) OVER w
   AND fx_source IS LEAD(fx_source) OVER w
   AND quote_date IS LEAD(quote_date) OVER w
   AND quote_time IS LEAD(quote_time) OVER w
   AND prev_close_e4 IS LEAD(prev_close_e4) OVER w
   AND prev_fx_rate_e8 IS LEAD(prev_fx_rate_e8) OVER w
   AND prev_close_date IS LEAD(prev_close_date) OVER w AS dup,
   LEAD(id) OVER w AS next_id
  FROM price_quotes
  WINDOW w AS (PARTITION BY instrument_id ORDER BY quote_date, fetched_at, id)
 ) WHERE next_id IS NOT NULL AND dup
);

-- +goose Down
-- Intentional no-op: the deleted rows were duplicates and cannot be restored.
SELECT 1;
