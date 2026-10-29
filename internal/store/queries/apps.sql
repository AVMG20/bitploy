-- Owned by the deploy agent: apps, slugs, env vars, uploads.

-- name: InsertApp :exec
INSERT INTO apps (id, user_id, slug, name, type, port, volume_enabled, memory_mb, cpu_millis,
                  desired_state, status, status_message, deploy_seq, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', 'new', '', 0, ?, ?);

-- name: UpdateAppSettings :exec
UPDATE apps
SET slug = ?, name = ?, type = ?, port = ?, health_path = ?, dockerfile_path = ?, context_dir = ?,
    volume_enabled = ?, memory_mb = ?, cpu_millis = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteAppRow :exec
DELETE FROM apps WHERE id = ?;

-- name: CountAppsByUser :one
SELECT COUNT(*) FROM apps WHERE user_id = ?;

-- name: SumAppResourcesByUser :one
SELECT CAST(COALESCE(SUM(memory_mb), 0) AS INTEGER) AS memory_mb,
       CAST(COALESCE(SUM(cpu_millis), 0) AS INTEGER) AS cpu_millis
FROM apps WHERE user_id = ? AND id != ?;

-- name: AppSlugExists :one
SELECT COUNT(*) FROM apps WHERE slug = ?;

-- name: GetSlugReservation :one
SELECT * FROM slug_reservations WHERE slug = ?;

-- name: UpsertSlugReservation :exec
INSERT INTO slug_reservations (slug, user_id, expires_at) VALUES (?, ?, ?)
ON CONFLICT(slug) DO UPDATE SET user_id = excluded.user_id, expires_at = excluded.expires_at;

-- name: DeleteSlugReservation :exec
DELETE FROM slug_reservations WHERE slug = ?;

-- name: DeleteExpiredSlugReservations :exec
DELETE FROM slug_reservations WHERE expires_at < ?;

-- name: SetAppDesiredState :exec
UPDATE apps SET desired_state = ?, status = ?, status_message = ?, updated_at = ? WHERE id = ?;

-- name: SetAppCurrentDeployment :exec
UPDATE apps SET current_deployment_id = ?, desired_state = 'running', status = 'running', status_message = '', updated_at = ? WHERE id = ?;

-- name: NextDeploySeq :one
UPDATE apps SET deploy_seq = deploy_seq + 1, updated_at = ? WHERE id = ? RETURNING deploy_seq;

-- name: RewriteDeploymentNames :exec
UPDATE deployments
SET image = CASE WHEN image IS NULL THEN NULL ELSE 'bitploy/' || sqlc.arg(slug) || substr(image, instr(image, ':')) END,
    container_name = CASE WHEN container_name IS NULL THEN NULL ELSE 'bp-' || sqlc.arg(slug) || '-' || seq END
WHERE app_id = sqlc.arg(app_id);

-- name: ListEnvVars :many
SELECT * FROM env_vars WHERE app_id = ? ORDER BY name;

-- name: UpsertEnvVar :exec
INSERT INTO env_vars (app_id, name, value_enc, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(app_id, name) DO UPDATE SET value_enc = excluded.value_enc, updated_at = excluded.updated_at;

-- name: DeleteEnvVar :exec
DELETE FROM env_vars WHERE app_id = ? AND name = ?;

-- name: InsertUpload :exec
INSERT INTO uploads (id, app_id, user_id, status, expires_at, created_at) VALUES (?, ?, ?, 'pending', ?, ?);

-- name: GetUpload :one
SELECT * FROM uploads WHERE id = ?;

-- name: MarkUploadReceived :execrows
UPDATE uploads SET status = 'received', size_bytes = ?, received_at = ? WHERE id = ? AND status = 'pending';

-- name: MarkUploadUsed :execrows
UPDATE uploads SET status = 'used', used_at = ? WHERE id = ? AND status = 'received';

-- name: ListStaleUploads :many
SELECT * FROM uploads
WHERE (status = 'pending' AND expires_at < sqlc.arg(now))
   OR (status IN ('received', 'used') AND COALESCE(used_at, received_at, created_at) < sqlc.arg(cutoff))
   OR status = 'expired';

-- name: DeleteUpload :exec
DELETE FROM uploads WHERE id = ?;
