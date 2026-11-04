-- +goose Up
CREATE TABLE portfolios (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL COLLATE NOCASE,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(user_id, name)
);
CREATE TABLE instruments (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 type TEXT NOT NULL CHECK(type IN ('STOCK','ETF')),
 name TEXT NOT NULL,
 ticker TEXT NOT NULL,
 isin TEXT NOT NULL UNIQUE,
 wkn TEXT UNIQUE
);
CREATE INDEX idx_instruments_ticker ON instruments(ticker);
CREATE INDEX idx_instruments_name ON instruments(name);
CREATE TABLE portfolio_items (
 portfolio_id INTEGER NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
 instrument_id INTEGER NOT NULL REFERENCES instruments(id),
 added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(portfolio_id, instrument_id)
);
-- +goose Down
DROP TABLE portfolio_items;
DROP TABLE instruments;
DROP TABLE portfolios;
