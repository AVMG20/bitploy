-- Users, sessions, invites, API tokens, audit reads. OWNER: auth-api agent.

-- name: InsertUser :exec
-- The limits come from the server settings (server_settings), not the column defaults.
INSERT INTO users (id, email, name, password_hash, role, status,
    quota_apps, quota_memory_mb, quota_cpu_millis, quota_deploys_per_hour, quota_domains_per_app, quota_volume_mb,
    joined_via, joined_by, joined_project, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at ASC;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserName :exec
UPDATE users SET name = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserQuota :exec
UPDATE users SET quota_apps = ?, quota_memory_mb = ?, quota_cpu_millis = ?,
    quota_deploys_per_hour = ?, quota_domains_per_app = ?, quota_volume_mb = ?, overallocate = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateUserStatus :exec
UPDATE users SET status = ?, updated_at = ? WHERE id = ?;

-- name: CountAppsOfUser :one
SELECT COUNT(*) FROM apps WHERE user_id = ?;

-- sessions

-- name: InsertSession :exec
INSERT INTO sessions (id_hash, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at, absolute_expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetSessionByHash :one
SELECT * FROM sessions WHERE id_hash = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?;

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteSessionsOfUser :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteOtherSessionsOfUser :exec
DELETE FROM sessions WHERE user_id = ? AND id_hash != ?;

-- name: DeleteSessionByPrefix :execrows
DELETE FROM sessions WHERE user_id = ? AND substr(id_hash, 1, 12) = ?;

-- name: ListSessionsOfUser :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at < ? OR absolute_expires_at < ?;

-- API tokens

-- name: InsertToken :exec
INSERT INTO api_tokens (id, user_id, name, prefix, hash, scope, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTokenByHash :one
SELECT * FROM api_tokens WHERE hash = ?;

-- name: GetTokenOfUser :one
SELECT * FROM api_tokens WHERE id = ? AND user_id = ?;

-- name: ListTokensOfUser :many
SELECT * FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC;

-- name: CountActiveTokensOfUser :one
SELECT COUNT(*) FROM api_tokens
WHERE user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?);

-- name: RevokeTokenRow :execrows
UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL;

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?;

-- invites

-- name: InsertInvite :exec
INSERT INTO invites (id, code_hash, email, role, created_by, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetInviteByHash :one
SELECT * FROM invites WHERE code_hash = ?;

-- name: GetInviteByID :one
SELECT * FROM invites WHERE id = ?;

-- name: ListInvitesNewestFirst :many
SELECT * FROM invites ORDER BY created_at DESC LIMIT 200;

-- name: MarkInviteUsed :execrows
UPDATE invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL;

-- name: DeleteUnusedInvite :execrows
DELETE FROM invites WHERE id = ? AND used_at IS NULL;

-- name: DeleteOldInvites :exec
DELETE FROM invites WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?);

-- audit reads and retention

-- name: ListAuditPage :many
SELECT * FROM audit_log WHERE id < ? ORDER BY id DESC LIMIT ?;

-- name: ListAuditBefore :many
SELECT * FROM audit_log WHERE ts < ? ORDER BY id DESC LIMIT ?;

-- name: DeleteOldAudit :exec
DELETE FROM audit_log WHERE ts < ?;

-- name: UpdateUserEmail :exec
UPDATE users SET email = ?, updated_at = ? WHERE id = ?;

-- password reset links

-- name: InsertPasswordReset :exec
INSERT INTO password_resets (id, user_id, code_hash, created_by, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteUnusedPasswordResetsOfUser :exec
DELETE FROM password_resets WHERE user_id = ? AND used_at IS NULL;

-- name: GetPasswordResetByHash :one
SELECT * FROM password_resets WHERE code_hash = ?;

-- name: MarkPasswordResetUsed :execrows
UPDATE password_resets SET used_at = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteOldPasswordResets :exec
DELETE FROM password_resets WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?);
