-- +goose Up
CREATE TABLE transactions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 portfolio_id INTEGER NOT NULL REFERENCES portfolios(id) ON DELETE CASCADE,
 trade_date TEXT NOT NULL,
 side TEXT NOT NULL DEFAULT 'BUY' CHECK(side IN ('BUY')),
 source TEXT NOT NULL CHECK(source IN ('MANUAL','CSV')),
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_tx_portfolio_date ON transactions(portfolio_id,trade_date);
CREATE TABLE transaction_lines (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
 instrument_id INTEGER NOT NULL REFERENCES instruments(id),
 amount_eur_e2 INTEGER NOT NULL CHECK(amount_eur_e2 > 0),
 quantity_e6 INTEGER NOT NULL CHECK(quantity_e6 > 0),
 price_eur_e4 INTEGER NOT NULL CHECK(price_eur_e4 > 0)
);
CREATE INDEX idx_txl_instrument ON transaction_lines(instrument_id);
-- +goose Down
DROP TABLE transaction_lines;
DROP TABLE transactions;
