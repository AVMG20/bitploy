-- +goose Up
-- How many builds may run at the same time (Admin > Server). 0: the default,
-- BITPLOY_MAX_PARALLEL_BUILDS (2 unless set). Read on every pass of the
-- deploy worker, so a change applies to the next build that starts.
ALTER TABLE server_settings ADD COLUMN max_parallel_builds INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN max_parallel_builds;
