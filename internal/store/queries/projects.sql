-- Projects: spaces that hold apps (docs/compose-plan.md, "Phase 4b").

-- name: InsertProject :exec
INSERT INTO projects (id, user_id, name, auto, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: ListProjectsByUser :many
SELECT * FROM projects WHERE user_id = ? ORDER BY created_at DESC, id;

-- name: RenameProject :exec
UPDATE projects SET name = ?, updated_at = ? WHERE id = ?;

-- name: DeleteProjectRow :exec
DELETE FROM projects WHERE id = ?;

-- name: ListAppsByProject :many
SELECT * FROM apps WHERE project_id = ? ORDER BY created_at, id;

-- name: CountAppsByProject :one
SELECT COUNT(*) FROM apps WHERE project_id = ?;

-- name: SetAppProject :exec
UPDATE apps SET project_id = ?, alias = ?, updated_at = ? WHERE id = ?;

-- name: SetAppTemplate :exec
UPDATE apps SET template = ?, template_version = ?, updated_at = ? WHERE id = ?;

-- name: DeleteAutoProjectIfEmpty :exec
-- A project made for one app goes with its last app.
DELETE FROM projects WHERE projects.id = ? AND projects.auto = 1
  AND NOT EXISTS (SELECT 1 FROM apps WHERE apps.project_id = projects.id);

-- name: SetProjectManual :exec
-- A project the user named or added an app to on purpose stays when it is empty.
UPDATE projects SET auto = 0, updated_at = ? WHERE id = ?;

-- name: GetAppByProjectAlias :one
SELECT * FROM apps WHERE project_id = ? AND alias = ?;

-- name: InsertDeploymentRestart :exec
INSERT OR IGNORE INTO deployment_restarts (deployment_id, app_id, created_at) VALUES (?, ?, ?);

-- name: TakeDeploymentRestarts :many
-- Claims the restarts of a deployment: whoever deletes them does them.
DELETE FROM deployment_restarts WHERE deployment_id = ? RETURNING app_id;

-- name: ListSettledDeploymentRestarts :many
-- Deployments with restarts left whose outcome is known.
SELECT DISTINCT r.deployment_id, d.state FROM deployment_restarts r
JOIN deployments d ON d.id = r.deployment_id
WHERE d.state IN ('running', 'superseded', 'failed', 'cancelled')
ORDER BY r.deployment_id;

-- ---- members (project_members.go) ----

-- name: ListProjectsForUser :many
-- The user's own projects and the projects shared with them, in the order
-- the user put them in: the ones without a position first (newest first),
-- then by position.
SELECT projects.* FROM projects
LEFT JOIN project_positions pp ON pp.project_id = projects.id AND pp.user_id = sqlc.arg(user_id)
WHERE projects.user_id = sqlc.arg(user_id)
   OR projects.id IN (SELECT pm.project_id FROM project_members pm WHERE pm.user_id = sqlc.arg(user_id))
ORDER BY pp.position IS NOT NULL, pp.position, projects.created_at DESC, projects.id;

-- name: DeleteProjectPositions :exec
DELETE FROM project_positions WHERE user_id = ?;

-- name: InsertProjectPosition :exec
INSERT INTO project_positions (user_id, project_id, position) VALUES (?, ?, ?);

-- name: DeleteProjectPosition :exec
DELETE FROM project_positions WHERE user_id = ? AND project_id = ?;

-- name: ListAppsForUser :many
-- The user's own apps and the apps of the projects shared with them.
SELECT * FROM apps
WHERE apps.user_id = sqlc.arg(user_id)
   OR (apps.project_id != '' AND apps.project_id IN (SELECT pm.project_id FROM project_members pm WHERE pm.user_id = sqlc.arg(user_id)))
ORDER BY created_at DESC;

-- name: IsProjectMember :one
SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: InsertProjectMember :exec
INSERT INTO project_members (project_id, user_id, role, invited_by, created_at) VALUES (?, ?, ?, ?, ?);

-- name: DeleteProjectMember :execrows
DELETE FROM project_members WHERE project_id = ? AND user_id = ?;

-- name: CountProjectMembers :one
SELECT COUNT(*) FROM project_members WHERE project_id = ?;

-- name: ListProjectMemberIDs :many
SELECT user_id FROM project_members WHERE project_id = ? ORDER BY user_id;

-- name: ListProjectMembers :many
SELECT m.user_id, m.role, m.invited_by, m.created_at, u.email, u.name
FROM project_members m JOIN users u ON u.id = m.user_id
WHERE m.project_id = ?
ORDER BY m.created_at, m.user_id;

-- name: SetAppCreatedBy :exec
UPDATE apps SET created_by = ? WHERE id = ?;

-- ---- invites to a project ----

-- name: InsertProjectInvite :exec
INSERT INTO project_invites (id, code_hash, project_id, email, role, created_by, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetProjectInviteByHash :one
SELECT * FROM project_invites WHERE code_hash = ?;

-- name: ListPendingProjectInvites :many
SELECT * FROM project_invites WHERE project_id = ? AND used_at IS NULL AND expires_at > ? ORDER BY created_at DESC, id;

-- name: CountPendingProjectInvites :one
SELECT COUNT(*) FROM project_invites WHERE project_id = ? AND used_at IS NULL AND expires_at > ?;

-- name: MarkProjectInviteUsed :execrows
UPDATE project_invites SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteUnusedProjectInvite :execrows
DELETE FROM project_invites WHERE id = ? AND project_id = ? AND used_at IS NULL;

-- name: DeleteOldProjectInvites :exec
DELETE FROM project_invites WHERE expires_at < ? OR (used_at IS NOT NULL AND used_at < ?);

-- name: CountProjectInvitesByCreatorSince :one
SELECT COUNT(*) FROM project_invites WHERE created_by = ? AND created_at > ?;
