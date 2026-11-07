-- +goose Up
-- Earlier Eulerpool ETF quotes could be off by a factor of 100 and would be
-- reused as "fresh" for the rest of the day. Drop them so they are refetched.
DELETE FROM price_quotes WHERE provider='EULERPOOL' AND instrument_id IN (SELECT id FROM instruments WHERE type='ETF');
-- +goose Down
SELECT 1;
