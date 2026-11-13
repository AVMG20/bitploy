-- Build history (build_history): written when a deployment that used a build
-- slot ends, read by the admin's server charts, build stats and person page.

-- name: InsertBuildHistory :exec
INSERT OR IGNORE INTO build_history (
    deployment_id, app_id, app_slug, app_name, owner_id, started_by, person_id, channel, source_kind,
    queued_at, started_at, built_at, finished_at, result, error_code, peak_cpu, peak_mem_mb
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: HostPeakBetween :one
-- The highest server CPU and memory of the samples from ts to ts_2.
SELECT COUNT(*) AS samples,
       CAST(COALESCE(MAX(cpu), 0) AS REAL) AS cpu,
       CAST(COALESCE(MAX(mem_used_bytes), 0) AS INTEGER) AS mem_used_bytes
FROM host_samples WHERE ts >= ? AND ts <= ?;

-- name: ListBuildHistoryOverlapping :many
-- Builds whose build step (started_at to built_at, else finished_at) ends at
-- or after since, newest first.
SELECT sqlc.embed(h), COALESCE(u.name, '') AS person_name
FROM build_history h LEFT JOIN users u ON u.id = h.person_id
WHERE h.finished_at >= sqlc.arg(since) AND COALESCE(h.built_at, h.finished_at) >= sqlc.arg(since)
ORDER BY h.started_at DESC
LIMIT sqlc.arg(max_rows);

-- name: ListBuildHistorySince :many
-- Every build that finished at or after the given time, for the stats per person.
SELECT sqlc.embed(h), COALESCE(u.name, '') AS person_name, COALESCE(u.email, '') AS person_email
FROM build_history h LEFT JOIN users u ON u.id = h.person_id
WHERE h.finished_at >= ?
ORDER BY h.finished_at;

-- name: ListBuildHistoryLatest :many
-- Everyone's builds that ended at or after the given time, newest first.
SELECT sqlc.embed(h), COALESCE(u.name, '') AS person_name
FROM build_history h LEFT JOIN users u ON u.id = h.person_id
WHERE h.finished_at >= ?
ORDER BY h.started_at DESC
LIMIT ?;

-- name: ListBuildHistoryByPerson :many
SELECT sqlc.embed(h), COALESCE(u.name, '') AS person_name
FROM build_history h LEFT JOIN users u ON u.id = h.person_id
WHERE h.person_id = ? AND h.finished_at >= ?
ORDER BY h.started_at DESC
LIMIT ?;

-- name: ListDeploymentsBuildingNow :many
-- Deployments that took a build slot and have not ended yet: the history has
-- no row for them yet.
SELECT d.id, d.app_id, d.channel, d.source_kind, d.created_by, d.queued_at, d.started_at, d.built_at,
       a.slug AS app_slug, a.name AS app_name, a.user_id AS owner_id,
       COALESCE(u.name, '') AS person_name
FROM deployments d
JOIN apps a ON a.id = d.app_id
LEFT JOIN users u ON u.id = COALESCE(d.created_by, a.user_id)
WHERE d.state IN ('fetching', 'validating', 'building', 'starting', 'health_check', 'swapping')
  AND d.source_kind != 'rollback' AND d.started_at IS NOT NULL
ORDER BY d.started_at DESC;

-- name: DeleteOldBuildHistory :execrows
DELETE FROM build_history WHERE finished_at < ?;

-- name: ListAuditPageByUser :many
-- The audit log of what one person did, newest first.
SELECT * FROM audit_log WHERE user_id = ? AND id < ? ORDER BY id DESC LIMIT ?;
