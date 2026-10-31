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
- **/data backups** (`internal/app/backups*.go`, `internal/api/endpoints_backups.go`, migration `00080`, queries `backups.sql`): `GET /apps/{app}/backups` returns `{items, settings, restore, busy, total_bytes, upload_max_bytes}` (newest first; `restore` is `{backup_id, state, error, started_at, finished_at, app_restarts}`); `DELETE /apps/{app}/backup-restore` forgets a finished restore (409 `backup_busy` while it runs); MCP: `create_backup` (deploy scope) and `list_backups` (read), no restore tool by design; download needs the deploy scope or a session (`token_read_only` otherwise); `POST /apps/{app}/backups` starts a manual backup and answers 202 with a `running` backup; `POST /apps/{app}/backups/{id}/restore` answers 202 and progress is in `restore` of the list; `DELETE /apps/{app}/backups/{id}`; `PUT /apps/{app}/backup-settings {schedule: off|daily|weekly, keep_mode: last|days, keep}`. Two plain handlers outside huma, behind the same auth (bearer or cookie, CSRF on the upload) but without the 8 MB body cap: `GET /apps/{app}/backups/{id}/download` (attachment, Range supported) and `POST /apps/{app}/backups/upload` (raw `.tar.gz` body). Errors: `backup_busy`, `volume_not_ready`, `backup_not_found`, `backup_not_ready`, `backup_file_missing`, `backup_invalid`, `backup_too_large`, `volume_off`, `storage_quota_exceeded`, `server_disk_full`. App and usage JSON carry `backups_mb`.
