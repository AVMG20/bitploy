-- +goose Up
-- CPU and memory of the isolated builder (Admin > Server), shared by the
-- builds that run at the same time. 0: the default, BITPLOY_BUILDER_CPUS and
-- BITPLOY_BUILDER_MEMORY (2 CPUs and 6g unless set). Read before every build;
-- the builder is recreated with the new limits once no build runs.
ALTER TABLE server_settings ADD COLUMN builder_cpu_millis INTEGER NOT NULL DEFAULT 0;
ALTER TABLE server_settings ADD COLUMN builder_memory_mb INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN builder_memory_mb;
ALTER TABLE server_settings DROP COLUMN builder_cpu_millis;
