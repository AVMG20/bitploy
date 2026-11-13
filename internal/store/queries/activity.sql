-- Activity: the audit rows a user may see (GET /activity). A row is theirs
-- when it is about something they own (owner_id), about a project they are a
-- member of now (project_id), or something they did themselves to an app or
-- project (not as an admin). Each branch has its own index (audit_log_owner,
-- audit_log_project, audit_log_user), so a page never scans the whole log.

-- The three ways a row is the user's are read apart, each newest first with
-- its own index and LIMIT; the service merges them. One query with OR (or
-- IN over the projects) made SQLite read and sort every matching row of the
-- last 90 days for each page.

-- name: ListActivityOwned :many
SELECT * FROM audit_log
WHERE owner_id = sqlc.arg(user_id) AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: ListActivityInProject :many
SELECT * FROM audit_log
WHERE project_id = sqlc.arg(project_id) AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: ListActivityDoneBy :many
-- What the user did to an app or project (not as an admin).
SELECT * FROM audit_log INDEXED BY audit_log_user_id
WHERE user_id = sqlc.arg(user_id) AND id < sqlc.arg(before_id)
  AND (app_id IS NOT NULL OR project_id IS NOT NULL) AND action NOT LIKE 'admin.%'
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: ListMemberProjectIDs :many
SELECT project_id FROM project_members WHERE user_id = ?;

-- name: ListActivityForApp :many
SELECT * FROM audit_log
WHERE app_id = sqlc.arg(app_id) AND id < sqlc.arg(before_id)
  AND (owner_id = sqlc.arg(user_id)
    OR project_id IN (SELECT pm.project_id FROM project_members pm WHERE pm.user_id = sqlc.arg(user_id))
    OR (user_id = sqlc.arg(user_id) AND action NOT LIKE 'admin.%'))
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: GetDomainAppID :one
SELECT app_id FROM domains WHERE id = ?;
