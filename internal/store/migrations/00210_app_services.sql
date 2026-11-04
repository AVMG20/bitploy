-- +goose Up
-- An app has services and volumes (docs/compose-plan.md). Every existing app
-- becomes an app with one service named "app" and, when /data is on, one
-- volume named "data". Their Docker names do not change.

-- How the app's services are laid out:
--   single  one service whose settings are the apps row (port, health_path,
--           memory_mb, ...); app_services and app_volumes mirror it. Docker
--           names have no service part: bp-<slug>-<n>, bitploy/<slug>:<n>.
--   multi   settings live in app_services and app_volumes; apps.memory_mb and
--           apps.cpu_millis are the sums. Docker names carry the service:
--           bp-<slug>_<service>-<n>, bitploy/<slug>_<service>:<n>. The "_"
--           cannot occur in a slug or a service name, so these names never
--           collide with another app's bp-<slug>-<n>.
ALTER TABLE apps ADD COLUMN service_layout TEXT NOT NULL DEFAULT 'single';

CREATE TABLE app_services (
    app_id          TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                     -- DNS label [a-z0-9-], 1-32 chars; also the network alias
    kind            TEXT NOT NULL DEFAULT 'build',     -- build | image
    image           TEXT NOT NULL DEFAULT '',          -- image services: the reference to pull, like postgres:16
    context_dir     TEXT NOT NULL DEFAULT '.',         -- build services
    dockerfile_path TEXT NOT NULL DEFAULT 'Dockerfile',
    public          INTEGER NOT NULL DEFAULT 0,        -- 1: the app's routes and domains point here (at most one)
    port            INTEGER,                           -- NULL: from EXPOSE or the image
    health_path     TEXT NOT NULL DEFAULT '/',
    memory_mb       INTEGER NOT NULL DEFAULT 256,
    cpu_millis      INTEGER NOT NULL DEFAULT 250,
    command         TEXT NOT NULL DEFAULT '',          -- JSON array; '' = the image's CMD
    entrypoint      TEXT NOT NULL DEFAULT '',          -- JSON array; '' = the image's ENTRYPOINT
    healthcheck     TEXT NOT NULL DEFAULT '',          -- JSON {test, interval_s, timeout_s, retries, start_period_s}; '' = the image's
    depends_on      TEXT NOT NULL DEFAULT '',          -- JSON [{service, condition}], condition service_started | service_healthy
    environment     TEXT NOT NULL DEFAULT '',          -- JSON object of non-secret variables from the compose file
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    PRIMARY KEY (app_id, name)
);

CREATE TABLE app_volumes (
    app_id      TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,                 -- like data or pgdata
    volume_name TEXT NOT NULL UNIQUE,          -- the Docker volume; kept across renames
    size_mb     INTEGER NOT NULL,              -- storage limit, reserved from the owner's allowance
    service     TEXT NOT NULL,                 -- the service that mounts it
    mount_path  TEXT NOT NULL,                 -- where, like /data
    used_bytes  INTEGER NOT NULL DEFAULT 0,    -- measured hourly
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (app_id, name)
);

-- One row per service of a deployment. deployments.image, container_name,
-- port and has_healthcheck stay and describe the public (or only) service.
CREATE TABLE deployment_services (
    deployment_id   TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    service         TEXT NOT NULL,
    image           TEXT,
    container_name  TEXT,          -- may be an older deployment's container that was kept running
    port            INTEGER,
    has_healthcheck INTEGER NOT NULL DEFAULT 0,
    config_hash     TEXT NOT NULL DEFAULT '', -- same hash and image: the container is kept, not recreated
    PRIMARY KEY (deployment_id, service)
);
CREATE INDEX deployment_services_container ON deployment_services(container_name);

INSERT INTO app_services (app_id, name, kind, context_dir, dockerfile_path, public, port, health_path,
                          memory_mb, cpu_millis, position, created_at, updated_at)
SELECT id, 'app', 'build', context_dir, dockerfile_path, CASE WHEN type = 'web' THEN 1 ELSE 0 END, port, health_path,
       memory_mb, cpu_millis, 0, created_at, updated_at
FROM apps;

INSERT INTO app_volumes (app_id, name, volume_name, size_mb, service, mount_path, used_bytes, position, created_at)
SELECT a.id, 'data', CASE WHEN a.volume_name != '' THEN a.volume_name ELSE 'bp-' || a.slug || '-data' END,
       a.volume_mb, 'app', '/data', COALESCE((SELECT d.volume_bytes FROM app_disk d WHERE d.app_id = a.id), 0), 0, a.created_at
FROM apps a WHERE a.volume_enabled = 1;

INSERT INTO deployment_services (deployment_id, service, image, container_name, port, has_healthcheck, config_hash)
SELECT id, 'app', image, container_name, port, has_healthcheck, '' FROM deployments;

-- Env vars: service '' is shared by every service (all existing vars).
CREATE TABLE env_vars_new (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    service    TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    value_enc  BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (app_id, service, name)
);
INSERT INTO env_vars_new (app_id, service, name, value_enc, updated_at)
SELECT app_id, '', name, value_enc, updated_at FROM env_vars;
DROP TABLE env_vars;
ALTER TABLE env_vars_new RENAME TO env_vars;

-- Metrics per service; app totals are sums over the services.
ALTER TABLE metric_samples ADD COLUMN service TEXT NOT NULL DEFAULT '';
UPDATE metric_samples SET service = 'app';

CREATE TABLE metric_rollups_new (
    app_id      TEXT NOT NULL,
    service     TEXT NOT NULL DEFAULT '',
    ts          INTEGER NOT NULL,
    cpu         REAL NOT NULL,
    mem_bytes   INTEGER NOT NULL,
    rx_bytes    INTEGER NOT NULL,
    tx_bytes    INTEGER NOT NULL,
    cpu_max     REAL NOT NULL,
    mem_max     INTEGER NOT NULL,
    PRIMARY KEY (app_id, service, ts)
);
INSERT INTO metric_rollups_new (app_id, service, ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max)
SELECT app_id, 'app', ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max FROM metric_rollups;
DROP TABLE metric_rollups;
ALTER TABLE metric_rollups_new RENAME TO metric_rollups;
CREATE INDEX metric_rollups_ts ON metric_rollups(ts);

-- Backups are of one volume.
ALTER TABLE app_backups ADD COLUMN volume TEXT NOT NULL DEFAULT 'data';

-- +goose Down
ALTER TABLE app_backups DROP COLUMN volume;

CREATE TABLE metric_rollups_old (
    app_id      TEXT NOT NULL,
    ts          INTEGER NOT NULL,
    cpu         REAL NOT NULL,
    mem_bytes   INTEGER NOT NULL,
    rx_bytes    INTEGER NOT NULL,
    tx_bytes    INTEGER NOT NULL,
    cpu_max     REAL NOT NULL,
    mem_max     INTEGER NOT NULL,
    PRIMARY KEY (app_id, ts)
);
INSERT INTO metric_rollups_old (app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max)
SELECT app_id, ts, SUM(cpu), SUM(mem_bytes), SUM(rx_bytes), SUM(tx_bytes), SUM(cpu_max), SUM(mem_max)
FROM metric_rollups GROUP BY app_id, ts;
DROP TABLE metric_rollups;
ALTER TABLE metric_rollups_old RENAME TO metric_rollups;
CREATE INDEX metric_rollups_ts ON metric_rollups(ts);

ALTER TABLE metric_samples DROP COLUMN service;

CREATE TABLE env_vars_old (
    app_id     TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    value_enc  BLOB NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (app_id, name)
);
INSERT INTO env_vars_old (app_id, name, value_enc, updated_at)
SELECT app_id, name, value_enc, updated_at FROM env_vars WHERE service = '';
DROP TABLE env_vars;
ALTER TABLE env_vars_old RENAME TO env_vars;

DROP INDEX deployment_services_container;
DROP TABLE deployment_services;
DROP TABLE app_volumes;
DROP TABLE app_services;
ALTER TABLE apps DROP COLUMN service_layout;
