-- +goose Up
-- An app on an extra app domain can also answer on the domain itself
-- (https://example.com): an admin sets its address to @. The slug keeps
-- naming the app's containers, routes and volume, and <slug>.<domain> keeps
-- working; the domain itself becomes the address shown everywhere. One app
-- per domain. The default domain (base_domain '') has no root app.
ALTER TABLE apps ADD COLUMN on_root INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX apps_root_domain ON apps(base_domain) WHERE on_root = 1;

-- +goose Down
DROP INDEX apps_root_domain;
ALTER TABLE apps DROP COLUMN on_root;
