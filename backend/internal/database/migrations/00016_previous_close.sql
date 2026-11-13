-- +goose Up
-- Previous close stored with a quote, for "change vs yesterday". Both come from
-- the Yahoo chart meta (chartPreviousClose): the previous session's close in the
-- listing currency (price scale x10^4), and, for a foreign listing priced at a
-- live Yahoo rate, the previous close of the {CCY}EUR=X pair (rate scale x10^8).
-- NULL means unknown (older quotes, other providers, ECB-converted quotes).
ALTER TABLE price_quotes ADD COLUMN prev_close_e4 INTEGER;
ALTER TABLE price_quotes ADD COLUMN prev_fx_rate_e8 INTEGER;

-- +goose Down
ALTER TABLE price_quotes DROP COLUMN prev_fx_rate_e8;
ALTER TABLE price_quotes DROP COLUMN prev_close_e4;
