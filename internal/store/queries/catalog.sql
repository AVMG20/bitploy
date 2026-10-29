-- Templates, Dockerfile checks and git sources. OWNER: MCP agent.

-- name: SetAppGitSource :exec
UPDATE apps SET git_repo_url = ?, git_branch = ?, git_subdir = ?, updated_at = ? WHERE id = ?;
