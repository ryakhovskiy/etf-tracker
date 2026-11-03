-- +goose Up
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    revoked_at INTEGER
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
ALTER TABLE refresh_tokens ADD COLUMN session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE;
CREATE INDEX idx_refresh_tokens_session_id ON refresh_tokens(session_id);

-- Legacy tokens have no family history. Require sign-in again while preserving users.
UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, unixepoch());

-- +goose Down
-- Invalidate sessions when reverting to the legacy schema, too.
UPDATE refresh_tokens SET revoked_at = COALESCE(revoked_at, unixepoch());
DROP INDEX idx_refresh_tokens_session_id;
ALTER TABLE refresh_tokens DROP COLUMN session_id;
DROP TABLE sessions;
