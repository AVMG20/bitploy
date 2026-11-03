-- +goose Up
-- The catalog version a deployment builds ('' when it is not a catalog
-- Dockerfile). apps.catalog_version takes it when the deployment goes live, so
-- a failed or cancelled update leaves the app on the version it runs.
ALTER TABLE deployments ADD COLUMN catalog_version TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deployments DROP COLUMN catalog_version;
