-- Templates, Dockerfile checks and git sources. OWNER: MCP agent.

-- name: SetAppGitSource :exec
UPDATE apps SET git_repo_url = ?, git_branch = ?, git_subdir = ?, git_auto_deploy = ?, updated_at = ? WHERE id = ?;

-- name: ClearAppGitSource :exec
UPDATE apps SET git_repo_url = NULL, git_branch = NULL, git_subdir = NULL, git_auto_deploy = 1, git_last_push_at = NULL, updated_at = ? WHERE id = ?;

-- name: SetAppGitAutoDeploy :exec
UPDATE apps SET git_auto_deploy = ?, updated_at = ? WHERE id = ?;

-- name: SetAppGitLastPush :exec
UPDATE apps SET git_last_push_at = ? WHERE id = ?;

-- name: ListAppsWithGitHubSource :many
SELECT * FROM apps WHERE git_repo_url IS NOT NULL AND lower(git_repo_url) LIKE 'https://%github.com/%';
