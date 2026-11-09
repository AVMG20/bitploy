-- Notification channels of a user (internal/app/notifications.go).

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (id, user_id, type, name, enabled, events, config_enc, dest_key, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = ?;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels WHERE user_id = ? ORDER BY created_at, id;

-- name: ListEnabledNotificationChannels :many
SELECT * FROM notification_channels WHERE user_id = ? AND enabled = 1 ORDER BY created_at, id;

-- name: CountNotificationChannels :one
SELECT COUNT(*) FROM notification_channels WHERE user_id = ?;

-- name: ListNotificationChannelsByDest :many
SELECT * FROM notification_channels WHERE dest_key = ? AND dest_key != '' ORDER BY created_at, id;

-- name: UpdateNotificationChannel :exec
UPDATE notification_channels SET name = ?, enabled = ?, events = ?, config_enc = ?, dest_key = ?, updated_at = ?
WHERE id = ? AND user_id = ?;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE id = ? AND user_id = ?;

-- name: SetNotificationChannelSuccess :exec
UPDATE notification_channels SET last_success_at = ?, last_error = '', last_error_at = NULL WHERE id = ?;

-- name: SetNotificationChannelError :exec
UPDATE notification_channels SET last_error = ?, last_error_at = ? WHERE id = ?;

-- name: DisableNotificationChannel :exec
UPDATE notification_channels SET enabled = 0, last_error = ?, last_error_at = ? WHERE id = ?;

-- The VAPID key pair (internal/app/notifications_push.go).

-- name: GetVapidKeys :one
SELECT * FROM vapid_keys WHERE id = 1;

-- name: InsertVapidKeys :exec
INSERT INTO vapid_keys (id, public_key, private_key_enc, created_at) VALUES (1, ?, ?, ?)
ON CONFLICT (id) DO NOTHING;
