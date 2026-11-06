-- +goose Up
-- Automatic backups are on by default: daily, keeping 14 days
-- (docs/decisions.md, "Automatic backups are on by default").
--
-- backup_schedule_set: 1 once the schedule was decided, by the user (the
-- backup settings were saved) or by bitploy turning on the default. bitploy
-- never changes a schedule that is set. 0: never set; the default is turned
-- on as soon as the app has something to back up (/data on, not a cache or
-- search index).
--
-- backup_default_at: when bitploy turned the default on (unix seconds), else
-- 0. The first automatic backup then waits for the app's first backup time
-- after that moment instead of running at once, so turning the default on for
-- many apps does not start all their backups together.
ALTER TABLE apps ADD COLUMN backup_schedule_set INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN backup_default_at  INTEGER NOT NULL DEFAULT 0;

-- The schema before this had no "never set": 'off' was the default and also
-- what a user picks to turn backups off. An app counts as set by its user when
-- its settings differ from the old defaults (off, keep the last 7), or when the
-- audit log (90 days) shows the settings were saved.
UPDATE apps SET backup_schedule_set = 1
WHERE backup_schedule != 'off' OR backup_keep_mode != 'last' OR backup_keep != 7
   OR EXISTS (
       SELECT 1 FROM audit_log l
       WHERE l.action = 'app.backup_settings' AND l.target_type = 'app' AND l.target_id = apps.id
   );

-- The others get the default when they have something to back up. Valkey,
-- Redis and Meilisearch hold data that can be rebuilt and get no backups
-- (servicetpl.Template.Backups).
UPDATE apps SET backup_schedule = 'daily', backup_keep_mode = 'days', backup_keep = 14,
    backup_schedule_set = 1, backup_default_at = CAST(strftime('%s', 'now') AS INTEGER)
WHERE backup_schedule_set = 0 AND volume_enabled = 1
  AND template NOT IN ('valkey', 'redis', 'meilisearch');

-- +goose Down
ALTER TABLE apps DROP COLUMN backup_default_at;
ALTER TABLE apps DROP COLUMN backup_schedule_set;
