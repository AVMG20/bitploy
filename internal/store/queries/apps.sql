-- Owned by the deploy agent: apps, slugs, env vars, uploads.

-- name: InsertApp :exec
INSERT INTO apps (id, user_id, slug, name, type, port, volume_enabled, volume_mb, volume_name, memory_mb, cpu_millis,
                  build_mode, home_page, output_dir, base_domain, project_id, alias, desired_state, status, status_message, deploy_seq, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'running', 'new', '', 0, ?, ?);

-- name: SetAppBuildMode :exec
UPDATE apps SET build_mode = ?, home_page = ?, output_dir = ?, updated_at = ? WHERE id = ?;

-- name: UpdateAppSettings :exec
UPDATE apps
SET slug = ?, name = ?, type = ?, port = ?, health_path = ?, dockerfile_path = ?, context_dir = ?,
    volume_enabled = ?, volume_mb = ?, memory_mb = ?, cpu_millis = ?, base_domain = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteAppRow :exec
DELETE FROM apps WHERE id = ?;

-- name: CountAppsByUser :one
SELECT COUNT(*) FROM apps WHERE user_id = ?;

-- name: SumAppResourcesByUser :one
-- Only apps that are meant to run and have a deployment reserve memory and CPU.
-- Static sites (live deployment built from bitploy's nginx templates: kinds
-- static, node-spa and node-static, see deploy.IsStaticKind) are free and not counted.
SELECT CAST(COALESCE(SUM(a.memory_mb), 0) AS INTEGER) AS memory_mb,
       CAST(COALESCE(SUM(a.cpu_millis), 0) AS INTEGER) AS cpu_millis
FROM apps a LEFT JOIN deployments d ON d.id = a.current_deployment_id
WHERE a.user_id = ? AND a.id != ?
  AND a.desired_state = 'running' AND a.current_deployment_id IS NOT NULL
  AND COALESCE(d.build_kind, '') NOT IN ('static', 'node-spa', 'node-static');

-- name: SumVolumeMBByUser :one
-- Every app with a volume reserves its storage limit, running or not: the files stay.
SELECT CAST(COALESCE(SUM(volume_mb), 0) AS INTEGER) FROM apps
WHERE user_id = ? AND id != ? AND volume_enabled = 1;

-- name: VolumeNameExists :one
SELECT (SELECT COUNT(*) FROM apps WHERE apps.volume_name = sqlc.arg(name))
     + (SELECT COUNT(*) FROM app_volumes WHERE app_volumes.volume_name = sqlc.arg(name));

-- name: GetAppDisk :one
SELECT * FROM app_disk WHERE app_id = ?;

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
-- After a rename, see RewriteDeploymentServiceNames.
UPDATE deployments
SET image = CASE WHEN substr(image, 1, length('bitploy/' || sqlc.arg(old_slug))) = 'bitploy/' || sqlc.arg(old_slug)
                 THEN 'bitploy/' || sqlc.arg(new_slug) || substr(image, length('bitploy/' || sqlc.arg(old_slug)) + 1)
                 ELSE image END,
    container_name = CASE WHEN substr(container_name, 1, length('bp-' || sqlc.arg(old_slug))) = 'bp-' || sqlc.arg(old_slug)
                 THEN 'bp-' || sqlc.arg(new_slug) || substr(container_name, length('bp-' || sqlc.arg(old_slug)) + 1)
                 ELSE container_name END
WHERE app_id = sqlc.arg(app_id);

-- name: ListEnvVars :many
SELECT * FROM env_vars WHERE app_id = ? ORDER BY service, name;

-- name: UpsertEnvVar :exec
-- service '' is shared by all of the app's services.
INSERT INTO env_vars (app_id, service, name, value_enc, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(app_id, service, name) DO UPDATE SET value_enc = excluded.value_enc, updated_at = excluded.updated_at;

-- name: DeleteEnvVar :exec
DELETE FROM env_vars WHERE app_id = ? AND service = ? AND name = ?;

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

-- name: CountParkedUploads :one
-- Uploads of a user that can still be sent or deployed.
SELECT COUNT(*) FROM uploads
WHERE user_id = sqlc.arg(user_id)
  AND ((status = 'pending' AND expires_at >= sqlc.arg(now))
    OR (status = 'received' AND received_at >= sqlc.arg(received_after)));

-- name: DeleteUpload :exec
DELETE FROM uploads WHERE id = ?;

-- name: SetAppProtection :exec
UPDATE apps SET protect_enabled = ?, protect_username = ?, protect_hash = ?, updated_at = ? WHERE id = ?;

-- name: SetAppResources :exec
UPDATE apps SET memory_mb = ?, cpu_millis = ?, updated_at = ? WHERE id = ?;

-- name: SetAppVolumeEnabled :exec
UPDATE apps SET volume_enabled = ?, updated_at = ? WHERE id = ?;

-- name: SetAppCatalog :exec
UPDATE apps SET catalog_id = ?, catalog_version = ?, updated_at = ? WHERE id = ?;

-- name: SetAppCatalogVersion :exec
UPDATE apps SET catalog_version = ?, updated_at = ? WHERE id = ? AND catalog_id != '';

-- name: SetEnvVarHidden :exec
-- hidden 1: the API never returns the value (00340_env_hidden.sql).
UPDATE env_vars SET hidden = ? WHERE app_id = ? AND service = ? AND name = ?;
