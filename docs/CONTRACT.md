# Build contract for parallel agents

Five agents build bitploy at the same time in one working tree. Read SPEC.md
(the product spec) and this file before writing code.

## Shared foundation (already written — read, don't rewrite)

| File | What |
| --- | --- |
| `internal/apperr/apperr.go` | The one error type `apperr.Error{Code, Message, Hint, Next, HTTPStatus, Details}`, helpers, `apperr.As`, `apperr.Redact` |
| `internal/ids/ids.go` | Prefixed IDs (`ids.New(ids.App)`), `Base62Bytes`, `Hex` |
| `internal/config/config.go` | All `BITPLOY_*` settings, data-dir paths, `AppURL(slug)` |
| `internal/store/store.go`, `crypto.go` | SQLite open (WAL), goose migrations, `Store.Tx`, AES-256-GCM `Crypto.Seal/Open`, `HashToken` |
| `internal/store/migrations/00001_init.sql` | The full schema. All timestamps are unix seconds |
| `internal/store/queries/core.sql` | Shared queries (GetUser, GetApp, GetAppBySlug, InsertAudit, …) |
| `internal/store/db/` | sqlc output — generated, never hand-edit |
| `internal/app/service.go` | `Service`, `Actor`, `Channel`, `Scope`, `Audit`, `RequireDeploy/Session/Admin`, `LoadApp`, long-poll hub |
| `internal/app/types.go` | Every public JSON shape (App, Deployment, Token, …) and input type |
| `internal/dockerx/dockerx.go` | moby client wrapper, naming helpers (`NetworkName`, `ContainerName`, `ImageName`, `VolumeName`), labels |
| `internal/proxy/proxy.go` | Traefik route files (`Writer.Write(Route)`, candidate route for health checks, catch-all) |
| `cmd/bitploy/main.go` | serve / migrate / admin commands, wiring |
| `web/embed.go` | `go:embed` of `web/dist` |

## Ownership

Each agent owns its files. **Never edit a file you don't own.** Stubs with the
right signatures already exist for every service method; implement the
bodies, keep the signatures. If you need something from another area that
isn't there, add a private helper in your own file rather than editing theirs.

| Agent | Owns |
| --- | --- |
| **auth-api** | `internal/auth/**`, `internal/app/accounts*.go`, `internal/api/**`, `cmd/bitploy/**`, queries `accounts.sql`, `api.sql`, migrations `00010`–`00019` |
| **deploy** | `internal/deploy/**` except `dockerfile*.go` and `git*.go`; `internal/dockerx/**` except `stats.go`, `events.go`; `internal/proxy/**`; `internal/app/apps*.go` plus new files `internal/app/deploy_*.go`; queries `apps.sql`, `deployments.sql`; migrations `00020`–`00029` |
| **mcp** | `internal/mcp/**`, `internal/templates/**`, `internal/deploy/dockerfile*.go`, `internal/deploy/git*.go`, `internal/app/catalog*.go`; queries `catalog.sql`; migrations `00030`–`00039` |
| **web** | `web/**` except `web/embed.go` |
| **ops** | `internal/domains/**`, `internal/metrics/**`, `internal/app/ops*.go`, `internal/dockerx/stats.go`, `internal/dockerx/events.go`, `deploy/**`, root `Dockerfile`, `.dockerignore`, `README.md`, `.github/**`; queries `ops.sql`; migrations `00040`–`00049` |

Shared files (`service.go`, `types.go`, `config.go`, `apperr.go`) may get
**small additive edits** (a new field, a new helper) when there is no other
way. Re-read the file right before editing, keep the edit minimal, never
rename or remove anything, and mention it in your final summary.

## Rules

- **Database**: add queries only to your own `.sql` file. Query names must be
  unique across all files — check with `grep -h '^-- name:' internal/store/queries/*.sql`.
  Regenerate with `~/go/bin/sqlc generate` (it rewrites `internal/store/db/`
  from all files; that's fine). Schema changes go in a new migration in your
  number range (`000NN_what.sql`, goose format), never by editing `00001`.
- **Go deps**: everything in the spec is already in `go.mod`. Don't run
  `go mod tidy` (the integrator does). If you truly need a new module, `go get` it.
- **Building**: build and test your own packages
  (`go build ./internal/deploy/... && go test ./internal/deploy/...`). Other
  agents are editing at the same time, so a compile error in a file you don't
  own is probably work in progress: wait a minute and retry; don't fix it.
- **Errors**: every failure returned to a user is an `*apperr.Error` with a
  plain-words message and an exact hint. Codes are snake_case and shared
  between REST and MCP. Run user-visible text through `apperr.Redact`.
- **Audit**: every service method that changes state calls `s.Audit(...)`.
- **Docker**: `s.Docker` may be nil (tests, `admin` commands). Check before use.
- **Tests**: table tests as the spec asks. Integration tests against real
  Docker go behind `//go:build integration`.
- **Decisions**: when the spec is ambiguous pick the simpler option and append
  one line to `docs/decisions.md` (append only; others write to it too).
- Don't add features that are not in SPEC.md. Don't `git commit` (the integrator does).
- Never run destructive commands on anything outside this repo; Docker
  containers/networks/images you create in tests must carry the
  `nl.bitploy.managed=1` label or a `bp-test-` name and be cleaned up.

## Cross-area conventions

- **REST JSON**: lists return `{"items": [...], "next_cursor": "..."}` (`app.Page[T]`).
  Errors: HTTP status from `apperr.Error.HTTPStatus` and body
  `{"error": {"code", "message", "hint", "next", "details"}}`.
- **Dashboard auth**: `POST /api/v1/auth/login {email, password}` sets the
  session cookie; `GET /api/v1/session` → `{user, csrf_token}`; every non-GET
  request sends `X-CSRF-Token`. In dev (`BITPLOY_DEV=1`) the cookie is
  `bitploy_session` without `Secure`; in production `__Host-bitploy_session`.
- **Health checks through Traefik**: bitploy (in a container, or on the host in
  dev on macOS) cannot reach app containers directly. During `health_check`,
  the deploy code writes the app's route file with `Route.Candidate` set; bitploy
  then probes `cfg.TraefikInternal + health_path` with
  `Host: proxy.CandidateHost(slug)`. Traefik has an unpublished entrypoint
  `internal` on `:8081` for this (dev publishes it on `localhost:8081`).
  Traefik is connected to every `bp-<slug>` network (container `cfg.TraefikContainer`).
- **Catch-all**: Traefik sends unknown `*.APPS_DOMAIN` hosts to bitploy
  (`cfg.InternalURL`). The root router renders "No app here" / "This app is
  stopped" for any Host that is a subdomain of `AppsDomain` and isn't the
  public URL's host.
- **Local dev**: `BITPLOY_DEV=1 BITPLOY_APPS_DOMAIN=localhost BITPLOY_APPS_SCHEME=http
  BITPLOY_APPS_PORT=8000 BITPLOY_PUBLIC_URL=http://localhost:5173
  BITPLOY_TRAEFIK_INTERNAL_URL=http://localhost:8081
  BITPLOY_INTERNAL_URL=http://host.docker.internal:8080 BITPLOY_DATA_DIR=./.data`.
  Traefik runs in Docker with the web entrypoint on host port 8000.
- **Traffic stats** (`internal/traffic`, `internal/app/traffic.go`, migration `00090`, queries `traffic.sql`):
  Traefik writes a JSON access log to `<data dir>/traefik/logs/access.log`; bitploy tails it,
  maps `RouterName` (`app-<slug>@file`, custom domains `app-<slug>-<host>@file`, names from
  `proxy.RouterName` / `proxy.HostRouterName`) to an app, and keeps only counts. Never store an
  IP address, user agent or query string. `GET /api/v1/apps/{app}/traffic?window=24h|7d|30d`.
  The fields kept in `deploy/traefik/traefik*.yml` and the ones read in `traffic.ParseLine` go together.
- **/data backups** (`internal/app/backups*.go`, `internal/api/endpoints_backups.go`, migration `00080`, queries `backups.sql`): `GET /apps/{app}/backups` returns `{items, settings, restore, busy, total_bytes, upload_max_bytes}` (newest first; `restore` is `{backup_id, state, error, started_at, finished_at, app_restarts}`); `DELETE /apps/{app}/backup-restore` forgets a finished restore (409 `backup_busy` while it runs); MCP: `create_backup` (deploy scope) and `list_backups` (read), no restore tool by design; download needs the deploy scope or a session (`token_read_only` otherwise); `POST /apps/{app}/backups` starts a manual backup and answers 202 with a `running` backup; `POST /apps/{app}/backups/{id}/restore` answers 202 and progress is in `restore` of the list; `DELETE /apps/{app}/backups/{id}`; `PUT /apps/{app}/backup-settings {schedule: off|daily|weekly, keep_mode: last|days, keep}`. Two plain handlers outside huma, behind the same auth (bearer or cookie, CSRF on the upload) but without the 8 MB body cap: `GET /apps/{app}/backups/{id}/download` (attachment, Range supported) and `POST /apps/{app}/backups/upload` (raw `.tar.gz` body). Errors: `backup_busy`, `volume_not_ready`, `backup_not_found`, `backup_not_ready`, `backup_file_missing`, `backup_invalid`, `backup_too_large`, `volume_off`, `storage_quota_exceeded`, `server_disk_full`. App and usage JSON carry `backups_mb`.
- **GitHub App** (`internal/github/**`, `internal/app/github*.go`, `internal/api/endpoints_github.go`, migration `00100`, queries `github.sql`; the old per-user token is gone, migration `00100` drops `users.github_token_enc`, `github_login` and `github_token_expires_at`): one GitHub App per instance (table `github_app`, one row; private key, client secret and webhook secret sealed with AAD `github_app:<field>`), created by an admin through GitHub's manifest flow. Users link installations with `github_installations` (primary key user + installation id; `status` is `active`, `suspended` or `deleted`) after an OAuth code exchange and a check against `GET /user/installations`; an `installation_id` from a query string is never trusted alone. `github_installations.github_login` is the GitHub user who proved access and `limited` is 0 only when that user owns the (personal) account; for limited links `github_user_repos` (user + installation + lowercase `owner/name`) holds the repositories GitHub says the user can open, and only those are picked, cloned or deployed on push. `github_installation_repos` holds the repositories of `selected` installations (lowercase `owner/name`). An app created without a webhook (public URL unreachable) stores an empty `webhook_secret_enc`; its `push_status` is `no_webhook` and the admin view has `push_events: false`. `GitHubInstallation` gains `limited`. `github_deliveries` holds webhook delivery ids for 7 days (dedupe). `apps` gains `git_auto_deploy` (default 1) and `git_last_push_at`.
  - Routes: `GET /github` (app info and the caller's installations), `POST /github/connect {mode: install|authorize}` (returns the GitHub URL, state is sealed and expires in 15 min), `POST /github/link {code, state, installation_id?}`, `DELETE /github/installations/{id}`, `GET /github/repositories?q=`, `GET /github/branches?repo=`; admin: `GET /admin/github`, `POST /admin/github/manifest {name, organization?}`, `POST /admin/github/manifest/complete {code, state}`, `DELETE /admin/github`; apps: `PUT /apps/{app}/git-source` (also takes `auto_deploy`), `PATCH /apps/{app}/git-source {auto_deploy}`, `DELETE /apps/{app}/git-source`. The `/github` and `/admin/github` routes are session-only; the app routes take a token with the deploy scope.
  - Webhook: `POST /api/v1/github/webhook`, outside session auth and CSRF. The signature (`X-Hub-Signature-256`, HMAC-SHA256, constant-time compare) is checked before the body is parsed; body cap 5 MB; `X-GitHub-Delivery` dedupes; the answer is `202` and only queues. A failed handler forgets the delivery id so GitHub's redelivery works.
  - Deploys from a push use channel `github` and record `commit_sha` and `commit_message`. A push deploys an app only when: it is a branch push (not a tag, not a deletion), the repository (case-insensitive, `.git` ignored) and branch match, `git_auto_deploy` is on, the app is not stopped, the app owner linked `installation.id`, and the installation's account is the repository owner. Older queued deployments of the app are cancelled, so quick pushes build only the newest commit.
  - Clone credentials for `github.com` come only from the app owner's own installations (a narrow, read-only token for that repo, cached until 5 minutes before expiry); otherwise the clone is anonymous. Other hosts are always anonymous.
  - Config: `BITPLOY_GITHUB_URL` and `BITPLOY_GITHUB_API_URL` (defaults github.com and api.github.com). CSP `form-action` allows that web origin, for the manifest form post.
  - Error codes: `git_source_linked` (409, upload refused on a git-linked app), `github_access_missing` (422, replaces `github_token_missing` and `github_repo_forbidden`), `github_app_missing`, `github_app_exists`, `github_app_rejected`, `github_app_broken`, `github_state_invalid`, `github_code_invalid`, `github_code_missing`, `github_installation_forbidden`, `github_too_many_accounts`, `github_not_installed`, `github_repo_not_connected`, `github_unreachable`, `github_signature_invalid`, `github_delivery_missing`, `github_payload_invalid`, `github_payload_too_large`.
  - `App.git_source` is `{repo_url, branch, subdir, repo?, repo_html_url?, auto_deploy, push_ready, push_status, push_message, last_push_at?}`. `push_status` (computed from the database, no GitHub call) is `ready`, `off`, `other_host`, `app_missing`, `no_webhook`, `not_connected`, `no_access` or `reconnect`.
