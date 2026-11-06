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
#   Traefik      a Traefik container set up like the real one ("bp-probe-traefik" on
#                "bp-probe-core", which plays the "bitploy" network, with the internal
#                entrypoint on its fixed address there) joins the app network:
#                its internal entrypoint must answer on the core network and must
#                not answer through its address on the app network. On a server
#                with bitploy, the real bitploy-traefik is checked the same way
#                (it joins bp-probe for the test, and bitploy must still reach it).
#   then         both apps join a project network "bp-probe-project" (like bp-p-<id>, one /24
#                from the pool): the client reaches the other app by its alias db there,
#                and still not through the other app's own network
#   public ports a network namespace "bp-probe-ext" (198.51.100.2, behind a veth
#                pair) plays a computer on the internet. Two containers on
#                "bp-probe-pub" publish a port: one inside the public port range
#                (like the forwarder of a public port), one outside it.
#     must work  from outside: the published port inside the range
#     must fail  from outside: the published port outside the range, and the
#                sibling container's own address (a service that is not public);
#                from an app: the public port of another app through the server's address
#   The range is BITPLOY_PUBLIC_PORTS (environment or /etc/bitploy/bitploy.env),
#   else 39990-39999 for the test; the firewall is put back with the server's
#   own settings at the end.
#
# Before it checks the "must fail" cases it takes the firewall away once, so a
# green run means the rules are what blocks the traffic, not the test setup.
# It only creates and removes containers and networks named bp-probe*.
#
#   PROBE_IMAGE=busybox:1.37  image with busybox wget and httpd
#   PROBE_TRAEFIK_IMAGE=traefik:v3.7   the Traefik image (same as docker-compose.yml)
#   PROBE_CORE_SUBNET=10.201.255.0/24  subnet of bp-probe-core (outside the app pool,
#                            and not the real "bitploy" network's 10.201.0.0/24)
#   PROBE_URL=http://example.com   an HTTP page on the internet
#   PROBE_SKIP_BASELINE=1    do not remove the firewall for the control run
#   PROBE_PUBLIC_PORT=39999  the port inside the range to publish (default: the highest)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="${PROBE_IMAGE:-busybox:1.37}"
URL="${PROBE_URL:-http://example.com}"
HOST_PORT="${PROBE_HOST_PORT:-18099}"
NET="bp-probe"
NET_OTHER="bp-probe-other"
NET_CORE="bp-probe-core"
CORE_SUBNET="${PROBE_CORE_SUBNET:-10.201.255.0/24}"
CORE_TRAEFIK_IP="${CORE_SUBNET%.*}.254"   # like 10.201.0.254 in docker-compose.yml
TRAEFIK_IMAGE="${PROBE_TRAEFIK_IMAGE:-traefik:v3.7}"
TRAEFIK="bp-probe-traefik"
CORE_CLIENT="bp-probe-core-client"
LIVE_TRAEFIK="bitploy-traefik"
NET_PROJECT="bp-probe-project"
CLIENT="bp-probe-client"
SIBLING="bp-probe-sibling"
OTHER="bp-probe-other-app"
LABEL="nl.bitploy.probe=1"
METADATA_IP="169.254.169.254"
NET_PUB="bp-probe-pub"
PUB_IN="bp-probe-pub-in"
PUB_OUT="bp-probe-pub-out"
EXT_NS="bp-probe-ext"
EXT_HOST_IF="bp-probe-h"   # host side of the veth pair
EXT_IF="bp-probe-e"        # inside the namespace
EXT_HOST_ADDR="198.51.100.1"
EXT_ADDR="198.51.100.2"

fail=0
firewall_removed=0
firewall_touched=0
listener_pid=""
www=""

say() { printf '%s\n' "$*"; }
pass() { say "  ok    $*"; }
bad() { say "  FAIL  $*"; fail=1; }

cleanup() {
  if [ -n "$listener_pid" ]; then kill "$listener_pid" 2>/dev/null || true; fi
  if [ -n "$www" ]; then rm -rf "$www"; fi
  docker rm -f "$CLIENT" "$SIBLING" "$OTHER" "$PUB_IN" "$PUB_OUT" "$TRAEFIK" "$CORE_CLIENT" >/dev/null 2>&1 || true
  docker network disconnect -f "$NET" "$LIVE_TRAEFIK" >/dev/null 2>&1 || true
  docker network rm "$NET" "$NET_OTHER" "$NET_PROJECT" "$NET_PUB" "$NET_CORE" >/dev/null 2>&1 || true
  ip link del "$EXT_HOST_IF" >/dev/null 2>&1 || true
  ip netns del "$EXT_NS" >/dev/null 2>&1 || true
  # The control run takes the rules away and the test may use its own port
  # range; always put the server's own rules back, also on Ctrl-C or an early exit.
  if [ "$firewall_removed" -eq 1 ] || [ "$firewall_touched" -eq 1 ]; then
    "$HERE/firewall.sh" apply >/dev/null || say "  WARNING: could not re-apply the firewall: run systemctl restart bitploy-firewall"
  fi
}
trap cleanup EXIT

[ "$(uname -s)" = Linux ] || { say "probe-test.sh needs Linux"; exit 2; }
[ "$(id -u)" -eq 0 ] || { say "run as root"; exit 2; }
command -v docker >/dev/null || { say "docker not found"; exit 2; }
command -v python3 >/dev/null || { say "python3 not found (used for the host-side test server)"; exit 2; }
command -v ip >/dev/null || { say "ip not found (apt-get install iproute2)"; exit 2; }
command -v curl >/dev/null || { say "curl not found"; exit 2; }

# Fetches a URL from inside the probe container; exit code 0 means "reached it with HTTP 200".
reach() { docker exec "$CLIENT" wget -q -T 4 -O /dev/null "$1" >/dev/null 2>&1; }
# The same from the "internet" namespace.
reach_ext() { ip netns exec "$EXT_NS" curl -fsS -m 4 -o /dev/null "$1" >/dev/null 2>&1; }

expect_reach() {
  if reach "$2"; then pass "$1"; else bad "$1 (expected to work: $2)"; fi
}
expect_blocked() {
  if reach "$2"; then bad "$1 (expected to be blocked, but $2 answered)"; else pass "$1"; fi
}
expect_reach_ext() {
  if reach_ext "$2"; then pass "$1"; else bad "$1 (expected to work: $2)"; fi
}
expect_blocked_ext() {
  if reach_ext "$2"; then bad "$1 (expected to be blocked, but $2 answered)"; else pass "$1"; fi
}

# The public port range: the server's, else one for the test. firewall.sh is
# always run with it here; cleanup runs it with the server's settings again.
PUB_RANGE="$("$HERE/firewall.sh" ports 2>/dev/null || true)"
[ -n "$PUB_RANGE" ] || PUB_RANGE="39990:39999"
PUB_LO="${PUB_RANGE%%:*}"
PUB_HI="${PUB_RANGE##*:}"
PUB_PORT="${PROBE_PUBLIC_PORT:-$PUB_HI}"
if [ "$PUB_HI" -lt 65535 ]; then OUT_PORT=$((PUB_HI + 1)); else OUT_PORT=$((PUB_LO - 1)); fi
fw_apply() { firewall_touched=1; BITPLOY_PUBLIC_PORTS="$PUB_LO-$PUB_HI" "$HERE/firewall.sh" apply; }

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
# The sibling also listens on 8081, to see whether firewall.sh's second lock on that port works here.
docker run -d --name "$SIBLING" --network "$NET" --label "$LABEL" "$IMAGE" \
  sh -c 'echo ok >/tmp/index.html; httpd -p 8081 -h /tmp; exec httpd -f -p 8000 -h /tmp' >/dev/null
docker run -d --name "$OTHER" --network "$NET_OTHER" --label "$LABEL" "$IMAGE" \
  sh -c 'echo ok >/tmp/index.html; exec httpd -f -p 8000 -h /tmp' >/dev/null
for c in "$SIBLING" "$OTHER"; do
  sleep 0.5
  if [ "$(docker inspect -f '{{.State.Running}}' "$c" 2>/dev/null)" != true ]; then
    say "probe server $c did not start: $(docker logs "$c" 2>&1 | tail -n1)"
    exit 2
  fi
done
SIBLING_IP="$(docker inspect "$SIBLING" -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}")"
OTHER_IP="$(docker inspect "$OTHER" -f "{{(index .NetworkSettings.Networks \"$NET_OTHER\").IPAddress}}")"

# Public ports: two containers that publish a port on the host's IPv4
# addresses, like the forwarder of a public port; one port inside the range,
# one outside it.
docker network create --label "$LABEL" "$NET_PUB" >/dev/null
for pair in "$PUB_IN:$PUB_PORT" "$PUB_OUT:$OUT_PORT"; do
  if ! docker run -d --name "${pair%%:*}" --network "$NET_PUB" --label "$LABEL" -p "0.0.0.0:${pair##*:}:8000" "$IMAGE" \
    sh -c 'echo ok >/tmp/index.html; exec httpd -f -p 8000 -h /tmp' >/dev/null; then
    say "cannot publish port ${pair##*:} (in use? set PROBE_PUBLIC_PORT to a free port of $PUB_LO-$PUB_HI)"
    exit 2
  fi
done
# The "internet": a network namespace behind a veth pair, routed through the host.
ip netns add "$EXT_NS"
ip link add "$EXT_HOST_IF" type veth peer name "$EXT_IF"
ip link set "$EXT_IF" netns "$EXT_NS"
ip addr add "$EXT_HOST_ADDR/30" dev "$EXT_HOST_IF"
ip link set "$EXT_HOST_IF" up
ip -n "$EXT_NS" addr add "$EXT_ADDR/30" dev "$EXT_IF"
ip -n "$EXT_NS" link set "$EXT_IF" up
ip -n "$EXT_NS" link set lo up
ip -n "$EXT_NS" route add default via "$EXT_HOST_ADDR"
say "  public ports $PUB_LO-$PUB_HI: $PUB_PORT published inside the range, $OUT_PORT outside it; outside client $EXT_ADDR"
sleep 1

if [ -z "${PROBE_SKIP_BASELINE:-}" ]; then
  say "Control run without the firewall (the traffic must get through)"
  firewall_removed=1
  "$HERE/firewall.sh" remove >/dev/null
  expect_reach "host gateway $GATEWAY:$HOST_PORT reachable without rules" "http://$GATEWAY:$HOST_PORT/"
  expect_reach "host address $HOST_IP:$HOST_PORT reachable without rules" "http://$HOST_IP:$HOST_PORT/"
  if ! reach "http://$OTHER_IP:8000/"; then
    say "  note  other app $OTHER_IP is isolated by Docker itself, even without our rules"
  fi
  expect_reach_ext "from outside: published port $PUB_PORT reachable without rules" "http://$HOST_IP:$PUB_PORT/"
  expect_reach_ext "from outside: published port $OUT_PORT (outside the range) reachable without rules" "http://$HOST_IP:$OUT_PORT/"
  if ! reach_ext "http://$SIBLING_IP:8000/"; then
    say "  note  from outside, the container address $SIBLING_IP is blocked by Docker itself, even without our rules"
  fi
  [ "$fail" -eq 0 ] || { say "The control run failed: the test setup is wrong, not the firewall."; exit 2; }
fi

say "Applying the firewall (twice, to prove it is idempotent)"
fw_apply >/dev/null
rules_before="$(iptables-save | grep -c BITPLOY || true)"
fw_apply >/dev/null
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
if reach "http://$SIBLING_IP:8081/"; then
  say "  note  port 8081 of a container on the same app network answered: traffic inside one bridge skips iptables here" \
    "(net.bridge.bridge-nf-call-iptables=$(cat /proc/sys/net/bridge/bridge-nf-call-iptables 2>/dev/null || echo off))," \
    "so only Traefik's listen address keeps apps off its internal entrypoint (checked below)"
else
  pass "port 8081 on the same app network dropped by the firewall too"
fi
expect_blocked "metadata service $METADATA_IP" "http://$METADATA_IP/"
drops_after="$(metadata_drops || true)"
if [ "${drops_after:-0}" -gt "${drops_before:-0}" ]; then
  pass "the metadata rule dropped packets (${drops_before:-0} -> ${drops_after:-0})"
else
  bad "the metadata rule counted no dropped packets"
fi
expect_blocked "private range 192.168.1.1" "http://192.168.1.1/"

say "Traefik's internal entrypoint (listens on its address on the bitploy network only)"
docker network create --label "$LABEL" --subnet "$CORE_SUBNET" "$NET_CORE" >/dev/null
docker run -d --name "$TRAEFIK" --network "$NET_CORE" --ip "$CORE_TRAEFIK_IP" --label "$LABEL" "$TRAEFIK_IMAGE" \
  --entryPoints.web.address=:80 --entryPoints.internal.address="$CORE_TRAEFIK_IP:8081" \
  --ping=true --ping.entryPoint=internal >/dev/null
docker run -d --name "$CORE_CLIENT" --network "$NET_CORE" --label "$LABEL" "$IMAGE" sleep 600 >/dev/null
# Joined to the app network after it started, like bitploy joins it to app networks.
docker network connect "$NET" "$TRAEFIK"
TRAEFIK_APP_IP="$(docker inspect "$TRAEFIK" -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}")"
core_reach() { docker exec "$CORE_CLIENT" wget -q -T 4 -O /dev/null "$1" >/dev/null 2>&1; }
for _ in $(seq 1 20); do core_reach "http://$CORE_TRAEFIK_IP:8081/ping" && break; sleep 0.5; done
if core_reach "http://$CORE_TRAEFIK_IP:8081/ping"; then
  pass "from the bitploy network: the internal entrypoint $CORE_TRAEFIK_IP:8081 (bitploy's health checks)"
else
  bad "from the bitploy network: the internal entrypoint $CORE_TRAEFIK_IP:8081 (expected to work; docker logs $TRAEFIK)"
fi
expect_blocked "from an app: Traefik's internal entrypoint through its address on the app network $TRAEFIK_APP_IP:8081" "http://$TRAEFIK_APP_IP:8081/ping"
if docker inspect "$LIVE_TRAEFIK" >/dev/null 2>&1; then
  docker network connect "$NET" "$LIVE_TRAEFIK"
  LIVE_IP="$(docker inspect "$LIVE_TRAEFIK" -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}")"
  expect_blocked "from an app: the internal entrypoint of $LIVE_TRAEFIK through $LIVE_IP:8081" "http://$LIVE_IP:8081/ping"
  docker network disconnect -f "$NET" "$LIVE_TRAEFIK" >/dev/null 2>&1 || true
  if docker inspect bitploy >/dev/null 2>&1; then
    if docker exec bitploy wget -q -T 4 -O /dev/null "http://$LIVE_TRAEFIK:8081/ping" >/dev/null 2>&1; then
      pass "bitploy reaches the internal entrypoint of $LIVE_TRAEFIK"
    else
      bad "bitploy cannot reach http://$LIVE_TRAEFIK:8081/ping (health checks would fail)"
    fi
  fi
fi

say "Project network (two apps of one project)"
docker network create --label "$LABEL" "$NET_PROJECT" >/dev/null
case "$(subnet "$NET_PROJECT")" in
  10.200.*) ;;
  *) bad "network $NET_PROJECT is not in 10.200.0.0/16" ;;
esac
docker network connect "$NET_PROJECT" "$CLIENT"
docker network connect --alias db "$NET_PROJECT" "$OTHER"
sleep 1
expect_reach "the other app by its alias db on the project network" "http://db:8000/"
expect_blocked "the other app through its own app network $OTHER_IP:8000" "http://$OTHER_IP:8000/"

say "Public ports (from a client outside the server)"
expect_reach_ext "published port $PUB_PORT inside the range" "http://$HOST_IP:$PUB_PORT/"
expect_blocked_ext "published port $OUT_PORT outside the range" "http://$HOST_IP:$OUT_PORT/"
expect_blocked_ext "a service that is not public, by its container address $SIBLING_IP:8000" "http://$SIBLING_IP:8000/"
expect_blocked "an app using another app's public port through the server address $HOST_IP:$PUB_PORT" "http://$HOST_IP:$PUB_PORT/"

if [ "$fail" -eq 0 ]; then
  say "All checks passed."
else
  say "Some checks FAILED."
fi
exit "$fail"
