#!/usr/bin/env bash
# Proves that firewall.sh isolates app containers. Run as root on a Linux host
# with Docker, after /etc/docker/daemon.json and firewall.sh are applied.
# CI runs it on ubuntu-latest; run it on the server after changing the firewall.
#
# It starts a probe container on a fresh network "bp-probe" (a /24 from the app
# pool, like any app) and checks:
#   must work    the internet; another container on the same network (Traefik <-> app)
#   must fail    the host (bridge gateway and primary address), the metadata
#                address, a container on another app network
#
# Before it checks the "must fail" cases it takes the firewall away once, so a
# green run means the rules are what blocks the traffic, not the test setup.
# It only creates and removes containers and networks named bp-probe*.
#
#   PROBE_IMAGE=alpine:3.24  image with busybox wget
#   PROBE_URL=http://example.com   an HTTP page on the internet
#   PROBE_SKIP_BASELINE=1    do not remove the firewall for the control run
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="${PROBE_IMAGE:-alpine:3.24}"
URL="${PROBE_URL:-http://example.com}"
HOST_PORT="${PROBE_HOST_PORT:-18099}"
NET="bp-probe"
NET_OTHER="bp-probe-other"
CLIENT="bp-probe-client"
SIBLING="bp-probe-sibling"
OTHER="bp-probe-other-app"
LABEL="nl.bitploy.probe=1"
METADATA_IP="169.254.169.254"

fail=0
listener_pid=""
www=""

say() { printf '%s\n' "$*"; }
pass() { say "  ok    $*"; }
bad() { say "  FAIL  $*"; fail=1; }

cleanup() {
  if [ -n "$listener_pid" ]; then kill "$listener_pid" 2>/dev/null || true; fi
  if [ -n "$www" ]; then rm -rf "$www"; fi
  docker rm -f "$CLIENT" "$SIBLING" "$OTHER" >/dev/null 2>&1 || true
  docker network rm "$NET" "$NET_OTHER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

[ "$(uname -s)" = Linux ] || { say "probe-test.sh needs Linux"; exit 2; }
[ "$(id -u)" -eq 0 ] || { say "run as root"; exit 2; }
command -v docker >/dev/null || { say "docker not found"; exit 2; }
command -v python3 >/dev/null || { say "python3 not found (used for the host-side test server)"; exit 2; }

# Fetches a URL from inside the probe container; exit code 0 means "reached it with HTTP 200".
reach() { docker exec "$CLIENT" wget -q -T 4 -O /dev/null "$1" >/dev/null 2>&1; }

expect_reach() {
  if reach "$2"; then pass "$1"; else bad "$1 (expected to work: $2)"; fi
}
expect_blocked() {
  if reach "$2"; then bad "$1 (expected to be blocked, but $2 answered)"; else pass "$1"; fi
}

metadata_drops() {
  iptables -nvxL BITPLOY-APPS 2>/dev/null | awk -v ip="$METADATA_IP" '$3 == "DROP" && $9 ~ ip { print $1; exit }'
}

cleanup
say "Setting up"
docker network create --label "$LABEL" "$NET" >/dev/null
docker network create --label "$LABEL" "$NET_OTHER" >/dev/null
docker pull -q "$IMAGE" >/dev/null

subnet() { docker network inspect "$1" -f '{{(index .IPAM.Config 0).Subnet}}'; }
gateway() { docker network inspect "$1" -f '{{(index .IPAM.Config 0).Gateway}}'; }
say "  $NET is $(subnet "$NET"), gateway $(gateway "$NET")"
case "$(subnet "$NET")" in
  10.200.*) ;;
  *) bad "network $NET is not in 10.200.0.0/16: is /etc/docker/daemon.json applied and Docker restarted?" ;;
esac

# A web server on the host, reachable on every address of the host.
www="$(mktemp -d)"
echo ok >"$www/index.html"
python3 -m http.server "$HOST_PORT" --bind 0.0.0.0 --directory "$www" >/dev/null 2>&1 &
listener_pid=$!
for _ in $(seq 1 20); do
  curl -fsS -o /dev/null "http://127.0.0.1:$HOST_PORT/" 2>/dev/null && break
  sleep 0.25
done
curl -fsS -o /dev/null "http://127.0.0.1:$HOST_PORT/" || { say "cannot start the host-side test server on port $HOST_PORT"; exit 2; }

HOST_IP="$(ip -4 route get 1.1.1.1 | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit } }')"
GATEWAY="$(gateway "$NET")"
[ -n "$HOST_IP" ] || { say "cannot find the host's primary address"; exit 2; }
say "  host address $HOST_IP, test server on port $HOST_PORT"

docker run -d --name "$CLIENT" --network "$NET" --label "$LABEL" "$IMAGE" sleep 600 >/dev/null
docker run -d --name "$SIBLING" --network "$NET" --label "$LABEL" "$IMAGE" \
  sh -c 'echo ok >/tmp/index.html; exec httpd -f -p 8000 -h /tmp' >/dev/null
docker run -d --name "$OTHER" --network "$NET_OTHER" --label "$LABEL" "$IMAGE" \
  sh -c 'echo ok >/tmp/index.html; exec httpd -f -p 8000 -h /tmp' >/dev/null
SIBLING_IP="$(docker inspect "$SIBLING" -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}")"
OTHER_IP="$(docker inspect "$OTHER" -f "{{(index .NetworkSettings.Networks \"$NET_OTHER\").IPAddress}}")"
sleep 1

if [ -z "${PROBE_SKIP_BASELINE:-}" ]; then
  say "Control run without the firewall (the traffic must get through)"
  "$HERE/firewall.sh" remove >/dev/null
  expect_reach "host gateway $GATEWAY:$HOST_PORT reachable without rules" "http://$GATEWAY:$HOST_PORT/"
  expect_reach "host address $HOST_IP:$HOST_PORT reachable without rules" "http://$HOST_IP:$HOST_PORT/"
  if ! reach "http://$OTHER_IP:8000/"; then
    say "  note  other app $OTHER_IP is isolated by Docker itself, even without our rules"
  fi
  [ "$fail" -eq 0 ] || { say "The control run failed: the test setup is wrong, not the firewall."; exit 2; }
fi

say "Applying the firewall (twice, to prove it is idempotent)"
"$HERE/firewall.sh" apply >/dev/null
rules_before="$(iptables-save | grep -c BITPLOY || true)"
"$HERE/firewall.sh" apply >/dev/null
rules_after="$(iptables-save | grep -c BITPLOY || true)"
if [ "$rules_before" = "$rules_after" ] && [ "$rules_after" -gt 0 ]; then
  pass "second run left $rules_after rule lines unchanged"
else
  bad "rule lines changed from $rules_before to $rules_after"
fi
jumps="$(iptables -S DOCKER-USER | grep -c -- '-j BITPLOY-APPS' || true)"
if [ "$jumps" = 1 ]; then pass "exactly one jump from DOCKER-USER"; else bad "$jumps jumps from DOCKER-USER"; fi

say "Must work"
expect_reach "internet ($URL)" "$URL"
expect_reach "another container on the same app network (Traefik <-> app)" "http://$SIBLING_IP:8000/"

say "Must be blocked"
drops_before="$(metadata_drops || true)"
expect_blocked "host via bridge gateway $GATEWAY:$HOST_PORT" "http://$GATEWAY:$HOST_PORT/"
expect_blocked "host via primary address $HOST_IP:$HOST_PORT" "http://$HOST_IP:$HOST_PORT/"
expect_blocked "container of another app $OTHER_IP:8000" "http://$OTHER_IP:8000/"
expect_blocked "metadata service $METADATA_IP" "http://$METADATA_IP/"
drops_after="$(metadata_drops || true)"
if [ "${drops_after:-0}" -gt "${drops_before:-0}" ]; then
  pass "the metadata rule dropped packets (${drops_before:-0} -> ${drops_after:-0})"
else
  bad "the metadata rule counted no dropped packets"
fi
expect_blocked "private range 192.168.1.1" "http://192.168.1.1/"

if [ "$fail" -eq 0 ]; then
  say "All checks passed."
else
  say "Some checks FAILED."
fi
exit "$fail"
