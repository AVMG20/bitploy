# bitploy

bitploy is a small, self-hosted deploy platform for one server. You give it a Dockerfile and some code, and it gives you back `https://<name>.yourdomain.nl`. Its main users are AI coding agents: they deploy through an MCP server or a REST API. People log in to a dashboard to create tokens, read logs and watch usage.

- One Go binary with the Vue dashboard embedded, one SQLite file, Traefik in front, Docker underneath.
- Every app is a Dockerfile. bitploy never guesses how to build code.
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

**Back up `BITPLOY_SECRET_KEY` now, somewhere that is not this server** (a password manager). It encrypts every saved env var and GitHub token. Without it a restored database is readable, but those secrets are gone for good.

```bash
sudo grep BITPLOY_SECRET_KEY /etc/bitploy/bitploy.env
```

### Configuration

All settings are environment variables in `/etc/bitploy/bitploy.env`. Restart with `docker compose --env-file /etc/bitploy/bitploy.env -f /opt/bitploy/docker-compose.yml up -d` after a change.

| Variable | Example | Meaning |
| --- | --- | --- |
| `BITPLOY_PUBLIC_URL` | `https://bitploy.nl` | Dashboard, API and MCP origin; also the CSRF origin |
| `BITPLOY_APPS_DOMAIN` | `bitploy.nl` | Parent domain of app subdomains |
| `BITPLOY_EDGE_HOST` | `edge.bitploy.nl` | DNS-only name that custom domains point to (default `edge.<apps domain>`) |
| `BITPLOY_SERVER_IPV4`, `BITPLOY_SERVER_IPV6` | `203.0.113.10` | Shown in DNS instructions and accepted by DNS checks |
| `BITPLOY_SECRET_KEY` | 32 random bytes, base64 | Encrypts env vars and GitHub tokens. `openssl rand -base64 32` |
| `BITPLOY_DATA_DIR` | `/var/lib/bitploy` | Database, uploads, logs, route files, backups |
| `BITPLOY_ACME_EMAIL` | `you@example.nl` | Let's Encrypt account (written into Traefik's config by `install.sh`) |
| `BITPLOY_SIGNUP` | `invite` | `invite` or `open` |
| `BITPLOY_ADMIN_PASSWORD` | — | Password for `bitploy admin create-user` without a prompt (scripts only) |
| `BITPLOY_MAX_PARALLEL_BUILDS` | `2` | Server-wide build slots |
| `BITPLOY_BUILDER_CPUS`, `BITPLOY_BUILDER_MEMORY` | `2`, `3g` | Limits of the isolated build container (builds run in their own buildx builder on the firewalled `bp-builder` network) |
| `BITPLOY_MAX_APPS` | `100` | Most apps on the server, all users together. Each user can have 10 by default; change that per user in Admin, Users |
| `BITPLOY_LISTEN` | `:8080` | Listen address |
| `BITPLOY_TRAEFIK_CONTAINER` | `bitploy-traefik` | Traefik's container name; joined to every app network |
| `BITPLOY_TRAEFIK_INTERNAL_URL` | `http://bitploy-traefik:8081` | Traefik's unpublished entrypoint, for health checks before a swap |
| `BITPLOY_INTERNAL_URL` | `http://bitploy:8080` | How Traefik reaches bitploy (dashboard route, "No app here" page) |
| `BITPLOY_APPS_SCHEME`, `BITPLOY_APPS_PORT` | `https`, empty | Scheme and port of app URLs (development uses `http` and `8000`) |
| `BITPLOY_GEOIP` | `off` | Traffic stats look up visitor countries with DB-IP's free country database, downloaded into `<data dir>/geoip`. `off` skips the download (country is then only known when Cloudflare sends it). Default on |
| `BITPLOY_DEV` | `1` | Local development only: plain HTTP, no `Secure` cookies, no Docker cache pruning |
| `BITPLOY_IMAGE` | `ghcr.io/avmg20/bitploy:1.2.0` | Used by `docker-compose.yml`, not by bitploy itself. Pin a version here. |

### Custom domains (what your users do)

`add_domain` returns two records. For `www.example.com`:

| Type | Name | Value |
| --- | --- | --- |
| CNAME | `www.example.com` | `edge.bitploy.nl` |
| TXT | `_bitploy-challenge.www.example.com` | the token bitploy shows |

A root domain (`example.com`) cannot have a CNAME in DNS, so it gets A (and AAAA) records with the server IP instead. A name counts as a root domain when it is a registrable domain on the Public Suffix List (`example.com`, `example.co.uk`; not `www.example.com`).

If the user's domain is on Cloudflare, the record must be **DNS only** (grey cloud). bitploy checks every 5 minutes for 48 hours through 1.1.1.1 and 8.8.8.8, and both must show the TXT token and a route to this server. Errors are readable, for example `CNAME points to parking.godaddy.com, expected edge.bitploy.nl`. When the checks pass, bitploy writes the route and Traefik gets the certificate.

### Network isolation

`deploy/firewall.sh` puts rules in their own chains (`BITPLOY-APPS` in `DOCKER-USER`, `BITPLOY-INPUT` in `INPUT`). From app subnets (`10.200.0.0/16`) it blocks the host, the Hetzner metadata address `169.254.169.254`, every private range, the bitploy network and other apps. Traffic inside one app's own /24 (Traefik to the app) and the internet stay open. `systemd` runs it after Docker starts. `deploy/probe-test.sh` proves it with a probe container and runs in CI.

```bash
sudo deploy/firewall.sh status
```

Known limits: bridge networks are IPv4 only, so there are no IPv6 rules. An app cannot call a hostname that resolves to this server's own address (for example a custom domain served by this same server); that traffic would end up in the private network, which the firewall drops. The Traefik `internal` entrypoint (`:8081`) is reachable from app networks; it only serves candidate health checks.

### Security notes

- The bitploy container mounts `/var/run/docker.sock`, which is root-equivalent on the host. It runs as root: the socket's group ID differs per host, and a non-root user would not make the socket any safer. Keep SSH to keys only, and run nothing else on this server.
- Only the bitploy container gets the socket. Traefik reads files and needs no socket.
- Apps run with a memory/CPU/PID limit, `no-new-privileges`, no extra capabilities, no mounts except their own volume, and on their own network.
- Builds run user code with the build container's privileges. That is acceptable for colleagues. Before opening bitploy to the public, follow the checklist at the end of "Security & limits" in [SPEC.md](SPEC.md).

### Backups

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
   # a. Install bitploy as usual (deploy/install.sh), but put the OLD BITPLOY_SECRET_KEY into
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
- Traffic: each web app has a Traffic tab (visitors, requests, top pages, countries, referrers; 30 days). It reads Traefik's access log, `<data dir>/traefik/logs/access.log`, which bitploy keeps small by truncating it. No cookies, no stored IP addresses or user agents. Visitors are counted per UTC day. Country data is from [DB-IP.com](https://db-ip.com) (IP to Country Lite, CC BY 4.0). **Existing installs: run `install.sh` again** (it creates the log folder, installs the new `traefik.yml` and restarts Traefik); until then there are no stats. Without `install.sh`: create `<data dir>/traefik/logs`, copy the new `traefik.yml` and `docker-compose.yml` to `/opt/bitploy`, then `docker compose up -d` and `docker restart bitploy-traefik`.
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
- **Traffic stats** (`internal/traffic`): Traefik writes its access log to a file in the data dir; bitploy tails it every 5 s, counts per app in memory and saves once a minute.
- **Metrics** (`internal/metrics`): one streaming stats connection per running container, a 15 s ticker writing one sample per app in a single transaction, a Docker events listener (`die`, `oom`, `health_status`) that updates app status at once and detects crash loops, and the nightly job.

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
