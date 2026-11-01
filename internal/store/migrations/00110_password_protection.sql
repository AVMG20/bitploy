-- +goose Up
-- Password protection: a browser login prompt (HTTP basic auth) in front of an app.
--  protect_enabled   1 while the prompt is on
--  protect_username  the login name ("" while the prompt is off)
--  protect_hash      bcrypt hash of the password ("" while the prompt is off); the password itself is never stored

ALTER TABLE apps ADD COLUMN protect_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN protect_username TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN protect_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE apps DROP COLUMN protect_hash;
ALTER TABLE apps DROP COLUMN protect_username;
ALTER TABLE apps DROP COLUMN protect_enabled;
