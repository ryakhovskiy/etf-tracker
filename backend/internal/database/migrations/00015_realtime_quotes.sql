-- +goose Up
-- Real-time Yahoo quotes: the market time of the price, and whether the EUR
-- conversion used a live Yahoo rate ('YAHOO') instead of the ECB reference (NULL).
ALTER TABLE price_quotes ADD COLUMN quote_time TEXT;
ALTER TABLE price_quotes ADD COLUMN fx_source TEXT;

-- +goose Down
ALTER TABLE price_quotes DROP COLUMN fx_source;
ALTER TABLE price_quotes DROP COLUMN quote_time;
