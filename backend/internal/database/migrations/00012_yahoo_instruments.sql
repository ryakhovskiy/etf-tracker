-- +goose NO TRANSACTION
-- +goose Up
-- Yahoo becomes the primary provider: instruments may now come from Yahoo search
-- (source 'YAHOO') and are identified by their Yahoo symbol, which must be unique.
-- instruments is rebuilt with SQLite's documented procedure (foreign keys off outside
-- a transaction, copy, swap, indexes, foreign_key_check, foreign keys on).
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE instruments_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK(type IN ('STOCK','ETF')),
 name TEXT NOT NULL,
 ticker TEXT NOT NULL,
 isin TEXT UNIQUE,
 wkn TEXT UNIQUE,
 source TEXT NOT NULL DEFAULT 'SEED' CHECK(source IN ('SEED','CSV','OPENFIGI','ALPHAVANTAGE','EULERPOOL','YAHOO')),
 figi TEXT UNIQUE,
 exch_code TEXT,
 av_symbol TEXT,
 av_currency TEXT,
 av_region TEXT,
 av_linked_by TEXT REFERENCES users(id) ON DELETE SET NULL,
 av_linked_at DATETIME,
 yahoo_symbol TEXT,
 yahoo_checked_at DATETIME,
 ep_currency TEXT
);
-- Two instruments cannot share a symbol: keep the first, let the others be resolved again.
UPDATE instruments SET yahoo_symbol=NULL,yahoo_checked_at=NULL
 WHERE yahoo_symbol IS NOT NULL AND id NOT IN (SELECT MIN(id) FROM instruments WHERE yahoo_symbol IS NOT NULL GROUP BY yahoo_symbol);
INSERT INTO instruments_new(id,type,name,ticker,isin,wkn,source,figi,exch_code,av_symbol,av_currency,av_region,av_linked_by,av_linked_at,yahoo_symbol,yahoo_checked_at)
 SELECT id,type,name,ticker,isin,wkn,source,figi,exch_code,av_symbol,av_currency,av_region,av_linked_by,av_linked_at,yahoo_symbol,yahoo_checked_at FROM instruments;
DROP TABLE instruments;
ALTER TABLE instruments_new RENAME TO instruments;
CREATE INDEX idx_instruments_ticker ON instruments(ticker);
CREATE INDEX idx_instruments_name ON instruments(name);
CREATE UNIQUE INDEX idx_instruments_av_symbol ON instruments(av_symbol);
CREATE UNIQUE INDEX idx_instruments_yahoo_symbol ON instruments(yahoo_symbol);

-- Abort (NOT NULL violation) if the rebuild left any dangling reference.
CREATE TEMP TABLE fk_guard(x INTEGER NOT NULL);
INSERT INTO fk_guard SELECT NULL FROM pragma_foreign_key_check LIMIT 1;
DROP TABLE fk_guard;
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose Down
DROP INDEX idx_instruments_yahoo_symbol;
ALTER TABLE instruments DROP COLUMN ep_currency;
DELETE FROM instruments WHERE source='YAHOO' AND id NOT IN (SELECT instrument_id FROM portfolio_items UNION SELECT instrument_id FROM transaction_lines);
UPDATE instruments SET source='SEED' WHERE source='YAHOO';
