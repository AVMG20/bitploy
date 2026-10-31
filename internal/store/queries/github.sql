-- GitHub App credentials, linked installations and webhook deliveries.

-- name: GetGitHubApp :one
SELECT * FROM github_app WHERE id = 1;

-- name: UpsertGitHubApp :exec
INSERT INTO github_app (id, github_app_id, slug, name, html_url, owner_login, owner_type, client_id,
                        client_secret_enc, webhook_secret_enc, private_key_enc, created_by, created_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    github_app_id = excluded.github_app_id, slug = excluded.slug, name = excluded.name,
    html_url = excluded.html_url, owner_login = excluded.owner_login, owner_type = excluded.owner_type, client_id = excluded.client_id,
    client_secret_enc = excluded.client_secret_enc, webhook_secret_enc = excluded.webhook_secret_enc,
    private_key_enc = excluded.private_key_enc, created_by = excluded.created_by, created_at = excluded.created_at;

-- name: DeleteGitHubApp :exec
DELETE FROM github_app WHERE id = 1;

-- name: DeleteAllGitHubInstallations :exec
DELETE FROM github_installations;

-- name: DeleteAllGitHubInstallationRepos :exec
DELETE FROM github_installation_repos;

-- name: UpsertGitHubInstallation :exec
INSERT INTO github_installations (user_id, installation_id, account_login, account_type, repository_selection,
                                  status, repos_synced_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'active', ?, ?, ?)
ON CONFLICT(user_id, installation_id) DO UPDATE SET
    account_login = excluded.account_login, account_type = excluded.account_type,
    repository_selection = excluded.repository_selection, status = 'active',
    repos_synced_at = excluded.repos_synced_at, updated_at = excluded.updated_at;

-- name: ListGitHubInstallationsByUser :many
SELECT * FROM github_installations WHERE user_id = ? ORDER BY account_login COLLATE NOCASE, installation_id;

-- name: GetGitHubInstallationLink :one
SELECT * FROM github_installations WHERE user_id = ? AND installation_id = ?;

-- name: DeleteGitHubInstallationLink :execrows
DELETE FROM github_installations WHERE user_id = ? AND installation_id = ?;

-- name: CountGitHubInstallationLinks :one
SELECT COUNT(*) FROM github_installations WHERE installation_id = ?;

-- name: SetGitHubInstallationStatus :execrows
UPDATE github_installations SET status = ?, updated_at = ? WHERE installation_id = ?;

-- name: SetGitHubInstallationSelection :exec
UPDATE github_installations SET repository_selection = ?, updated_at = ? WHERE installation_id = ?;

-- name: SetGitHubInstallationSynced :exec
UPDATE github_installations SET repos_synced_at = ?, updated_at = ? WHERE installation_id = ?;

-- name: ClearGitHubInstallationRepos :exec
DELETE FROM github_installation_repos WHERE installation_id = ?;

-- name: AddGitHubInstallationRepo :exec
INSERT OR IGNORE INTO github_installation_repos (installation_id, repo) VALUES (?, ?);

-- name: RemoveGitHubInstallationRepo :exec
DELETE FROM github_installation_repos WHERE installation_id = ? AND repo = ?;

-- name: GitHubInstallationHasRepo :one
SELECT COUNT(*) FROM github_installation_repos WHERE installation_id = ? AND repo = ?;

-- name: InsertGitHubDelivery :execrows
INSERT OR IGNORE INTO github_deliveries (delivery_id, received_at) VALUES (?, ?);

-- name: DeleteGitHubDelivery :exec
DELETE FROM github_deliveries WHERE delivery_id = ?;

-- name: PruneGitHubDeliveries :exec
DELETE FROM github_deliveries WHERE received_at < ?;

-- name: GetAnyGitHubInstallationLink :one
SELECT * FROM github_installations WHERE installation_id = ? LIMIT 1;

-- name: CountGitHubInstallationRepos :one
SELECT COUNT(*) FROM github_installation_repos WHERE installation_id = ?;
