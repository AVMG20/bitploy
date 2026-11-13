-- Owned by the deploy agent: deployments and the job queue.

-- name: InsertDeployment :exec
INSERT INTO deployments (id, app_id, seq, state, source_kind, source_ref, channel, created_by, token_id,
                         image, port, has_healthcheck, warnings, build_kind, queued_at)
VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetDeploymentBySeq :one
SELECT * FROM deployments WHERE app_id = ? AND seq = ?;

-- name: ListDeploymentsByApp :many
SELECT * FROM deployments WHERE app_id = ? AND seq < ? ORDER BY seq DESC LIMIT ?;

-- name: ListAllDeploymentsByApp :many
SELECT * FROM deployments WHERE app_id = ? ORDER BY seq DESC;

-- name: ListQueuedDeployments :many
-- The server's line, oldest first, with the owner of each app: the worker
-- gives build slots fairly between owners (fairBuildOrder).
SELECT d.id, d.app_id, d.source_kind, a.user_id AS owner_id
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.state = 'queued'
ORDER BY d.queued_at, d.seq;

-- name: ListActiveDeploymentsForUser :many
-- Deployments that wait or run, of the user's apps and the apps of the
-- projects shared with them (the builds widget), oldest first.
SELECT d.id, d.app_id, d.seq, d.state, d.source_kind, d.commit_message, d.queued_at, d.started_at,
    a.slug AS app_slug, a.name AS app_name, a.project_id AS app_project_id
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE d.state IN ('queued', 'fetching', 'validating', 'building', 'starting', 'health_check', 'swapping')
  AND (a.user_id = sqlc.arg(user_id)
    OR (a.project_id != '' AND a.project_id IN (SELECT pm.project_id FROM project_members pm WHERE pm.user_id = sqlc.arg(user_id))))
ORDER BY d.queued_at, d.seq;

-- name: ClaimDeployment :execrows
UPDATE deployments SET state = 'fetching', started_at = ? WHERE id = ? AND state = 'queued';

-- name: SetDeploymentState :exec
UPDATE deployments SET state = ? WHERE id = ?;

-- name: CancelQueuedDeployments :many
UPDATE deployments SET state = 'cancelled', finished_at = ? WHERE app_id = ? AND state = 'queued' RETURNING id;

-- name: CancelQueuedDeployment :execrows
UPDATE deployments
SET state = 'cancelled', error_code = 'cancelled', error_message = ?, finished_at = ?
WHERE id = ? AND state = 'queued';

-- name: FinishCancelledDeployment :exec
UPDATE deployments
SET state = 'cancelled', error_code = 'cancelled', error_message = ?, finished_at = ?
WHERE id = ?;

-- name: SetDeploymentCommit :exec
UPDATE deployments SET commit_sha = ?, commit_message = ? WHERE id = ?;

-- name: SetDeploymentValidated :exec
UPDATE deployments SET port = ?, has_healthcheck = ?, warnings = ?, build_kind = ? WHERE id = ?;

-- name: SetDeploymentServicesSpec :exec
UPDATE deployments SET services_spec = ? WHERE id = ?;

-- name: SetDeploymentPort :exec
UPDATE deployments SET port = ? WHERE id = ?;

-- name: SetDeploymentBuildReads :exec
UPDATE deployments SET build_reads = ? WHERE id = ?;

-- name: SetDeploymentBuilt :exec
UPDATE deployments SET image = ?, image_size_bytes = ?, built_at = ? WHERE id = ?;

-- name: SetDeploymentContainer :exec
UPDATE deployments SET container_name = ?, port = ?, has_healthcheck = ? WHERE id = ?;

-- name: FinishDeployment :exec
UPDATE deployments SET state = ?, finished_at = ? WHERE id = ?;

-- name: FailDeployment :exec
UPDATE deployments
SET state = 'failed', error_code = ?, error_message = ?, error_hint = ?, error_next = ?, error_service = ?, log_tail = ?, finished_at = ?
WHERE id = ?;

-- name: InterruptDeployment :execrows
UPDATE deployments
SET state = 'failed', error_code = ?, error_message = ?, error_hint = ?, error_next = ?, finished_at = ?
WHERE id = ? AND state = ?;

-- name: SupersedeDeployments :exec
UPDATE deployments SET state = 'superseded' WHERE app_id = ? AND state = 'running' AND id != ?;

-- name: ListInterruptibleDeployments :many
SELECT * FROM deployments WHERE state IN ('fetching', 'validating', 'building', 'starting', 'health_check', 'swapping');

-- name: CountUserDeploymentsSince :one
SELECT COUNT(*) FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE a.user_id = ? AND d.queued_at >= ?;

-- name: CountPendingDeploymentsByApp :one
SELECT COUNT(*) FROM deployments
WHERE app_id = ? AND state IN ('queued', 'fetching', 'validating', 'building', 'starting', 'health_check', 'swapping');

-- name: SetDeploymentCatalogVersion :exec
UPDATE deployments SET catalog_version = ? WHERE id = ?;
