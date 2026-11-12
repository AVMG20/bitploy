-- +goose Up
-- Admin notifications: the bitploy version that ran last, so a start on a
-- new version tells the admins (server.updated); '' until the first start.
ALTER TABLE server_settings ADD COLUMN last_version TEXT NOT NULL DEFAULT '';

-- Admins who already hear about the server's disk also hear about updates.
UPDATE notification_channels SET events = json_insert(events, '$[#]', 'server.updated')
WHERE user_id IN (SELECT id FROM users WHERE role = 'admin')
  AND json_valid(events)
  AND EXISTS (SELECT 1 FROM json_each(notification_channels.events) WHERE value = 'server.disk_low')
  AND NOT EXISTS (SELECT 1 FROM json_each(notification_channels.events) WHERE value = 'server.updated');

-- +goose Down
UPDATE notification_channels SET events = COALESCE((SELECT json_group_array(value) FROM json_each(notification_channels.events)
    WHERE value NOT IN ('server.updated', 'account.created')), '[]')
WHERE json_valid(events);
ALTER TABLE server_settings DROP COLUMN last_version;
