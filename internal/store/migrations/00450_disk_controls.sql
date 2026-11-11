-- +goose Up
-- Admin > Server > Disk: how many earlier versions every app keeps for a
-- rollback (was a fixed 3), which steps the automatic cleanup may take
-- (replaces disk_auto_other_images), and the free space below which admins
-- get an early warning (0: none).
ALTER TABLE server_settings ADD COLUMN rollback_versions INTEGER NOT NULL DEFAULT 3;
ALTER TABLE server_settings ADD COLUMN disk_auto_steps TEXT NOT NULL
    DEFAULT '["build_cache","unused_images","stopped_containers","rollback_images","other_images"]';
UPDATE server_settings SET disk_auto_steps = '["build_cache","unused_images","stopped_containers","rollback_images"]'
WHERE disk_auto_other_images = 0;
ALTER TABLE server_settings DROP COLUMN disk_auto_other_images;
ALTER TABLE server_settings ADD COLUMN disk_warn_free_percent INTEGER NOT NULL DEFAULT 20;

-- +goose Down
ALTER TABLE server_settings DROP COLUMN disk_warn_free_percent;
ALTER TABLE server_settings ADD COLUMN disk_auto_other_images INTEGER NOT NULL DEFAULT 1;
UPDATE server_settings SET disk_auto_other_images = 0 WHERE disk_auto_steps NOT LIKE '%other_images%';
ALTER TABLE server_settings DROP COLUMN disk_auto_steps;
ALTER TABLE server_settings DROP COLUMN rollback_versions;
