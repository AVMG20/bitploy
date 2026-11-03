#!/usr/bin/env bash
# `make dev`: the Go server, Vite and a local Traefik, all in the foreground.
#
#   dashboard   http://localhost:5173        (Vite, proxies /api and /mcp to the Go server)
#   Go server   http://localhost:8080
#   apps        http://<slug>.localhost:8000 (Traefik in Docker, plain HTTP)
#
# Data lives in ./.data. Delete it to start from scratch. Press Ctrl-C to stop the
# Go server and Vite; Traefik keeps running (it is cheap). Stop it with
#   docker compose -f deploy/docker-compose.dev.yml down
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
command -v docker >/dev/null || { echo "dev.sh: Docker is not installed or not on PATH" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "dev.sh: the Docker daemon is not running" >&2; exit 1; }

mkdir -p .data/traefik/dynamic .data/traefik/logs
# Traefik (root in its container) appends to this file and the Go server, running as you,
# truncates it. Create it open for both so that works on Linux too.
touch .data/traefik/logs/access.log
chmod 666 .data/traefik/logs/access.log

# Dev settings from docs/CONTRACT.md.
export BITPLOY_DEV=1
export BITPLOY_APPS_DOMAIN=localhost
export BITPLOY_APPS_SCHEME=http
export BITPLOY_APPS_PORT=8000
export BITPLOY_PUBLIC_URL=http://localhost:5173
export BITPLOY_TRAEFIK_INTERNAL_URL=http://localhost:8081
export BITPLOY_INTERNAL_URL=http://host.docker.internal:8080
export BITPLOY_DATA_DIR=./.data

# A stable secret key, so env vars saved in the dev database survive restarts.
# Set BITPLOY_SECRET_KEY yourself to use another one.
if [ -z "${BITPLOY_SECRET_KEY:-}" ]; then
  if [ ! -f .data/dev-secret-key ]; then
    ( umask 077; openssl rand -base64 32 >.data/dev-secret-key )
  fi
  BITPLOY_SECRET_KEY="$(cat .data/dev-secret-key)"
  export BITPLOY_SECRET_KEY
fi

# The SQLite viewer runs its queries in a helper container with a Linux build of this
# program (`bitploy db-helper`). On Linux bitploy uses itself; on a Mac it needs this one,
# built for the architecture of Docker's VM. Without it the viewer answers
# db_helper_unavailable and everything else works.
case "$(docker info --format '{{.Architecture}}')" in
  x86_64) helper_arch=amd64 ;;
  aarch64) helper_arch=arm64 ;;
  *) helper_arch="" ;;
esac
if [ -n "$helper_arch" ]; then
  if CGO_ENABLED=0 GOOS=linux GOARCH="$helper_arch" go build -trimpath -o .data/bitploy-db-helper ./cmd/bitploy; then
    BITPLOY_DB_HELPER_BIN="$PWD/.data/bitploy-db-helper"
    export BITPLOY_DB_HELPER_BIN
  else
    echo "dev.sh: could not build the database helper; the SQLite viewer will not work" >&2
  fi
else
  echo "dev.sh: unknown Docker architecture; the SQLite viewer will not work" >&2
fi

docker compose -f deploy/docker-compose.dev.yml up -d

if [ ! -d web/node_modules ]; then
  echo "Installing web dependencies..."
  (cd web && npm ci --no-audit --no-fund)
fi

# First run: a dev admin so you can log in at once.
if [ ! -f .data/.dev-admin-created ]; then
  if BITPLOY_ADMIN_PASSWORD="devpassword123" go run ./cmd/bitploy admin create-user --email dev@bitploy.localhost --name Dev --admin; then
    touch .data/.dev-admin-created
    echo "Dev login: dev@bitploy.localhost / devpassword123"
  fi
fi

pids=()
cleanup() {
  trap - EXIT INT TERM
  for pid in ${pids[@]+"${pids[@]}"}; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

go run ./cmd/bitploy serve &
pids+=("$!")
(cd web && exec npm run dev) &
pids+=("$!")

echo
echo "Dashboard: http://localhost:5173   Apps: http://<slug>.localhost:8000   (Ctrl-C to stop)"
# Stop as soon as either process exits (macOS ships bash 3.2, which has no `wait -n`).
while kill -0 "${pids[0]}" 2>/dev/null && kill -0 "${pids[1]}" 2>/dev/null; do sleep 1; done
