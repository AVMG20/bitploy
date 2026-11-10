-- +goose Up
-- GitHub's numeric id of an app's linked repository, so a push still finds the
-- app after the repository was renamed or moved to another account. NULL until
-- the first push that matches by name (and again after the source changes).
ALTER TABLE apps ADD COLUMN github_repo_id INTEGER;
-- A push delivery that could not queue every app it matched: GitHub's
-- "Redeliver" (same delivery id) is then handled again.
ALTER TABLE github_deliveries ADD COLUMN retry INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE github_deliveries DROP COLUMN retry;
ALTER TABLE apps DROP COLUMN github_repo_id;
