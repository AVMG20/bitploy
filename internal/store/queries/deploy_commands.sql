-- Owned by the deploy agent: the before- and after-deploy commands of an app.

-- name: GetAppDeployCommands :one
SELECT before_deploy, after_deploy FROM app_deploy_commands WHERE app_id = ?;

-- name: SetAppDeployCommands :exec
INSERT INTO app_deploy_commands (app_id, before_deploy, after_deploy, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (app_id) DO UPDATE SET before_deploy = excluded.before_deploy,
    after_deploy = excluded.after_deploy, updated_at = excluded.updated_at;

-- name: SetDeploymentAfterDeploy :exec
UPDATE deployments SET after_deploy = ? WHERE id = ?;

-- name: SetDeploymentWarnings :exec
UPDATE deployments SET warnings = ? WHERE id = ?;

-- name: ListAfterDeployBusy :many
SELECT id FROM deployments WHERE after_deploy IN ('pending', 'running');
