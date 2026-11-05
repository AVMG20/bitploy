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
