# bitploy

bitploy is a small, self-hosted deploy platform for one server. You give it a Dockerfile and some code, and it gives you back `https://<name>.yourdomain.nl`. Its main users are AI coding agents: they deploy through an MCP server or a REST API. People log in to a dashboard to create tokens, read logs and watch usage.

- One Go binary with the Vue dashboard embedded, one SQLite file, Traefik in front, Docker underneath.
- Every app is built from a Dockerfile: your own, a ready-made one, or one bitploy writes for a plain website or a Node.js app. It refuses code it cannot build instead of guessing.
- User code is untrusted: each app runs in its own locked-down container on its own network.

The full product spec is [SPEC.md](SPEC.md). Choices made where the spec was open are in [docs/decisions.md](docs/decisions.md). The rules for working on the code are in [docs/CONTRACT.md](docs/CONTRACT.md).

## Quick start (local development)

You need Go 1.26, Node 24 and Docker.

```bash
git clone <this repo> && cd bitploy
make dev
```

`make dev` (`deploy/dev.sh`) starts three things:

| What | Where |
| --- | --- |
| Dashboard (Vite, proxies `/api` and `/mcp`) | http://localhost:5173 |
| Go server | http://localhost:8080 |
| Traefik in Docker, plain HTTP | apps at `http://<slug>.localhost:8000` |

On the first run it creates a dev admin: `dev@bitploy.localhost` / `devpassword123`. Data goes to `./.data`; delete that folder to start over. Stop with Ctrl-C. Traefik keeps running (and writes the access log that feeds the Traffic tab to `.data/traefik/logs`); remove it with `docker compose -f deploy/docker-compose.dev.yml down`.

Other commands:

```bash
make test               # go test ./cmd/... ./internal/... ./web
make test-integration   # also tests that need a Docker daemon (-tags=integration)
make lint               # go vet, staticcheck, vue-tsc, eslint
make generate           # sqlc generate, after changing internal/store/queries/*.sql
make build              # one binary in bin/bitploy with the dashboard embedded
```

## One-click apps and pre-built images

bitploy ships a catalog of 27 self-hosted tools, such as Vaultwarden, Uptime Kuma, n8n, Forgejo and Ghost. Each one is a Dockerfile with a pinned version that keeps its data in `/data`. Some, like Umami and Ghost, need a database server: bitploy adds it next to the app in a project of its own. Pick one under **Ready-made** in the dashboard, or deploy one from `GET /api/v1/catalog` and `POST /api/v1/catalog/{id}/deploy`, or let an AI assistant call `list_catalog` and `deploy_catalog_app`. An app made this way shows when the catalog has a newer version; update it with `POST /api/v1/apps/{app}/catalog-update`.

Any other published image works too: deploy a Dockerfile that is only `FROM <image>:<tag>` with a pinned tag. `EXPOSE` is optional; when it is missing and the app has no port setting, bitploy reads the port from the built image (and asks you to set the port if the image declares none, several, or a reserved one). Keep the image's data under `/data`. The Dockerfile of an app deployed from files or a zip can be read and replaced with `GET` and `PUT /api/v1/apps/{app}/dockerfile`.

## Projects and databases

Every app is in a project. An app created on its own gets a project named after it, and that project goes away with the app. Make a project yourself (`POST /api/v1/projects` or `create_project`) to keep apps that belong together in one place, like a shop and its database.

Apps of one project reach each other on a private network by their short name in the project (`alias`, the subdomain unless you pick another), for example `db:5432`. Nothing outside the project reaches them there, and a database has no web address at all.

When the code needs a database server and SQLite in `/data` will not do, add one from a service template:

```bash
curl -X POST https://bitploy.example.com/api/v1/projects/<project id>/services \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"template": "postgres", "name": "db", "connect_to": ["shop"]}'
```

bitploy creates the app `db` from PostgreSQL 18.6, generates its passwords (stored encrypted, never shown), deploys it with its data in its own volume, and gives `shop` the variable `DATABASE_URL=${{db.DATABASE_URL}}`. The reference is filled in each time `shop` starts, and `shop` restarts once the database runs. Templates: `postgres`, `pgvector`, `mariadb`, `mysql`, `valkey` (Redis-compatible; `redis` by name), `meilisearch` (`GET /api/v1/service-templates`, or `list_service_templates` over MCP). Any variable of another app in the project can be used the same way: `${{<alias>.<VAR>}}`.

A database counts as one app against your app limit, and is backed up, restarted and removed like any app.

## Deploying a compose file

Most apps should **not** use compose. One Dockerfile with the data in SQLite under `/data` is simpler, starts faster, uses less memory, and backs up, restores and rolls back as one piece. That is what bitploy and its AI instructions recommend. Do not write a compose file for code that has none, do not split a small app into services, and do not add Postgres or Redis when SQLite and an in-process cache do the job.

Use compose when the code **already has** a `compose.yaml` (or `docker-compose.yml`) and needs what is in it: a web app next to Postgres, Redis or a worker, as its authors run it.

1. Check the file: `POST /api/v1/compose/validate` with `{"compose": "<the file's text>"}` (or the MCP tool `validate_compose`). It answers 200 with what would run (services, volumes, memory and CPU totals, whether it fits your quota) and every warning and error, each with a `code`, the `service` and compose `key` at fault, and a `hint`.
2. Create the app with `build_mode: "compose"` (`POST /api/v1/apps`, or `create_app` over MCP); `compose_path` when the file has another name or folder.
3. Set the variables the file reads as `${VAR}` with `PUT /api/v1/apps/{app}/env` (bitploy never reads `.env` files). Add `"service": "<name>"` for a variable only one service gets.
4. Send the code (git, zip or files) with the compose file and every Dockerfile it builds. A file with only `image:` services can be sent alone.

What bitploy does with the file: `build:` services are built one at a time on the isolated builder, `image:` services are pulled (pin a tag), and each service runs as its own locked-down container, reachable by its service name on the app's private network (`postgres://db:5432/...`). One service is public: the one with `x-bitploy: {public: true}`, else the one with `ports`. It gets the address and the health check (`x-bitploy: {health_path: /health}`, its `healthcheck:`, or `GET /`). `ports` are never published. Named volumes are kept between deploys and backed up on the app's schedule; their size is `x-bitploy: {size_mb: 2048}` on the volume (default 5 GB). Memory and CPU come from `deploy.resources.limits` (default 256 MB per service, 512 MB for a public service built from the project, 0.25 CPU). Services that did not change keep running on a redeploy, so a database is not restarted on every push. At most 6 services. Refused: `privileged`, `cap_add`, `devices`, `network_mode`, host bind mounts, the Docker socket, custom networks and the like (`compose_forbidden`).

An app that already runs from one Dockerfile keeps doing so even when a compose file appears in its code: the deploy warns `compose_ignored`. To switch, `PATCH /api/v1/apps/{app}` with `{"build_mode": "compose"}` and deploy again. The `/data` volume and its files are kept (they move to the compose volume mounted at `/data`). There is no way back to one container; create a new app for that. Only apps in **auto** mode (the dashboard's default) pick up a compose file by themselves, and only on their first deploy.

## Production on Hetzner

One Hetzner Cloud server runs everything: Docker, Traefik and bitploy. About 4 vCPU, 8 GB RAM and 160 GB disk is a good start for a group of colleagues. bitploy refuses new apps when the sum of all memory limits would pass the server's RAM minus 1.5 GB.

### 1. DNS and TLS at Cloudflare

Apps live on subdomains of your domain (the examples use `bitploy.nl`). Create these records:

| Record | Type | Value | Proxy |
| --- | --- | --- | --- |
| `bitploy.nl` | A (and AAAA) | server IP | Proxied (orange cloud) |
| `*.bitploy.nl` | A (and AAAA) | server IP | Proxied |
| `edge.bitploy.nl` | A (and AAAA) | server IP | **DNS only** (grey cloud) |

- Set SSL/TLS mode to **Full (strict)**.
- Create an **Origin CA certificate** (SSL/TLS, Origin Server, Create Certificate) for `bitploy.nl` and `*.bitploy.nl`, format PEM. `install.sh` offers to paste it (or uses a self-signed one until you do); Traefik serves it as the default certificate.
- Custom domains of your users do not go through Cloudflare. They point at `edge.bitploy.nl` (DNS only) and get their own Let's Encrypt certificate from Traefik.

#### More app domains

An admin can add more domains for app addresses under Admin, Domains (for example `example.com`, so apps can live at `name.example.com`). Each one is a Cloudflare domain set up like the one above:

1. DNS: `*.example.com`, A (and AAAA if `BITPLOY_SERVER_IPV6` is set) to the server IP, **Proxied**. The Domains page shows the IPs from `BITPLOY_SERVER_IPV4`/`_IPV6`.
2. SSL/TLS mode **Full** for that domain. Cloudflare then encrypts the connection to the server without checking its certificate, so no certificate is needed: Traefik serves the default one.
3. Optional, for **Full (strict)**: SSL/TLS, Origin Server, Create Certificate for `example.com` and `*.example.com`, and paste the certificate and private key into the form (now or later), then switch the mode to Full (strict). bitploy keeps the key encrypted in its database (with `BITPLOY_SECRET_KEY`, like env vars), writes both to `<data dir>/traefik/dynamic/certs/` (mode 0600) and lists them in `_certs.yml`, so Traefik picks the right certificate by name.
4. Add the domain under Admin, Domains, then give people access with the domain menu next to their name under People. Admins may always use every domain; everyone may use the default one.

Slugs stay unique across all domains. When a person may use more than one domain, New project, Ready-made and the app's Settings show a domain menu next to the name; AI assistants pass `domain` to `create_app`, `update_app` or `deploy_catalog_app` (`whoami` lists them). Moving an app to another domain changes its address at once. A domain that apps still use cannot be removed; taking a domain away from a person leaves their apps where they are. No compose or install changes are needed: the certificate files sit in the folder Traefik already reads.

### 2. Hetzner Cloud Firewall

| Port | Allow from |
| --- | --- |
| 22 (SSH) | your own IP only |
| 80 and 443 | anywhere (custom domains arrive directly, so these cannot be limited to Cloudflare) |

### 3. Install

Use a fresh Ubuntu 24.04 or 26.04 LTS server, log in as root with an SSH key, copy [`install.sh`](install.sh) to it and run:

```bash
sudo bash install.sh
# private repository: sudo GITHUB_TOKEN=<token that can read the repo> bash install.sh
```

It installs git and Docker if they are missing, downloads the source to `/opt/bitploy/src`, builds the bitploy image on the server, asks for your domain, Let's Encrypt email and the first admin account, starts everything and checks the network isolation. From a checkout, `sudo ./install.sh` does the same with that checkout. To skip the questions, pass the answers as environment variables (`BITPLOY_APPS_DOMAIN`, `BITPLOY_ACME_EMAIL`, `BITPLOY_ADMIN_EMAIL`, `BITPLOY_ADMIN_PASSWORD`; see the top of `deploy/install.sh`). Without a Cloudflare Origin CA certificate it makes a self-signed one, which works with Cloudflare's SSL mode **Full**; add the Origin CA certificate later for **Full (strict)**.

The script is safe to run twice; running it again is also how you upgrade. Under the hood `deploy/install.sh` does five things:

1. Installs Docker Engine and the buildx plugin from Docker's apt repository.
2. Writes `/etc/docker/daemon.json` (app networks from `10.200.0.0/16` in `/24` blocks, log rotation 10 MB x 3, `live-restore` so apps survive a Docker restart).
3. Installs `firewall.sh` as a systemd unit, turns on unattended upgrades and turns off SSH password login (only if it finds an SSH key, so it cannot lock you out).
4. Creates `/etc/bitploy/bitploy.env` (mode 0600) with a generated `BITPLOY_SECRET_KEY`, asks for the domain, the Let's Encrypt email and the Origin CA certificate.
5. Runs `docker compose up -d`, waits for `/healthz` and prints what to do next.

Then:

```bash
docker exec -it bitploy bitploy admin create-user --email you@example.com --admin
sudo deploy/probe-test.sh     # must end with "All checks passed."
```

Open your domain, log in, and invite colleagues from the Admin page. Under Connect AI they find the MCP setup for their assistant.

**Back up `BITPLOY_SECRET_KEY` now, somewhere that is not this server** (a password manager). It encrypts every saved env var and the GitHub App's credentials. Without it a restored database is readable, but those secrets are gone for good.

```bash
sudo grep BITPLOY_SECRET_KEY /etc/bitploy/bitploy.env
```

### Connect GitHub (optional)

With GitHub connected, people pick a repository (in an app's Settings, or right away in New app) and every push to its branch deploys the app. Uploading files works for everyone, with or without GitHub.

**Admin, once.** bitploy uses one GitHub App for the whole server, and you create it from the dashboard:

1. Make sure `BITPLOY_PUBLIC_URL` is an address GitHub can reach over the internet. GitHub posts push events to `<public url>/api/v1/github/webhook`. On `localhost`, a private address or a name without a dot, the Admin page warns you, and the app is created without push events: private repositories work, but every deploy is by hand. To add push events later, fix the address, disconnect GitHub on the Admin page and create the app again.
2. Open Admin, GitHub, choose a name (and an organization if the app should belong to one) and press Create GitHub App. GitHub shows what will be created. Confirm there, and you land back in bitploy.

The app asks for read-only access to repository contents and metadata, and for push events when GitHub can reach the server. bitploy keeps its private key, client secret and webhook secret encrypted with `BITPLOY_SECRET_KEY`. Disconnect on the same page removes them from bitploy; delete the app on GitHub too if you want it gone there.

**Everyone, once.** Settings, GitHub, Connect GitHub. GitHub asks which account and which repositories bitploy may read; pick the ones you want. If the app is already installed on GitHub, or you changed its access there, press Sync from GitHub. Then, in an app's Settings, under Source, pick a repository, a branch and optionally a folder. Press Redeploy for the first build; after that every push to that branch deploys by itself. A few quick pushes in a row end in one build of the newest commit.

**What to know**

- An app built from a repository does not accept uploaded files (the error says `git_source_linked`). Disconnect the repository in the app's Settings, or call `set_git_source` with `remove: true`, to upload again. The running app is not touched.
- Private repositories need the app installed with access to that repository. Public repositories on any git host also work through a plain address, but only GitHub repositories deploy on push.
- bitploy clones with a short-lived token for that one repository, read-only, and never writes it to disk, a log or an image.
- For an organization, or someone else's account, bitploy only uses the repositories that you can open on GitHub yourself, and only those show up in the picker. This list is taken when you connect. Press Sync from GitHub after your access changes; someone who leaves the organization keeps the repositories they had until they disconnect, so remove them from bitploy too.
- bitploy does not poll GitHub. If a push is missed (the server was down for a long time, or the public URL was wrong), redeliver it from the GitHub App's Advanced tab, or press Redeploy.

### Configuration

All settings are environment variables in `/etc/bitploy/bitploy.env`. Restart with `docker compose --env-file /etc/bitploy/bitploy.env -f /opt/bitploy/docker-compose.yml up -d` after a change.

| Variable | Example | Meaning |
| --- | --- | --- |
| `BITPLOY_PUBLIC_URL` | `https://bitploy.nl` | Dashboard, API and MCP origin; also the CSRF origin |
| `BITPLOY_APPS_DOMAIN` | `bitploy.nl` | Default parent domain of app subdomains (admins can add more under Admin, Domains) |
| `BITPLOY_EDGE_HOST` | `edge.bitploy.nl` | DNS-only name that custom domains point to (default `edge.<apps domain>`) |
| `BITPLOY_SERVER_IPV4`, `BITPLOY_SERVER_IPV6` | `203.0.113.10` | Shown in DNS instructions and accepted by DNS checks |
| `BITPLOY_SECRET_KEY` | 32 random bytes, base64 | Encrypts env vars and the GitHub App's credentials. `openssl rand -base64 32` |
| `BITPLOY_DATA_DIR` | `/var/lib/bitploy` | Database, uploads, logs, route files, backups |
| `BITPLOY_ACME_EMAIL` | `you@example.nl` | Let's Encrypt account (written into Traefik's config by `install.sh`) |
| `BITPLOY_SIGNUP` | `invite` | `invite` or `open` |
| `BITPLOY_ADMIN_PASSWORD` | — | Password for `bitploy admin create-user` without a prompt (scripts only) |
| `BITPLOY_MAX_PARALLEL_BUILDS` | `2` | Server-wide build slots |
| `BITPLOY_BUILDER_CPUS`, `BITPLOY_BUILDER_MEMORY` | `2`, `6g` | Limits of the isolated build container (builds run in their own buildx builder on the firewalled `bp-builder` network) |
| `BITPLOY_DB_HELPER_BIN` | — | A bitploy binary built for Linux for the SQLite viewer's helper image. Only needed when bitploy does not run on Linux itself; `make dev` builds and sets it |
| `BITPLOY_MAX_APPS` | `100` | Most apps on the server, all users together. Each user can have 10 by default; change that per user in Admin, Users |
| `BITPLOY_LISTEN` | `:8080` | Listen address |
| `BITPLOY_TRAEFIK_CONTAINER` | `bitploy-traefik` | Traefik's container name; joined to every app network |
| `BITPLOY_TRAEFIK_INTERNAL_URL` | `http://bitploy-traefik:8081` | Traefik's unpublished entrypoint, for health checks before a swap |
| `BITPLOY_INTERNAL_URL` | `http://bitploy:8080` | How Traefik reaches bitploy (dashboard route, "No app here" page) |
| `BITPLOY_APPS_SCHEME`, `BITPLOY_APPS_PORT` | `https`, empty | Scheme and port of app URLs (development uses `http` and `8000`) |
| `BITPLOY_GEOIP` | `on` | Traffic stats look up visitor countries with DB-IP's free country database, downloaded into `<data dir>/geoip`. Default `on`; `off` skips the download (country is then only known when Cloudflare sends it) |
| `BITPLOY_GITHUB_URL`, `BITPLOY_GITHUB_API_URL` | `https://github.com`, `https://api.github.com` | Where bitploy talks to GitHub. Leave them alone, unless you run GitHub Enterprise Server or a test double |
| `BITPLOY_DEV` | `1` | Local development only: plain HTTP, no `Secure` cookies, no Docker cache pruning |
| `BITPLOY_IMAGE` | `ghcr.io/avmg20/bitploy:1.2.0` | Used by `docker-compose.yml`, not by bitploy itself. Pin a version here. |

### Custom domains (what your users do)

`add_domain` returns two records. For `www.example.com`:

| Type | Name | Value |
| --- | --- | --- |
| CNAME | `www.example.com` | `edge.bitploy.nl` |
| TXT | `_bitploy-challenge.www.example.com` | the token bitploy shows |

A root domain (`example.com`) cannot have a CNAME in DNS, so it gets A (and AAAA) records with the server IP instead. A name counts as a root domain when it is a registrable domain on the Public Suffix List (`example.com`, `example.co.uk`; not `www.example.com`).

If the user's domain is on Cloudflare, the record must be **DNS only** (grey cloud). bitploy checks every 5 minutes for 48 hours through 1.1.1.1 and 8.8.8.8, and both must show the TXT token and a route to this server. Errors name the problem, for example `CNAME points to parking.godaddy.com, expected edge.bitploy.nl`. When the checks pass, bitploy writes the route and Traefik gets the certificate.

### Network isolation

`deploy/firewall.sh` puts rules in their own chains (`BITPLOY-APPS` in `DOCKER-USER`, `BITPLOY-INPUT` in `INPUT`). From app subnets (`10.200.0.0/16`) it blocks the host, the Hetzner metadata address `169.254.169.254`, every private range, the bitploy network and other apps. Traffic inside one app's own /24 (Traefik to the app), inside a project network (`bp-p-<id>`, the apps of one project; Traefik is not on it) and to the internet stays open. `systemd` runs it after Docker starts. `deploy/probe-test.sh` proves it with a probe container and runs in CI.

```bash
sudo deploy/firewall.sh status
```

Known limits: bridge networks are IPv4 only, so there are no IPv6 rules. An app cannot call a hostname that resolves to this server's own address (for example a custom domain served by this same server); that traffic would end up in the private network, which the firewall drops. The Traefik `internal` entrypoint (`:8081`) is reachable from app networks; it only serves candidate health checks.

### Security notes

- The bitploy container mounts `/var/run/docker.sock`, which is root-equivalent on the host. It runs as root: the socket's group ID differs per host, and a non-root user would not make the socket any safer. Keep SSH to keys only, and run nothing else on this server.
- Only the bitploy container gets the socket. Traefik reads files and needs no socket.
- Apps run with a memory/CPU/PID limit, `no-new-privileges`, no extra capabilities, no mounts except their own volume, and on their own network.
- Builds run user code with the build container's privileges. That is acceptable for colleagues. Before opening bitploy to the public, follow the checklist at the end of "Security & limits" in [SPEC.md](SPEC.md).

### Backups of an app's saved files (/data)

Each app with `/data` turned on has a **Storage** tab in the dashboard with a Back up now button, an upload button, automatic backups and the list of backups (download, restore, delete).

- A backup is a `.tar.gz` of the folder contents in `<data dir>/backups/<app id>/<backup id>.tar.gz`. Kinds: manual, automatic, uploaded, before restore.
- Automatic backups: off, every night (03:00 UTC) or every week (Sunday 03:00 UTC). A missed one is made as soon as bitploy is up again. Keep the newest N backups or N days. This applies to automatic and before-restore backups; manual and uploaded ones stay until deleted.
- Restoring makes a backup of the current data first, stops the app, replaces the files and starts the app again. If the restore fails, the old data is put back; if that fails too, the app stays stopped and its page says what to do. If bitploy itself is restarted in the middle of a restore, it puts the old data back when it starts again. Restoring is only in the dashboard; AI assistants (MCP) can make and list backups (`create_backup`, `list_backups`) but not restore.
- Backups count towards the owner's storage allowance. A manual backup or upload that does not fit is refused; an automatic one is skipped and shows why in the list.
- A running app is paused (`docker pause`) while its files are read, so the copy is consistent. Deleting an app deletes its backups, and so does turning `/data` off (it asks first: `update_app volume=false` needs `delete_data=true` when `/data` has files or there are backups).
- The API: `GET/POST /api/v1/apps/{app}/backups`, `DELETE /api/v1/apps/{app}/backups/{id}`, `POST .../{id}/restore`, `PUT /api/v1/apps/{app}/backup-settings`, `DELETE /api/v1/apps/{app}/backup-restore` (dismiss a finished restore), `GET .../{id}/download` (a plain link works with the session cookie, or use a bearer token with the deploy scope; a read-only token cannot download) and `POST /api/v1/apps/{app}/backups/upload` (the raw `.tar.gz` as the body).
- The SQLite viewer (Storage tab, **Open** next to a `.db`, `.sqlite`, `.sqlite3` or `.db3` file): the tables and views of the database with their columns and indexes, a data grid (paging, sorting, filters, add, edit and delete rows) and a SQL console that is read-only until you turn writes on. It runs in a throwaway helper container as the owner of the file, while the app keeps running; the app's own database never leaves the volume. AI assistants can query a database with the MCP tool `query_db` (read-only unless `write=true` with a deploy token). The API is `GET /api/v1/apps/{app}/db?path=` and `POST .../db/rows`, `/db/insert`, `/db/update`, `/db/delete`, `/db/sql` (reading needs the read scope, changing the deploy scope).
- The file browser (Storage tab, only with /data on): `GET /api/v1/apps/{app}/files?path=` (list a folder), `GET .../files/download?path=` (a plain link works with the session cookie; deploy scope for a token), `POST .../files/upload?path=<folder>&name=<file>[&overwrite=true]` (raw body, up to 100 MB), `POST .../files/folder` and `DELETE .../files?path=`.
- These files are on the same disk as bitploy. `deploy/backup.sh` does not copy them off the server; it backs up the app volumes themselves.

### Server backups

Every night at 03:30 UTC bitploy writes a consistent database copy (`VACUUM INTO`) to `<data dir>/backups/`, keeps 7 of them, and does the other housekeeping (rollups, pruning, disk measuring, Docker prune, old uploads).

`deploy/backup.sh` sends the database, the Let's Encrypt state and one tar per app volume to a restic repository, keeping 7 daily and 4 weekly snapshots. A systemd timer runs it at 04:30 UTC once `/etc/bitploy/backup.env` exists.

```bash
# /etc/bitploy/backup.env  (chmod 600)
RESTIC_REPOSITORY=sftp:u123456@u123456.your-storagebox.de:/bitploy
RESTIC_PASSWORD=change-me            # keep a copy outside this server
# or an S3-compatible bucket:
# RESTIC_REPOSITORY=s3:https://fsn1.your-objectstorage.com/my-bucket
# AWS_ACCESS_KEY_ID=...
# AWS_SECRET_ACCESS_KEY=...
```

A Hetzner Storage Box speaks SFTP on port 23. Put an SSH key for root on the box and add this to `/root/.ssh/config`:

```
Host u123456.your-storagebox.de
  Port 23
  IdentityFile /root/.ssh/storagebox_ed25519
```

First time:

```bash
sudo /opt/bitploy/backup.sh init
sudo /opt/bitploy/backup.sh          # first backup, by hand
sudo systemctl enable --now bitploy-backup.timer
```

`/etc/bitploy/bitploy.env` is deliberately **not** in the repository. Store `BITPLOY_SECRET_KEY` and the restic password in your password manager.

### Restore drill

Do this once a quarter, and once right after setting up backups.

1. **Check the newest backup without touching anything live:**
   ```bash
   sudo /opt/bitploy/backup.sh drill
   ```
   It restores the newest database to a temp folder, runs `PRAGMA integrity_check`, counts users and apps, and checks that volume tars can be read.
2. **Restore for real on a new server** (or after a loss):
   ```bash
   # a. Install bitploy with deploy/install.sh, but put the OLD BITPLOY_SECRET_KEY into
   #    /etc/bitploy/bitploy.env before the first start, and the old backup.env in place.
   # b. Stop bitploy:
   docker compose --env-file /etc/bitploy/bitploy.env -f /opt/bitploy/docker-compose.yml stop bitploy
   # c. Restore the database:
   sudo /opt/bitploy/backup.sh restic restore latest --tag db --target /tmp/restore
   sudo cp /tmp/restore/var/lib/bitploy/backups/restic-stage/bitploy.db /var/lib/bitploy/bitploy.db
   sudo rm -f /var/lib/bitploy/bitploy.db-wal /var/lib/bitploy/bitploy.db-shm
   # d. Restore the Let's Encrypt state (optional, avoids rate limits):
   sudo /opt/bitploy/backup.sh restic restore latest --tag acme --target /tmp/restore
   sudo cp -a /tmp/restore/var/lib/bitploy/acme/. /var/lib/bitploy/acme/
   # e. Start bitploy. The reconciler recreates networks, routes and containers from the database.
   docker compose --env-file /etc/bitploy/bitploy.env -f /opt/bitploy/docker-compose.yml up -d
   ```
3. **Restore an app volume.** Redeploy or start the app once so its volume exists, stop it, then:
   ```bash
   V=bp-quiet-otter-3f9a-data   # a renamed app keeps its first name: docker volume ls --filter label=nl.bitploy.app_id=<app id>
   MP=$(docker volume inspect -f '{{.Mountpoint}}' $V)
   sudo /opt/bitploy/backup.sh restic dump latest --tag vol:$V /volumes/$V.tar | sudo tar -xf - -C "$MP"
   ```
   Then start the app again.
4. Open the dashboard, check the apps and one app's data. Write down how long it took.

Apps that keep SQLite files in `/data` are backed up as a live copy of the files. Expect a crash-consistent copy, the same as after a power cut.

### Upgrades

1. Take a Hetzner snapshot.
2. Run the installer again: `sudo bash /opt/bitploy/src/install.sh` (add `GITHUB_TOKEN=…` for a private repository). It pulls the latest code, rebuilds the image and recreates bitploy when the image changed; Traefik is restarted only when its static config (`traefik.yml`) changed. If you use a published image instead (`BITPLOY_IMAGE` in `/etc/bitploy/bitploy.env`), it pulls that.
3. Migrations run when bitploy starts. Apps keep serving while it restarts, because Traefik routes straight to them.

Cloudflare changes its IP ranges now and then: run `deploy/update-cloudflare-ips.sh` in your checkout of the repository (it edits `deploy/traefik/traefik.yml`), copy the same ranges into `internal/auth/cloudflare.go`, commit, and run `install.sh` again.

### Operating notes

- Logs: `docker logs -f bitploy`, `docker logs -f bitploy-traefik`. Both rotate at 10 MB x 3.
- Crash loop: an app that dies 5 times in 5 minutes is stopped and marked `crashed` with its last log lines. Start it again after fixing the cause.
- Out of memory: the app status says "Out of memory: raise the memory limit or use less".
- Disk: builds are refused under 15% free disk. The nightly job prunes dangling bitploy images and caps the build cache at 10 GB.
- Traffic: each web app has a Traffic tab (visitors, requests, errors, bots, top pages, countries, referrers, 404s; up to 30 days, compared with the period before). It reads Traefik's access log, `<data dir>/traefik/logs/access.log`, which bitploy keeps small by truncating it. No cookies, no stored IP addresses or user agents. Visitors are counted per UTC day. Country data is from [DB-IP.com](https://db-ip.com) (IP to Country Lite, CC BY 4.0). **Servers installed before the Traffic tab existed: run `install.sh` again** (it creates the log folder, installs the current `traefik.yml` and restarts Traefik); until then there are no stats. Without `install.sh`: create `<data dir>/traefik/logs`, copy the current `traefik.yml` and `docker-compose.yml` to `/opt/bitploy`, then `docker compose up -d` and `docker restart bitploy-traefik`.
- Password protection: a web app can ask for a username and password (browser login prompt, set in the app's Settings tab or by the AI assistant when the user asks). Traefik checks it with a bcrypt hash in the app's route file; the password itself is never stored. It covers the app's address and its custom domains, not bitploy's own health checks. **Servers installed before password protection existed: run `install.sh` again** so Traefik's access log keeps `OriginStatus`; without it the login prompt's 401 responses are counted as errors in the Traffic tab. The protection works either way.
- Metrics: 15-second samples for 24 hours, 5-minute rollups for 30 days, audit log for 90 days.

## Architecture

```
  browser / AI agent
        |  HTTPS
   Cloudflare (proxied)            custom domains come straight in
        |  HTTPS (Origin CA)               |  HTTPS (Let's Encrypt)
        +------------------+---------------+
                           v
                       Traefik  <-- reads route files (/var/lib/bitploy/traefik/dynamic)
                  /                \
        bitploy :8080         app containers (bp-<slug>-<n>, one network each)
   dashboard, /api/v1, /mcp          ^
   service layer, deploy worker,     |  Docker API (docker.sock)
   reconciler, metrics  ------------+
        |
     SQLite (WAL) + data dir
```

- **Three doors, one service layer.** The dashboard, REST API and MCP server call the same Go functions in `internal/app`, which is the only place with business rules.
- **Traefik sends app traffic straight to containers**, never through bitploy, so apps keep serving while bitploy restarts. bitploy writes one YAML route file per app (atomic rename); Traefik picks it up within a second.
- **Deploys** go through a queue in SQLite: fetch, validate the Dockerfile, `docker buildx build`, start, health check through Traefik, swap the route, stop the old container. A failed deploy leaves the old version running.
- **Reconciler** compares the database with real containers, networks and route files every 60 seconds and repairs drift.
- **Traffic stats** (`internal/traffic`): Traefik writes its access log to a file in the data dir; bitploy tails it every 5 s, counts per app in memory and saves once a minute (every 2 s while someone has the numbers open).
- **Metrics** (`internal/metrics`): one streaming stats connection per running container, a 15 s ticker writing one sample per app in a single transaction (the "now" numbers and the end of the hour chart come straight from the last ~5 s of the stream), a Docker events listener (`die`, `oom`, `health_status`) that updates app status at once and detects crash loops, and the nightly job.

| Path | Contents |
| --- | --- |
| `cmd/bitploy` | `serve`, `migrate`, `admin create-user`, `admin invite` |
| `internal/app` | service layer |
| `internal/auth`, `internal/api`, `internal/mcp` | the three doors |
| `internal/deploy`, `internal/dockerx`, `internal/proxy` | builds, Docker wrapper, Traefik route files |
| `internal/domains` | hostname rules and DNS checks for custom domains |
| `internal/metrics` | collector, rollups, nightly job |
| `internal/traffic` | access log reader, visitor counting, country lookup |
| `internal/store` | SQLite, migrations, sqlc queries, crypto |
| `web` | Vue dashboard (embedded into the binary from `web/dist`) |
| `deploy` | compose files, Traefik config, `daemon.json`, firewall, install, backup, dev scripts |
