-- Backups of an app's /data folder (internal/app/backups*.go).

-- name: InsertBackup :exec
INSERT INTO app_backups (id, app_id, kind, status, size_bytes, error, created_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetBackup :one
SELECT * FROM app_backups WHERE id = ? AND app_id = ?;

-- name: ListBackupsByApp :many
SELECT * FROM app_backups WHERE app_id = ? ORDER BY created_at DESC, id DESC;

-- name: FinishBackup :exec
UPDATE app_backups SET status = ?, size_bytes = ?, error = ?, finished_at = ? WHERE id = ?;

-- name: DeleteBackup :exec
DELETE FROM app_backups WHERE id = ?;

-- name: DeleteBackupsByApp :exec
DELETE FROM app_backups WHERE app_id = ?;

-- name: SumBackupBytesByUser :one
SELECT CAST(COALESCE(SUM(b.size_bytes), 0) AS INTEGER) FROM app_backups b
JOIN apps a ON a.id = b.app_id WHERE a.user_id = ?;

-- name: SumBackupBytesByApp :one
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS INTEGER) FROM app_backups WHERE app_id = ?;

-- name: LatestScheduledBackupAt :one
-- The newest automatic attempt, successful or not: a failed night is not retried every few minutes.
SELECT CAST(COALESCE(MAX(created_at), 0) AS INTEGER) FROM app_backups WHERE app_id = ? AND kind = 'scheduled';

-- name: FailRunningBackups :exec
UPDATE app_backups SET status = 'failed', error = ?, finished_at = ? WHERE status = 'running';

-- name: UpdateAppBackupSettings :exec
UPDATE apps SET backup_schedule = ?, backup_keep_mode = ?, backup_keep = ?, updated_at = ? WHERE id = ?;
