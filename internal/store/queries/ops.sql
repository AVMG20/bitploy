-- Queries owned by the ops agent: custom domains, metrics, usage, nightly jobs.

-- ---- domains ----

-- name: OpsInsertDomain :exec
INSERT INTO domains (id, app_id, hostname, status, txt_token, last_error, check_until, created_at)
VALUES (?, ?, ?, 'pending_dns', ?, '', ?, ?);

-- name: OpsGetDomainByHostname :one
SELECT * FROM domains WHERE hostname = ?;

-- name: OpsListDomainsByApp :many
SELECT * FROM domains WHERE app_id = ? ORDER BY created_at, hostname;

-- name: OpsCountDomainsByApp :one
SELECT COUNT(*) FROM domains WHERE app_id = ?;

-- name: OpsListDomainsByStatuses :many
SELECT * FROM domains WHERE status IN (sqlc.slice('statuses')) ORDER BY created_at;

-- name: OpsListHostnamesByStatuses :many
SELECT hostname FROM domains WHERE app_id = ? AND status IN (sqlc.slice('statuses')) ORDER BY hostname;

-- name: OpsSetDomainStatus :exec
UPDATE domains
SET status = ?, last_error = ?, last_checked_at = ?, check_until = ?, activated_at = ?
WHERE id = ?;

-- name: OpsDeleteDomain :execrows
DELETE FROM domains WHERE id = ?;

-- name: OpsDeleteStaleDomain :execrows
-- A claim that never proved ownership (still waiting for DNS, or gave up) may be replaced.
DELETE FROM domains WHERE id = ? AND status IN ('pending_dns', 'failed');

-- ---- apps ----

-- name: OpsSetAppDesiredState :exec
UPDATE apps SET desired_state = ?, updated_at = ? WHERE id = ?;

-- name: OpsGetCurrentContainer :one
SELECT d.container_name AS container_name
FROM apps a JOIN deployments d ON d.id = a.current_deployment_id
WHERE a.id = ?;

-- name: OpsSumMemoryLimitsMB :one
SELECT CAST(COALESCE(SUM(memory_mb), 0) AS INTEGER) AS total FROM apps
WHERE desired_state = 'running' AND current_deployment_id IS NOT NULL;

-- ---- metric samples and rollups ----

-- name: OpsInsertSample :exec
INSERT INTO metric_samples (app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes) VALUES (?, ?, ?, ?, ?, ?);

-- name: OpsListSamples :many
SELECT app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes
FROM metric_samples WHERE app_id = ? AND ts >= ? ORDER BY ts;

-- name: OpsListSamplesBefore :many
SELECT app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes
FROM metric_samples WHERE app_id = ? AND ts >= ? AND ts < ? ORDER BY ts;

-- name: OpsListSampleApps :many
SELECT DISTINCT app_id FROM metric_samples;

-- name: OpsUpsertRollup :exec
INSERT INTO metric_rollups (app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (app_id, ts) DO UPDATE SET
    cpu = excluded.cpu, mem_bytes = excluded.mem_bytes, rx_bytes = excluded.rx_bytes,
    tx_bytes = excluded.tx_bytes, cpu_max = excluded.cpu_max, mem_max = excluded.mem_max;

-- name: OpsListRollups :many
SELECT app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max
FROM metric_rollups WHERE app_id = ? AND ts >= ? ORDER BY ts;

-- name: OpsLastRollupTS :one
SELECT CAST(COALESCE(MAX(ts), 0) AS INTEGER) AS ts FROM metric_rollups WHERE app_id = ?;

-- name: OpsDeleteSamplesBefore :execrows
DELETE FROM metric_samples WHERE ts < ?;

-- name: OpsDeleteRollupsBefore :execrows
DELETE FROM metric_rollups WHERE ts < ?;

-- name: OpsDeleteOrphanSamples :exec
DELETE FROM metric_samples WHERE app_id NOT IN (SELECT id FROM apps);

-- name: OpsDeleteOrphanRollups :exec
DELETE FROM metric_rollups WHERE app_id NOT IN (SELECT id FROM apps);

-- ---- usage ----

-- name: OpsAddRunningSeconds :exec
INSERT INTO usage_daily (app_id, user_id, day, running_seconds) VALUES (?, ?, ?, ?)
ON CONFLICT (app_id, day) DO UPDATE SET running_seconds = running_seconds + excluded.running_seconds;

-- name: OpsRunningSecondsSince :one
SELECT CAST(COALESCE(SUM(running_seconds), 0) AS INTEGER) AS seconds
FROM usage_daily WHERE user_id = ? AND day >= ?;

-- name: OpsUsageAppTotals :one
SELECT
    COUNT(*) AS apps,
    CAST(COALESCE(SUM(CASE WHEN a.status = 'running' THEN 1 ELSE 0 END), 0) AS INTEGER) AS apps_running,
    -- Static sites (see SumAppResourcesByUser) do not count towards memory and CPU.
    CAST(COALESCE(SUM(CASE WHEN a.desired_state = 'running' AND a.current_deployment_id IS NOT NULL AND COALESCE(d.build_kind, '') NOT IN ('static', 'node-spa') THEN a.memory_mb ELSE 0 END), 0) AS INTEGER) AS memory_mb,
    CAST(COALESCE(SUM(CASE WHEN a.desired_state = 'running' AND a.current_deployment_id IS NOT NULL AND COALESCE(d.build_kind, '') NOT IN ('static', 'node-spa') THEN a.cpu_millis ELSE 0 END), 0) AS INTEGER) AS cpu_millis,
    CAST(COALESCE(SUM(CASE WHEN a.volume_enabled = 1 THEN a.volume_mb ELSE 0 END), 0) AS INTEGER) AS volume_mb
FROM apps a LEFT JOIN deployments d ON d.id = a.current_deployment_id
WHERE a.user_id = ?;

-- name: OpsUserVolumeBytes :one
SELECT CAST(COALESCE(SUM(x.volume_bytes), 0) AS INTEGER) AS bytes
FROM app_disk x JOIN apps a ON a.id = x.app_id WHERE a.user_id = ? AND a.volume_enabled = 1;

-- name: OpsCountDeploymentsSince :one
SELECT COUNT(*) FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE a.user_id = ? AND d.queued_at >= ?;

-- name: OpsBuildSecondsSince :one
SELECT CAST(COALESCE(SUM(COALESCE(d.built_at, d.finished_at) - d.started_at), 0) AS INTEGER) AS seconds
FROM deployments d JOIN apps a ON a.id = d.app_id
WHERE a.user_id = ? AND d.queued_at >= ? AND d.started_at IS NOT NULL
  AND COALESCE(d.built_at, d.finished_at) IS NOT NULL
  AND d.source_kind != 'rollback'; -- a rollback reuses an image and builds nothing

-- name: OpsUserDiskBytes :one
SELECT CAST(COALESCE(SUM(x.image_bytes + x.source_bytes + x.volume_bytes), 0) AS INTEGER) AS bytes
FROM app_disk x JOIN apps a ON a.id = x.app_id WHERE a.user_id = ?;

-- name: OpsUserFilesBytes :one
-- App files: kept images and stored source snapshots, measured hourly.
SELECT CAST(COALESCE(SUM(x.image_bytes + x.source_bytes), 0) AS INTEGER) AS bytes
FROM app_disk x JOIN apps a ON a.id = x.app_id WHERE a.user_id = ?;

-- name: OpsUpsertAppDisk :exec
INSERT INTO app_disk (app_id, image_bytes, source_bytes, volume_bytes, measured_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (app_id) DO UPDATE SET
    image_bytes = excluded.image_bytes, source_bytes = excluded.source_bytes,
    volume_bytes = excluded.volume_bytes, measured_at = excluded.measured_at;

-- name: OpsDeleteOrphanAppDisk :exec
DELETE FROM app_disk WHERE app_id NOT IN (SELECT id FROM apps);

-- name: OpsDeleteOrphanUsage :exec
DELETE FROM usage_daily WHERE day < ? AND app_id NOT IN (SELECT id FROM apps);

-- name: OpsListAppVolumes :many
SELECT id, slug, volume_name, volume_enabled FROM apps;

-- name: OpsListDeploymentApps :many
SELECT id, app_id FROM deployments;

-- name: OpsListVolumeUsage :many
SELECT a.id, a.slug, a.status, a.status_message, a.desired_state, a.volume_mb, d.volume_bytes
FROM apps a
JOIN app_disk d ON d.app_id = a.id
WHERE a.volume_enabled = 1;

-- ---- admin server stats ----

-- name: OpsBuildCounts :one
SELECT
    CAST(COALESCE(SUM(CASE WHEN state IN ('fetching', 'validating', 'building') THEN 1 ELSE 0 END), 0) AS INTEGER) AS running,
    CAST(COALESCE(SUM(CASE WHEN state = 'queued' THEN 1 ELSE 0 END), 0) AS INTEGER) AS queued
FROM deployments WHERE state IN ('queued', 'fetching', 'validating', 'building');

-- name: OpsAppCounts :one
SELECT COUNT(*) AS total,
    CAST(COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0) AS INTEGER) AS running
FROM apps;

-- ---- housekeeping ----

-- name: OpsListUploadsBefore :many
SELECT id FROM uploads WHERE created_at < ?;

-- name: OpsDeleteUpload :exec
DELETE FROM uploads WHERE id = ?;
