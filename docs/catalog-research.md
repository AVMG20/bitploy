# Catalog research (2026-11-04)

Working notes for the catalog agent; delete once the entries are built. Versions
come from GitHub `releases/latest` / Docker Hub on 2026-11-04 — re-check each tag
exists (and whether GHCR tags carry a leading `v`) before pinning. Ports, paths
and env names are from upstream docs/knowledge: smoke-test every entry.

S = single container (SQLite/embedded), M = multi-service (needs a service template).

## Build first (usefulness x simplicity)

| # | App | Shape | Image : tag | Port | Data | Env / secrets | RAM | Health | Gotchas |
|---|---|---|---|---|---|---|---|---|---|
| 1 | n8n — automate tasks between apps, like Zapier | S | `n8nio/n8n:2.46.6` | 5678 | `/home/node/.n8n` | `N8N_ENCRYPTION_KEY` (secret), `WEBHOOK_URL={url}`, `N8N_PROTOCOL=https`, `N8N_PROXY_HOPS=1`, `GENERIC_TIMEZONE` | 512 MB | `/healthz` | runs as `node`; websockets; losing the key makes saved credentials unreadable |
| 2 | Umami — privacy-friendly visitor stats | M (+PostgreSQL) | `ghcr.io/umami-software/umami:postgresql-v3.4.1` (verify tag format) | 3000 | in Postgres | `DATABASE_URL`, `APP_SECRET` (secret) | 256 + 256 MB | `/api/heartbeat` | default login admin/umami — first step: change it |
| 3 | Linkding — bookmarks with tags and search | S | `sissbruecker/linkding:1.47.0` | 9090 | `/etc/linkding/data` | `LD_SUPERUSER_NAME`, `LD_SUPERUSER_PASSWORD` (secret) | 128 MB | `/health` | check it runs with dropped caps |
| 4 | PrivateBin — share text the server cannot read | S | `privatebin/nginx-fpm-alpine:2.1.0` | 8080 | `/srv/data` | — | 64 MB | `/` | runs as 65534 on 8080: ideal |
| 5 | Excalidraw — whiteboard for sketches | S, stateless | `excalidraw/excalidraw` — no version tags, pin by digest | 80 | — | — | 64 MB | `/` | nginx on 80 needs NET_BIND_SERVICE or a port change; no live collaboration |
| 6 | Open WebUI — ChatGPT-style UI for your own AI keys | S | `ghcr.io/open-webui/open-webui:v0.13.0` | 8080 | `/app/backend/data` | `WEBUI_SECRET_KEY` (secret), `WEBUI_URL={url}`, user's `OPENAI_API_KEY` | 1 GB | `/health` | first start downloads models: long start period |
| 7 | Vikunja — to-do lists and kanban | S | `vikunja/vikunja:2.7.1` | 3456 | `/app/vikunja/files`, SQLite `/db` | `VIKUNJA_SERVICE_PUBLICURL={url}`, `VIKUNJA_SERVICE_JWTSECRET` (secret) | 128 MB | `/api/v1/info` | uid 1000 |
| 8 | ntfy — phone notifications from an HTTP request | S | `binwiederhier/ntfy` (version unverified, ~v2.x) | `NTFY_LISTEN_HTTP=:2586` | `/var/lib/ntfy` | `NTFY_BASE_URL={url}`, `NTFY_BEHIND_PROXY=true`, cache/auth files under /var/lib/ntfy, `NTFY_AUTH_DEFAULT_ACCESS=deny-all` | 64 MB | `/v1/health` | command `serve`; alt: Gotify `gotify/server:3.1.1` |
| 9 | Mealie — recipes and meal plans | S | `ghcr.io/mealie-recipes/mealie:v3.30.0` | 9000 | `/app/data` | `BASE_URL={url}`, `ALLOW_SIGNUP=false` | 512 MB | `/api/app/about` | default login changeme@example.com |
| 10 | Docmost — team wiki, Notion-like | M (+PostgreSQL +Valkey) | `docmost/docmost:v0.97.0` | 3000 | `/app/data/storage` | `APP_URL={url}`, `APP_SECRET` (secret), `DATABASE_URL`, `REDIS_URL` | 512 MB | `/api/health` | websockets |
| 11 | Forgejo — Git hosting with issues and PRs | S | `codeberg.org/forgejo/forgejo:<16.x or 17.0.1>-rootless` | 3000 | `/data` | `FORGEJO__server__ROOT_URL={url}`, `FORGEJO__database__DB_TYPE=sqlite3` | 256 MB | `/api/healthz` | HTTPS clone only (no SSH) |
| 12 | Ghost — blog and newsletter | M (+MySQL 8) | `ghost:6.75.0` | 2368 | `/var/lib/ghost/content` | `url={url}`, `database__client=mysql`, `database__connection__*` | 512 + 512 MB | `/ghost/api/admin/site/` | SMTP needed for invites/newsletters |

## Runners-up

| App | Shape | Image : tag | Port | Notes |
|---|---|---|---|---|
| Shlink — short links with stats | S (`DB_DRIVER=sqlite`, `/etc/shlink/data`) | `shlinkio/shlink:5.1.8` | 8080 | `INITIAL_API_KEY`, `IS_HTTPS_ENABLED=true`; web client is separate |
| NocoDB — spreadsheet-style database | S | `nocodb/nocodb:2026.10.1` | 8080 | `/usr/app/data`, `NC_AUTH_JWT_SECRET`, `NC_PUBLIC_URL`; 512 MB |
| Directus — admin panel + API on a database | S (sqlite3) | `directus/directus:12.6.1` | 8055 | `SECRET`, `ADMIN_EMAIL/PASSWORD`, `PUBLIC_URL`; 512 MB |
| Stirling-PDF — merge/split/convert PDFs | S, stateless | `stirlingtools/stirling-pdf:v2.37.2` | 8080 | 768 MB; big image |
| Karakeep — bookmarks with AI tagging | M (+Meilisearch +headless Chrome) | `ghcr.io/karakeep-app/karakeep:0.34.1` | 3000 | 1 GB |
| Linkwarden — archive bookmarked pages | M (+PostgreSQL) | `ghcr.io/linkwarden/linkwarden:v2.16.3` | 3000 | `NEXTAUTH_URL/SECRET`, 768 MB |
| Miniflux — minimal RSS reader | M (+PostgreSQL) | `miniflux/miniflux:2.3.5` | 8080 | `RUN_MIGRATIONS=1`, `CREATE_ADMIN=1`; 64 MB |
| FreshRSS — RSS reader | S | `freshrss/freshrss:1.30.4` | 80 | Apache on 80 |
| Glance — dashboard of feeds and bookmarks | S | `glanceapp/glance:v0.8.7` | 8080 | needs a YAML config file |
| Listmonk — newsletters | M (+PostgreSQL) | latest is a v7 beta: wait for stable | 9000 | needs SMTP |
| Metabase — charts from your data | M (+PostgreSQL) | `metabase/metabase:v0.65.2` | 3000 | 1 GB JVM |
| code-server — VS Code in the browser | S | `codercom/code-server:4.144.0` | 8080 | 1 GB; mark advanced |

## Not now

Immich, Jellyfin (too heavy), Plane (8+ services), Plausible (ClickHouse), Outline
(needs SSO), Cal.com (build-time env), Nextcloud (root + upgrade pain), Homepage
(needs the Docker socket), MinIO (archived upstream; Garage needs a config file).
