# Bitploy — Build Prompt

Oct 29, 2026 · Arno

Defaults picked where the brief was open:

| Decision | Default in this prompt | Why |
| --- | --- | --- |
| Signup | Invite-only now; `BITPLOY_SIGNUP=open` later | Colleagues first; may go public, so public is a config switch, not a rewrite |
| Database | One SQLite file (WAL mode) | One server, nothing to operate |
| Reverse proxy | Traefik v3; bitploy writes the routes as files | Zero-downtime swaps, routing owned by bitploy |
| Control plane | `bitploy.nl` serves dashboard, `/api/v1` and `/mcp` | Apps live on `*.bitploy.nl` |
| App shape | One container per app, optional `/data` volume | No compose or managed databases in v1 |
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
- Each user installs the app on their own account or organizations from Settings, GitHub. Bitploy links an installation only after exchanging the OAuth code and finding the installation in `GET /user/installations`; an `installation_id` in a URL is never trusted alone.
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

**The Dockerfile rule**: a deploy without a Dockerfile at `dockerfile_path` fails at validation with `dockerfile_missing`. There is no buildpack or auto-detect build path. Bitploy does detect the stack only to make the error helpful: `index.html` only → `static`, `package.json` → `node`, `requirements.txt` or `pyproject.toml` → `python`, `go.mod` → `go`, `composer.json` or `index.php` → `php`. The error names the matching template and includes its full text.

**Pipeline**, one row per deployment state:

| State | What happens | Fails with |
| --- | --- | --- |
| `queued` | Waits for a build slot | — |
| `fetching` | Source copied or cloned into `builds/<deployment_id>/` | `source_*` codes |
| `validating` | Dockerfile checks (MCP section); stops before any build | `dockerfile_*` codes |
| `building` | `docker buildx build`, 15 min timeout, image `bitploy/<slug>:<n>` | `build_failed` + last 50 log lines |
| `starting` | Container `bp-<slug>-<n>` on network `bp-<slug>` with env, limits, volume | `start_failed` |
| `health_check` | Port open, then `GET health_path` returns < 500 within 60 s (max 300 s); a Dockerfile `HEALTHCHECK` replaces this | `unhealthy` + last 50 runtime log lines |
| `swapping` | Route file points at the new container; old one stops after 10 s | — |
| `running` | Live; previous deployment marked `superseded` | — |
| `failed` | Old container keeps serving; error code, hint and log tail stored | — |

With a volume attached, use **recreate** instead of rolling: stop the old container, start the new one, and restart the old one automatically if the new one fails. Two writers on one SQLite file is worse than a few seconds of downtime.

**Runtime contract** for every app:

- Listen on `0.0.0.0:$PORT`. Bitploy injects `PORT`, `BITPLOY_APP` and `BITPLOY_URL`. The port comes from the app setting, else the single `EXPOSE` in the final stage.
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

1. Every app is built from a Dockerfile. Bitploy refuses to deploy without one.
   If the project has none, call get_template with the stack (static, node,
   node-spa, python, go, php, generic), adapt it, and add a .dockerignore.
2. The app must listen on 0.0.0.0 on the EXPOSE port (also in $PORT), never on
   localhost or 127.0.0.1.
3. Call validate_dockerfile before every deploy and fix all errors.
4. Never put secrets in the Dockerfile or the source. Use set_env.
5. Send the code. If the project already has a GitHub remote, prefer git:
   set_git_source with its HTTPS URL, then deploy once. After that every push
   to the branch deploys by itself: update by committing and pushing, and
   follow it with get_deployment (app=<slug>, wait_seconds=30). Otherwise
   upload, which is fully supported: up to 2 MB and 200 files deploy_files;
   bigger folders zip them, call create_upload and run the curl command it
   returns. Never ask the user to create a repository or install git just to
   deploy; at most mention once that connecting GitHub makes updates automatic.
   An app linked to a repository refuses uploads (git_source_linked): push.
6. A deployment (deploy, deploy_files, or a push to a linked repository) builds
   in the background. Call get_deployment with wait_seconds=30 until the state
   is running or failed. On failure, read error.hint and the log tail, fix, and
   send the fix again. After 3 failures with the same error code, stop and
   explain the problem to the user in plain words.
7. When it runs, give the user the URL and one or two plain sentences on what
   you did. Assume the user is not technical.
8. Files that must survive a redeploy go in /data, and only after turning the
   volume on with update_app.
9. Never stop, delete or roll back an app the user did not name.
```

**Tools**

| Tool | Does | Scope | Hints |
| --- | --- | --- | --- |
| `whoami` | User, quotas, usage, base domain | read | read-only |
| `get_deploy_rules` | The rules above plus the runtime contract | read | read-only |
| `get_template(stack)` | Dockerfile + .dockerignore for a stack | read | read-only |
| `validate_dockerfile(dockerfile, file_list?)` | Static checks below; no build | read | read-only |
| `list_apps` / `get_app(app)` | Apps with URL, state, current deployment | read | read-only |
| `create_app(name, type?, slug?, port?, volume?)` | Reserves a subdomain, returns the URL | deploy | — |
| `update_app(app, …)` | Port, health path, volume, resources | deploy | idempotent |
| `deploy_files(app, files[])` | Inline source, validates, queues a deployment | deploy | — |
| `create_upload(app)` | One-time URL (15 min) and the exact `curl` command | deploy | — |
| `set_git_source(app, repo_url?, branch?, subdir?, auto_deploy?, remove?)` | Saves the source and checks access with `ls-remote`; `auto_deploy` (default on for GitHub) deploys on every push; `remove: true` unlinks | deploy | idempotent |
| `deploy(app, upload_id?)` | Queues a deployment from the upload or git source | deploy | — |
| `get_deployment(id?, app?, wait_seconds?)` | State, timings, error, last 50 log lines; with only `app` it returns the latest deployment | read | read-only |
| `list_deployments(app)` | History, newest first | read | read-only |
| `get_logs(app, kind, tail?, since?)` | `build` or `runtime` logs, max 1,000 lines | read | read-only |
| `set_env(app, set, unset)` | Encrypted env vars; values are never returned | deploy | idempotent |
| `restart_app` / `start_app` / `stop_app` | Lifecycle | deploy | stop: destructive |
| `rollback(app, deployment_id)` | Runs a previous image | deploy | destructive |
| `delete_app(app, confirm_slug)` | Removes containers, images, routes, volume | deploy | destructive |
| `add_domain` / `check_domain` / `remove_domain` | Custom domains (Domains section) | deploy | — |
| `get_metrics(app, window)` | CPU, memory, network for `1h`, `24h` or `7d` | read | read-only |
| `create_backup(app)` / `list_backups(app)` | Back up /data in the background; list backups, settings and restore state. Restoring is dashboard-only on purpose | deploy / read | — / read-only |

Also expose the rules as resource `bitploy://rules` and each template as `bitploy://templates/{stack}`, and add a prompt `deploy_project` that walks through the eight steps. Keep the tools anyway: many clients ignore resources and prompts.

**Dockerfile checks** (one Go function, run by `validate_dockerfile` and again in the `validating` state):

| Code | Severity | Rule |
| --- | --- | --- |
| `dockerfile_missing` | error | No file at `dockerfile_path` |
| `dockerfile_parse_error` | error | BuildKit parser fails; report the line |
| `no_from` | error | No `FROM` instruction |
| `port_unknown` | error | No `EXPOSE` in the final stage and no app port |
| `port_ambiguous` | error | Several `EXPOSE` ports and no app port |
| `image_too_large` | error | Built image over 2 GB (checked after build) |
| `env_file_in_context` | warning | `.env` would be copied into the image; suggest `set_env` and `.dockerignore` |
| `no_dockerignore` | warning | `node_modules`, `.git` or `venv` present without a `.dockerignore` |
| `unpinned_base` | warning | Base image has no tag or uses `latest` |
| `runs_as_root` | warning | Final stage has no `USER` |
| `no_healthcheck` | info | Bitploy will use its HTTP check on `health_path` |

When `unhealthy` fires and the runtime log mentions `127.0.0.1` or `localhost`, set the hint to "Your app listens on localhost; make it listen on 0.0.0.0:$PORT". That is the most common noob failure.

**Templates** live in `internal/templates/<stack>/` with a Dockerfile, a .dockerignore and a short README, pinned to current major versions: `static`, `node`, `node-spa`, `python`, `go`, `php`, `generic`. The static one shows the style:

```dockerfile
FROM nginxinc/nginx-unprivileged:alpine
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
| `GET` `POST /api/v1/apps` | `list_apps`, `create_app` |
| `GET` `PATCH` `DELETE /api/v1/apps/{app}` | `get_app`, `update_app`, `delete_app` |
| `POST /api/v1/apps/{app}/files` | `deploy_files` |
| `POST /api/v1/apps/{app}/uploads` | `create_upload` |
| `POST /u/{upload_id}` | Raw zip body; the unguessable ID is the credential, single use, 15 min |
| `PUT` `DELETE /api/v1/apps/{app}/git-source`, `PATCH` for `auto_deploy` | `set_git_source`, with `remove: true` to unlink |
| `GET` `POST /api/v1/apps/{app}/deployments` | `list_deployments`, `deploy` |
| `GET /api/v1/deployments/{id}?wait=30` | `get_deployment` with long-poll |
| `GET /api/v1/deployments/{id}/log[?follow=1]` | Build log as text or SSE |
| `GET /api/v1/apps/{app}/logs?kind=runtime&tail=200[&follow=1]` | `get_logs` |
| `PUT /api/v1/apps/{app}/env` | `set_env` (names and masked values only on read) |
| `POST /api/v1/apps/{app}/restart`, `/start`, `/stop`, `/rollback` | Lifecycle tools |
| `GET` `POST` `DELETE /api/v1/apps/{app}/domains[/{host}]`, `POST …/{host}/check` | Domain tools |
| `GET /api/v1/apps/{app}/metrics?window=24h` | `get_metrics` |
| `GET` `POST /api/v1/apps/{app}/backups` | `list_backups`, `create_backup` (restore, download, upload and settings are dashboard and REST only) |
| `/api/v1/admin/users`, `/invites`, `/quotas`, `/server` | Admin only |

Serve the generated spec at `/api/v1/openapi.json` and a read-only reference page at `/docs/api`.

## Domains & routing

Every app gets `https://<slug>.bitploy.nl` the moment it is created, through Cloudflare's proxy. Custom domains skip the proxy and get their own Let's Encrypt certificate on the server, because a custom domain cannot CNAME to a proxied `bitploy.nl` record: Cloudflare answers error 1001 for domains outside Cloudflare and error 1014 for domains in another Cloudflare account. Cloudflare for SaaS would fix that, but it is out of scope for v1.

**Subdomains**

- Default slug: `adjective-noun-xxxx` with 4 hex characters, e.g. `quiet-otter-3f9a`. Users may rename it: `a-z`, `0-9` and `-`, 3 to 40 characters, unique, not reserved. An old slug stays reserved for its owner for 30 days.
- One level only. `api.myapp.bitploy.nl` is not allowed: Universal SSL covers the apex and first-level subdomains only.
- Reserved: `www app api mcp admin dashboard docs status mail edge custom cdn static assets auth login bitploy traefik`, and anything starting with `bp-`.
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

## Dashboard

The dashboard is read-mostly: people watch their apps, read logs, manage tokens and do a few safe actions. Deploying is the agent's job, though a Redeploy button is fine. It calls only `/api/v1`, never a private endpoint.

Layout: shadcn-vue `Sidebar` with Apps, Tokens, Connect AI, Settings and (admins only) Admin; a top bar with the user menu and a light/dark toggle.

| Page | Shows | Actions |
| --- | --- | --- |
| Login, Accept invite | Email and password form | Log in, set password |
| Overview `/` | Cards: apps running of total, memory used of quota, CPU now, deploys this week. Table: name + URL, status badge, CPU %, memory MB, uptime, last deploy | Open an app |
| App `/apps/:slug` → Overview | URL with copy, status, uptime, current deployment and its source, CPU and memory charts for 1 h / 24 h / 7 d | Redeploy, restart, stop, start |
| App → Deployments | State badge, duration, channel (MCP, API, dashboard), commit or upload, error and hint | Rollback to a row |
| App → Logs | Build and runtime logs, live tail, filter box | Download |
| App → Domains | Subdomain; custom domains with the DNS records to copy and their status | Add, check now, remove |
| App → Settings | Source (linked repo, branch, folder, deploy on every push), env var names with masked values, volume, memory and CPU within quota | Edit; delete app (type the slug to confirm) |
| Tokens | Name, prefix, scope, created, last used, expiry | Create (value shown once), revoke |
| Connect AI | Copy-paste setup per client, plus a starter prompt | Copy |
| Settings | Profile, password, active sessions, GitHub (connected accounts, choose repos) | Change, connect or disconnect GitHub, sign out everywhere |
| Admin | Users with app count, usage and quota; invites; server CPU, RAM, disk, Docker disk use, build queue | Invite, edit quota, disable user, stop any app |

**Connect AI page** — the page noobs will use most. Prefill the token they just created, and verify each snippet against the client's current docs when you build it:

```bash
# Claude Code
claude mcp add --transport http bitploy https://bitploy.nl/mcp \
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
| Host | `/proc` CPU, RAM, disk, load | Admin page only |

How to collect it:

- Open one streaming stats connection per running container (a goroutine each) and keep the latest sample in memory; a 15 s ticker writes them in one transaction. One-shot stats calls are slow because Docker samples twice per call.
- Subscribe to the Docker events stream for `die`, `oom` and `health_status`. Update the app state at once and write an audit row. An OOM kill shows in the dashboard and in `get_app` as "Out of memory: raise the memory limit or use less".
- Tables: `metric_samples(app_id, ts, cpu, mem, rx, tx)` and `metric_rollups` with the same columns plus max values. A nightly job prunes and rolls up.

Per-user usage (returned by `whoami`, `GET /api/v1/usage` and the Overview cards): apps, memory reserved against quota, build minutes, running hours and deployments this calendar month, and disk used.

## Security & limits

User code is untrusted. Each app runs as one locked-down container with hard limits on its own network, and nothing it does can reach the Docker socket, the bitploy database, the host or another app.

**Container defaults**

| Setting | Value |
| --- | --- |
| Memory | 512 MB default; swap equal to memory, so no extra swap |
| CPU | 0.5 CPU |
| Processes | `pids-limit` 256; `nofile` 4096 |
| Privileges | `no-new-privileges`, Docker's default seccomp and AppArmor profiles, drop `NET_RAW`; never `--privileged`, never added capabilities |
| Mounts | Only the optional named volume at `/data`. Never `docker.sock`, never a host path |
| Network | Own bridge network `bp-<slug>` that only Traefik joins; no published ports |
| Logs | `json-file`, `max-size` 10 MB, `max-file` 3 |
| Restarts | `unless-stopped`; 5 restarts within 5 minutes → stop it and mark it `crashed` with the log tail |

**Network isolation**

- Set `default-address-pools` in `daemon.json` to `10.200.0.0/16` in `/24` blocks. Docker's default pools run out after about 30 networks, and every app needs one.
- In `deploy/firewall.sh`, block app subnets from the host itself (INPUT), from the Hetzner metadata address `169.254.169.254` and from private ranges (DOCKER-USER chain). Outbound internet stays open. A CI test runs a probe container and asserts each rule.

**Quotas** (per user, editable by admins): 10 apps, 2 GB memory and 2 CPUs in total, 20 deployments per hour, 5 custom domains per app, 25 GB of storage in total for app files (kept images, stored source) and the /data volumes, each volume with its own limit (5 GB by default, warning at 80%).

**Protecting the one server**

- Keep the sum of all memory limits under the host RAM minus 1.5 GB reserved for Docker, Traefik and bitploy. Past that, `create_app` and `update_app` fail with `server_full` and a hint to tell the admin.
- Refuse new builds when the disk has under 15% free; prune images and build cache nightly.
- Build steps run untrusted code. For colleagues that is acceptable with the 15-minute timeout; the public checklist below moves builds out.

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
3. Apply `deploy/firewall.sh`, enable unattended upgrades, allow SSH keys only.
4. Create `/etc/bitploy/bitploy.env` (mode 0600) with a generated `BITPLOY_SECRET_KEY`, and ask for the domain, ACME email and the Cloudflare Origin CA certificate.
5. `docker compose up -d`, wait for `/healthz`, then print the `bitploy admin create-user` command.

In the Hetzner Cloud Firewall, allow 22 from the admin's IP only, and 80 and 443 from anywhere. They cannot be limited to Cloudflare's ranges, because custom domains arrive directly.

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
- Multi-container apps (Compose) and managed databases. Apps use SQLite in `/data` or an external database through env vars.
- Any build path without a Dockerfile: no buildpacks, no Nixpacks.
- Raw TCP or UDP ports; only HTTP(S) through Traefik.
- Billing, teams and shared apps.
- Next after v1: preview deployments per branch, push webhooks for other git hosts, and MCP OAuth for clients that only connect through OAuth.
