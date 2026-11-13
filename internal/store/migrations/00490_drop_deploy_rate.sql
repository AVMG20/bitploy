-- +goose Up
-- There is no limit on deploys per hour any more: a new deploy replaces the
-- app's queued one and build slots are shared fairly between people, so the
-- build queue keeps the server in check. Deploys of the last hour are still
-- counted for the usage view, from the deployments table.
ALTER TABLE users DROP COLUMN quota_deploys_per_hour;
ALTER TABLE server_settings DROP COLUMN quota_deploys_per_hour;

-- +goose Down
ALTER TABLE users ADD COLUMN quota_deploys_per_hour INTEGER NOT NULL DEFAULT 20;
ALTER TABLE server_settings ADD COLUMN quota_deploys_per_hour INTEGER NOT NULL DEFAULT 20;
