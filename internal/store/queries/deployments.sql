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
SELECT * FROM deployments WHERE state = 'queued' ORDER BY queued_at, seq;

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

-- name: SetDeploymentPort :exec
UPDATE deployments SET port = ? WHERE id = ?;

-- name: SetDeploymentBuilt :exec
UPDATE deployments SET image = ?, image_size_bytes = ?, built_at = ? WHERE id = ?;

-- name: SetDeploymentContainer :exec
UPDATE deployments SET container_name = ?, port = ?, has_healthcheck = ? WHERE id = ?;

-- name: FinishDeployment :exec
UPDATE deployments SET state = ?, finished_at = ? WHERE id = ?;

-- name: FailDeployment :exec
UPDATE deployments
SET state = 'failed', error_code = ?, error_message = ?, error_hint = ?, error_next = ?, log_tail = ?, finished_at = ?
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

-- name: OldestUserDeploymentSince :one
SELECT CAST(COALESCE(MIN(d.queued_at), 0) AS INTEGER) FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE a.user_id = ? AND d.queued_at >= ?;

-- name: CountPendingDeploymentsByApp :one
SELECT COUNT(*) FROM deployments
WHERE app_id = ? AND state IN ('queued', 'fetching', 'validating', 'building', 'starting', 'health_check', 'swapping');

-- name: SetDeploymentCatalogVersion :exec
UPDATE deployments SET catalog_version = ? WHERE id = ?;
