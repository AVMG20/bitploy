-- The disk page and the automatic disk cleanup (migration 00430).

-- name: UpdateDiskCleanupSettings :exec
UPDATE server_settings SET disk_auto_cleanup = ?, disk_cleanup_min_free_percent = ?,
    disk_builds_min_free_percent = ?, disk_auto_other_images = ?, disk_build_cache_days = ?, updated_at = ?
WHERE id = 1;

-- name: InsertDiskCleanup :exec
INSERT INTO disk_cleanups (trigger, started_at, finished_at, total_bytes, free_before, free_after, freed_bytes, steps, result)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDiskCleanups :many
SELECT * FROM disk_cleanups ORDER BY id DESC LIMIT ?;

-- name: DeleteOldDiskCleanups :exec
-- Keeps the newest 50 runs.
DELETE FROM disk_cleanups WHERE id NOT IN (SELECT id FROM disk_cleanups ORDER BY id DESC LIMIT 50);

-- name: ListAdminUserIDs :many
SELECT id FROM users WHERE role = 'admin' AND status = 'active' ORDER BY created_at;

-- name: ListDeploymentImageRefs :many
-- Every image a deployment of an app that exists refers to: its own build
-- or, for a service, a registry image. current: the app's live deployment.
SELECT d.app_id, d.state, CAST(d.image AS TEXT) AS image,
    CAST(COALESCE(a.current_deployment_id = d.id, 0) AS INTEGER) AS is_current
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.image IS NOT NULL AND d.image != ''
UNION ALL
SELECT d.app_id, d.state, CAST(s.image AS TEXT),
    CAST(COALESCE(a.current_deployment_id = d.id, 0) AS INTEGER)
FROM deployment_services s JOIN deployments d ON d.id = s.deployment_id JOIN apps a ON a.id = d.app_id
WHERE s.image IS NOT NULL AND s.image != '';
