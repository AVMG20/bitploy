-- +goose Up
-- Projects (docs/compose-plan.md, "Phase 4b"): a space that holds apps. Every
-- app is in exactly one project; the apps of a project share a private Docker
-- network (bp-p-<id>) and can reference each other's variables with
-- ${{<alias>.VAR}}.
CREATE TABLE projects (
    id         TEXT PRIMARY KEY,                                   -- prj_…
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- 1: made for one app (create_app without a project, or the backfill
    -- below); it goes when its last app is deleted or moved out. 0: made
    -- with create_project; it stays until it is deleted.
    auto       INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX projects_user ON projects(user_id, created_at);

-- project_id: the project the app is in (no REFERENCES: SQLite cannot add a
-- foreign key column with a default; the service keeps it valid).
-- alias: the app's short name in the project, a DNS label; other apps of the
-- project reach it under this name and reference it as ${{<alias>.VAR}}.
-- template / template_version: the service template the app was made from
-- (internal/servicetpl), '' for an app with its own source.
ALTER TABLE apps ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN alias TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN template TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN template_version TEXT NOT NULL DEFAULT '';

-- One project per existing app, named after it; the alias is the slug.
INSERT INTO projects (id, user_id, name, auto, created_at, updated_at)
SELECT 'prj_' || substr(id, 5), user_id, name, 1, created_at, updated_at FROM apps;
UPDATE apps SET project_id = 'prj_' || substr(id, 5), alias = slug;

CREATE UNIQUE INDEX apps_project_alias ON apps(project_id, alias) WHERE project_id != '';

-- +goose Down
DROP INDEX apps_project_alias;
ALTER TABLE apps DROP COLUMN template_version;
ALTER TABLE apps DROP COLUMN template;
ALTER TABLE apps DROP COLUMN alias;
ALTER TABLE apps DROP COLUMN project_id;
DROP TABLE projects;
