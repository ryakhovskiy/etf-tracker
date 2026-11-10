-- +goose Up
-- Days between ex-date and payment, chosen by each user per instrument. The
-- calendar adds them to the ex-date and moves the result to the next German
-- working day. No row means 0 days.
CREATE TABLE instrument_pay_delays (
 user_id       TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 instrument_id INTEGER NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
 days          INTEGER NOT NULL CHECK (days BETWEEN 0 AND 90),
 updated_at    DATETIME NOT NULL,
 PRIMARY KEY (user_id, instrument_id)
);
-- +goose Down
DROP TABLE instrument_pay_delays;
