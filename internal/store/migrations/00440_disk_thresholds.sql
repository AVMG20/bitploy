-- +goose Up
-- Admin > Server > Disk: the free space below which builds, uploads and
-- backups are refused (was a fixed 15%), and whether the automatic cleanup
-- may delete images that are not bitploy's, and after how many days without
-- a build that used it build cache is deleted (0: never).
ALTER TABLE server_settings ADD COLUMN disk_builds_min_free_percent INTEGER NOT NULL DEFAULT 15;
ALTER TABLE server_settings ADD COLUMN disk_auto_other_images INTEGER NOT NULL DEFAULT 1;
ALTER TABLE server_settings ADD COLUMN disk_build_cache_days INTEGER NOT NULL DEFAULT 7;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN disk_build_cache_days;
ALTER TABLE server_settings DROP COLUMN disk_auto_other_images;
ALTER TABLE server_settings DROP COLUMN disk_builds_min_free_percent;
