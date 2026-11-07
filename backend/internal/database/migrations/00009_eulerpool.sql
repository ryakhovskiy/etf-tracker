-- +goose NO TRANSACTION
-- +goose Up
-- instruments is rebuilt with SQLite's documented procedure (foreign keys off,
-- copy, swap, recreate indexes, foreign_key_check, foreign keys on). The PRAGMA
-- only takes effect outside a transaction, hence NO TRANSACTION and explicit BEGIN/COMMIT.
PRAGMA foreign_keys = OFF;
BEGIN;
-- Generalize the AV ledger and state to all providers.
ALTER TABLE av_requests RENAME TO provider_requests;
ALTER TABLE provider_requests ADD COLUMN provider TEXT NOT NULL DEFAULT 'ALPHAVANTAGE';
ALTER TABLE provider_requests ADD COLUMN counted INTEGER NOT NULL DEFAULT 1;
CREATE INDEX idx_provreq_provider_time ON provider_requests(provider,requested_at);

CREATE TABLE provider_state (
 provider TEXT NOT NULL, k TEXT NOT NULL, v TEXT NOT NULL,
 PRIMARY KEY(provider,k)
);
INSERT INTO provider_state(provider,k,v) SELECT 'ALPHAVANTAGE',k,v FROM av_state;
DROP TABLE av_state;

ALTER TABLE price_quotes ADD COLUMN provider TEXT NOT NULL DEFAULT 'ALPHAVANTAGE';

-- Rebuild instruments to extend the source CHECK.
CREATE TABLE instruments_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK(type IN ('STOCK','ETF')),
 name TEXT NOT NULL,
 ticker TEXT NOT NULL,
 isin TEXT UNIQUE,
 wkn TEXT UNIQUE,
 source TEXT NOT NULL DEFAULT 'SEED' CHECK(source IN ('SEED','CSV','OPENFIGI','ALPHAVANTAGE','EULERPOOL')),
 figi TEXT UNIQUE,
 exch_code TEXT,
 av_symbol TEXT,
 av_currency TEXT,
 av_region TEXT,
 av_linked_by TEXT REFERENCES users(id) ON DELETE SET NULL,
 av_linked_at DATETIME
);
INSERT INTO instruments_new(id,type,name,ticker,isin,wkn,source,figi,exch_code,av_symbol,av_currency,av_region,av_linked_by,av_linked_at)
 SELECT id,type,name,ticker,isin,wkn,source,figi,exch_code,av_symbol,av_currency,av_region,av_linked_by,av_linked_at FROM instruments;
DROP TABLE instruments;
ALTER TABLE instruments_new RENAME TO instruments;
CREATE INDEX idx_instruments_ticker ON instruments(ticker);
CREATE INDEX idx_instruments_name ON instruments(name);
CREATE UNIQUE INDEX idx_instruments_av_symbol ON instruments(av_symbol);

CREATE TABLE etf_profiles (
 instrument_id INTEGER PRIMARY KEY REFERENCES instruments(id) ON DELETE CASCADE,
 etf_company TEXT, tracking_index TEXT, asset_class TEXT, domicile TEXT,
 expense_ratio TEXT, aum_eur TEXT, dividend_yield TEXT, inception_date TEXT,
 currency TEXT, website TEXT,
 raw TEXT NOT NULL,
 fetched_at DATETIME NOT NULL
);

-- Abort (NOT NULL violation) if the rebuild left any dangling reference.
CREATE TEMP TABLE fk_guard(x INTEGER NOT NULL);
INSERT INTO fk_guard SELECT NULL FROM pragma_foreign_key_check LIMIT 1;
DROP TABLE fk_guard;
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose Down
DROP TABLE etf_profiles;
DELETE FROM instruments WHERE source='EULERPOOL' AND id NOT IN (SELECT instrument_id FROM portfolio_items UNION SELECT instrument_id FROM transaction_lines);
UPDATE instruments SET source='SEED' WHERE source='EULERPOOL';
ALTER TABLE price_quotes DROP COLUMN provider;
CREATE TABLE av_state (k TEXT PRIMARY KEY,v TEXT NOT NULL);
INSERT INTO av_state(k,v) SELECT k,v FROM provider_state WHERE provider='ALPHAVANTAGE';
DROP TABLE provider_state;
DROP INDEX idx_provreq_provider_time;
DELETE FROM provider_requests WHERE provider<>'ALPHAVANTAGE';
ALTER TABLE provider_requests DROP COLUMN counted;
ALTER TABLE provider_requests DROP COLUMN provider;
ALTER TABLE provider_requests RENAME TO av_requests;
