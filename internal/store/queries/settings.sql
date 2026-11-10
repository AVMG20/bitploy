-- Server settings (one row, migration 00380) and the admin's view of every
-- user's project invites.

-- name: GetServerSettings :one
SELECT * FROM server_settings WHERE id = 1;

-- name: UpdateServerSettings :exec
UPDATE server_settings SET signups = ?, project_invite_apps = ?,
    quota_apps = ?, quota_memory_mb = ?, quota_cpu_millis = ?, quota_deploys_per_hour = ?,
    quota_domains_per_app = ?, quota_volume_mb = ?, max_parallel_builds = ?, updated_at = ?
WHERE id = 1;

-- name: ListAllPendingProjectInvites :many
SELECT i.id, i.project_id, p.name AS project_name, i.email, i.created_by,
    COALESCE(u.name, '') AS created_by_name, COALESCE(u.email, '') AS created_by_email,
    i.created_at, i.expires_at
FROM project_invites i
JOIN projects p ON p.id = i.project_id
LEFT JOIN users u ON u.id = i.created_by
WHERE i.used_at IS NULL AND i.expires_at > ?
ORDER BY i.created_at DESC, i.id
LIMIT 500;

-- name: GetProjectInviteByID :one
SELECT * FROM project_invites WHERE id = ?;
