-- +goose Up
-- Whose stuff an audit row is about, so every user can see the activity on
-- their own apps, projects and account (GET /activity) without scanning the
-- whole log. Filled by Service.Audit when the row is written:
--   app_id     the app the row is about (an app, its deployments, domains,
--              backups, files, ...); kept after the app is deleted.
--   project_id the project of that app, or the project the row is about. Its
--              members see the row while they are members.
--   owner_id   the user who owns it: the app's or project's owner, the user
--              an account row is about, or the user who made their own
--              token, session or notification channel.
-- Rows about the server (settings, invites, the GitHub app, disk cleanup)
-- get none of them and stay in Admin > Activity log only.
ALTER TABLE audit_log ADD COLUMN app_id TEXT;
ALTER TABLE audit_log ADD COLUMN project_id TEXT;
ALTER TABLE audit_log ADD COLUMN owner_id TEXT;

CREATE INDEX audit_log_owner ON audit_log(owner_id);
CREATE INDEX audit_log_project ON audit_log(project_id);
CREATE INDEX audit_log_app ON audit_log(app_id);

-- Best-effort backfill of the rows written before this migration (the
-- action index only speeds up the lookups below).
CREATE INDEX audit_log_action_backfill ON audit_log(action);
UPDATE audit_log SET app_id = target_id WHERE target_type = 'app' AND target_id != '';
UPDATE audit_log SET app_id = (SELECT d.app_id FROM deployments d WHERE d.id = audit_log.target_id)
WHERE target_type = 'deployment';
UPDATE audit_log SET app_id = COALESCE(
    (SELECT d.app_id FROM domains d WHERE d.id = audit_log.target_id),
    CASE WHEN json_valid(details) THEN json_extract(details, '$.app_id') END)
WHERE target_type = 'domain';
-- A deployment of a deleted app: the first app deleted with the slug in its
-- details after the event.
UPDATE audit_log SET app_id = (
    SELECT x.target_id FROM audit_log x
    WHERE x.action = 'app.delete' AND x.id > audit_log.id AND json_valid(x.details)
      AND json_extract(x.details, '$.slug') = json_extract(audit_log.details, '$.app')
    ORDER BY x.id LIMIT 1)
WHERE target_type = 'deployment' AND app_id IS NULL AND json_valid(details);

UPDATE audit_log SET
    project_id = (SELECT a.project_id FROM apps a WHERE a.id = audit_log.app_id AND a.project_id != ''),
    owner_id = (SELECT a.user_id FROM apps a WHERE a.id = audit_log.app_id)
WHERE app_id IS NOT NULL;
-- A deleted app: its owner is the user bitploy deployed it for
-- (deployment.running and .failed carry the owner as the user).
UPDATE audit_log SET owner_id = (
    SELECT x.user_id FROM audit_log x
    WHERE x.app_id = audit_log.app_id AND x.channel = 'system' AND x.user_id IS NOT NULL
      AND x.action IN ('deployment.running', 'deployment.failed')
    LIMIT 1)
WHERE app_id IS NOT NULL AND owner_id IS NULL;

UPDATE audit_log SET project_id = target_id,
    owner_id = COALESCE(
        (SELECT p.user_id FROM projects p WHERE p.id = audit_log.target_id),
        (SELECT x.user_id FROM audit_log x WHERE x.action = 'project.create' AND x.target_id = audit_log.target_id LIMIT 1))
WHERE target_type = 'project' AND target_id != '';
UPDATE audit_log SET owner_id = target_id WHERE target_type = 'user' AND target_id != '';
UPDATE audit_log SET owner_id = user_id WHERE target_type IN ('token', 'session', 'github_installation');
UPDATE audit_log SET owner_id = COALESCE(
    (SELECT n.user_id FROM notification_channels n WHERE n.id = audit_log.target_id),
    CASE WHEN json_valid(details) THEN json_extract(details, '$.user') END,
    user_id)
WHERE target_type = 'notification_channel';

DROP INDEX audit_log_action_backfill;

-- +goose Down
DROP INDEX audit_log_app;
DROP INDEX audit_log_project;
DROP INDEX audit_log_owner;
ALTER TABLE audit_log DROP COLUMN owner_id;
ALTER TABLE audit_log DROP COLUMN project_id;
ALTER TABLE audit_log DROP COLUMN app_id;
