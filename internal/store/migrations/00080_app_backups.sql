-- +goose Up
-- Backups of an app's /data folder: one .tar.gz per row under
-- <data dir>/backups/<app id>/<backup id>.tar.gz. They count towards the
-- owner's storage allowance.
CREATE TABLE app_backups (
    id          TEXT PRIMARY KEY,
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,                    -- manual | scheduled | uploaded | before_restore
    status      TEXT NOT NULL DEFAULT 'running',  -- running | ready | failed
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    finished_at INTEGER
);
CREATE INDEX app_backups_app ON app_backups(app_id, created_at);

-- Automatic backups: off | daily | weekly. Retention applies to automatic
-- (and before-restore) backups: keep the last N, or keep N days.
ALTER TABLE apps ADD COLUMN backup_schedule  TEXT NOT NULL DEFAULT 'off';
ALTER TABLE apps ADD COLUMN backup_keep_mode TEXT NOT NULL DEFAULT 'last'; -- last | days
ALTER TABLE apps ADD COLUMN backup_keep      INTEGER NOT NULL DEFAULT 7;

-- +goose Down
ALTER TABLE apps DROP COLUMN backup_keep;
ALTER TABLE apps DROP COLUMN backup_keep_mode;
ALTER TABLE apps DROP COLUMN backup_schedule;
DROP INDEX app_backups_app;
DROP TABLE app_backups;
