-- +goose Up
PRAGMA defer_foreign_keys = ON;
CREATE TABLE instruments_new (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK(type IN ('STOCK','ETF')),
 name TEXT NOT NULL,
 ticker TEXT NOT NULL,
 isin TEXT UNIQUE,
 wkn TEXT UNIQUE,
 source TEXT NOT NULL DEFAULT 'SEED' CHECK(source IN ('SEED','CSV','OPENFIGI','ALPHAVANTAGE')),
 figi TEXT UNIQUE,
 exch_code TEXT
);
INSERT INTO instruments_new(id,type,name,ticker,isin,wkn,source)
 SELECT id,type,name,ticker,isin,wkn,source FROM instruments;
DROP TABLE instruments;
ALTER TABLE instruments_new RENAME TO instruments;
CREATE INDEX idx_instruments_ticker ON instruments(ticker);
CREATE INDEX idx_instruments_name ON instruments(name);
-- +goose Down
PRAGMA defer_foreign_keys = ON;
CREATE TABLE instruments_old (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK(type IN ('STOCK','ETF')),
 name TEXT NOT NULL,
 ticker TEXT NOT NULL,
 isin TEXT NOT NULL UNIQUE,
 wkn TEXT UNIQUE,
 source TEXT NOT NULL DEFAULT 'SEED'
);
INSERT INTO instruments_old(id,type,name,ticker,isin,wkn,source)
 SELECT id,type,name,ticker,isin,wkn,source FROM instruments;
DROP TABLE instruments;
ALTER TABLE instruments_old RENAME TO instruments;
CREATE INDEX idx_instruments_ticker ON instruments(ticker);
CREATE INDEX idx_instruments_name ON instruments(name);
