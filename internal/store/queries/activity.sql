-- Activity: the audit rows a user may see (GET /activity). A row is theirs
-- when it is about something they own (owner_id), about a project they are a
-- member of now (project_id), or something they did themselves to an app or
-- project (not as an admin). Each branch has its own index (audit_log_owner,
-- audit_log_project, audit_log_user), so a page never scans the whole log.

-- name: ListActivity :many
SELECT * FROM audit_log
WHERE id < sqlc.arg(before_id)
  AND (owner_id = sqlc.arg(user_id)
    OR project_id IN (SELECT pm.project_id FROM project_members pm WHERE pm.user_id = sqlc.arg(user_id))
    OR (user_id = sqlc.arg(user_id) AND (app_id IS NOT NULL OR project_id IS NOT NULL) AND action NOT LIKE 'admin.%'))
ORDER BY id DESC
LIMIT sqlc.arg(lim);

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
