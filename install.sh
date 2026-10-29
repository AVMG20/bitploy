#!/usr/bin/env bash
# One command to install or upgrade bitploy on a fresh Ubuntu 24.04/26.04 server.
#
# From a checkout:     sudo ./install.sh
# Without a checkout:  copy this file to the server and run  sudo bash install.sh
#                      (for a private repository also set GITHUB_TOKEN, a token
#                      that can read the repo; it is used for this clone only)
#
# It installs git and Docker when they are missing, fetches (or updates) the
# source in /opt/bitploy/src, then runs deploy/install.sh, which sets up Docker,
# the firewall, settings, Traefik and bitploy, builds the bitploy image on the
# server and offers to create the first admin. Run it again at any time to
# upgrade: it pulls the latest code, rebuilds and restarts, keeping your data.
#
# Settings can be passed as environment variables to skip the questions; see
# the header of deploy/install.sh. Extra here:
#   BITPLOY_REPO    git URL (default https://github.com/AVMG20/bitploy.git)
#   BITPLOY_BRANCH  branch to install (default main)
set -euo pipefail

REPO_URL="${BITPLOY_REPO:-https://github.com/AVMG20/bitploy.git}"
BRANCH="${BITPLOY_BRANCH:-main}"
SRC=/opt/bitploy/src

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "run as root: sudo bash $0"
[ "$(uname -s)" = Linux ] || die "install.sh only runs on Linux"

# Run from a checkout: use it as it is.
here=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi
if [ -n "$here" ] && [ -f "$here/deploy/install.sh" ] && [ "$here" != "$SRC" ]; then
  exec bash "$here/deploy/install.sh" "$@"
fi

if ! command -v git >/dev/null; then
  echo "==> Installing git"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq git ca-certificates >/dev/null
fi

# The token goes in a one-off HTTP header, never into .git/config.
git_auth=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  basic="$(printf 'x-access-token:%s' "$GITHUB_TOKEN" | base64 -w0)"
  git_auth=(-c "http.https://github.com/.extraheader=AUTHORIZATION: basic $basic")
fi

if [ -d "$SRC/.git" ]; then
  echo "==> Updating $SRC ($BRANCH)"
  git "${git_auth[@]}" -C "$SRC" fetch --depth 1 origin "$BRANCH"
  git -C "$SRC" checkout -q -B "$BRANCH" FETCH_HEAD
else
  echo "==> Downloading bitploy into $SRC"
  install -d -m 0755 "$(dirname "$SRC")"
  if ! git "${git_auth[@]}" clone -q --depth 1 --branch "$BRANCH" "$REPO_URL" "$SRC"; then
    die "could not clone $REPO_URL. For a private repository run: sudo GITHUB_TOKEN=<token with read access> bash $0"
  fi
fi

exec bash "$SRC/deploy/install.sh" "$@"
