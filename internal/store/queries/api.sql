-- REST API support (idempotency keys). OWNER: auth-api agent.

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE user_id = ? AND key = ?;

-- name: InsertIdempotencyKey :exec
INSERT OR IGNORE INTO idempotency_keys (user_id, key, method, path, status, body, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteOldIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE created_at < ?;
