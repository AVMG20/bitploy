-- Extra wildcard domains for app addresses (admin-managed) and who may use them.

-- name: ListAppDomains :many
SELECT * FROM app_domains ORDER BY domain;

-- name: GetAppDomain :one
SELECT * FROM app_domains WHERE id = ?;

-- name: GetAppDomainByName :one
SELECT * FROM app_domains WHERE domain = ?;

-- name: InsertAppDomain :exec
INSERT INTO app_domains (id, domain, cert_pem, key_enc, cert_not_after, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: SetAppDomainCert :exec
UPDATE app_domains SET cert_pem = ?, key_enc = ?, cert_not_after = ? WHERE id = ?;

-- name: DeleteAppDomain :exec
DELETE FROM app_domains WHERE id = ?;

-- name: CountAppsOnDomain :one
SELECT COUNT(*) FROM apps WHERE base_domain = ?;

-- name: CountAppsByBaseDomain :many
SELECT base_domain, COUNT(*) AS apps FROM apps GROUP BY base_domain;

-- name: CountUsersOnAppDomain :one
SELECT COUNT(*) FROM user_app_domains WHERE domain_id = ?;

-- name: ListUserAppDomainIDs :many
SELECT domain_id FROM user_app_domains WHERE user_id = ? ORDER BY domain_id;

-- name: ListUserAppDomainNames :many
SELECT d.domain FROM user_app_domains u JOIN app_domains d ON d.id = u.domain_id
WHERE u.user_id = ? ORDER BY d.domain;

-- name: ListAllUserAppDomains :many
SELECT user_id, domain_id FROM user_app_domains;

-- name: GrantUserAppDomain :exec
INSERT INTO user_app_domains (user_id, domain_id, created_at) VALUES (?, ?, ?)
ON CONFLICT(user_id, domain_id) DO NOTHING;

-- name: RevokeUserAppDomain :exec
DELETE FROM user_app_domains WHERE user_id = ? AND domain_id = ?;

-- name: CustomDomainsUnder :many
-- Custom domains (per-app hostnames) equal to or below a domain.
SELECT hostname FROM domains WHERE hostname = sqlc.arg(domain) OR hostname LIKE '%.' || sqlc.arg(domain);
