#!/usr/bin/env bash
# Refresh the Cloudflare ranges in traefik/traefik.yml (forwardedHeaders.trustedIPs)
# from https://www.cloudflare.com/ips. Run it, review the diff, then
#   docker compose --env-file /etc/bitploy/bitploy.env up -d --force-recreate traefik
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
file="${1:-$dir/traefik/traefik.yml}"

v4="$(curl -fsS --max-time 20 https://www.cloudflare.com/ips-v4)"
v6="$(curl -fsS --max-time 20 https://www.cloudflare.com/ips-v6)"
if [ "$(printf '%s\n' "$v4" | wc -l)" -lt 5 ] || [ "$(printf '%s\n' "$v6" | wc -l)" -lt 3 ]; then
  echo "Cloudflare returned fewer ranges than expected; leaving $file unchanged." >&2
  exit 1
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
{
  sed -n '1,/# BEGIN cloudflare/p' "$file"
  printf '%s\n%s\n' "$v4" "$v6" | sed 's/^/        - /'
  sed -n '/# END cloudflare/,$p' "$file"
} >"$tmp"
cat "$tmp" >"$file"
echo "Updated $file"
