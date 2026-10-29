#!/usr/bin/env bash
# Nightly backup of bitploy to a restic repository (Hetzner Storage Box or any
# S3-compatible bucket). Keeps 7 daily and 4 weekly snapshots.
#
#   backup.sh [backup]   copy the database, tar every app volume, send to restic, prune
#   backup.sh init       create the restic repository (once)
#   backup.sh drill      restore drill: restore the newest database and check it (touches nothing live)
#   backup.sh restic ... run any restic command with the repository settings loaded
#
# Settings live in /etc/bitploy/backup.env (mode 0600), for example:
#   RESTIC_REPOSITORY=sftp:u123456@u123456.your-storagebox.de:/bitploy
#   RESTIC_PASSWORD=...                     # keep a copy somewhere that is not this server
#   # S3-compatible bucket instead:
#   # RESTIC_REPOSITORY=s3:https://fsn1.your-objectstorage.com/my-bucket
#   # AWS_ACCESS_KEY_ID=...  AWS_SECRET_ACCESS_KEY=...
#   BITPLOY_HEALTHCHECK_URL=                # optional: pinged after a good run (healthchecks.io style)
# A Hetzner Storage Box speaks SFTP on port 23. restic calls ssh, so put this in /root/.ssh/config:
#   Host u123456.your-storagebox.de
#     Port 23
#     IdentityFile /root/.ssh/storagebox_ed25519
#
# What is in the repository: the SQLite database (a consistent VACUUM INTO copy),
# the Let's Encrypt state, one tar per app volume. NOT in it: /etc/bitploy/bitploy.env.
# BITPLOY_SECRET_KEY must be backed up separately; see README.
set -euo pipefail

CONF="${BITPLOY_BACKUP_ENV:-/etc/bitploy/backup.env}"
ENV_FILE="${BITPLOY_ENV_FILE:-/etc/bitploy/bitploy.env}"

die() { echo "backup.sh: $*" >&2; exit 1; }
log() { echo "backup.sh: $*"; }

[ -r "$CONF" ] || die "$CONF not found. Create it first; see the comments at the top of this script."
set -a
# shellcheck disable=SC1090
. "$CONF"
set +a
[ -n "${RESTIC_REPOSITORY:-}" ] || die "RESTIC_REPOSITORY is not set in $CONF"
[ -n "${RESTIC_PASSWORD:-}${RESTIC_PASSWORD_FILE:-}" ] || die "RESTIC_PASSWORD is not set in $CONF"
command -v restic >/dev/null || die "restic is not installed (apt-get install restic)"

DATA_DIR="${BITPLOY_DATA_DIR:-}"
if [ -z "$DATA_DIR" ] && [ -r "$ENV_FILE" ]; then
  DATA_DIR="$(grep '^BITPLOY_DATA_DIR=' "$ENV_FILE" | head -n1 | cut -d= -f2- || true)"
fi
DATA_DIR="${DATA_DIR:-/var/lib/bitploy}"
STAGE="$DATA_DIR/backups/restic-stage"

cleanup() { rm -rf "$STAGE" "${DRILL_DIR:-}"; }
trap cleanup EXIT

copy_database() {
  mkdir -p "$STAGE"
  rm -f "$STAGE/bitploy.db"
  if command -v sqlite3 >/dev/null; then
    # VACUUM INTO is consistent while bitploy keeps writing (WAL).
    sqlite3 "$DATA_DIR/bitploy.db" ".timeout 20000" "VACUUM INTO '$STAGE/bitploy.db'"
    return
  fi
  local newest
  newest="$(ls -1t "$DATA_DIR"/backups/bitploy-*.db 2>/dev/null | head -n1 || true)"
  [ -n "$newest" ] || die "sqlite3 is not installed and there is no copy from bitploy's nightly job in $DATA_DIR/backups"
  log "sqlite3 not found; using the nightly copy $newest"
  cp "$newest" "$STAGE/bitploy.db"
}

backup_volumes() {
  command -v docker >/dev/null || { log "docker not found; skipping volumes"; return; }
  local v mp count=0
  while IFS= read -r v; do
    [ -n "$v" ] || continue
    mp="$(docker volume inspect -f '{{.Mountpoint}}' "$v")"
    [ -d "$mp" ] || { log "volume $v has no mountpoint on this host; skipping"; continue; }
    # A tar stream per volume. pipefail turns a failing tar into a failing backup.
    tar -C "$mp" -cf - . | restic backup --quiet --stdin --stdin-filename "volumes/$v.tar" --tag volume --tag "vol:$v"
    count=$((count + 1))
  done < <(docker volume ls -q --filter label=nl.bitploy.managed=1)
  log "backed up $count app volume(s)"
}

do_backup() {
  restic cat config >/dev/null 2>&1 || die "the repository is not initialised: run '$0 init' first"
  log "copying the database"
  copy_database
  restic backup --quiet --tag db "$STAGE/bitploy.db"
  rm -rf "$STAGE"
  if [ -d "$DATA_DIR/acme" ]; then restic backup --quiet --tag acme "$DATA_DIR/acme"; fi
  backup_volumes
  log "pruning: keep 7 daily and 4 weekly snapshots"
  restic forget --quiet --group-by host,tags --keep-daily 7 --keep-weekly 4 --prune
  if [ "$(date -u +%u)" = 7 ]; then
    log "weekly check of the repository"
    restic check --quiet --read-data-subset=2%
  fi
  if [ -n "${BITPLOY_HEALTHCHECK_URL:-}" ]; then curl -fsS --max-time 10 -o /dev/null "$BITPLOY_HEALTHCHECK_URL" || true; fi
  log "done"
}

do_drill() {
  command -v sqlite3 >/dev/null || die "the drill needs sqlite3 (apt-get install sqlite3)"
  DRILL_DIR="$(mktemp -d)"
  log "restoring the newest database snapshot to $DRILL_DIR (nothing live is touched)"
  restic restore latest --quiet --tag db --target "$DRILL_DIR"
  local db
  db="$(find "$DRILL_DIR" -name bitploy.db -type f | head -n1)"
  [ -n "$db" ] || die "no bitploy.db in the newest snapshot"
  [ "$(sqlite3 "$db" 'PRAGMA integrity_check;')" = ok ] || die "the restored database fails PRAGMA integrity_check"
  log "database ok: $(sqlite3 "$db" 'SELECT count(*) FROM users;') users, $(sqlite3 "$db" 'SELECT count(*) FROM apps;') apps, $(sqlite3 "$db" 'SELECT count(*) FROM audit_log;') audit rows"
  log "newest snapshots:"
  restic snapshots --group-by host,tags --latest 1 --compact
  local v
  for v in $(docker volume ls -q --filter label=nl.bitploy.managed=1 2>/dev/null | head -n3); do
    if restic dump latest --tag "vol:$v" "/volumes/$v.tar" 2>/dev/null | tar -tf - >/dev/null 2>&1; then
      log "volume $v: tar readable"
    else
      log "WARNING: volume $v: no readable tar in the repository"
    fi
  done
  log "drill finished. To restore for real, follow 'Restore drill' in the README."
}

case "${1:-backup}" in
  backup) do_backup ;;
  init) restic init ;;
  drill) do_drill ;;
  restic) shift; restic "$@" ;;
  *) die "usage: $0 [backup|init|drill|restic ...]" ;;
esac
