-- +goose Up
-- One-time password reset links an admin makes in Admin > People (or with
-- bitploy admin reset-link). Valid 24 hours; only the hash of the code is
-- kept. A new link for a user deletes their older unused ones.
CREATE TABLE password_resets (
    id         TEXT PRIMARY KEY,                                        -- prs_…
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL UNIQUE,                                    -- hex SHA-256 of the code
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,           -- NULL: the command line
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX password_resets_user ON password_resets(user_id);

-- +goose Down
DROP TABLE password_resets;
