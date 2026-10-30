-- +goose Up
-- Storage is now handed out like memory and CPU: users.quota_volume_mb is the
-- user's total storage allowance, and each app with a volume reserves its own
-- limit (apps.volume_mb) from it.
ALTER TABLE apps ADD COLUMN volume_mb INTEGER NOT NULL DEFAULT 5120;
-- The Docker volume keeps the name it got when it was created, so a renamed app
-- keeps its data. Existing volumes are named after the current slug.
ALTER TABLE apps ADD COLUMN volume_name TEXT NOT NULL DEFAULT '';
UPDATE apps SET volume_name = 'bp-' || slug || '-data';
CREATE UNIQUE INDEX apps_volume_name ON apps(volume_name) WHERE volume_name != '';
-- Keep each existing app's limit: it was the old per-app quota of its owner.
UPDATE apps SET volume_mb = COALESCE((SELECT quota_volume_mb FROM users WHERE users.id = apps.user_id AND quota_volume_mb > 0), 5120);
-- The old default (5 GB per app) becomes the new default allowance (25 GB in total).
UPDATE users SET quota_volume_mb = 25600 WHERE quota_volume_mb = 5120;

-- +goose Down
UPDATE users SET quota_volume_mb = 5120 WHERE quota_volume_mb = 25600;
DROP INDEX apps_volume_name;
ALTER TABLE apps DROP COLUMN volume_name;
ALTER TABLE apps DROP COLUMN volume_mb;
