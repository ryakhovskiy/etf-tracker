-- +goose Up
-- Dividend events per instrument, shared by all users. Amounts are gross, per
-- share, in the listing currency of the symbol they were fetched for.
CREATE TABLE dividend_events (
 id              INTEGER PRIMARY KEY AUTOINCREMENT,
 instrument_id   INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
 ex_date         TEXT    NOT NULL,     -- YYYY-MM-DD
 amount_e6       INTEGER NOT NULL,     -- per share, listing currency, x 10^6
 currency        TEXT    NOT NULL,
 close_before_e4 INTEGER,              -- close before the ex-date, listing currency
 provider        TEXT    NOT NULL,     -- 'YAHOO'
 fetched_at      DATETIME NOT NULL,
 UNIQUE (instrument_id, ex_date)
);
-- What has been synced per instrument. last_error is NULL after a successful sync.
CREATE TABLE dividend_sync (
 instrument_id     INTEGER PRIMARY KEY REFERENCES instruments(id) ON DELETE CASCADE,
 synced_from       TEXT NOT NULL,      -- earliest date covered
 last_synced_at    DATETIME,
 latest_close_e4   INTEGER,
 latest_close_date TEXT,
 currency          TEXT,
 last_error        TEXT,
 last_error_at     DATETIME
);
-- +goose Down
DROP TABLE dividend_sync;
DROP TABLE dividend_events;
