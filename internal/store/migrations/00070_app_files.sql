-- +goose Up
-- App files count towards the storage allowance too: the stored source
-- snapshots (zip per deployment) next to the images and the /data volume.
ALTER TABLE app_disk ADD COLUMN source_bytes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE app_disk DROP COLUMN source_bytes;
