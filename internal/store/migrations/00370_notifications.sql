-- +goose Up
-- Notification channels (docs/decisions.md, "Notifications"): where a user
-- hears about deployments, backups and crashes of the apps they can use
-- (their own and those of projects shared with them). type picks the sender
-- (internal/notify); its settings are JSON, sealed with store.Crypto (AAD
-- "notification-channel\x00<id>") because they hold credentials such as a
-- Discord webhook token or a browser's push subscription. events is a JSON
-- array of the event types the channel gets. dest_key is a keyed hash
-- (store.Crypto.MAC) of the destination for types where one destination
-- belongs to one channel (a browser's push endpoint), else ''.
CREATE TABLE notification_channels (
    id              TEXT PRIMARY KEY,                         -- nch_…
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,                            -- discord | browser
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    events          TEXT NOT NULL DEFAULT '[]',
    config_enc      BLOB NOT NULL,
    dest_key        TEXT NOT NULL DEFAULT '',
    last_success_at INTEGER,
    last_error      TEXT NOT NULL DEFAULT '',
    last_error_at   INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX notification_channels_user ON notification_channels(user_id, created_at);
CREATE INDEX notification_channels_dest ON notification_channels(dest_key) WHERE dest_key != '';

-- The server's VAPID key pair (RFC 8292) for Web Push: made once, on first
-- use. Browsers subscribe with the public key; the private key signs every
-- push and is sealed with store.Crypto (AAD "vapid").
CREATE TABLE vapid_keys (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    public_key      TEXT NOT NULL,                            -- base64url, uncompressed P-256 point
    private_key_enc BLOB NOT NULL,
    created_at      INTEGER NOT NULL
);

-- +goose Down
DROP TABLE vapid_keys;
DROP TABLE notification_channels;
