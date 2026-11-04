-- +goose Up
CREATE TABLE csv_import_settings (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 schema_version INTEGER NOT NULL DEFAULT 1,
 settings TEXT NOT NULL,
 updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE instruments ADD COLUMN source TEXT NOT NULL DEFAULT 'SEED';
-- +goose Down
ALTER TABLE instruments DROP COLUMN source;
DROP TABLE csv_import_settings;
