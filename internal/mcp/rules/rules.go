// Package rules holds the markdown the MCP server and the REST API hand to
// agents: the server instructions and the runtime contract. It lives in its
// own package so the service layer (internal/app) can use it without
// importing internal/mcp, which imports the service layer.
// OWNER: MCP agent.
package rules

import (
	_ "embed"
	"strings"
)

//go:embed instructions.md
var instructions string

// Instructions returns the server instructions with the example address
// replaced by the real app address pattern (for example
// https://<slug>.bitploy.nl). exampleURL is cfg.AppURL("<slug>"), domain is
// cfg.AppsDomain.
func Instructions(exampleURL, domain string) string {
	s := strings.Replace(instructions, "https://<slug>.bitploy.nl", exampleURL, 1)
	return strings.ReplaceAll(s, "bitploy.nl", domain)
}

// RuntimeContract is what every deployed app must follow, plus the playbook
// an agent needs after the first deploy (updates, env vars, fix loops).
const RuntimeContract = `## Runtime contract

- Listen on 0.0.0.0:$PORT. Bitploy sets PORT, BITPLOY_APP and BITPLOY_URL. The port is the app's port setting, otherwise the single EXPOSE of the last stage of the Dockerfile.
- Log to stdout and stderr; build and runtime logs come from get_deployment and get_logs.
- Write persistent data only to /data. A new app gets it with create_app (volume=true); an existing app with update_app (volume=true) and then restart_app. Everything else in the container is lost on redeploy. Turning the volume off (update_app volume=false) deletes the files in /data and all backups: it is refused with volume_has_data unless you pass delete_data=true, which you do only after the user agrees. The app user must be able to write there: in the Dockerfile run mkdir /data and chown it to the app user before USER (for example RUN mkdir /data && chown node /data). Docker copies that ownership into the new volume. Each volume has a storage limit (volume_mb, default 5 GB) taken from the account's storage allowance, which also holds every app's own files (kept images and stored source); bitploy measures /data hourly, warns at 80% and stops the app above 100%.
- Apps run as one locked-down container: 512 MB memory and 0.5 CPU by default, no root privileges, no access to the Docker socket or the host. A static site (plain files or a Vite site that bitploy builds itself, not your own Dockerfile) has a fixed 64 MB and 0.1 CPU, no /data, and does not count towards the memory and CPU quota.
- A worker (type=worker: bot, queue consumer) has no route and no port check; it counts as healthy when it is still running after 30 seconds. Its process must keep running in the foreground.
- A web app is healthy when its port is open and GET health_path (default /) answers with a status below 500 within 60 seconds. A Dockerfile HEALTHCHECK replaces this check.
- A failed deploy never takes a working app down: the old version keeps serving until the new one is healthy.
- With a volume attached, a deploy stops the old container before starting the new one (a few seconds of downtime).
- The last 3 images are kept per app so rollback works.

## Databases

- An app is one container: there is no database server next to it. Use SQLite, with the file in /data (for example /data/app.db) and the volume on (create_app with volume=true for a new app).
- The project expects Postgres or MySQL (docker-compose.yml, DATABASE_URL=postgres://..., Prisma provider "postgresql")? Moving to SQLite is usually one setting in the ORM (Prisma, Drizzle, Django, Laravel, SQLAlchemy). Ask the user first. If they want to keep Postgres or MySQL, use an external hosted database (Neon, Supabase...) and set DATABASE_URL with set_env.
- Never install a database server (postgres, mysql, mariadb, redis) in the image or run several processes in one container.
- Run migrations when the app starts, in CMD: for example sh -c "npx prisma migrate deploy && node server.js", php artisan migrate --force, python manage.py migrate.
- SQLite is safe here: with a volume a deploy stops the old container before the new one starts (never two writers), and create_backup freezes the app while it copies, so backups are consistent.
- Need Redis for cache or queues? Laravel: CACHE_STORE=file and QUEUE_CONNECTION=sync (the laravel template sets both: there is no separate queue worker). Elsewhere an in-process cache or a SQLite-backed queue.

## Limits

- deploy_files: at most 2 MB in total and 200 files. Anything bigger: zip + create_upload.
- Zip upload: at most 95 MB compressed, 500 MB unpacked, 20,000 files. If everything sits in one top-level folder, that folder is stripped. Leave out node_modules, .git, .env and build output.
- Git: shallow clone of one branch, at most 500 MB, HTTPS only. Public repositories work as is. A private GitHub repository needs the user to open the dashboard, go to Settings > GitHub, press Connect GitHub and give bitploy access to the repository; you cannot do that yourself, ask the user. If GitHub is not set up on this bitploy yet, only an admin can do that (Admin page); until then only public repositories work.
- A build takes at most 15 minutes. The built image must be under 2 GB. Images (3 kept per app) and stored source count towards the account's storage; when it is full, new builds are refused with storage_full. Keep images small: a slim base image, and no big files (videos, datasets) in the source.
- Deployments per hour, number of apps, memory, CPU, storage and custom domains per app are limited per account: whoami shows the numbers.

## Updating an app later (also from a new session)

- Which app is this project? (1) A .bitploy file in the project (JSON like {"app":"quiet-otter-3f9a","url":"https://..."}) names it: update that app. (2) No .bitploy, but the user says the project is already online: find it with list_apps and confirm with the user by name and URL; if several could match, ask the user which. (3) Otherwise create a NEW app. Never reuse an app just because its name looks similar: other projects may have an app with the same name.
- After create_app (and after the first successful deploy), save {"app":"<slug>","url":"<url>"} as .bitploy in the project folder. Do not send that file with the deploy.
- An app linked to a git repository (set_git_source; get_app shows git_source) is updated by committing and pushing. With push_ready true every push to the branch starts a deployment by itself: follow it with get_deployment (app=<slug>, wait_seconds=30), no deploy call needed. Call deploy only to build the first time, when auto-deploy is off (push_ready false) or to rebuild the same commit. A linked app refuses deploy_files, create_upload and deploy with an upload_id (git_source_linked); to go back to uploads on purpose call set_git_source with remove=true first.
- A project that has a GitHub remote is best linked with set_git_source. Bitploy builds what is pushed, not local files: commit and push first. A project without a GitHub remote is uploaded: never ask the user to create a repository or install git just to deploy; at most mention once, briefly, that connecting GitHub makes updates automatic.
- deploy_files is a full snapshot, not a patch: send ALL files again; the previous source is replaced. For a zip source, create_upload again and deploy with the new upload_id. deploy with only app= rebuilds the last stored source, which is how you apply new env vars or settings to an app that is not running.
- The URL stays the same across deploys.

## Applying changes

- set_env: new values reach a running app only after restart_app (it recreates the container; no rebuild, a few seconds). If the app is failed, new or stopped-by-crash, call deploy (with only app=) instead, because restart_app needs a deployment that already ran.
- update_app volume, memory_mb, cpu_millis: apply on restart_app. port, dockerfile_path, context_dir, type: apply on the next deploy. health_path: next deploy. slug (rename): applies at once, the URL changes, the app is down for a few seconds, files in /data are kept. volume_mb (storage limit): applies at once.
- Code or Dockerfile changes: a push (linked repository), or a new deploy_files or deploy.
- rollback(app, deployment_id) runs an older image again; pick a deployment from list_deployments with state running or superseded (only the last 3 images exist). Roll back only when the user asks.

## Fixing failures (read error.code, error.hint and log_tail of get_deployment)

- dockerfile_missing: get_template, save Dockerfile and .dockerignore, deploy_files with ALL files again.
- port_unknown / port_ambiguous: put one EXPOSE <port> in the last stage, or call update_app with port=<number>. A bot with no web port: create the app with type=worker.
- build_failed: read the last lines of log_tail: usually a missing file the Dockerfile copies, a wrong package name, a lock file that is missing (npm ci needs package-lock.json), or a base image tag that does not exist. node-gyp or better-sqlite3 errors on Alpine: add "RUN apk add --no-cache python3 make g++" before npm ci. Prisma on Alpine: "RUN apk add --no-cache openssl". Fix and deploy again.
- unhealthy and the logs mention localhost or 127.0.0.1: the app listens on the wrong address. Make it listen on 0.0.0.0 and the PORT variable.
- unhealthy and the app "stopped right after starting": the program crashed. The log_tail shows why. A missing environment variable (KeyError, "undefined", "is required") means: set_env, then deploy with only app=.
- unhealthy and "ran out of memory": raise memory_mb with update_app (check whoami for the quota), then deploy; or make the app use less.
- unhealthy and "did not answer GET /...": the app is too slow to start or returns 500 on that path. Point health_path at a cheap path that answers below 500 (for example /healthz) with update_app, then deploy. Or fix the route.
- deploy_rate_limited: wait the minutes the hint names; tell the user. Do not retry in a loop.
- token_read_only: the token cannot change anything. Ask the user for a token with the deploy scope (dashboard, Tokens).
- github_access_missing: bitploy cannot read the GitHub repository. Check the address first. If it is private, ask the user to open the dashboard, go to Settings > GitHub, press Connect GitHub and give bitploy access to this repository (the error says what to do when GitHub is not set up), or to make the repository public. Then call set_git_source again. If the user cannot do either, upload the files instead (deploy_files or create_upload): a failed set_git_source links nothing.
- git_source_linked: the app deploys from a repository. Push your change instead (get_deployment with app=<slug> follows the deployment it starts). Only when the user wants uploads again, call set_git_source with remove=true first.
- After 3 failures with the same code, stop and explain the problem to the user in plain words.

## Password protection

set_password_protection(app, enabled, username, password) puts a browser login prompt in front of the app's web addresses (its bitploy address and every custom domain). Use it only when the user asks, for example "keep this private until it is ready". Get the username and password from the user; if they have none, suggest a password of a few words and a number and say it once in your answer. Nobody can read the password back; to change it, call the tool again. A protected app answers 401 to anyone without the login. That is not a failure, and bitploy's own health checks still work. enabled=false removes the prompt.

## Custom domains

add_domain returns DNS records. Tell the user in plain words: "Open the place where you bought the domain (GoDaddy, Namecheap, Cloudflare, TransIP...), find DNS settings, add these records exactly, wait 5 to 30 minutes, then tell me." Then call check_domain until the status is active. For a root domain (example.com) the records are A records; for www.example.com a CNAME. On Cloudflare the record must be DNS only (grey cloud).

## Backups of /data

- create_backup(app) saves the /data folder of an app that has the volume on and has been deployed. It runs in the background (the app is frozen for a moment while its files are read): call list_backups until the backup is ready or failed (error says why). Backups count towards the account's storage allowance. Offer one before a risky change to an app that keeps data in /data; do not make them in a loop.
- list_backups(app) shows the backups, the automatic backup settings, whether something is running (busy) and how the latest restore went.
- Restoring is deliberately dashboard-only: it replaces every file in /data and stops the app for a moment. When the user wants to go back to a backup, tell them to open the app, go to the Storage tab and press Restore there. Downloading and uploading backup files and the automatic schedule are in the same tab.
- The Storage tab also has a file browser for /data: the user can download, upload or replace single files there (for example the SQLite database) and make or delete folders. Point the user there when they want one file out of /data or back in.
- backup_busy: one backup or restore per app at a time; wait and try again. volume_not_ready: /data is on but the app was not started since, so restart_app first. storage_quota_exceeded: no room left for a backup; the user can delete an old backup or app.

## Dockerfile checks (validate_dockerfile)

Errors stop the deploy: dockerfile_missing, dockerfile_parse_error, no_from, port_unknown, port_ambiguous, port_reserved, base_image_forbidden, image_too_large.
Warnings: env_file_in_context, no_dockerignore, unpinned_base, runs_as_root, compose_ignored (docker-compose.yml is not run: only the Dockerfile is built), database_server_in_image (use SQLite in /data), lockfile_mismatch (npm ci but the lock file is pnpm, yarn or bun). Info: no_healthcheck.
Fix every error and read every warning before deploying.
`
