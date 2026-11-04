# Compose support and projects: design

Working plan for multi-service apps. Agreed 2026-11-04. When this is built, fold
the lasting parts into SPEC.md and docs/decisions.md, then delete this file.

## Decisions

- **Compose is an input format, not a runtime.** bitploy never runs
  `docker compose up`. It parses the file (github.com/compose-spec/compose-go/v2),
  rejects anything unsafe and creates every container itself through
  `dockerx`, with the same hardening, limits, labels (managed + instance id),
  reconciler and firewall assumptions as today (SPEC.md "Security & limits").
- **Project → Services.** The database table and API object stay `app` (MCP
  tools such as `create_app` keep their names, so existing agents keep working).
  The dashboard calls it a **Project**. An app has one or more **services**; a
  Dockerfile, static or catalog app is an app with exactly one service.
- **One source per project in v1** (a repo, upload, pasted Dockerfile or pasted
  compose file). Services with separate sources inside one project come later.
- **A deployment covers the whole project**: one source, one build log (a section
  per service), one rollback target.
- **Databases are allowed** through compose (Postgres, Redis, ...). Their data
  lives in named volumes that count against the storage quota and are backed up.
- **Quota**: `quota_apps` counts projects. At most 6 services per project.
  The project's memory and CPU reservation is the sum of its services.
- **Dashboard**: a card grid of services (not a draggable canvas), with a side
  panel per service. Keep the current visual language (see web/src/style.css).

## Data model

- `app_services`: one row per service. Name (DNS label `[a-z0-9-]`, 1-32 chars),
  kind `build` | `image`, image ref (image services), build context and
  Dockerfile (build services), public flag, port, health path, memory, CPU,
  command/entrypoint, healthcheck, depends_on, non-secret environment from the
  compose file, volume mounts, position. Unique (app_id, name).
- `app_volumes`: one row per named volume (Docker name `bp-<slug>-<vol>` for
  new ones; an existing app's volume keeps its `volume_name`), its size limit and
  which service mounts it where. The existing `/data` volume becomes one row.
- Per deployment, one row per service: image, container name, port,
  has_healthcheck, config hash (so unchanged services are not restarted).
- `env_vars` gains a `service` column (`''` = shared by every service).
  `${VAR}` in a compose file is filled from the shared vars.
- `metric_samples` / rollups gain a `service` column; app totals are sums.
- Existing apps migrate to one service. **Their container, network, volume and
  route names do not change** (`bp-<slug>-<n>`). Compose services use
  `bp-<slug>-<service>-<n>`. All services join the app network `bp-<slug>` with
  the service name as a DNS alias, so `web` reaches `postgres:5432`. The
  firewall already allows traffic inside one app /24; app-to-app stays blocked.

## Compose rules

Allowed: `image`, `build` (context, dockerfile, args, target), `environment`,
`env_file` (from the source), `command`, `entrypoint`, `working_dir`, `user`,
`depends_on` (incl. `condition: service_healthy` / `service_started`),
`healthcheck`, named volumes, `expose`, `ports` (only the container port is
used; nothing is published), `deploy.resources.limits` (memory, cpus),
`restart` (ignored; bitploy's policy applies), `x-bitploy` extensions.

Rejected with error code `compose_forbidden` (message names the service and
key, hint says what to do instead): `privileged`, `cap_add`, `devices`,
`network_mode`, `pid`, `ipc`, `userns_mode`, `cgroup_parent`, `sysctls`,
`security_opt`, `extra_hosts`, custom `networks`, host bind mounts (any
`volumes:` entry that is a path), `docker.sock`, `secrets`/`configs` from
files outside the source, `profiles` (ignored with a warning), more than 6
services. Unknown keys: warning, not error.

**Public service**: the one service with `ports:` (or `x-bitploy: {public: true}`).
None → if exactly one service has `expose`/EXPOSE treat it as public, else the
project is a worker set. Several candidates and none marked → error
`compose_public_ambiguous` asking to add `x-bitploy: {public: true}`.
In v1 exactly one service is public and the app's routes/domains point to it.

**Memory**: `deploy.resources.limits.memory` if set, else 256 MB per service
(512 MB for the public build service); CPU likewise (`cpus`, else 0.25).

## Deploy

1. Fetch source → find the compose file (`compose_path`, default: first of
   `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`).
2. Validate (rules above); check quota for the summed memory/CPU and volumes.
3. Build `build:` services one at a time on the isolated builder (same
   Dockerfile checks as today, per service); pull `image:` services.
4. Start in `depends_on` order. Only services whose config hash or image
   changed are recreated; others keep running (a Postgres is not bounced on every
   push). Health-check the public service (and wait for `service_healthy`
   dependencies). On failure, restore the previous set.
5. Swap the route to the new public container.

## API / MCP

- `GET /apps/{app}` gains `services: [...]`, `volumes: [...]`, `build_mode` may be `compose`.
- `POST /api/v1/compose/validate` `{compose: string}` → preview:
  `{services: [{name, kind, image, build, public, port, memory_mb, cpu_millis, depends_on, volumes}], volumes: [{name, size_mb}], memory_mb_total, cpu_millis_total, warnings: [...], errors: [{code, service, key, message, hint}]}`.
- Logs, metrics and restart take an optional `service`. Volume endpoints
  (storage, backups, files) take a volume name; default is the app's first volume.
- MCP: `deploy`/`create_app` accept compose; `get_app` returns services;
  `get_logs`, `get_metrics`, `restart_app` take `service`. Copy for AI names the
  failing service.
