-- +goose Up
-- The exchange-local date (YYYY-MM-DD) of the stored previous close, taken from
-- the dated daily bars of the Yahoo chart. prev_fx_rate_e8 is only filled from an
-- FX daily close of exactly this date.
ALTER TABLE price_quotes ADD COLUMN prev_close_date TEXT;

-- +goose Down
ALTER TABLE price_quotes DROP COLUMN prev_close_date;
