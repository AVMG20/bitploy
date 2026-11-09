-- +goose Up
-- Server settings an admin changes in the dashboard (Admin > Signups), one
-- row (docs/decisions.md, "Signups and new accounts").
--
-- signups: who can create an account.
--   admin_invites  only with an invite link from an admin; a project invite
--                  then only works for people who already have an account
--   any_invite     with an admin invite, or a project invite can also create
--                  the account (the default)
--   open           anyone, on /invite/open; this replaced BITPLOY_SIGNUP=open
-- project_invite_apps: an account made with a project invite gets the
--   limits below; 0: no apps, memory or CPU of its own (it still works in
--   the projects it was invited to, which count against their owners).
-- quota_*: the limits of every account made from now on; the defaults are
--   those of users before this (quota_volume_mb as set by InsertUser).
CREATE TABLE server_settings (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    signups                TEXT    NOT NULL DEFAULT 'any_invite',
    project_invite_apps    INTEGER NOT NULL DEFAULT 1,
    quota_apps             INTEGER NOT NULL DEFAULT 10,
    quota_memory_mb        INTEGER NOT NULL DEFAULT 2048,
    quota_cpu_millis       INTEGER NOT NULL DEFAULT 2000,
    quota_deploys_per_hour INTEGER NOT NULL DEFAULT 20,
    quota_domains_per_app  INTEGER NOT NULL DEFAULT 5,
    quota_volume_mb        INTEGER NOT NULL DEFAULT 25600,
    updated_at             INTEGER NOT NULL DEFAULT 0
);
INSERT INTO server_settings (id) VALUES (1);

-- How an account joined, recorded at signup (empty for accounts made before
-- this): setup (bitploy admin create-user), admin_invite, project_invite or
-- open. joined_by is the user id of who made the invite, joined_project the
-- project id of a project invite ('' for none). No foreign keys: the
-- dashboard looks them up and says so when the project is gone.
ALTER TABLE users ADD COLUMN joined_via TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN joined_by TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN joined_project TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN joined_project;
ALTER TABLE users DROP COLUMN joined_by;
ALTER TABLE users DROP COLUMN joined_via;
DROP TABLE server_settings;
