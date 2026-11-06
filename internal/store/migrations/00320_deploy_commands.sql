-- +goose Up
-- Deploy commands (docs/decisions.md, "Deploy commands"): a shell command
-- run before the new version takes traffic (database migrations) and one run
-- in the live container after it does. In a table of their own: most apps
-- have none, and the apps row stays as it is.
CREATE TABLE app_deploy_commands (
    app_id         TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    before_deploy  TEXT NOT NULL DEFAULT '',
    after_deploy   TEXT NOT NULL DEFAULT '',
    updated_at     INTEGER NOT NULL
);

-- What the after-deploy command of a deployment did: '' (none ran),
-- running, succeeded or failed. A failure is also a warning on the
-- deployment; it never rolls back.
ALTER TABLE deployments ADD COLUMN after_deploy TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN after_deploy;
DROP TABLE app_deploy_commands;
