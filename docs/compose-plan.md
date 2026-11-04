# Compose support and projects: design

Working plan for multi-service apps. Agreed 2026-11-04. When this is built, fold
the lasting parts into SPEC.md and docs/decisions.md, then delete this file.

## Direction

Head toward Railway (projects with several services), but stay simple enough
for beginners who barely know what a deploy is and let an AI assistant do it
over MCP.

**Single container + SQLite stays the default and the recommended path.** Most
small apps are best built that way. Compose is the extra door that makes full
stack apps possible (an existing compose project, or something that really
needs Postgres/Redis next to it), not the new normal. So:

- New project leads with the single-container sources (upload, GitHub,
  Dockerfile, ready-made); compose is detected when the source has a compose
  file, and pasting one is a secondary option.
- MCP tool descriptions, server instructions, the starter prompt and hints keep
  telling the AI: one Dockerfile, data in SQLite under /data. Use compose only
  when the project already has a compose file or truly needs separate services.
  An AI must never add a compose file just to deploy a simple app.
- Catalog stays mostly single-container; multi-service entries are the exception.

- A one-service project must look and feel as simple as an app does today; the
  service grid, per-service settings and volumes only show up when there is
  more than one service or the user asks for them.
- Plain words in the dashboard ("Database", "Website", "Stored files"), not
  Docker jargon, with Docker terms only as secondary detail.
- Every MCP response and error must let an AI fix the problem without the user
  understanding Docker: a stable error code, the service and compose key at
  fault, a hint, and the next tool call to make.

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
  panel per service. The user explicitly invited a **bold, creative redesign**
  of the project page and the New project form — don't just bolt services onto
  the current tabs; rethink the layout so it fits projects and services.
  Constraints: Vercel/Linear-like dark, neutral near-black greys, orange
  accent, no blue (use the tokens in web/src/style.css), beginner-friendly words,
  and a one-service project must still feel simple. Bold must never cost
  clarity: every screen has to be easy to understand for all types of users,
  from beginners to developers (obvious next step, plain labels, no guessing).

## Data model

- `app_services`: one row per service. Name (DNS label `[a-z0-9-]`, 1-32 chars),
  kind `build` | `image`, image ref (image services), build context and
  Dockerfile (build services), public flag, port, health path, memory, CPU,
  command/entrypoint, healthcheck, depends_on, non-secret environment from the
  compose file, volume mounts, position. Unique (app_id, name).
- `app_volumes`: one row per named volume (Docker name `bp-<slug>_<vol>` for
  new ones; an existing app's volume keeps its `volume_name`), its size limit and
  which service mounts it where. The existing `/data` volume becomes one row.
- Per deployment, one row per service: image, container name, port,
  has_healthcheck, config hash (so unchanged services are not restarted).
- `env_vars` gains a `service` column (`''` = shared by every service).
  `${VAR}` in a compose file is filled from the shared vars.
- `metric_samples` / rollups gain a `service` column; app totals are sums.
- Existing apps migrate to one service. **Their container, network, volume and
  route names do not change** (`bp-<slug>-<n>`). Compose services use
  `bp-<slug>_<service>-<n>` and images `bitploy/<slug>_<service>:<n>` (built in
  phase 1: `_` instead of `-`, because `-` would let slug `shop-web` collide with
  service `web` of slug `shop`; `_` is in neither). All services join the app network `bp-<slug>` with
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

## Phase 1 (built): the data model and the deploy code

What was built differs from the plan above in these points:

- **Layout column.** `apps.service_layout` is `single` (every existing and new
  app) or `multi`. For `single`, the apps and deployments rows stay the source
  of truth and are mirrored into `app_services` (service `app`), `app_volumes`
  (volume `data` at `/data`) and `deployment_services` in the same transaction
  (`syncSingleLayout`, `SyncSingleDeploymentService`). For `multi`, the new
  tables are the source of truth and the apps row holds the sums
  (`SetMultiAppTotals`: memory, CPU, volume size), so quotas and usage need no
  change. Phase 2 writes `multi` rows from a compose file.
- **Only rollbacks deployed a `multi` app** in phase 1 (they copy the target's
  `deployment_services`). Phase 2 builds them from the compose file;
  `services_not_buildable` is gone.
- **App-wide settings** (type, port, health_path, build settings, volume,
  volume_mb, memory_mb, cpu_millis) are refused for a `multi` app with
  `set_per_service`.
- **Defaults when `service`/`volume` is left out:** logs follow the public
  service (else the first), metrics are the sum, restart restarts every service,
  files and backups use the first volume. (Phase 2: scheduled backups cover
  every volume and the SQLite viewer takes a volume.)
- Env var precedence: compose `environment` < shared vars (service `''`) <
  vars of the service < `PORT`/`BITPLOY_*`. Service vars are sealed with the
  service in the associated data.

## Phase 2 (built): compose files

What shipped, and where it differs from the plan above:

- **Parsing** (`internal/deploy/compose.go`): compose-go v2 in two passes.
  `${VAR}` is filled from the app's shared variables; a variable whose value is
  only known at start (a secret) is carried as a marker and expanded when the
  container starts, so secrets never land in the stored spec. At most 6
  services. Every issue is `{code, service, key, message, hint}`.
- **When a deployment runs the compose file** (`chooseCompose`):
  - build mode `compose`, or an app that is already `multi`: always; a source
    without the file fails with `compose_file_missing`.
  - build mode `auto` and nothing ever went live: a file with two or more
    services, or one service that cannot be held by the app's own settings
    (an image, build args or target, command, entrypoint, user, working_dir,
    healthcheck, or a volume elsewhere than /data). One plain build service is
    built like a Dockerfile app: its context, Dockerfile, port, health path,
    environment (stored as app variables if not set yet) and /data volume are
    copied into the app's settings, build mode stays `auto`. Problems in a
    one-service file leave the file out; with several services they are errors
    whose hint names `update_app(build_mode="dockerfile")`.
  - anything else (build mode `dockerfile`, which `create_app` makes by
    default, or an app that already went live): never. The Dockerfile checks
    warn `compose_ignored` and the hint names `update_app(build_mode="compose")`.
- **Single to multi** is explicit (`update_app build_mode=compose`) and one
  way (`set_per_service` refuses going back). The app's /data volume keeps its
  Docker volume and files: it becomes the file's volume mounted at /data (the
  public service's first), else it is mounted at /data of the public service
  (`data_volume_kept`, info), else it is kept unmounted (`data_volume_unmounted`,
  warning). The old single container stops before the service that inherits
  the volume starts. Build mode `auto` is stored as `compose` at the swap.
- **Builds**: build services one at a time on the isolated builder, each with
  its own build timeout and the 2 GB image limit, errors prefixed
  `Service <name>: ` with `detail.service`. Image services are pulled through a
  one-line `FROM <ref>` build on the same builder (so the pull is isolated and
  `base_image_forbidden` rules apply); a failed pull is `image_pull_failed`. An
  image service whose reference did not change reuses the live image. The
  deployment log has one section per service: `[i/n] Service web: building
  web/Dockerfile`, `[i/n] Service db: pulling postgres:16-alpine`,
  `... is already on the server`.
- **Start**: `depends_on` order; `condition: service_healthy` waits for the
  dependency's healthcheck. A service whose config hash (image fingerprint:
  layers and config without labels, plus every runtime setting) is unchanged
  keeps its container. The public service gets the health check
  (`x-bitploy.health_path`, its `healthcheck:`, or GET /). On failure the
  previous containers are restored; on success the route swaps to the new
  public container. Rollbacks rebuild the run plan from
  `deployments.services_spec` (the stored spec), so the compose file is not
  re-read.
- **Quota** is checked before anything is built: the summed memory, CPU and
  volume sizes against the account's allowance, the app's own use left out.
- **Variables**: `set_env` / `PUT /apps/{app}/env` take an optional `service`;
  shared variables (service empty) go to every service and fill `${VAR}`.
- **Backups**: the schedule backs up every attached volume (retention is per
  volume); `GET /apps/{app}/backups` lists `volumes: [{name, service,
  mount_path, size_mb, upload_max_bytes}]`.
- **SQLite viewer**: every database endpoint and `query_db` take `volume`
  (default: the first volume); paths are relative to that volume.

### API

`POST /api/v1/compose/validate` (read scope). Body
`{compose: string, app?: string}`; `app` fills `${VAR}` from that app's
variables and leaves its own use out of the quota. A file with problems answers
**200** with `ok: false`; only a body that is not JSON, a compose text over
256 KB, or an unknown app is an error.

```json
{
  "ok": true,
  "services": [{"name": "web", "kind": "build", "image": "", "build": {"context": "web", "dockerfile": "web/Dockerfile", "target": ""},
                "public": true, "port": 3000, "health_path": "/", "memory_mb": 512, "cpu_millis": 250,
                "depends_on": ["db"], "volumes": []}],
  "volumes": [{"name": "pgdata", "service": "db", "mount_path": "/var/lib/postgresql/data", "size_mb": 5120}],
  "memory_mb_total": 768, "cpu_millis_total": 500, "volume_mb_total": 5120,
  "quota": {"memory_mb_left": 1536, "cpu_millis_left": 1500, "storage_mb_left": 20480, "fits": true,
            "message": "This needs 768 MB of memory of the 1.5 GB you have left."},
  "warnings": [{"code": "compose_key_ignored", "service": "web", "key": "restart", "message": "...", "hint": ""}],
  "errors": []
}
```

`POST /apps` and `PATCH /apps/{app}` take `build_mode: "compose"` and
`compose_path`. `PUT /apps/{app}/env` takes `service`; `GET /apps/{app}/env`
returns `service` per variable. Database endpoints take `volume` (query or body).

### MCP

- `validate_compose {compose, app?}` (read-only): the preview above; the
  summary names the first error as `<code> in service <s> at <key>: <message>`.
- `create_app` and `update_app`: `build_mode` (`dockerfile` | `compose`) and
  `compose_path`.
- `set_env`: optional `service`. `query_db`: optional `volume`.
- The server instructions (rule 8) and the runtime contract ("Compose apps",
  "Compose checks") keep one Dockerfile with SQLite in /data the default and
  tell the AI never to write a compose file just to deploy.

### Known gaps

- Volumes kept detached (no longer in the file, rows with no service) are not
  counted in the compose quota check, only in storage measurement.
- Pulling through a `FROM` build exports and loads the image; reusing an
  unchanged image service avoids it on later deploys.
- A failed compose deployment of a single app can leave `deployment_services`
  rows for that deployment (harmless: the deployment is failed).

## Phase 4: lean into Railway (agreed 2026-11-04)

The user: "take Railway as an example of a good easy deploy application", "be
bold", "go nuts". Single Dockerfile + SQLite stays the recommended default, but
the product shape moves to Railway's: a project is a small private network of
services you can add to with one click.

- **Add to a project** (Railway's "+ New"): a button on the project page and an
  MCP tool. Choices: a database or other ready service from a template
  (PostgreSQL, MySQL/MariaDB, Redis/Valkey, Meilisearch, more later), any
  published Docker image (port, public or private), or a ready-made app.
- Added services are stored by bitploy (not taken from a compose file) and are
  kept across deploys of the project's source; unchanged services keep running.
  Adding a service to a one-service project converts it to several services
  explicitly (the existing single -> multi path; /data kept).
- **Connection variables, wired for you.** A database template generates its
  credentials (secret, encrypted) and publishes connection variables such as
  `DATABASE_URL`, `REDIS_URL`, `MEILI_URL`/`MEILI_MASTER_KEY`. Other services
  get them through references, Railway style: `${{postgres.DATABASE_URL}}`,
  resolved at start. Adding a database offers "Connect to <service>" (on by
  default), which adds the reference variable to that service and redeploys it.
- **Per-service settings** for added services: image version, memory/CPU,
  variables, volume size, public or private, remove (type the name; says the
  data goes too).
- Databases are only reachable inside their project (the firewall blocks
  everything else; there are no TCP ports to the internet). The UI says so in
  plain words, and the ready-made page's databases are added to a project
  (pick one, or start a new project with it).
- MCP: `add_service`, `update_service`, `remove_service`,
  `list_service_templates`. Instructions keep saying: SQLite in /data first;
  add a database only when the user asks or the app needs it.

## Phase 4b: projects are spaces that hold apps (supersedes parts of Phase 4)

User, 2026-11-04: "A project should be some kind of space the user can add one
main app to, but also deploy more apps into, like Redis and a separate
database", "take any GitHub repo and deploy it", "build with a Dockerfile or
docker compose". So:

- **Project** (new table `projects`, id `prj_…`, name, owner) is a space. Every
  app belongs to exactly one project; migration backfills one project per
  existing app, named after it. The phase-3 dashboard that called an app a
  "project" is re-pivoted: the project page shows all its apps.
- **App** stays the deploy unit: one source (GitHub repo, upload, pasted
  Dockerfile, pasted or detected compose file, ready-made image), its own
  deployments, logs, domains, variables, volumes. A compose app still has
  several services inside it (phases 1-2 unchanged).
- **Databases and other ready services are apps** created from a service
  template (image source, private, no address), not services bolted onto
  another app. Same for "any Docker image".
- **Private network per project.** Each project gets a Docker network
  `bp-p-<id>` from the app pool; every container of every app in the project
  joins it besides its own `bp-<slug>` network, with a DNS alias = the app's
  short name in the project (e.g. `postgres`, `web`; compose services also get
  `<app>-<service>`). Traefik never joins project networks. The firewall's
  same-/24 rule must allow this and nothing across projects — verify in
  deploy/firewall.sh and probe-test.sh.
- **Variable references** `${{<name>.VAR}}` resolve against apps in the same
  project. "Connect to" when adding a database writes the reference into the
  chosen apps and redeploys them.
- Quotas: `quota_apps` keeps counting apps (a database counts as one); raise
  the default only if needed and document it.

API contract (backend and UI agents build against it in parallel):
- `GET /api/v1/projects` → `[{id, name, created_at, apps: [App]}]` (App as today,
  plus `project: {id, name}`, `alias` (short name in the project),
  `template` (service template id or null), `internal_host` e.g. `postgres:5432`).
- `GET /api/v1/projects/{id}`, `POST /api/v1/projects {name}`,
  `PATCH /api/v1/projects/{id} {name}`, `DELETE /api/v1/projects/{id}?confirm=<name>`
  (refused while it still has apps, unless `with_apps=true`).
- `POST /api/v1/apps` gains `project` (id); without it a new project named
  after the app is created. `PATCH /apps/{app}` can move an app to another
  project (`project`), and set `alias`.
- `GET /api/v1/service-templates` → `[{id, name, category, description, image, version, port, memory_mb, volume_mb, connection_vars: [names], main_var}]`.
- `POST /api/v1/projects/{id}/services {template, name?, connect_to?: [app slugs]}`
  → creates the app from the template and deploys it; returns the App.
- MCP: `list_projects`, `create_project`, `list_service_templates`,
  `add_service(project, template, name?, connect_to?)`; `create_app` takes
  `project`; `get_app` shows project and alias; references work in `set_env`.
