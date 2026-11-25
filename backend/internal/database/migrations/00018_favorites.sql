-- +goose Up
-- A user's starred titles (instruments), shown as ordered tiles on the portfolio
-- list page. One row per user and instrument, however many of the user's
-- portfolios hold it. position is contiguous from 0 per user; the application
-- compacts it on every removal and rewrites it atomically on a reorder, so it
-- carries no UNIQUE constraint (a swap would collide mid-statement).
CREATE TABLE favorites (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 instrument_id INTEGER NOT NULL REFERENCES instruments(id),
 position INTEGER NOT NULL CHECK(position >= 0),
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(user_id, instrument_id)
);
CREATE INDEX idx_favorites_user_position ON favorites(user_id, position);

-- +goose Down
DROP INDEX idx_favorites_user_position;
DROP TABLE favorites;
