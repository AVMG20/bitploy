-- Owned by the deploy agent: an app's services and volumes, and the
-- containers of each deployment (docs/compose-plan.md).

-- ---- services ----

-- name: ListAppServices :many
SELECT * FROM app_services WHERE app_id = ? ORDER BY position, name;

-- name: InsertAppService :exec
INSERT INTO app_services (app_id, name, kind, image, context_dir, dockerfile_path, public, port, health_path,
                          memory_mb, cpu_millis, command, entrypoint, healthcheck, depends_on, environment,
                          position, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SyncSingleAppService :exec
-- A single-layout app keeps its settings on the apps row; its one service
-- ("app") mirrors them.
INSERT INTO app_services (app_id, name, kind, context_dir, dockerfile_path, public, port, health_path,
                          memory_mb, cpu_millis, position, created_at, updated_at)
SELECT a.id, 'app', 'build', a.context_dir, a.dockerfile_path, CASE WHEN a.type = 'web' THEN 1 ELSE 0 END,
       a.port, a.health_path, a.memory_mb, a.cpu_millis, 0, a.created_at, a.updated_at
FROM apps a WHERE a.id = ? AND a.service_layout = 'single'
ON CONFLICT (app_id, name) DO UPDATE SET
    context_dir = excluded.context_dir, dockerfile_path = excluded.dockerfile_path, public = excluded.public,
    port = excluded.port, health_path = excluded.health_path, memory_mb = excluded.memory_mb,
    cpu_millis = excluded.cpu_millis, updated_at = excluded.updated_at;

-- name: SetAppServiceLayout :exec
UPDATE apps SET service_layout = ?, updated_at = ? WHERE id = ?;

-- ---- volumes ----

-- name: ListAppVolumes :many
SELECT * FROM app_volumes WHERE app_id = ? ORDER BY position, name;

-- name: InsertAppVolume :exec
INSERT INTO app_volumes (app_id, name, volume_name, size_mb, service, mount_path, position, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: SyncSingleAppVolume :exec
-- The /data volume of a single-layout app mirrors apps.volume_enabled,
-- volume_mb and volume_name.
INSERT INTO app_volumes (app_id, name, volume_name, size_mb, service, mount_path, position, created_at)
SELECT a.id, 'data', CASE WHEN a.volume_name != '' THEN a.volume_name ELSE 'bp-' || a.slug || '-data' END,
       a.volume_mb, 'app', '/data', 0, a.updated_at
FROM apps a WHERE a.id = ? AND a.service_layout = 'single' AND a.volume_enabled = 1
ON CONFLICT (app_id, name) DO UPDATE SET volume_name = excluded.volume_name, size_mb = excluded.size_mb;

-- name: DeleteSingleAppVolumeIfOff :exec
DELETE FROM app_volumes
WHERE app_id = sqlc.arg(app_id) AND name = 'data'
  AND EXISTS (SELECT 1 FROM apps a WHERE a.id = sqlc.arg(app_id) AND a.service_layout = 'single' AND a.volume_enabled = 0);

-- name: SetAppVolumeUsed :exec
UPDATE app_volumes SET used_bytes = ? WHERE volume_name = ?;

-- name: ListAllAppVolumes :many
SELECT v.app_id, v.name, v.volume_name FROM app_volumes v ORDER BY v.app_id, v.position, v.name;

-- name: SumServiceVolumeMBByUser :one
-- Storage reserved by multi-layout apps (single-layout apps count through
-- apps.volume_mb, see SumVolumeMBByUser).
SELECT CAST(COALESCE(SUM(v.size_mb), 0) AS INTEGER) FROM app_volumes v JOIN apps a ON a.id = v.app_id
WHERE a.user_id = ? AND a.id != ? AND a.service_layout = 'multi';

-- name: AppVolumeNameExists :one
SELECT COUNT(*) FROM app_volumes WHERE volume_name = ?;

-- ---- deployment services ----

-- name: UpsertDeploymentService :exec
INSERT INTO deployment_services (deployment_id, service, image, container_name, port, has_healthcheck, config_hash)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (deployment_id, service) DO UPDATE SET
    image = excluded.image, container_name = excluded.container_name, port = excluded.port,
    has_healthcheck = excluded.has_healthcheck, config_hash = excluded.config_hash;

-- name: ListDeploymentServices :many
SELECT * FROM deployment_services WHERE deployment_id = ? ORDER BY service;

-- name: ListAppDeploymentImages :many
-- Every image a deployment of the app refers to, newest deployment first.
SELECT d.seq, s.image FROM deployment_services s JOIN deployments d ON d.id = s.deployment_id
WHERE d.app_id = ? AND s.image IS NOT NULL ORDER BY d.seq DESC;

-- name: ListCurrentContainers :many
-- The containers of every app's current deployment, whatever its desired
-- state. Deployments from before deployment_services existed (tests only)
-- count with deployments.container_name.
SELECT a.id AS app_id, s.service, s.container_name
FROM apps a JOIN deployment_services s ON s.deployment_id = a.current_deployment_id
WHERE s.container_name IS NOT NULL
UNION
SELECT a.id, '', d.container_name
FROM apps a JOIN deployments d ON d.id = a.current_deployment_id
WHERE d.container_name IS NOT NULL;

-- name: OpsListCurrentContainers :many
SELECT s.service, s.container_name
FROM apps a JOIN deployment_services s ON s.deployment_id = a.current_deployment_id
WHERE a.id = sqlc.arg(app_id) AND s.container_name IS NOT NULL
UNION
SELECT '', d.container_name
FROM apps a JOIN deployments d ON d.id = a.current_deployment_id
WHERE a.id = sqlc.arg(app_id) AND d.container_name IS NOT NULL;

-- name: RewriteDeploymentServiceNames :exec
-- After a rename: bp-<old>... becomes bp-<new>... and bitploy/<old>...
-- becomes bitploy/<new>..., for single (bp-<slug>-<n>) and multi
-- (bp-<slug>_<service>-<n>) names alike. Images bitploy did not build keep
-- their reference.
UPDATE deployment_services
SET image = CASE WHEN substr(image, 1, length('bitploy/' || sqlc.arg(old_slug))) = 'bitploy/' || sqlc.arg(old_slug)
                 THEN 'bitploy/' || sqlc.arg(new_slug) || substr(image, length('bitploy/' || sqlc.arg(old_slug)) + 1)
                 ELSE image END,
    container_name = CASE WHEN substr(container_name, 1, length('bp-' || sqlc.arg(old_slug))) = 'bp-' || sqlc.arg(old_slug)
                 THEN 'bp-' || sqlc.arg(new_slug) || substr(container_name, length('bp-' || sqlc.arg(old_slug)) + 1)
                 ELSE container_name END
WHERE deployment_id IN (SELECT id FROM deployments WHERE app_id = sqlc.arg(app_id));

-- name: CopyDeploymentServices :exec
-- A rollback starts the services of the deployment it rolls back to.
INSERT INTO deployment_services (deployment_id, service, image, container_name, port, has_healthcheck, config_hash)
SELECT sqlc.arg(new_id), src.service, src.image, NULL, src.port, src.has_healthcheck, ''
FROM deployment_services src WHERE src.deployment_id = sqlc.arg(from_id);

-- ---- env vars per service ----

-- name: ListServiceEnvVars :many
-- Shared variables (service '') and the ones of one service.
SELECT * FROM env_vars WHERE app_id = ? AND (service = '' OR service = sqlc.arg(service)) ORDER BY service, name;
