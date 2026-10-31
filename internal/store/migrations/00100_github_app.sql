-- +goose Up
-- GitHub integration through one GitHub App per bitploy instance.
--  github_app                one row: the app the admin created (secrets sealed with the secret key)
--  github_installations      which bitploy user linked which installation of that app
--  github_installation_repos the repositories of installations that cover only selected ones
--  github_deliveries         webhook delivery ids, so a repeated delivery is handled once
-- The personal access token of the first GitHub feature is gone: its columns are dropped.

ALTER TABLE apps ADD COLUMN git_auto_deploy INTEGER NOT NULL DEFAULT 1;
ALTER TABLE apps ADD COLUMN git_last_push_at INTEGER;

CREATE TABLE github_app (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    github_app_id      INTEGER NOT NULL,
    slug               TEXT NOT NULL,
    name               TEXT NOT NULL,
    html_url           TEXT NOT NULL,
    owner_login        TEXT NOT NULL DEFAULT '',
    owner_type         TEXT NOT NULL DEFAULT 'User',          -- User | Organization
    client_id          TEXT NOT NULL,
    client_secret_enc  BLOB NOT NULL,
    webhook_secret_enc BLOB NOT NULL,
    private_key_enc    BLOB NOT NULL,
    created_by         TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at         INTEGER NOT NULL
);

CREATE TABLE github_installations (
    user_id              TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    installation_id      INTEGER NOT NULL,
    account_login        TEXT NOT NULL,
    account_type         TEXT NOT NULL,                       -- User | Organization
    repository_selection TEXT NOT NULL DEFAULT 'selected',    -- all | selected
    status               TEXT NOT NULL DEFAULT 'active',      -- active | suspended | deleted
    repos_synced_at      INTEGER,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    PRIMARY KEY (user_id, installation_id)
);
CREATE INDEX github_installations_installation ON github_installations(installation_id);

CREATE TABLE github_installation_repos (
    installation_id INTEGER NOT NULL,
    repo            TEXT NOT NULL,                            -- owner/name, lower case
    PRIMARY KEY (installation_id, repo)
) WITHOUT ROWID;

CREATE TABLE github_deliveries (
    delivery_id TEXT PRIMARY KEY,
    received_at INTEGER NOT NULL
) WITHOUT ROWID;

ALTER TABLE users DROP COLUMN github_token_enc;
ALTER TABLE users DROP COLUMN github_login;
ALTER TABLE users DROP COLUMN github_token_expires_at;

-- +goose Down
ALTER TABLE users ADD COLUMN github_token_enc BLOB;
ALTER TABLE users ADD COLUMN github_login TEXT;
ALTER TABLE users ADD COLUMN github_token_expires_at INTEGER;
DROP TABLE github_deliveries;
DROP TABLE github_installation_repos;
DROP TABLE github_installations;
DROP TABLE github_app;
ALTER TABLE apps DROP COLUMN git_last_push_at;
ALTER TABLE apps DROP COLUMN git_auto_deploy;
