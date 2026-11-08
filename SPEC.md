# Bitploy — Build Prompt

Oct 29, 2026 · Arno

Defaults picked where the brief was open:

| Decision | Default in this prompt | Why |
| --- | --- | --- |
| Signup | Invite-only now; `BITPLOY_SIGNUP=open` later | Colleagues first; may go public, so public is a config switch, not a rewrite |
| Database | One SQLite file (WAL mode) | One server, nothing to operate |
| Reverse proxy | Traefik v3; bitploy writes the routes as files | Zero-downtime swaps, routing owned by bitploy |
| Control plane | `bitploy.nl` serves dashboard, `/api/v1` and `/mcp` | Apps live on `*.bitploy.nl` |
| App shape | One container per app, optional `/data` volume, in a project | A database server is an app of its own from a service template, next to the app in its project; compose files only when a project already has one |
| Default limits | 512 MB RAM and 0.5 CPU per app, 10 apps per user | Many colleagues on one Hetzner box |
| MCP auth | Bearer token in a header; OAuth later | Works in Claude Code, Cursor and VS Code today |

---

## Role & mission

You are a senior Go and Vue engineer building **bitploy**: a small, self-hosted deploy platform in the spirit of Dokploy, run on a single Hetzner server. Its main users are AI coding agents, driven through an MCP server and a REST API. Humans log in to a clean dashboard to create tokens, read logs and watch usage.

The core promise: **anyone can deploy anything**. A colleague who has never heard of Docker drags a folder with one `index.html` into their AI chat, says "put this online", and gets `https://quiet-otter-3f9a.bitploy.nl` back in under two minutes. An expert connects GitHub and points the agent at a private repo with a Go API and gets the same result.

The rule that makes this work: **every app is a Dockerfile**. Bitploy never guesses how to build or run code. The MCP server teaches the agent this rule, hands it templates, validates its Dockerfile, and refuses any deploy without one. That single rule keeps bitploy small and makes it able to run "any weird app".

Principles, in priority order:

1. Noob-proof errors: every failure says what went wrong, why, and the exact fix, so an agent can recover without a human.
2. One service layer: the API, MCP server and dashboard call the same Go functions. No behaviour exists in only one of them.
3. Boring and small: standard library first, few dependencies, one binary, one database file.
4. Safe by default: user code is untrusted, limited and isolated. Colleagues come first, on one Hetzner server, but the public may follow, so nothing may assume every user is trusted.

## Tech stack

Bitploy ships as one Go binary with the Vue dashboard embedded, next to Traefik and the Docker daemon. Use these choices unless one is clearly broken; ask before adding anything outside this list.

| Layer | Use | Notes |
| --- | --- | --- |
| Language | Latest stable Go (1.26 at time of writing) | `net/http` with method+path patterns, `log/slog` JSON logs |
| Database | SQLite via `modernc.org/sqlite` (pure Go, no CGO) | WAL mode, one file in the data dir; embedded migrations (goose); typed queries with sqlc |
| MCP | Official SDK `github.com/modelcontextprotocol/go-sdk` v1.8+ | Streamable HTTP transport with `Stateless: true` so it speaks protocol `2026-07-28` and still falls back to `2025-11-25` clients |
| Docker | `github.com/moby/moby/client` | The old `github.com/docker/docker` module is deprecated since Engine v29; use it for containers, networks, stats, logs |
| Builds | `docker buildx build --progress=plain` as a subprocess | BuildKit, streamed line by line into the build log |
| Dockerfile checks | `github.com/moby/buildkit/frontend/dockerfile/parser` | Parse, never regex |
| Git | `go-git` v5 | Shallow clone (depth 1) of one branch |
| Reverse proxy | Traefik v3, file provider | Bitploy writes one YAML file per app; Traefik watches the folder |
| Passwords / secrets | argon2id; AES-256-GCM | Key from `BITPLOY_SECRET_KEY` |
| API spec | OpenAPI 3.1 generated from Go handler types (e.g. huma v2) | Served at `/api/v1/openapi.json`; never hand-edited |
| Frontend | Vue 3, Vite, TypeScript, shadcn-vue on Reka UI, Tailwind v4 | vue-router, TanStack Vue Query, shadcn-vue charts, lucide icons, vee-validate + zod |
| Packaging | `go:embed` of `web/dist` | SPA fallback to `index.html` for unknown non-API paths |

Keep the frontend boring: generated shadcn-vue components in `web/src/components/ui`, no extra UI kit, no global state library beyond Vue Query, light and dark mode from the shadcn-vue theme tokens.

## Architecture

Three doors (dashboard, REST API, MCP) lead into one service layer; only the service layer touches the database, Docker and Traefik. The doors only parse input, check auth and format output.

App traffic goes from Traefik straight to the containers, never through bitploy, so apps keep serving while bitploy restarts or upgrades.

Inside the `bitploy` binary:

- **HTTP server** on `:8080` behind Traefik, serving the dashboard at `/`, the API at `/api/v1`, MCP at `/mcp`, and one-time upload URLs at `/u/{id}`.
- **Service layer** (`internal/app`): users, tokens, apps, deployments, env vars, domains, metrics queries. Every function takes a typed actor (`user`, `channel = dashboard | api | mcp`) and writes an audit row.
- **Deploy worker**: a job queue stored in SQLite, run by goroutines. At most 2 builds run at once server-wide and 1 per app; a newer deploy for the same app cancels a queued older one.
- **Route writer**: writes `traefik/dynamic/<slug>.yml` atomically (temp file + rename). Traefik picks it up within a second.
- **Metrics collector**: samples Docker stats every 15 s and stores them.
- **Reconciler**: on startup and every 60 s, compares desired state in the database with real containers, networks and route files, and repairs drift. A crash or reboot must never leave an app half-deployed.

On disk (`BITPLOY_DATA_DIR`, default `/var/lib/bitploy`):

```
bitploy.db                 SQLite, WAL
uploads/<upload_id>.zip    deleted 24 h after use
builds/<deployment_id>/    extracted build context, deleted after the build
logs/<deployment_id>.log   build log, kept for the last 20 deployments per app
traefik/dynamic/*.yml      one route file per app, owned by bitploy
```

Repo layout:

```
cmd/bitploy/            main: serve, migrate, admin create-user, admin invite
internal/app/           service layer (the only place with business rules)
internal/auth/          passwords, sessions, API tokens, CSRF, middleware
internal/store/         migrations, sqlc queries, crypto helpers for secrets
internal/deploy/        sources (zip, files, git), dockerfile checks, build, run, health, swap
internal/dockerx/       thin wrapper over the moby client
internal/proxy/         Traefik route files
internal/domains/       DNS checks for custom domains
internal/metrics/       collector and rollups
internal/api/           REST handlers + OpenAPI
internal/mcp/           tools, resources, prompts, instructions.md
internal/templates/     embedded Dockerfile templates
web/                    Vue app (shadcn-vue), built into web/dist and embedded
deploy/                 docker-compose.yml, traefik config, daemon.json, install.sh
Makefile                dev, build, test, lint, release
```

## Auth

A user has two kinds of credential: a session cookie for the dashboard and bearer API tokens for agents and scripts. Both resolve to the same user and the same permission checks in the service layer.

**Users and signup**

- Invite-only. The first admin is created with `bitploy admin create-user --email you@x.nl --admin`. Admins create single-use invite links (7-day expiry) in the dashboard; the invitee picks a name and password.
- Roles: `user` and `admin`. Admins manage users, invites and quotas, can read every app, and can stop any app.
- Passwords: argon2id at or above the OWASP minimum parameters, at least 10 characters. Failed logins are limited to 5 per 15 minutes per email and IP.

**Sessions (dashboard)**

- 32 random bytes in cookie `__Host-bitploy_session`: `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, no `Domain`. The `__Host-` prefix stops apps on subdomains from overwriting it.
- Store only the SHA-256 of the value. 14-day sliding expiry, 30-day absolute limit. Logout deletes the row; a password change deletes all other sessions; Settings has "sign out everywhere".
- CSRF matters more than usual here: every app on `*.bitploy.nl` is **same-site** with `bitploy.nl`, so `SameSite` gives no protection. Every cookie-authenticated request that is not GET must pass two checks: `Origin` equals `BITPLOY_PUBLIC_URL` exactly, and `X-CSRF-Token` matches the per-session token from `GET /api/v1/session`. Bearer requests skip CSRF.

**API tokens (API + MCP)**

| Property | Rule |
| --- | --- |
| Format | `bp_` + 32 random bytes in base62 |
| Storage | SHA-256 hash, plus the first 8 characters as a display prefix |
| Shown | Once, at creation, with a copy button and ready-made MCP config snippets |
| Use | `Authorization: Bearer bp_…` on `/api/v1` and `/mcp` |
| Scopes | `read` (list, get, logs, metrics) or `deploy` (everything except tokens and account settings) |
| Expiry | Optional: never, 30 or 90 days |
| Tracking | name, created, last used time and IP; revoke takes effect immediately |
| Limits | 20 tokens per user; a token can never create or list tokens |

Redact anything matching `bp_[A-Za-z0-9]{20,}` from logs, build logs and error messages. On `/mcp`, plug the token check into the SDK's bearer-token middleware (`auth.RequireBearerToken`) with bitploy's own verifier.

**GitHub (private repos and push to deploy)**

- One GitHub App per instance, created by an admin from the dashboard with GitHub's manifest flow (read-only contents and metadata, push events). Its private key, client secret and webhook secret are stored AES-256-GCM encrypted and never returned.
- Each user installs the app on their own account or organizations from Settings, GitHub. Bitploy links an installation only after exchanging the OAuth code and finding the installation in `GET /user/installations`; an `installation_id` in a URL is never trusted alone. Unless the installation is on the user's own GitHub account, bitploy uses only the repositories GitHub says that user can open.
- Cloning a `github.com` repository uses a short-lived, read-only token for that one repository, minted from the app owner's own installation. It reaches git only as HTTP basic auth (username `x-access-token`) and must never reach `.git/config`, a log line, the build context or an image. With no covering installation the clone is anonymous.
- A private repo bitploy cannot read fails with `github_access_missing`, with a hint pointing to Settings, GitHub: "Connect GitHub and give bitploy access to this repo".
- `POST /api/v1/github/webhook` receives push events: signature checked first (HMAC-SHA256, constant time), 5 MB cap, deduped by delivery id, answered with `202`. A push to the linked branch of an app whose owner linked that installation queues a deployment on channel `github` (commit sha and message recorded); quick pushes collapse into one build of the newest commit.
- An app linked to a repository refuses uploads (`git_source_linked`) until it is disconnected.

## Deploy model

An app is a name, a subdomain and a source. A deployment turns one snapshot of that source into a running container, and replaces the live container only after the new one passes a health check. A failed deploy never takes a working app down.

**App settings**: `slug` (the subdomain), `name`, source, `dockerfile_path` (default `Dockerfile`), `context_dir` (default `.`), `port`, env vars, `volume` (off, or a named volume at `/data`), memory and CPU within the user's quota, `health_path` (default `/`).

App `type` is `web` (default) or `worker`. A worker is for bots, queue consumers and other weird apps without an HTTP port: it gets no route and no port check, and counts as healthy when it is still running after 30 s.

**Sources**

| Source | How it arrives | Limits | Typical user |
| --- | --- | --- | --- |
| Inline files | MCP `deploy_files` or API JSON: a list of `{path, content, encoding}` | 2 MB total, 200 files | A noob with three HTML files |
| Zip upload | One-time upload URL, `curl --data-binary @app.zip` | 95 MB compressed (under Cloudflare's 100 MB request cap), 500 MB unpacked, 20,000 files | A folder on a laptop |
| Public git | HTTPS URL, branch, optional subdirectory | Shallow clone, 500 MB | Open-source repos |
| GitHub | Same, plus private repos through the user's GitHub App installation; every push to the branch deploys | Same | Company repos, anyone who already lives in git |

Zip handling: if everything sits in one top-level folder, strip it (people zip the folder, not its contents). Drop `__MACOSX/` and `.DS_Store`. Reject absolute paths, `..`, symlinks and device files. Count bytes while extracting and abort past the unpacked limit.

**The Dockerfile rule**: a server is built from a Dockerfile. In the default build mode (`dockerfile`) a deploy without a Dockerfile at `dockerfile_path` fails at validation with `dockerfile_missing`, unless the source is a static site (plain website files, or a front end that builds to files), which bitploy builds with its own nginx templates in every build mode (see Build modes). There are no buildpacks; the only builds without the user's Dockerfile are the fixed templates of the build modes below. For the error, bitploy detects the stack to make it helpful: `index.html` only → `static`, `package.json` → `node`, `requirements.txt` or `pyproject.toml` → `python`, `go.mod` → `go`, `composer.json` or `index.php` → `php`. The error names the matching template and includes its full text.

**Build modes (opt-in per app)**: the rule above is the default (`build_mode: dockerfile`) and is what MCP and REST get. An app can opt in to `auto`, which builds a Dockerfile when there is one, otherwise serves an `index.html` at the top of the project through the static template (nginx), otherwise detects a Node project from its `package.json` (and the Next.js config): SvelteKit with `@sveltejs/adapter-static`, Astro without a server adapter, Next.js with `output: 'export'`, Create React App and a Vite site (build script, `vite`, `index.html` at the top, no start script) are built and their output folder is served with nginx (`node-spa`: unknown paths load `index.html`; `node-static`: real pages, 404 otherwise); SvelteKit with another adapter, Next.js, Remix, React Router, Nuxt, NestJS, Express, Fastify, Hono, Koa, hapi, Astro with `@astrojs/node` and anything else with a start script run as a Node app (`node` template, after the build script if there is one; `nextjs` template with `output: 'standalone'`). A Vite project with a start script is a guess: it runs as a Node app and says so. It fails with `nothing_to_build` when it finds none of these. The other modes force one answer: `node` (a Node.js app), `static_build` (build, then serve the output folder; a static site) and `static` (plain website files). `output_dir` is the folder a static build serves (empty: the default of the framework: `dist`, `build` or `out`). In `auto` and `static`, a project without `index.html` but with one other page at the top (`Planning tool.html`) serves that page at `/`; with several pages the app's `home_page` setting says which. If a project marker (`package.json`, `requirements.txt`, `go.mod`, ...) sits next to the `index.html`, `auto` does not treat it as a website, because that is a front-end project that needs a build. The dashboard creates apps in `auto` and has a Type select for the others. The choice is made in one function (`deploy.PlanBuild`) and the injected files are not part of the stored source, so a rollback repeats it.

**Compose apps (the extra door)**: the recommended shape stays one Dockerfile with SQLite in `/data`. A project that already has a compose file it needs (a web app next to Postgres or Redis) can be deployed as a compose app: `build_mode: compose` (optional `compose_path`, else the first of `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`). bitploy never runs `docker compose`: it parses the file (compose-go v2), builds `build:` services one at a time on the isolated builder, pulls `image:` services through the same builder, and runs each service as its own locked-down container `bp-<slug>_<service>-<n>` on the app's network, reachable by its service name. At most 6 services; one is public (`x-bitploy: {public: true}`, else the one with `ports`, a built service before images) and gets the route and the health check; named volumes become `bp-<slug>_<volume>`, each with its own storage limit (`x-bitploy: {size_mb}`, default 5 GB). Memory and CPU per service come from `deploy.resources.limits` (default 256 MB, 512 MB for the public build service, 0.25 CPU; at most 64 GB and 64 CPUs per service, like one app) and their sum is checked against the quota before anything is built. Services start in `depends_on` order (`condition: service_healthy` waits for the healthcheck); a service whose image and settings did not change keeps running; a failure restores the previous containers. `${VAR}` is filled from the app's variables (never from `.env` files or the server). A variable with a value is refused in the `image:` of a pulled service (`compose_image_variable`): the image is shown in the deploy log and errors, and variable values never are; `${TAG:-16}` with `TAG` unset still takes the default, and a variable with neither a value nor a default is refused the same way (the image would lose its name or tag, and setting the variable would not help). Variables set without `service` go to every service, `service` scopes one. Settings of the whole app (port, memory, CPU, volume, health path) are refused for a compose app (`set_per_service`): they come from the file.

Detection is conservative. `create_app` (MCP and REST) makes build mode `dockerfile`, which never runs a compose file: the deploy warns `compose_ignored` and names `update_app(build_mode="compose")`. Only an app in `auto` (the dashboard's default; MCP `create_app` takes it for a repository it cannot change) picks a compose file by itself, and only on its first deploy: several services, or one the app's settings cannot hold, make it a compose app; one plain build service is built like a Dockerfile app with that service's context, Dockerfile, port and `/data`. After that the shape never changes by itself. Turning an app with one container into a compose app is explicit, one way, and keeps `/data`: its volume becomes the file's volume at `/data` (or stays mounted at `/data` of the public service). Before a deploy, `POST /api/v1/compose/validate` (`validate_compose`) previews what would run and every problem with a stable code, the service, the compose key and a hint.

**Projects**: every app is in exactly one project (`prj_…`, a name, an owner). A project is a space for apps that belong together, like a shop and its database. `create_app` without `project` makes a project named after the app; that project goes away with its last app. A project made with `create_project`, renamed, or picked on `create_app` stays when it is empty. Each app has a short name in its project (`alias`, a DNS label of 1 to 40 characters, default the slug, unique in the project). Once a project has two apps, all their containers join a private network `bp-p-<id>` (a /24 from the app pool, label `nl.bitploy.project`) besides their own `bp-<slug>`, reachable at their alias (an app with several services: `<alias>-<service>` for each, and the bare alias on the public service). Traefik never joins a project network, and the firewall drops traffic between projects. An app stays in the project it was created in (`update_app` with `project` is refused with `project_move_unsupported`: deploy it again as a new app in the other project). `update_app` renames it (`alias`); a rename rewrites the references in the project's other apps and restarts them. At most 100 projects per user. `quota_apps` counts apps, so a database counts as one.

**Variable references**: a variable value may contain `${{<alias>.<VAR>}}`, which bitploy fills in when a container starts with the variable `VAR` of the app `<alias>` in the same project (a variable set for every service, not one scoped with `service`). For an app made from a service template, `VAR` may also be one of the template's connection variables (`DATABASE_URL`, `REDIS_URL`, ...), built from its generated passwords and its address on the project network. References may chain up to 3 deep; `set_env` refuses an unknown app or variable (`variable_reference_unknown`), the app itself (`variable_reference_self`) and loops (`variable_reference_loop`). The stored value stays the reference, so a changed password reaches every app at its next start. A deployment whose variables refer to a service-template app waits up to 5 minutes for that app to run.

**Build-time variables**: every app variable reaches the running app and the image build; nobody picks per variable. bitploy's own builds (a front end or Node.js app without a Dockerfile) mount every variable into the build script as a BuildKit secret in an environment variable of the same name (`RUN --mount=type=secret,id=NAME,env=NAME`, `PATH`, `HOME` and `PWD` excepted), so Vite (`VITE_*`), Next.js (`NEXT_PUBLIC_*`) and Astro (`PUBLIC_*`) read them while they build and none is in the image or its history; front-end tools put only their own prefixed variables into the files, and those are readable by every visitor of the site. A user's Dockerfile gets the variables it declares with `ARG NAME` as `--build-arg` (never the names BuildKit gives a meaning: `TARGET*`, `BUILD*` platform arguments, `BUILDKIT_*`, `SOURCE_DATE_EPOCH`) and the ones its `RUN` steps mount (`--mount=type=secret,id=NAME`) as BuildKit secrets, never on a command line. Above 1 MB of values together the largest stay out of the build (the log names them). Each deployment records the variables its build read (`current_deployment.build_reads`, `["*"]` for every one): a change to one of them, or any change for a site bitploy serves, applies on the next deploy; a change to any other after a restart. `set_env` says which. Hidden values and values that may be secrets are masked in the build log, and every step that mounts a secret also gets the build argument `BITPLOY_BUILD_SECRETS`, a keyed fingerprint of the app and the secret values, so the shared builder's cache of that step is never reused by another app or after a value changed.

**Service templates** (`internal/servicetpl`): databases and other ready services, each with a pinned image version, its own sizes, a non-root user and a Docker HEALTHCHECK. `add_service(project, template, name?, connect_to?)` creates a private worker app from one (alias = `name`, default the template id), generates its passwords (32 alphanumeric characters, stored encrypted like every variable), keeps its data in its own volume at `/data`, builds the template's Dockerfile and deploys it. Each app in `connect_to` (slugs in the same project) gets `<main_var>=${{<alias>.<main_var>}}`, for example `DATABASE_URL=${{db.DATABASE_URL}}`, and restarts once the service is live. If a step fails, the service app is removed and the variables it set are taken out again.

| Template | Image | Port | Memory, storage | User | Connection variables (main first) |
| --- | --- | --- | --- | --- | --- |
| `postgres` | `postgres:18.6` | 5432 | 256 MB, 2 GB (shm 128 MB) | 999:999 | `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` |
| `pgvector` | `pgvector/pgvector:0.8.7-pg18` | 5432 | 256 MB, 2 GB (shm 128 MB) | 999:999 | as `postgres` |
| `mariadb` | `mariadb:11.8.9` | 3306 | 256 MB, 2 GB | 999:999 | `DATABASE_URL`, `MYSQL_HOST`, `MYSQL_PORT`, `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_DATABASE` |
| `mysql` | `mysql:8.4.13` | 3306 | 512 MB, 2 GB | 999:999 | as `mariadb` |
| `valkey` | `valkey/valkey:9.1.3` | 6379 | 128 MB, 512 MB | 999:999 | `REDIS_URL`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `VALKEY_URL` |
| `redis` (only by name) | `redis:8.8.4` | 6379 | 128 MB, 512 MB | 999:999 | `REDIS_URL`, `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` |
| `meilisearch` | `getmeili/meilisearch:v1.56.1` | 7700 | 512 MB, 2 GB | 10001:10001 | `MEILI_URL`, `MEILI_HOST`, `MEILI_MASTER_KEY` |

**Pre-built images**: a Dockerfile that is only `FROM <image>:<tag>` (plus `USER`, `ENV` and the like) runs a published image. `EXPOSE` is optional there: after the build the pipeline reads the port from the image config and stores it on the deployment, so a rollback reuses it. The image's data must go under `/data`.

**Pipeline**, one row per deployment state:

| State | What happens | Fails with |
| --- | --- | --- |
| `queued` | Waits for a build slot | — |
| `fetching` | Source copied or cloned into `builds/<deployment_id>/` | `source_*` codes |
| `validating` | Dockerfile checks (MCP section); stops before any build | `dockerfile_*` codes |
| `building` | `docker buildx build`, 30 min timeout, image `bitploy/<slug>:<n>` | `build_failed` + last 50 log lines |
| `starting` | Container `bp-<slug>-<n>` on network `bp-<slug>` with env, limits, volume | `start_failed` |
| `health_check` | Port open, then `GET health_path` returns < 500 within 60 s (a fixed window, not a setting); a Dockerfile `HEALTHCHECK` replaces this | `unhealthy` + last 50 runtime log lines |
| `swapping` | Route file points at the new container; old one stops after 10 s | — |
| `running` | Live; previous deployment marked `superseded` | — |
| `failed` | Old container keeps serving; error code, hint and log tail stored | — |
| `cancelled` | Replaced by a newer queued deployment, the app was stopped or deleted, or the user cancelled it (see Cancel below) | — |

**Cancel** (`POST /deployments/{id}/cancel`, `cancel_deployment`; deploy scope, own apps only): a `queued` deployment becomes `cancelled` at once. In `fetching`, `validating`, `building`, `starting` or `health_check` the pipeline's context is cancelled: the `docker buildx build` process group gets SIGTERM and, 5 s later, SIGKILL (Linux), the build slot is freed, the candidate container and the new image are removed and the previous version keeps serving untouched. The call waits up to 5 s for that, so the answer usually already says `cancelled`; otherwise it shows the state as it is. From `swapping` on it is too late: 409 `too_late_to_cancel` ("It is already going live." / "Wait a moment; roll back if needed."); a finished deployment (`running`, `failed`, `superseded`, `cancelled`) is 409 `deployment_finished`. The point of no return is decided under one lock (`commitSwap`), so a deployment never both goes live and is cancelled. A cancel is not a failure: the state is `cancelled`, `error` is `{code: "cancelled", message: "Cancelled by the user."}`, `failed`'s hints, audit row and "3 failures" advice do not apply, and the audit row is `deployment.cancelled` (actor, channel, `app`, `seq`, `was`). The app status settles like after a failure, without the failure: a live version keeps `running` (or `stopped` when the app is meant to be stopped); an app that was never deployed goes back to `new`, or `failed` when an earlier deployment failed; another queued deployment keeps it `deploying`.

**Deploy commands** (`before_deploy_command`, `after_deploy_command`; per app, table `app_deploy_commands`, migration `00320`): two optional shell commands, run with `sh -c` (so the image needs `sh`), each at most 2,000 characters.

- *Before deploy* (database migrations: `php artisan migrate --force`, `python manage.py migrate`, `npx prisma migrate deploy`) runs in the `starting` state, after the build and before the new container is created: in a one-off container `bp-<slug>-<n>-before` from the new image with the app's variables, limits, networks (also the project network, without DNS aliases) and `/data`, with the image's `ENTRYPOINT`, `CMD` and `HEALTHCHECK` replaced and no restart policy. It runs before a `/data` app's old container is stopped, so the live version keeps serving meanwhile (two containers share `/data` for that time). A non-zero exit, a start failure or 10 minutes stops the deployment with `before_deploy_failed` or `before_deploy_timeout` (hint, `next_tool`, log tail with the output); the one-off container is removed and no new app container is started. Cancelling the deployment stops it too.
- *After deploy* runs once the swap is done, with `docker exec` in the live container, for up to 5 minutes. A failure (exit code, timeout, bitploy restart) is a warning on the deployment (`warnings[]` code `after_deploy_failed`) and never rolls back. While it runs, the deployment is `running` with `after_deploy: pending|running` and is not terminal for long polls and the build log stream; it ends as `succeeded` or `failed`.
- Their output streams into the build log under `==> Running before-deploy command: <cmd>`, each line prefixed `[before-deploy]` or `[after-deploy]`, at most 1 MB per command. bitploy logs the command, never variable values.
- Not run on rollbacks (the data stays migrated and the commands belong to the newer code), for static sites, for apps with several services (refused with `deploy_commands_unsupported` until there are per-service commands) or for service template and one-click apps (refused).

With a volume attached, use **recreate** instead of rolling: stop the old container, start the new one, and restart the old one automatically if the new one fails. Two writers on one SQLite file is worse than a few seconds of downtime.

**Runtime contract** for every app:

- Listen on `0.0.0.0:$PORT`. Bitploy injects `PORT`, `BITPLOY_APP` and `BITPLOY_URL`. The port comes from the app setting, else the single `EXPOSE` in the final stage, else (a web app only) the single TCP port the built image declares. When the image declares several ports, none, or 8081 (Traefik's internal entrypoint), the deployment fails with `port_ambiguous`, `port_unknown` or `port_reserved` and a hint to set the port.
- Log to stdout and stderr. Write persistent data only to `/data`, and only with the volume on.
- Keep the last 3 images per app for `rollback`, which reruns from `starting`. Prune older images, and run `docker builder prune` nightly with a storage cap.

## MCP server

The MCP server is the product. It must let an agent that knows nothing about bitploy go from "put this online" to a live URL by itself, and it enforces the Dockerfile rule at three points: the server instructions, `validate_dockerfile`, and the deploy service.

**Transport**

- `POST https://bitploy.nl/mcp`, Streamable HTTP, `StreamableHTTPOptions{Stateless: true}`. Request body limit 4 MB.
- Do not rely on MCP logging, sampling or roots; protocol `2026-07-28` deprecates them. Build and runtime logs come from tools.
- No tool call may block longer than 50 s: Cloudflare's proxy read timeout is 125 s. `deploy` returns at once; `get_deployment` long-polls with `wait_seconds` (max 50).
- Every result carries `structuredContent` plus a one-line text summary. Successful results include `next_steps`; failures set `isError: true` and return `{code, message, hint, next_tool}` so the agent knows what to call next.

**Server instructions** (send exactly this as the server's `instructions`; keep it in `internal/mcp/instructions.md`):

```markdown
You are connected to bitploy. It deploys the user's code to https://<slug>.bitploy.nl.

1. A static site needs no Dockerfile: plain HTML/CSS/JS files, or a front
   end that builds to files (Vite, React, Vue, Astro, SvelteKit static,
   Next.js export). Send the files as they are, without a Dockerfile or nginx
   config; bitploy builds and serves it for free (it does not count towards
   the memory and CPU quota). Every other app is built from a Dockerfile;
   bitploy refuses to deploy a server without one. If the code has none, call
   get_template with the stack and file_list, adapt it, and add a .dockerignore.
   Only for a repository you cannot change (not the user's, or no checkout)
   that holds a Node.js app without a Dockerfile: create_app build_mode=auto,
   and bitploy builds it by itself.
2. The app must listen on 0.0.0.0 on the EXPOSE port (also in $PORT), never on
   localhost or 127.0.0.1.
3. Call validate_dockerfile before every deploy and fix all errors.
4. Never put secrets in the Dockerfile or the source. Use set_env. A value
   the build bakes in (VITE_*, NEXT_PUBLIC_*) reaches the build too and
   applies on the next deploy.
5. Send the code. If it has a GitHub remote, prefer git: commit and push, then
   set_git_source with the remote's HTTPS URL and deploy once; after that
   every push to the branch deploys by itself. No GitHub remote, or the
   repository cannot be read and the user cannot connect GitHub? Upload:
   deploy_files for up to 2 MB and 200 files, else zip the folder, call
   create_upload and run the curl command it returns. Never ask the user to
   create a repository or install git only to deploy. An app linked to a
   repository refuses uploads (git_source_linked): push instead.
6. A deployment (deploy, deploy_files, or a push to a linked repository) builds
   in the background. Call get_deployment with wait_seconds=30 until the state
   is running or failed. If a build is clearly wrong or stuck, call
   cancel_deployment instead of waiting. On failure, read error.hint and the
   log tail, fix, and send the fix again. After 3 failures with the same error
   code, stop and explain the problem to the user in plain words.
7. When it runs, give the user the URL and one or two plain sentences on what
   you did. Assume the user is not technical.
8. Files that must survive a redeploy go in /data: create_app volume=true, or
   for an existing app update_app volume=true, then restart_app. Keep a
   database as SQLite in /data inside the app's one container; never add a
   database server to the image. Only when the code needs a database server
   (PostgreSQL, MySQL, MariaDB, Redis or Meilisearch) and cannot use SQLite,
   add one next to the app: add_service with the app's project (get_app shows
   it) and connect_to=[<slug>], before the app's first deploy; the app then
   reads DATABASE_URL (or REDIS_URL, ...) from its environment. Database
   migrations go in before_deploy_command (update_app), like
   php artisan migrate --force, not in the Dockerfile CMD.
   create_backup of a database service dumps its databases while it runs;
   Redis and Meilisearch hold data that can be rebuilt and get no backups.
   When the code already has a compose file (compose.yaml,
   docker-compose.yml) whose services it needs, deploy that file instead:
   validate_compose, then create_app build_mode=compose.
   Never write a compose file only to deploy.
9. Never stop, delete or roll back an app the user did not name. Turn password
   protection (set_password_protection) on or off only when the user asks.
   Open a public port (set_public_port) only when the user wants to connect
   to a database from their computer; keep it short and close it afterwards.
10. For a well-known tool (password manager, notes, uptime monitor, ...) call
    list_catalog, then deploy_catalog_app. To run another published image,
    deploy one Dockerfile `FROM <image>:<tag>` with a pinned tag (EXPOSE is
    optional) and keep its data in /data.
```

**Tools**

| Tool | Does | Scope | Hints |
| --- | --- | --- | --- |
| `whoami` | User, quotas, usage, base domain | read | read-only |
| `get_deploy_rules` | The rules above plus the runtime contract | read | read-only |
| `get_template(stack, file_list?)` | Dockerfile + .dockerignore for a stack, adapted to the lock files in `file_list` | read | read-only |
| `validate_dockerfile(dockerfile, file_list?)` | Static checks below; no build | read | read-only |
| `validate_compose(compose, app?)` | Previews a compose file: services, volumes, totals, quota, warnings and errors (`{code, service, key, message, hint}`); a bad file is `ok: false`, never a tool error. Only for a project that already has a compose file | read | read-only |
| `list_catalog` | The one-click apps (id, name, tagline, pinned version, memory, volume, category) | read | read-only |
| `deploy_catalog_app(id, name?, slug?, project?, alias?)` | Creates the app from the catalog, sets its env, deploys, returns app, deployment and `first_steps` with the URL filled in; removes what it made again if the deployment cannot be queued. An entry with services (like Umami with PostgreSQL) adds the services and the app to `project` (default: a new project named after the app); taken service aliases get a `-2` suffix | deploy | — |
| `list_projects` / `get_project(project)` | Projects with their apps: alias, `internal_host`, template, connection variables, references | read | read-only |
| `create_project(name)` | An empty project that stays when empty | deploy | — |
| `list_service_templates` | The templates of `add_service` with image, port, sizes, `connection_vars` and `main_var` | read | read-only |
| `add_service(project, template, name?, connect_to?, memory_mb?, volume_mb?)` | Adds a database or other service from a template to the project as a private app and deploys it; `connect_to` apps get the main variable as a reference and restart once it runs | deploy | — |
| `list_apps` / `get_app(app)` | Apps with URL, state, current deployment, project, alias, `internal_host` and template | read | read-only |
| `create_app(name, type?, slug?, port?, volume?, build_mode?, compose_path?, project?, alias?)` | Reserves a subdomain, returns the URL; `build_mode: compose` only for a project that already has a compose file; `project` puts it in an existing project | deploy | — |
| `update_app(app, …)` | Port, health path, volume, resources, `build_mode` (`compose` turns a one-container app into a compose app, keeping `/data`; no way back), `alias` (rename; references follow) | deploy | idempotent |
| `deploy_files(app, files[])` | Inline source, validates, queues a deployment; a compose app sends the compose file and every Dockerfile it builds | deploy | — |
| `create_upload(app)` | One-time URL (15 min) and the exact `curl` command | deploy | — |
| `set_git_source(app, repo_url?, branch?, subdir?, auto_deploy?, remove?)` | Saves the source and checks access with `ls-remote`; `auto_deploy` (default on for GitHub) deploys on every push; `remove: true` unlinks | deploy | idempotent |
| `deploy(app, upload_id?)` | Queues a deployment from the upload or git source | deploy | — |
| `get_deployment(id?, app?, wait_seconds?)` | State, timings, error, last 50 log lines; with only `app` it returns the latest deployment | read | read-only |
| `list_deployments(app)` | History, newest first | read | read-only |
| `get_logs(app, kind, tail?, since?, service?)` | `build` or `runtime` logs, max 1,000 lines; `service` picks one service of an app with several | read | read-only |
| `set_env(app, set, unset, service?, hidden?)` | Encrypted env vars, readable unless hidden: `hidden` per name, else hidden when the name contains SECRET, TOKEN, KEY or PASSWORD; a hidden value is never returned. `service` scopes them to one service of a compose app. Values may hold `${{<alias>.VAR}}` references to apps of the same project, checked on set. Every variable also reaches the image build (Build-time variables) | deploy | idempotent |
| `set_password_protection(app, enabled, username?, password?)` | Browser login prompt in front of the app's public addresses; only when the user asks; the password is never returned | deploy | idempotent |
| `set_public_port(app, enabled, service?, close_after?, allow_from?, only_my_ip?, show_password?)` | For debugging: a port on the server that forwards to an app without a web address (Public ports); `show_password` adds the connection URL with the password (audited) | deploy | idempotent |
| `restart_app` / `start_app` / `stop_app` | Lifecycle | deploy | stop: destructive |
| `rollback(app, deployment_id)` | Runs a previous image | deploy | destructive |
| `cancel_deployment(id or app)` | Cancels a queued or in-progress deployment (`app`: the app's open one; `no_deployment_to_cancel` 409 when there is none); the live version keeps running | deploy | write |
| `delete_app(app, confirm_slug)` | Removes containers, images, routes, volume; refused with `app_in_use` while other apps refer to its variables | deploy | destructive |
| `add_domain` / `check_domain` / `remove_domain` | Custom domains (Domains section) | deploy | — |
| `get_metrics(app, window, service?)` | CPU, memory, network for `1h`, `24h` or `7d`; `service` narrows an app with several services | read | read-only |
| `create_backup(app, volume?)` / `list_backups(app)` | Back up /data (every volume of a compose app unless `volume` picks one) in the background; list backups, settings (with `time_utc`) and restore state. A database service gets a dump (`format: dump`). Restoring is dashboard-only on purpose | deploy / read | — / read-only |
| `set_backup_schedule(app, schedule?, keep_mode?, keep?)` | `PUT /apps/{app}/backup-settings`: off, daily or weekly at the app's time; keep the newest N or N days | deploy | idempotent |
| `query_db(app, path?, sql, volume?)` | Read-only SQL on a SQLite file in /data (`volume` picks another volume of a compose app), or on a PostgreSQL, MySQL or MariaDB service (`path` is the database name, left out: its own). Always `write: false` on `/db/sql`. Results as compact text tables, at most 200 rows and about 50 KB | read | read-only |
| `write_db(app, path?, sql, volume?)` | The same with `write: true`: changes are committed, audited as `db.write` | deploy | destructive |
| `list_env(app, names?)` | Variables with their values (a hidden one only masked), services and references; `names` says which are set | read | read-only |
| `get_traffic(app, window?)` | `GET /apps/{app}/traffic` for `24h`, `7d` or `30d`: requests, 5xx errors and rate, page views, visitors, median and p95 answer times | read | read-only |
| `update_catalog_app(app)` | `POST /apps/{app}/catalog-update`: deploys the catalog's newer version of a one-click app | deploy | write |
| `rename_project(project, name)` / `delete_project(project, confirm, with_apps?)` | `PATCH` / `DELETE /projects/{id}`; delete needs the name as `confirm`, a project with apps needs `with_apps=true` | deploy | idempotent / destructive |

Also expose the rules as resource `bitploy://rules` and each template as `bitploy://templates/{stack}`, and add a prompt `deploy_project` that walks through the eight steps. Keep the tools anyway: many clients ignore resources and prompts.

**Dockerfile checks** (one Go function, run by `validate_dockerfile` and again in the `validating` state):

| Code | Severity | Rule |
| --- | --- | --- |
| `dockerfile_missing` | error | No file at `dockerfile_path` |
| `dockerfile_parse_error` | error | BuildKit parser fails; report the line |
| `no_from` | error | No `FROM` instruction |
| `port_unknown` | error | An `EXPOSE` that cannot be read (a variable without a default) and no app port; also raised after the build when the image declares no port |
| `port_from_image` | info | No `EXPOSE` and no app port: the port is read from the built image after the build; `port_unknown` (none declared), `port_ambiguous` (several) or `port_reserved` fail there |
| `port_ambiguous` | error | Several `EXPOSE` ports and no app port (also raised after the build when the image declares several) |
| `image_too_large` | error | Built image over 2 GB (checked after build) |
| `cache_mount_forbidden` | error | A `RUN --mount=type=cache`: all apps build on one builder, so the cache would be shared between apps |
| `env_file_in_context` | warning | `.env` would be copied into the image; suggest `set_env` and `.dockerignore` |
| `no_dockerignore` | warning | `node_modules`, `.git` or `venv` present without a `.dockerignore` |
| `unpinned_base` | warning | Base image has no tag or uses `latest` |
| `runs_as_root` | warning | Final stage has no `USER` |
| `compose_ignored` | warning | A compose file with several services at the top: only the Dockerfile is built; the hint suggests SQLite in `/data`, and `update_app(build_mode="compose")` only if the app needs those services |
| `database_server_in_image` | warning | A `RUN` installs a database server (postgresql, mysql-server, mariadb-server, redis); use SQLite in `/data` |
| `lockfile_mismatch` | warning | `npm ci` but the only lock file is pnpm's, yarn's or bun's |
| `no_healthcheck` | info | Bitploy will use its HTTP check on `health_path` |

When `unhealthy` fires and the runtime log mentions `127.0.0.1` or `localhost`, set the hint to "Your app listens on localhost; make it listen on 0.0.0.0:$PORT". That is the most common noob failure.

**Catalog** (`internal/catalog`): one-click apps, each one Dockerfile with a pinned version, a non-root `USER` and its data in `/data`, which passes the Dockerfile checks without errors (a test runs them). Deploying one creates a normal app (the usual quotas and slug rules) with `volume` on where the app has data, records `catalog_id` and `catalog_version` on it, and queues a deployment from the catalog Dockerfile. An env value can contain `{url}` (the app's address) or be a generated secret (32 random base62 characters, stored encrypted like any env var and never returned). Each deployment remembers the catalog version it builds, and the app's `catalog_version` takes it when that deployment goes live (a rollback brings the older version back). Apps report `catalog_update` when the catalog has a newer version and the app is not linked to a repository; updating is an explicit action, never automatic.

**Templates** live in `internal/templates/<stack>/` with a Dockerfile, a .dockerignore and a short README, pinned to current major versions: `static`, `node-spa`, `node-static`, `node`, `nextjs`, `python`, `django`, `go`, `php`, `laravel`, `generic` (`templates.Stacks`). `get_template` with `file_list` adapts the Node and Python ones to the lock file (`templates.ForFiles`: pnpm, yarn, bun, no lock; uv, Poetry, pyproject). The static one shows the style (the nginx config keeps the proxy from seeing port 8080 in redirects, and serves `/about` from `about.html`):

```dockerfile
FROM nginxinc/nginx-unprivileged:alpine
COPY <<'NGINXCONF' /etc/nginx/conf.d/default.conf
server {
    listen 8080;
    root /usr/share/nginx/html;
    index index.html index.htm;
    # Behind the proxy the port nginx listens on (8080) must not end up in redirects.
    absolute_redirect off;
    # Shows 404.html when the site has one.
    error_page 404 /404.html;
    # Hidden files such as .env or .git are never served; /.well-known/ is.
    location ~ /\.(?!well-known/) {
        deny all;
    }
    location / {
        # /about finds about.html, /docs finds docs/index.html. Unknown paths are a 404.
        try_files $uri $uri/ $uri.html =404;
    }
}
NGINXCONF
COPY . /usr/share/nginx/html
EXPOSE 8080
```

## REST API

The REST API mirrors the MCP tools one to one under `/api/v1`: same service calls, same error codes, same limits. Anything an agent can do through MCP, a script can do with `curl`.

**Conventions**

- JSON in and out, `snake_case`, prefixed IDs (`usr_…`, `app_…`, `dep_…`, `tok_…`), timestamps in RFC 3339 UTC.
- Auth: `Authorization: Bearer bp_…`, or the session cookie plus CSRF checks for the dashboard.
- Errors: a fitting HTTP status and `{"error": {"code", "message", "hint", "next"}}`, with codes shared with MCP.
- Lists: `?limit=` (max 100) and an opaque `?cursor=`.
- `Idempotency-Key` header accepted on every POST that creates something; replays within 24 h return the first result.
- Rate limits: 120 requests per minute per token, 20 deployments per hour per user. Answer `429` with `Retry-After`.
- Streams (`?follow=1`) use Server-Sent Events with a comment heartbeat every 15 s, so Cloudflare never sees an idle connection.

**Endpoints**

| Endpoint | Does (MCP tool) |
| --- | --- |
| `POST /api/v1/auth/login`, `POST /api/v1/auth/logout` | Dashboard login and logout |
| `GET /api/v1/session` | Current user and CSRF token (session only) |
| `POST /api/v1/invites/{code}/accept` | Finish signup from an invite |
| `GET /api/v1/me`, `GET /api/v1/usage` | `whoami`, usage totals |
| `GET` `POST` `DELETE /api/v1/tokens[/{id}]` | Manage API tokens (session only) |
| `GET /api/v1/github`, `POST /api/v1/github/connect`, `POST /api/v1/github/link`, `DELETE /api/v1/github/installations/{id}` | The user's GitHub connection (session only) |
| `GET /api/v1/github/repositories`, `GET /api/v1/github/branches` | Repository and branch pickers (session only) |
| `GET` `DELETE /api/v1/admin/github`, `POST /api/v1/admin/github/manifest[/complete]` | Create or remove the instance's GitHub App (admin, session only) |
| `POST /api/v1/github/webhook` | GitHub's push and installation events (signature, no session) |
| `GET /api/v1/templates[/{stack}]` | `get_template` |
| `POST /api/v1/dockerfile/validate` | `validate_dockerfile` |
| `POST /api/v1/compose/validate` | `validate_compose` (`{compose, app?}`; any scope; 200 with `ok: false` and `errors` for a bad file) |
| `GET /api/v1/catalog[/{id}]` | `list_catalog`; the one-app form adds the Dockerfile, env fields and first steps (any scope) |
| `POST /api/v1/catalog/{id}/deploy` | `deploy_catalog_app` (deploy scope; 202; accepts `Idempotency-Key`) |
| `POST /api/v1/apps/{app}/catalog-update` | Replace the Dockerfile with the catalog's current one and deploy (accepts `Idempotency-Key`); 409 `catalog_up_to_date`, `not_catalog_app`, `git_source_linked` |
| `GET` `PUT /api/v1/apps/{app}/dockerfile` | Read or replace the Dockerfile in the newest stored source, then deploy (REST and dashboard only; 64 KB; 409 `git_source_linked`, `dockerfile_not_used`, `source_missing`; 400 `dockerfile_empty`; 413 `dockerfile_too_large`) |
| `GET` `POST /api/v1/projects`, `GET` `PATCH` `DELETE /api/v1/projects/{id}` | `list_projects`, `create_project`, `get_project`; rename; delete with `?confirm=<name>` (409 `project_not_empty` unless `with_apps=true`). `{id}` is an id or a name |
| `GET /api/v1/service-templates` | `list_service_templates` (any scope) |
| `POST /api/v1/projects/{id}/services` | `add_service` (`{template, name?, connect_to?, memory_mb?, volume_mb?}`; 202 with the App; accepts `Idempotency-Key`) |
| `GET` `POST /api/v1/apps` | `list_apps`, `create_app` |
| `GET` `PATCH` `DELETE /api/v1/apps/{app}` | `get_app`, `update_app`, `delete_app` |
| `POST /api/v1/apps/{app}/files` | `deploy_files` |
| `POST /api/v1/apps/{app}/uploads` | `create_upload` |
| `POST /u/{upload_id}` | Raw zip body; the unguessable ID is the credential, single use, 15 min |
| `PUT` `DELETE /api/v1/apps/{app}/git-source`, `PATCH` for `auto_deploy` | `set_git_source`, with `remove: true` to unlink |
| `GET` `POST /api/v1/apps/{app}/deployments` | `list_deployments`, `deploy` |
| `GET /api/v1/deployments/{id}?wait=30` | `get_deployment` with long-poll |
| `POST /api/v1/deployments/{id}/cancel` | `cancel_deployment` (deploy scope, owner only): returns the Deployment; 409 `too_late_to_cancel` while swapping, 409 `deployment_finished` |
| `GET /api/v1/deployments/{id}/log[?follow=1]` | Build log as text or SSE |
| `GET /api/v1/apps/{app}/logs?kind=runtime&tail=200[&follow=1][&service=]` | `get_logs` |
| `PUT /api/v1/apps/{app}/env` | `set_env` (values on read, a hidden one masked; optional `service` and `hidden`) |
| `PUT /api/v1/apps/{app}/password-protection` | `set_password_protection` (`{enabled, username?, password?}`; apps report `protected` and `protected_username`, never the password) |
| `PUT /api/v1/apps/{app}/public-port`, `POST …/public-port/reveal` | `set_public_port` (`{enabled, service?, close_after?, allow_from?, only_my_ip?}`, returns the App with `public_port`); reveal returns `{connection_url, address, secrets}` with the password (owner, deploy scope, audited) |
| `GET /api/v1/admin/public-ports`, `DELETE /api/v1/admin/public-ports/{app}` | Admin only, dashboard session only: the range, the per-user limit and every open port; close any of them |
| `POST /api/v1/apps/{app}/restart`, `/start`, `/stop`, `/rollback` | Lifecycle tools |
| `GET` `POST` `DELETE /api/v1/apps/{app}/domains[/{host}]`, `POST …/{host}/check` | Domain tools |
| `GET /api/v1/apps/{app}/metrics?window=24h[&service=]` | `get_metrics` |
| `POST /api/v1/apps/{app}/upload` | Raw zip body, upload and deploy in one call (session with CSRF, or a deploy token); the dashboard's "Upload new version" |
| `GET` `POST /api/v1/apps/{app}/backups`, `PUT …/backup-settings` | `list_backups`, `create_backup`, `set_backup_schedule` (restore, download and upload are dashboard and REST only) |
| `GET /api/v1/apps/{app}/db?path=`, `POST …/db/rows`, `/db/cell`, `/db/insert`, `/db/update`, `/db/delete`, `/db/sql` | The SQLite viewer (see SQLite viewer). Reading and `sql` with `write: false` need the read scope, the rest the deploy scope. `query_db` is `/db/sql` with `write: false`, `write_db` with `write: true` |
| `/api/v1/admin/users`, `/invites`, `/quotas`, `/server`, `/server/metrics` | Admin only |
| `GET /api/v1/admin/db`, `POST /api/v1/admin/db/rows`, `POST /api/v1/admin/db/sql` | Admin only, dashboard session only: a read-only view of bitploy's own database with every secret hidden (see Admin database viewer). No MCP tool |

Serve the generated spec at `/api/v1/openapi.json` and a read-only reference page at `/docs/api`.

### SQLite viewer

Apps keep their data in SQLite files in /data, so the Storage tab can open one: tables and views with their columns and indexes, a data grid (paging, sorting, filters, insert, edit and delete of rows) and a SQL console. The access rules are those of the /data file browser: only the owner's own app, /data on and created, writes need the deploy scope, nothing while a backup restore runs. The dashboard opens every database read-only: one "Allow changes" switch (confirmed, kept for that browser tab) turns on editing in the grid and writes in the SQL console together. The dashboard offers Open for the names `.db`, `.sqlite`, `.sqlite3` and `.db3` (not the `-wal`, `-shm` and `-journal` files); the backend decides by the 16-byte header on every call (`not_sqlite`, 422). `path` is relative to /data and refused when it contains `..` or goes through a symbolic link.

| Endpoint | Body | Answer |
| --- | --- | --- |
| `GET /apps/{app}/db?path=` | | `path`, `size`, `journal_mode`, `page_size`, `sqlite_version`, `tables[]`: `name`, `type` (table or view), `without_rowid`, `editable`, `rows` (null when counting took too long; all counts together get about 2 s), `sql`, `columns[]` (`name`, `type`, `notnull`, `default`, `pk`, `hidden`, `generated`, `redacted`: always false for apps), `indexes[]` (`name`, `unique`, `columns`, `sql`). `sqlite_*` tables are left out |
| `POST /apps/{app}/db/rows` | `path`, `table`, `limit` (1 to 1000, default 100), `offset`, `order_by`, `desc`, `search`, `filters[]` (`column`, `op`, `value`; ops `=`, `!=`, `<`, `<=`, `>`, `>=`, `like`, `not like`, `is null`, `is not null`, ANDed) | `columns`, `key_columns`, `editable`, `rows[]` (`key`, `values`), `total` (null on a timeout), `offset`, `limit` |
| `POST /apps/{app}/db/insert` (201) | `path`, `table`, `values{column: value}` | `key`, `row` |
| `POST /apps/{app}/db/cell` | `path`, `table`, `key`, `column` | `{"value": ...}`: one value of one row whole (read scope). A text up to 4 MiB is a plain string, a blob up to 4 MiB `{"blob": "<base64 of all of it>", "size": n, "truncated": false}`; anything bigger has the normal cut-off form. Key rules of update (`not_editable` for views); 404 `row_not_found`, 400 `column_not_found` |
| `POST /apps/{app}/db/update` | `path`, `table`, `key`, `values`, optional `match{column: value}` | `row`; 404 `row_not_found`; 409 `row_changed` |
| `POST /apps/{app}/db/update-many` | `path`, `table`, `keys[]` (1 to 1000), `values`, optional `match[]` (one set per key, same order) | `updated`; 409 `row_changed` with `details.index` (0-based place in `keys`) |
| `POST /apps/{app}/db/delete` | `path`, `table`, `keys[]` (1 to 1000, one transaction), optional `match[]` (one set per key, same order) | `deleted`; 409 `row_changed` |
| `POST /apps/{app}/db/sql` | `path`, `sql` (at most 64 KiB, several statements), `write` | `results[]` (`sql`, `columns`, `rows`, `truncated`, `rows_affected`, `last_insert_rowid`, `took_ms`), `took_ms` |

- **Values** go in and out as JSON: NULL is `null`; an integer within ±2^53 a number, a bigger one `{"int": "9007199254740993"}`; a real a number (`{"real": "NaN"}` or `"Inf"` where JSON has none); text a string, and over 64 KiB in a result `{"text": "<first 64 KiB>", "size": n, "truncated": true}`; a blob `{"blob": "<base64>", "size": n, "truncated": bool}` with the first 4 KiB in a result. A value that came back truncated can never be sent back (`value_truncated`, 400); to edit a long text read it whole with `/db/cell` (a text of up to 4 MiB can be sent in insert and update; the request body limit of `/api/v1` is 32 MiB, the helper's request limit too). A client leaves a cut-off column out of `match`. Request bodies keep numbers exactly as sent (a bare integer beyond 2^53 is stored as that integer).
- **Row keys**: a table with a rowid has `{"rowid": n}` (the first of `_rowid_`, `rowid`, `oid` that no column shadows; if all three are shadowed the table has no usable key); a WITHOUT ROWID table has its primary key columns; views and tables without a usable key have `key: null` and are read-only (`not_editable`). Columns that SQLite computes (generated) cannot be set (`column_read_only`).
- **Search (`search` on rows)**: free text, trimmed, empty means none, at most 200 characters (`search_invalid`, 400); ANDed with `filters`, and `total` follows it. It is matched against every visible column (also in views), ORed, with the text as a bound parameter in which `%`, `_` and `\` are escaped (`LIKE ... ESCAPE '\'`, case-insensitive for ASCII). A blob value never matches. Columns with TEXT affinity, no declared type or a date type (DATE, DATETIME, TIMESTAMP) contain the text, compared as stored. When the text is a number (`12`, `-3`, `1.5`) number columns (INTEGER, REAL, NUMERIC affinity, and the rowid) match by prefix of their text, so `12` finds 12, 120 and 1234 (big integers are exact); for other text number columns are skipped. When no column can match the page is empty, not an error.
- **Bulk update (`update-many`)**: sets the same `values` on every row of `keys`, in one transaction, all or nothing; the same rules as `update` (generated and hidden columns are refused, values are bound, deploy scope). Gone keys are skipped. With `match` a row that changed stops it all: 409 `row_changed` with `details.index`. An error of SQLite (a unique index, a foreign key) undoes all. Audited as `db.write` with `rows_affected` the number of rows changed.
- **Stale keys (`match`)**: a rowid can be reused (delete, then insert) and `VACUUM` renumbers the rowids of a table without an INTEGER PRIMARY KEY, so a grid that was loaded earlier could change or delete another row. `update` takes an optional `match` (the values the row had when it was loaded, as listed; values that were cut off are left out) and `delete` an optional `match` list with one set per key. Each column adds `AND "col" IS ?` to the WHERE (NULL matches NULL; text is compared as stored, so DATE and DATETIME columns use the text that was shown). If the key still exists but a value differs, nothing is changed and the answer is 409 `row_changed` ("This row changed since you loaded it.", hint "Reload the rows and try again."); a delete is all or nothing, so one changed row stops it and the transaction is rolled back. A key that is gone is `row_not_found` for an update and skipped by a delete. An unknown column is `column_not_found`; a `match` list of another length than `keys` is `key_invalid`.
- **Safe by construction**: table and column names are looked up in the schema and quoted, values are always bound parameters, foreign keys are enforced, `delete` and `update` run in a transaction. A DATETIME, DATE or TIMESTAMP column comes back as the text that is stored (the driver would otherwise reformat it).
- **SQL console**: `write: false` (the default) runs with `PRAGMA query_only=1`; a write fails with `db_read_only` ("Turn on writes to change data"). `write: true` runs each statement in autocommit (an explicit `BEGIN`/`COMMIT` is allowed). ATTACH, DETACH, `load_extension`, `VACUUM INTO` and the PRAGMAs `query_only`, `writable_schema`, `trusted_schema`, `locking_mode`, `temp_store_directory`, `data_store_directory`, the heap limits and (read-only) `journal_mode` writes are refused with `statement_not_allowed` (SQLite's authorizer plus a check for `VACUUM INTO`). A write request that starts a transaction and does not end it (`BEGIN` without `COMMIT`) is rolled back and answers 400 `transaction_open` ("The SQL started a transaction but did not COMMIT, so nothing in it was saved."; statements before the `BEGIN` were saved, and `details.results` lists what ran). At most 1000 rows per statement (`truncated`); when statement N fails the answer is 400 `sql_error` with `details: {statement: N, results: [what ran before]}`.
- **Errors** (codes): `not_sqlite`, `db_not_found`, `table_not_found`, `column_not_found`, `row_not_found`, `db_busy` (409, the database stayed locked for more than 5 s, or more than 3 helpers run for the app or 8 for the server and a place did not free up within 2 s), `db_timeout` (422, over 15 s, the helper is killed), `db_read_only`, `sql_error`, `statement_not_allowed`, `value_truncated`, `result_too_large`, plus `row_changed` (409), `transaction_open` (400), `value_invalid`, `key_invalid`, `filter_invalid`, `not_editable`, `column_read_only`, `limit_invalid`, `sql_empty`, `sql_too_long`, `db_unreadable`, `db_out_of_memory`, `db_helper_unavailable` (503), `file_symlink`, `not_a_file`, `path_invalid` and the errors of the file browser (`volume_off`, `volume_not_ready`, `backup_busy`, `files_busy`).
- **Audit**: every write that reached the helper (`insert`, `update`, `delete`, `sql` with `write: true`) writes `db.write` with `app`, `path`, `table` (when there is one), `statements` (the kinds, like `["insert","update"]`), `rows_affected` and `outcome`: `ok`, `error` (the helper answered an error; for a console request `completed` says how many statements had been committed, and `rows_affected` counts those) or `unknown` (the helper did not answer, for example it was killed, so the change may have been made); an `error` field holds the code. A change that is too big to report (`result_too_large` on a write) is `ok`, and its message says the changes were saved. Never values or SQL text.
- **MCP**: `query_db(app, path, sql, write=false)`, same as `/db/sql`; results as text tables (at most 200 rows and about 50 KB, cells cut at 200 characters).
- **How it runs**: SQLite never runs inside bitploy or the app container. Each request runs in a fresh helper container (no network, all capabilities dropped but those needed, 256 MB, 64 processes, a 64 MB /tmp, removed afterwards, killed after 20 s) from a local image `bitploy/db-helper:<hash of the binary>`, built once from busybox plus the static Linux bitploy binary (`bitploy db-helper`, a hidden subcommand; `os.Executable()` on Linux, else `BITPLOY_DB_HELPER_BIN`) and rebuilt when the binary changes. The helper stops itself after 15 s and waits 5 s for locks (`busy_timeout`). It gives up SETUID and SETGID as soon as it has become the owner of the file, and checks. At most 3 helpers run per app and 8 for the server (a request waits up to 2 s for a place, then answers `db_busy`). Reads are counted: a backup restore waits for the reads that run before it touches the files, and new reads are refused while it runs. The app keeps running: SQLite's file locks work across containers on the same volume. The file is never copied out and back. See docs/decisions.md.


### Admin database viewer

Admins can look into bitploy's own SQLite database (the control plane, `bitploy.db` in the data dir) with the same grid and SQL console as the app viewer, reusing `internal/dbview`. It is a read-only view and a secret never leaves the database through it.

| Endpoint | Body | Answer |
| --- | --- | --- |
| `GET /admin/db` | | The schema of `GET /apps/{app}/db` (`path` is `bitploy.db`), with `redacted` (true or false) on every column; `editable` is false everywhere |
| `POST /admin/db/rows` | The body of `/apps/{app}/db/rows` without `path`: `table`, `limit`, `offset`, `order_by`, `desc`, `search`, `filters[]` | The answer of `/apps/{app}/db/rows`: `editable` false, `key_columns` empty, every `key` null; a secret column is `null` in every row |
| `POST /admin/db/sql` | `sql`; `write` is not accepted (true is 400 `db_read_only`) | The answer of `/apps/{app}/db/sql` |

- **Access**: `RequireAdmin`, like the rest of `/admin`: the admin role and a dashboard session. A user is 403 `admin_only`, an API token (any scope) is refused, and the helper does not even start. There is no path parameter: the file is `cfg.DBPath()`.
- **Read-only, always.** There is no insert, update, delete, update-many or cell operation, and the console runs with `PRAGMA query_only=1` plus an authorizer that allows only what reads (SELECT, functions, recursive CTEs, transactions and savepoints, and the PRAGMAs that look at the schema or at a setting). INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, REINDEX, ANALYZE, VACUUM, ATTACH and every PRAGMA that sets something are 400 `db_read_only` or `statement_not_allowed`.
- **Secrets read as NULL.** The authorizer answers `SQLITE_IGNORE` to every read of a secret column, so SQLite uses NULL in its place wherever the column is used: in `SELECT *`, expressions (`substr`, `hex`, `length`, `json_object`, `||`), joins, views, CTEs, subqueries, WHERE, ORDER BY and aggregates. A filter that depends on a secret matches nothing and gives away nothing. `sqlite_schema` and the CREATE statements stay visible. `sqlite_dbpage` (the raw pages of the file) is refused (400 `statement_not_allowed`), and the samples of `sqlite_stat4` that ANALYZE keeps of indexed values are redacted too. The schema marks the columns `"redacted": true` so the grid can show them as hidden.
- **Which columns** are in ONE list, `controlSecrets` in `internal/dbview/secrets.go`: `users.password_hash`, `sessions.id_hash`, `sessions.csrf_token`, `api_tokens.hash`, `invites.code_hash`, `env_vars.value_enc`, `uploads.id` (the id is the credential of `POST /u/{id}`), `github_app.client_secret_enc`, `github_app.webhook_secret_enc`, `github_app.private_key_enc`, `apps.protect_hash`, `domains.txt_token`, `idempotency_keys.key`, `idempotency_keys.body`, `sqlite_stat4.sample`. A test migrates a fresh database and fails when a column has a name that looks like a secret (hash, secret, token, key, enc, password, credential, csrf, private, cipher, salt) and is in neither `controlSecrets` nor `controlSafe` (`audit_log.token_id`, `deployments.token_id`, `traffic_cursor.head_hash`), or when a list names a column that no longer exists.
- **Filter, sort, search**: `search` skips redacted columns (a table with no other column finds nothing); a filter or `order_by` on a redacted column is 400 `column_redacted`.
- **How it runs**: not in Docker. bitploy starts its own binary, `bitploy db-helper --control <path to bitploy.db> -` (the request on standard input, the answer on standard output), as a process of its own, with an empty environment (none of bitploy's secrets), the helper's 15 s query time and busy timeout (the process is killed 5 s later), an answer of at most 8 MB, and on Linux a 1 GB data limit (an allocation beyond it is `db_out_of_memory`, 422). A crash, a hang or a statement that eats memory does not take bitploy down. The process is the same user as bitploy, so the -wal and -shm files stay its own; as root (the production container) it also gives up SETUID and SETGID before it opens the file. At most 2 run at once; a request waits up to 2 s for a place and then answers 409 `db_busy`. It works on macOS and Linux alike (`os.Executable()`; no helper image or Linux binary needed).
- **Audit**: a console run writes `admin.db.query` (target `database`, `bitploy`) with `statements` (the kinds, like `["select","pragma"]`), `outcome` (`ok` or `error`) and `code` on an error; never the SQL text. Browsing (schema, rows) is not audited, and neither is a request that was refused before it ran (not an admin, `write`, `db_busy`).

## Domains & routing

Every app gets `https://<slug>.bitploy.nl` the moment it is created, through Cloudflare's proxy. Custom domains skip the proxy and get their own Let's Encrypt certificate on the server, because a custom domain cannot CNAME to a proxied `bitploy.nl` record: Cloudflare answers error 1001 for domains outside Cloudflare and error 1014 for domains in another Cloudflare account. Cloudflare for SaaS would fix that, but it is out of scope for v1.

**Subdomains**

- Default slug: `adjective-noun-xxxx` with 4 hex characters, e.g. `quiet-otter-3f9a`. Users may rename it: `a-z`, `0-9` and `-`, 3 to 40 characters, unique, not reserved. An old slug stays reserved for its owner for 30 days.
- One level only. `api.myapp.bitploy.nl` is not allowed: Universal SSL covers the apex and first-level subdomains only.
- Reserved: `www app api mcp admin dashboard docs status mail edge custom cdn static assets auth login bitploy traefik builder`, and anything starting with `bp-` (`builder` because its network would be `bp-builder`, the build network).
- Unknown subdomains and stopped apps hit a low-priority catch-all router that shows a friendly bitploy page ("No app here" or "This app is stopped") instead of a Traefik 404.

**Cloudflare setup** (done once by the operator; put it in the README):

| Record | Type | Value | Proxy |
| --- | --- | --- | --- |
| `bitploy.nl` | A (+ AAAA) | Server IP | Proxied |
| `*.bitploy.nl` | A (+ AAAA) | Server IP | Proxied |
| `edge.bitploy.nl` | A (+ AAAA) | Server IP | DNS only |

SSL/TLS mode **Full (strict)**, with a Cloudflare Origin CA certificate for `bitploy.nl` and `*.bitploy.nl` loaded in Traefik as the default certificate. Behind the proxy, trust `CF-Connecting-IP` only when the TCP peer is in Cloudflare's published IP ranges (Traefik `forwardedHeaders.trustedIPs`); otherwise any client could fake its IP and dodge rate limits.

**Custom domains**

1. `add_domain(app, "www.example.com")` stores it as `pending_dns` and returns two records to create: `CNAME www.example.com → edge.bitploy.nl` (or an A record to the server IP for an apex domain), and `TXT _bitploy-challenge.www.example.com = <token>`.
2. If the user's domain is itself on Cloudflare, tell them to set that CNAME to DNS only.
3. `check_domain` (and a background check every 5 minutes for 48 hours) resolves through 1.1.1.1 and 8.8.8.8. It requires the TXT token, plus a CNAME chain ending at `edge.bitploy.nl` or an A record equal to the server IP.
4. When both pass, add a router with `certResolver: letsencrypt` (HTTP-01 on port 80) and move through `issuing_cert` to `active`. Errors stay readable: "CNAME points to parking.godaddy.com, expected edge.bitploy.nl".

The TXT token keeps a user from claiming someone else's domain that already points at bitploy, which matters once the platform opens up. Limit: 5 custom domains per app; one hostname belongs to one app.

**Route file** written by bitploy for each app:

```yaml
http:
  routers:
    app-quiet-otter-3f9a:
      rule: Host(`quiet-otter-3f9a.bitploy.nl`)
      entryPoints: [websecure]
      service: app-quiet-otter-3f9a
      tls: {}
    app-quiet-otter-3f9a-www-example-com:
      rule: Host(`www.example.com`)
      entryPoints: [websecure]
      service: app-quiet-otter-3f9a
      tls:
        certResolver: letsencrypt
  services:
    app-quiet-otter-3f9a:
      loadBalancer:
        servers:
          - url: http://bp-quiet-otter-3f9a-7:8080
```

Traefik joins each app network `bp-<slug>` so it can reach `bp-<slug>-<n>` by name. A swap rewrites only the server URL.

**Password protection.** When an app has it on, the route file also holds a `basicAuth` middleware and the subdomain router and every custom domain router list it:

```yaml
    app-quiet-otter-3f9a:
      middlewares: [app-quiet-otter-3f9a-auth]
  middlewares:
    app-quiet-otter-3f9a-auth:
      basicAuth:
        users: ["team:$2a$08$..."]   # bcrypt hash, cost 8; the password itself is never stored
        realm: Private app
        removeHeader: true           # the app never sees the login
```

The internal candidate and live routers (`*.bitploy.internal`) never get it, so health checks and the deploy swap work as before. The middleware is part of the one route file, so deploys, rollbacks, restarts and the reconciler keep it. Usernames are limited to `A-Za-z0-9._@+-` because Traefik reads route files as templates and splits `user:hash` on `:`. The 401 a visitor gets before the browser sends the login is not counted as traffic (Traefik's access log keeps `OriginStatus`, which is 0 when Traefik answered itself).

## Dashboard

The dashboard is read-mostly: people watch their apps, read logs, manage tokens and do a few safe actions. Deploying is mostly the agent's job, but people can also make an app themselves (New app: upload a folder or zip, or pick a GitHub repository) and upload a new version of an uploaded app. It calls only `/api/v1`, never a private endpoint.

Layout: shadcn-vue `Sidebar` with Apps, Tokens, Connect AI, Settings and (admins only) Admin; a top bar with the user menu and a light/dark toggle.

| Page | Shows | Actions |
| --- | --- | --- |
| Login, Accept invite | Email and password form | Log in, set password |
| Overview `/` | Cards: apps running of total, memory used of quota, CPU now, deploys this week. Table: name + URL, status badge, CPU %, memory MB, uptime, last deploy | Open an app, New app |
| New app `/new` | Name (the address follows it, with a live preview and the slug rules), source (GitHub: repository and branch, deploys on every push; Upload: drop a folder, a .zip or a single .html page), "Keep files in /data". For uploads it shows "Detected: Static site / Dockerfile" before deploying, with an override, and a "Home page" select when there are several pages and no index.html | Deploy: creates the app and opens its page with live progress |
| App `/apps/:slug` → Overview | URL with copy (and a "Private" lock while password-protected), status, uptime, current deployment and its source, CPU and memory charts for 1 h / 24 h / 7 d | Redeploy, restart, stop, start; Copy for AI when the app failed or keeps crashing |
| App → Deployments | State badge, duration, channel (MCP, API, dashboard), commit or upload, error and hint | Rollback to a row; Copy for AI on a failed row (short text with the error and the last 70 log lines, to paste into an assistant); Upload new version (apps that are not linked to git) |
| App → Logs | Build and runtime logs, live tail, filter box | Download |
| App → Domains | Subdomain; custom domains with the DNS records to copy and their status | Add, check now, remove |
| App → Storage | Backups, automatic backup settings and the /data file browser (download, upload or replace a file, make or delete a folder). A SQLite file (`.db`, `.sqlite`, `.sqlite3`, `.db3`) has an Open action | Back up, restore, upload, download; Open a database |
| App → Storage → Database `/apps/:slug/storage/db?path=` | The tables and views of a SQLite file in /data with their columns and indexes; a data grid with paging, sorting and filters; a SQL console | Add, edit and delete rows (tables with a rowid or a primary key), run SQL (read-only until writes are turned on) |
| App → Settings | Source (linked repo, branch, folder, deploy on every push), password protection (switch, username, write-only password), public port for apps without a web address (switch, close after 1 h / 24 h / never, allow list with "only my IP", address and connection URL with copy, password hidden until Show), env vars with their values (a hidden one masked), memory and CPU within quota (fixed for a static site) | Edit; delete app (type the slug to confirm) |
| Tokens | Name, prefix, scope, created, last used, expiry | Create (value shown once), revoke |
| Connect AI | Copy-paste setup per client, plus a starter prompt | Copy |
| Settings | Profile, password, active sessions, GitHub (connected accounts, choose repos) | Change, connect or disconnect GitHub, sign out everywhere |
| Admin | Users with app count, usage and quota; invites; server CPU, RAM, disk, Docker disk use, build queue; the public port range and the open ports; a read-only view of bitploy's own database (secrets hidden) | Invite, edit quota, disable user, stop any app, close any public port |

**Connect AI page** — the page noobs will use most. Prefill the token they just created, and verify each snippet against the client's current docs when you build it:

```bash
# Claude Code
claude mcp add --scope user --transport http bitploy https://bitploy.nl/mcp \
  --header "Authorization: Bearer bp_xxx"
```

```json
{ "mcpServers": { "bitploy": {
    "type": "http",
    "url": "https://bitploy.nl/mcp",
    "headers": { "Authorization": "Bearer bp_xxx" } } } }
```

Starter prompt to copy: *"Use bitploy to put this folder online. Pick a short name, add a Dockerfile if it needs one, and send me the link."*

**UX rules**

- Empty states say what to do next: "No apps yet. Open Connect AI, then tell your assistant: deploy this folder."
- Plain status words with colour: Building, Running, Failed, Stopped. Relative times with the exact time on hover.
- Skeletons while loading, toasts after actions, a confirm dialog for anything destructive.
- Overview and app pages refetch every 5 s with Vue Query; logs stream over SSE.
- Works at phone width; dark mode from shadcn-vue theme tokens.

## Metrics & usage

Bitploy samples every running container every 15 s, keeps raw samples for 24 hours and 5-minute rollups for 30 days. That is enough for the dashboard charts and usage numbers without Prometheus or any other metrics stack.

| Metric | Source | Shown as |
| --- | --- | --- |
| CPU | Docker stats: CPU delta ÷ system delta × online CPUs | % of one core |
| Memory | Usage minus `inactive_file` cache | MB and % of the app's limit |
| Network | Docker stats per interface | Bytes in and out per interval |
| Uptime, restarts | Container `State.StartedAt`, `RestartCount` | "Up 3 d 4 h", restart count |
| Build time | Deployment timestamps | Seconds per deployment, minutes per month |
| Running hours | Sum of running time per app per day | Hours per month |
| Disk | Image size; volume size measured nightly | MB |
| Host | `/proc` CPU, RAM, disk, load | Admin page only: the numbers now, plus CPU % and memory used of total over time |

How to collect it:

- Open one streaming stats connection per running container (a goroutine each) and keep the latest sample in memory; a 15 s ticker writes them in one transaction. One-shot stats calls are slow because Docker samples twice per call.
- Subscribe to the Docker events stream for `die`, `oom` and `health_status`. Update the app state at once and write an audit row. An OOM kill shows in the dashboard and in `get_app` as "Out of memory: raise the memory limit or use less".
- Tables: `metric_samples(app_id, ts, cpu, mem, rx, tx)` and `metric_rollups` with the same columns plus max values. A nightly job prunes and rolls up.
- The server itself is sampled on the same 15 s tick from `/proc/stat` and `/proc/meminfo` (the source of the admin Server numbers): `host_samples(ts, cpu, mem_used, mem_total)` and `host_rollups` with max values, rolled up and pruned by the same rules. CPU is % of the whole server between two ticks; memory is used without reclaimable cache. `GET /api/v1/admin/server/metrics?window=1h|24h|7d` returns the series for the charts on the admin Server tab.

Per-user usage (returned by `whoami`, `GET /api/v1/usage` and the Overview cards): apps, memory reserved against quota (static sites excluded), build minutes, running hours and deployments this calendar month, and disk used.

## Security & limits

User code is untrusted. Each app runs as one locked-down container with hard limits on its own network, and nothing it does can reach the Docker socket, the bitploy database, the host or another app.

**Container defaults**

| Setting | Value |
| --- | --- |
| Memory | 512 MB default; swap equal to memory, so no extra swap |
| CPU | 0.5 CPU |
| Static sites | A static site is an app whose live deployment was built from bitploy's own nginx templates (a plain website, or a Vite site built by auto mode), never from the user's own Dockerfile, even if that starts from nginx. It has a fixed 64 MB and 0.5 CPU (`cpu_millis` 500), cannot be changed, has no `/data` volume, and does not count towards the memory and CPU quota. It still counts as an app and for storage. The App JSON carries `"static": true`. A static site that is later deployed from the user's own Dockerfile becomes a normal app (default size, checked against the quota at deploy time) |
| Processes | `pids-limit` 256; `nofile` 4096 |
| Privileges | `no-new-privileges`, Docker's default seccomp and AppArmor profiles, drop `NET_RAW`; never `--privileged`, never added capabilities |
| Mounts | Only the optional named volume at `/data` (a compose app: its named volumes, one service each). Never `docker.sock`, never a host path |
| Network | Own bridge network `bp-<slug>` that only Traefik joins; no published ports (a public port publishes the port of a separate forwarder container, see Public ports) |
| Logs | `json-file`, `max-size` 10 MB, `max-file` 3 |
| Restarts | `unless-stopped`; 5 restarts within 5 minutes → stop it and mark it `crashed` with the log tail |

**Compose security**: a compose file is input, never a runtime: every service gets the same container defaults as an app (limits, no-new-privileges, dropped capabilities, no published ports, logs). Refused with `compose_forbidden`, naming the service and key: `privileged`, `cap_add`, `devices`, `security_opt`, `sysctls`, `ulimits`, `network_mode`, `pid`, `ipc`, `userns_mode`, `volumes_from`, `extra_hosts`, `secrets`, `configs`, `extends`, custom networks, host bind mounts and the Docker socket, `external` or driver volumes, `include`, build contexts or Dockerfiles outside the project, `build.network`, `build.ssh`, `build.secrets`, `build.additional_contexts`. `ports` are never published (only the public service gets the route). `image:` services are pulled by the isolated builder, not the daemon, so the base-image rules apply. Env files must be inside the project. `${VAR}` only reads the app's own variables.

**Network isolation**

- Set `default-address-pools` in `daemon.json` to `10.200.0.0/16` in `/24` blocks. Docker's default pools run out after about 30 networks, and every app needs one.
- In `deploy/firewall.sh`, block app subnets from the host itself (INPUT), from the Hetzner metadata address `169.254.169.254` and from private ranges (DOCKER-USER chain). Outbound internet stays open. Published ports into the app subnets pass only for the public port range (and 80/443). A CI test runs a probe container and asserts each rule.

**Public ports** (off unless `BITPLOY_PUBLIC_PORTS` is set): the owner of an app without a web address (a service template like postgres or meilisearch, a worker, a non-public service of a compose app) can open a port of the range for a while, to connect with psql, TablePlus or redis-cli. bitploy picks the lowest free port and keeps it while the port is open; it is released when the port closes or the app is deleted.

- A forwarder container `bitploy-port-<app id>` (`bitploy port-proxy`, from the database helper image) joins only `bp-<slug>` and forwards to the service (`app:5432`). It is the only container with a published port, IPv4 only (`0.0.0.0`). It runs as nobody with no capabilities, `no-new-privileges`, a read-only root, 64 MB, 64 processes, 0.25 CPU, at most 64 connections, 8 of them from one address, each closed after 30 minutes without bytes in either direction. The app container keeps no published port and does not restart.
- Web apps (and the public service of a compose app) are refused with `public_port_web_app`: the port would go around HTTPS and password protection.
- Closes by itself after 1 h unless `close_after` says 24 h or never. The forwarder enforces the expiry itself (it closes its listener and open connections), and the reconciler removes the port and writes `app.public_port_closed` with reason `expired`, also after a restart.
- An optional allow list of IPv4 addresses or networks (at most 20); `only_my_ip` adds the caller's address. The forwarder refuses other addresses.
- At most `BITPLOY_PUBLIC_PORTS_PER_USER` (3) open per user (`public_port_limit`). Audit rows: `app.public_port_opened`, `_changed`, `_closed` (reason `closed`, `expired`, `admin`, `app_deleted`, `public_ports_off`), `_moved`, `_revealed`; never the password.
- The App JSON has `public_port` with `address` (host:port), and for service templates `connection_url` with the generated passwords as `********`. The owner can read the URL with the password (`POST …/public-port/reveal`, audited): the one exception to "nobody can read env var values back", for generated passwords of service templates only, because a deploy token can already pass them to an app it deploys with `${{<alias>.VAR}}`.
- Apps cannot use another app's public port through the server's address: after Docker's DNAT that is app-to-app traffic, which the firewall drops.

**Quotas** (per user, editable by admins): 10 apps, 2 GB memory and 2 CPUs in total, 20 deployments per hour, 5 custom domains per app, 25 GB of storage in total for app files (kept images, stored source) and the /data volumes, each volume with its own limit (5 GB by default, warning at 80%).

**Protecting the one server**

- Keep the sum of all memory limits under the host RAM minus 1.5 GB reserved for Docker, Traefik and bitploy. Past that, `create_app` and `update_app` fail with `server_full` and a hint to tell the admin.
- Refuse new builds when the disk has under 15% free; prune images and build cache nightly.
- Build steps run untrusted code. For colleagues that is acceptable with the 30-minute timeout; the public checklist below moves builds out.

**Control plane**

- Only the bitploy container mounts `docker.sock`, which is root-equivalent: SSH keys only on the host, no other services on it.
- `BITPLOY_SECRET_KEY` (32 random bytes) lives in an env file with mode 0600. Losing it means losing env vars and the GitHub App's credentials, so the README says to back it up separately.
- Dashboard headers: a strict CSP with no inline scripts, `X-Frame-Options: DENY`, HSTS, `Referrer-Policy: same-origin`.
- Audit log: every change, with user, channel, token ID, IP and target, kept 90 days and visible to admins. Nobody, admins included, can read env var values back.

**Built now so going public is a switch, not a rewrite**

- Config separates `BITPLOY_PUBLIC_URL` (dashboard, API, MCP) from `BITPLOY_APPS_DOMAIN` (app subdomains) from day one, even though both are `bitploy.nl` today.
- Signup mode (`invite` or `open`), quotas, rate limits and an admin "suspend user" exist from v1.

Checklist before opening to the public (not v1 work):

- [ ] Move apps to their own registrable domain (e.g. `bitploy.app`) and add it to the Public Suffix List
- [ ] Run app containers under gVisor (`runsc`) and move builds to an isolated builder with CPU and memory limits
- [ ] Email verification and bot protection on signup
- [ ] Block outbound port 25; add an abuse-report link on app error pages
- [ ] Terms, privacy notice, account export and deletion (GDPR)
- [ ] Cloudflare for SaaS, so custom domains can sit behind the proxy too

## Running bitploy on Hetzner

One Hetzner Cloud server runs everything: Docker, Traefik and bitploy, started from one compose file. About 4 vCPU, 8 GB RAM and 160 GB disk is a sensible start for a group of colleagues; the memory guard in Security keeps it from being overbooked.

**`deploy/install.sh`** (idempotent; safe to run twice):

1. Check for the current Ubuntu LTS; install Docker Engine and the buildx plugin from Docker's apt repository.
2. Write `/etc/docker/daemon.json`: address pools from Security, `json-file` log limits, `"live-restore": true` so apps survive a daemon restart.
3. Apply `deploy/firewall.sh`, enable unattended upgrades (also for the Docker apt repository: `origin=Docker`, so docker-ce, containerd.io and runc get security fixes), allow SSH keys only.
4. Create `/etc/bitploy/bitploy.env` (mode 0600) with a generated `BITPLOY_SECRET_KEY`, and ask for the domain, ACME email and the Cloudflare Origin CA certificate.
5. `docker compose up -d`, wait for `/healthz`, then print the `bitploy admin create-user` command.

In the Hetzner Cloud Firewall, allow 22 from the admin's IP only, and 80 and 443 from anywhere. They cannot be limited to Cloudflare's ranges, because custom domains arrive directly. With public ports on, also allow the `BITPLOY_PUBLIC_PORTS` TCP range, and run `systemctl restart bitploy-firewall` after changing it.

**Config**

| Variable | Example | Meaning |
| --- | --- | --- |
| `BITPLOY_PUBLIC_URL` | `https://bitploy.nl` | Dashboard, API and MCP origin (also the CSRF origin) |
| `BITPLOY_APPS_DOMAIN` | `bitploy.nl` | Parent domain for app subdomains |
| `BITPLOY_EDGE_HOST` | `edge.bitploy.nl` | DNS-only CNAME target for custom domains |
| `BITPLOY_SERVER_IPV4`, `BITPLOY_SERVER_IPV6` | `203.0.113.10` | Used in DNS checks and instructions |
| `BITPLOY_SECRET_KEY` | 32 random bytes, base64 | Encrypts env vars and the GitHub App's credentials |
| `BITPLOY_DATA_DIR` | `/var/lib/bitploy` | Database, uploads, logs, route files |
| `BITPLOY_ACME_EMAIL` | `you@example.nl` | Let's Encrypt account |
| `BITPLOY_SIGNUP` | `invite` | `invite` or `open` |
| `BITPLOY_MAX_PARALLEL_BUILDS` | `2` | Server-wide build slots |
| `BITPLOY_PUBLIC_PORTS` | `30000-30099` | Ports users may open to connect to a database for debugging (Public ports); at most 1000, 1024 and up, not 8080 or 8081. Empty or `off`: none. firewall.sh reads it too |
| `BITPLOY_PUBLIC_PORTS_PER_USER` | `3` | Open public ports per user; 0: nobody can open one |
| `BITPLOY_PUBLIC_PORTS_HOST` | `203.0.113.10` | The address shown for public ports; default `BITPLOY_SERVER_IPV4`, else `BITPLOY_EDGE_HOST`. Not the apps domain: that goes through Cloudflare, which carries HTTP only |
| `BITPLOY_DB_HELPER_BIN` | `/path/to/bitploy-linux` | A bitploy binary built for Linux that the SQLite viewer puts in its helper image. Only for a bitploy that does not run on Linux itself (`make dev` on a Mac sets it); on Linux the running binary is used |

**Compose** (`deploy/docker-compose.yml`, shape only):

```yaml
services:
  traefik:
    image: traefik:v3          # pin a minor version in the real file
    ports: ["80:80", "443:443"]
    volumes:
      - ./traefik:/etc/traefik:ro                     # static config, Cloudflare IP ranges
      - /var/lib/bitploy/traefik/dynamic:/dynamic:ro  # route files written by bitploy
      - /var/lib/bitploy/acme:/acme                   # Let's Encrypt storage
      - /etc/bitploy/origin:/certs:ro                 # Cloudflare Origin CA cert
    restart: unless-stopped
  bitploy:
    image: ghcr.io/<owner>/bitploy:<version>
    env_file: /etc/bitploy/bitploy.env
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - /var/lib/bitploy:/var/lib/bitploy
    restart: unless-stopped
```

Traefik needs no Docker socket: it reads only files. Bitploy attaches the Traefik container to each app network through the Docker API. The bitploy image includes the Docker CLI and buildx plugin for builds.

**Backups and upgrades**

- Nightly: `VACUUM INTO` a timestamped copy of the database, tar each app volume, and send both with restic to a Hetzner Storage Box or S3-compatible bucket. Keep 7 daily and 4 weekly copies. Document a restore drill in the README.
- Take a Hetzner snapshot before upgrades. An upgrade is `docker compose pull && docker compose up -d`; migrations run on start, and apps keep serving while bitploy restarts because Traefik routes straight to them.

**Local development**: `make dev` runs the Go server, Vite with a proxy to it, and Traefik in HTTP-only mode on the local Docker daemon, with `BITPLOY_APPS_DOMAIN=localhost`, so apps open at `http://<slug>.localhost:8000`.

## Milestones, acceptance & non-goals

| # | Milestone | Done when |
| --- | --- | --- |
| M0 | Skeleton: repo layout, Makefile, `/healthz`, SQLite and migrations, embedded shadcn-vue shell, CI (`go vet`, staticcheck, tests, `vue-tsc`, eslint) | `make build` produces one binary that serves the dashboard shell |
| M1 | Auth: invites, argon2id, sessions, CSRF, API tokens, audit log | A POST from an `https://x.bitploy.nl` origin with a valid cookie is rejected; a revoked token gets 401 on its next request |
| M2 | Deploy core: inline files, zip uploads, Dockerfile checks, build, run, health check, swap, route files, subdomains, logs, reconciler | A zip with `index.html` and the static Dockerfile is live over HTTPS in under 2 minutes; a broken deploy leaves the old version serving; killing bitploy mid-build leaves no orphans after restart |
| M3 | MCP: all tools, instructions, templates, resources, prompt | A fresh agent with only bitploy's MCP and a token deploys a folder holding one `index.html` and no Dockerfile, unaided; given a Node app on `localhost`, it follows the hint and fixes it |
| M4 | Git sources: public git, and GitHub through the GitHub App (private repos, push to deploy) | A private repo deploys through the app installation, a push to its branch deploys it, and no token appears in any log, image layer (`docker history`) or API response |
| M5 | Dashboard and metrics: every page, charts, Connect AI | A non-technical colleague goes from invite to a live app in under 10 minutes without help |
| M6 | Custom domains | A domain outside Cloudflare goes from `add_domain` to `active` with a valid certificate; a wrong CNAME gives a readable error |
| M7 | Hardening and ops: quotas, rate limits, firewall and probe test, crash-loop detection, backups, `install.sh` | A fresh Hetzner server reaches its first deploy within 15 minutes of `install.sh`; the probe container cannot reach the host, the metadata address or another app |

**How to work**

- One error type everywhere: `apperr.Error{Code, Message, Hint, Next, HTTPStatus}`. MCP and REST render it; nothing else invents error text.
- Table tests for Dockerfile checks, zip extraction (include zip-slip and zip-bomb fixtures), slug rules, auth middleware and CSRF. Integration tests against a real Docker daemon behind `-tags=integration`.
- When this prompt is ambiguous, pick the simpler option, write it down in `docs/decisions.md`, and mention it in the milestone summary. Do not add features that are not here.

**Not in v1**

- More than one server: no clustering, Swarm or Kubernetes.
- Managed databases with backups, failover or upgrades run by bitploy: a service-template app is a container with a volume. PostgreSQL, MySQL and MariaDB are backed up as a dump made by their own tool while they run (a copy of the files while stopped); Valkey, Redis and Meilisearch hold data that can be rebuilt and get no backups. Its image version stays at the one it was made with.
- Buildpacks or Nixpacks: a server is built from a Dockerfile, the user's or one of bitploy's templates (build modes `auto`, `node`, `static_build`, `static`).
- UDP ports, and raw TCP ports for web apps: web traffic is HTTP(S) through Traefik. The one TCP exception is a public port, which opens a database or other app without a web address for a while (see Public ports).
- Billing, teams and shared apps.
- Next after v1: preview deployments per branch, push webhooks for other git hosts, and MCP OAuth for clients that only connect through OAuth.
