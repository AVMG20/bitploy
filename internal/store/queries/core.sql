-- Shared queries used by several areas. Area-specific queries live in their own files.

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetApp :one
SELECT * FROM apps WHERE id = ?;

-- name: GetAppBySlug :one
SELECT * FROM apps WHERE slug = ?;

-- name: ListAppsByUser :many
SELECT * FROM apps WHERE user_id = ? ORDER BY created_at DESC;

-- name: ListAllApps :many
SELECT * FROM apps ORDER BY created_at DESC;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = ?;

-- name: SetAppStatus :exec
UPDATE apps SET status = ?, status_message = ?, updated_at = ? WHERE id = ?;

-- name: InsertAudit :exec
INSERT INTO audit_log (ts, user_id, channel, token_id, ip, action, target_type, target_id, details, app_id, project_id, owner_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountAllApps :one
SELECT count(*) FROM apps;
