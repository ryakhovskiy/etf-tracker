-- +goose Up
ALTER TABLE instruments ADD COLUMN av_symbol TEXT;
ALTER TABLE instruments ADD COLUMN av_currency TEXT;
ALTER TABLE instruments ADD COLUMN av_region TEXT;
ALTER TABLE instruments ADD COLUMN av_linked_by TEXT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE instruments ADD COLUMN av_linked_at DATETIME;
CREATE UNIQUE INDEX idx_instruments_av_symbol ON instruments(av_symbol);
ALTER TABLE transaction_lines ADD COLUMN orig_price_e4 INTEGER;
ALTER TABLE transaction_lines ADD COLUMN orig_currency TEXT;
ALTER TABLE transaction_lines ADD COLUMN fx_rate_e8 INTEGER;
ALTER TABLE transaction_lines ADD COLUMN fx_rate_date TEXT;
CREATE TABLE price_quotes (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
 price_eur_e4 INTEGER NOT NULL, orig_price_e4 INTEGER NOT NULL, orig_currency TEXT NOT NULL,
 fx_rate_e8 INTEGER, fx_rate_date TEXT, quote_date TEXT NOT NULL,
 fetched_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_quotes_instr_fetched ON price_quotes(instrument_id,fetched_at);
CREATE TABLE fx_rates (
 rate_date TEXT NOT NULL, from_ccy TEXT NOT NULL, to_ccy TEXT NOT NULL,
 rate_e8 INTEGER NOT NULL, provider TEXT NOT NULL,
 fetched_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(rate_date,from_ccy,to_ccy)
);
CREATE TABLE av_requests (
 id INTEGER PRIMARY KEY AUTOINCREMENT, requested_at DATETIME NOT NULL,
 user_id TEXT REFERENCES users(id) ON DELETE SET NULL, function TEXT NOT NULL
);
CREATE INDEX idx_avreq_time ON av_requests(requested_at);
CREATE TABLE av_state (k TEXT PRIMARY KEY,v TEXT NOT NULL);
-- +goose Down
DROP TABLE av_state;
DROP TABLE av_requests;
DROP TABLE fx_rates;
DROP TABLE price_quotes;
ALTER TABLE transaction_lines DROP COLUMN fx_rate_date;
ALTER TABLE transaction_lines DROP COLUMN fx_rate_e8;
ALTER TABLE transaction_lines DROP COLUMN orig_currency;
ALTER TABLE transaction_lines DROP COLUMN orig_price_e4;
DROP INDEX idx_instruments_av_symbol;
ALTER TABLE instruments DROP COLUMN av_linked_at;
ALTER TABLE instruments DROP COLUMN av_linked_by;
ALTER TABLE instruments DROP COLUMN av_region;
ALTER TABLE instruments DROP COLUMN av_currency;
ALTER TABLE instruments DROP COLUMN av_symbol;
