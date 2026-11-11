-- +goose Up
-- The automatic disk cleanup (Admin > Server > Disk). When free space on the
-- data directory's filesystem drops below disk_cleanup_min_free_percent,
-- bitploy deletes build cache and unused images until it is back above it.
-- It never deletes volumes, and never touches running containers.
ALTER TABLE server_settings ADD COLUMN disk_auto_cleanup INTEGER NOT NULL DEFAULT 1;
ALTER TABLE server_settings ADD COLUMN disk_cleanup_min_free_percent INTEGER NOT NULL DEFAULT 10;

-- Every automatic cleanup that ran. trigger: low_disk (the watcher saw free
-- space under the threshold) or build_refused (a build, upload or backup
-- was refused for disk space; only build cache and unused images go).
-- steps: JSON list of {kind, freed_bytes, removed, error}. result: recovered
-- (free space is back above the target), still_low, or failed.
CREATE TABLE disk_cleanups (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    trigger     TEXT    NOT NULL,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    total_bytes INTEGER NOT NULL,
    free_before INTEGER NOT NULL,
    free_after  INTEGER NOT NULL,
    freed_bytes INTEGER NOT NULL,
    steps       TEXT    NOT NULL DEFAULT '[]',
    result      TEXT    NOT NULL
);

-- Admins hear about it on the channels they already have (server.disk_low).
UPDATE notification_channels SET events = json_insert(events, '$[#]', 'server.disk_low')
WHERE user_id IN (SELECT id FROM users WHERE role = 'admin')
  AND json_valid(events) AND NOT EXISTS (SELECT 1 FROM json_each(notification_channels.events) WHERE value = 'server.disk_low');

-- +goose Down
UPDATE notification_channels SET events = COALESCE((SELECT json_group_array(value) FROM json_each(notification_channels.events) WHERE value != 'server.disk_low'), '[]')
WHERE json_valid(events);
DROP TABLE disk_cleanups;
ALTER TABLE server_settings DROP COLUMN disk_cleanup_min_free_percent;
ALTER TABLE server_settings DROP COLUMN disk_auto_cleanup;
