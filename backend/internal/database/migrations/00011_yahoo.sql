-- +goose Up
-- Yahoo symbol resolved from the ISIN (cached; an empty symbol with a check time
-- records "Yahoo does not know this ISIN" so it is not searched again at once).
ALTER TABLE instruments ADD COLUMN yahoo_symbol TEXT;
ALTER TABLE instruments ADD COLUMN yahoo_checked_at DATETIME;
-- Daily closes in the listing currency, as returned by Yahoo.
CREATE TABLE price_history (
 instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
 day TEXT NOT NULL,
 close_e4 INTEGER NOT NULL,
 currency TEXT NOT NULL,
 provider TEXT NOT NULL,
 symbol TEXT NOT NULL,
 fetched_at DATETIME NOT NULL,
 PRIMARY KEY(instrument_id, day, provider)
);
-- +goose Down
DROP TABLE price_history;
ALTER TABLE instruments DROP COLUMN yahoo_checked_at;
ALTER TABLE instruments DROP COLUMN yahoo_symbol;
