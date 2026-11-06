#!/usr/bin/env bash
# Network isolation for app containers. Run as root on the bitploy server.
#
#   firewall.sh [apply]   install the rules (default; safe to run again)
#   firewall.sh remove    take the rules out again
#   firewall.sh status    show the rules and their packet counters
#   firewall.sh ports     print the public port range (LO:HI) it opens, if any
#
# What it does, for traffic that comes from the app subnets (default
# 10.200.0.0/16, set in /etc/docker/daemon.json as /24 blocks, one per app):
#
#   FORWARD, via the DOCKER-USER chain (Docker never touches this chain)
#     - replies to connections that were already allowed: pass
#     - NEW TCP connections to port 8081 (Traefik's internal entrypoint): DROP.
#       Traefik has an address on every app network, so without this rule an
#       app could reach that entrypoint through its own /24. As a result an
#       app cannot listen on port 8081 itself (Traefik's connections to it
#       would be dropped too).
#     - the app's own /24: pass. Traefik is a member of every app network, so
#       Traefik <-> app traffic stays inside one /24 and must keep working.
#       A project network (bp-p-<id>, shared by the apps of one project once it
#       has two) is a /24 from the same pool, so traffic between apps of one
#       project passes by this rule too; Traefik never joins it. Apps of other
#       projects have no address in that /24, and their traffic is dropped below.
#     - 169.254.169.254 (Hetzner metadata service) and every private, link-local,
#       loopback and multicast range: DROP. That covers the host's other
#       networks, the "bitploy" network with bitploy and Traefik (10.201.0.0/24),
#       and every other app. Docker already isolates bridges from each other;
#       this is the second lock.
#     - everything else (the internet): pass
#
#   and for traffic from outside the app subnets to a published port of a
#   container in them (Docker DNATs it, so it goes through FORWARD, not INPUT):
#     - the public port range (BITPLOY_PUBLIC_PORTS, like 30000-30099): pass.
#       bitploy publishes those ports for the forwarder container of a public
#       port (a database opened for debugging), and nothing else.
#     - 80 and 443: pass, in case Docker maps Traefik's ports to its address
#       on an app network
#     - any other published port into the app subnets: DROP. bitploy never
#       publishes one, so this only stops a mistake from opening an app.
#   The range is read from the environment, else from /etc/bitploy/bitploy.env.
#   Unset (or "off"): no published port into the app subnets passes. After
#   changing it, run this script again (systemctl restart bitploy-firewall).
#   Apps cannot reach the public port of another app through the server's
#   address: after the DNAT that is app-to-app traffic, dropped above.
#
#   INPUT: connections from the app subnets to the host itself are DROPPED,
#   except replies. Traefik is a container on the same bridge as the app, so
#   its traffic is switched on the bridge or forwarded and never reaches INPUT;
#   nothing Traefik needs is lost. DNS works because containers on user-defined
#   networks ask Docker's embedded resolver (127.0.0.11) inside their own namespace.
#
# The rules live in their own chains (BITPLOY-APPS, BITPLOY-INPUT) that are
# rebuilt in one atomic step on every run, so running this twice changes nothing.
# They are lost on reboot and when Docker restarts its firewall setup:
# install.sh adds a systemd unit that runs this script after docker.service.
#
# IPv6: bridge networks are IPv4 only (daemon.json has no "ipv6": true), so there
# is nothing to filter. If you enable IPv6 for Docker, add the same rules with ip6tables.
set -euo pipefail

APP_POOL="${BITPLOY_APP_POOL:-10.200.0.0/16}"   # must match daemon.json (a /16 split into /24 blocks)
METADATA_IP="169.254.169.254"
TRAEFIK_INTERNAL_PORT=8081   # unpublished "internal" entrypoint of Traefik; apps cannot listen on it
FWD_CHAIN="BITPLOY-APPS"
IN_CHAIN="BITPLOY-INPUT"
ENV_FILE="${BITPLOY_ENV_FILE:-/etc/bitploy/bitploy.env}"
# Never reachable from an app. 10.0.0.0/8 includes the whole app pool and the bitploy network.
BLOCKED_RANGES=(
  10.0.0.0/8 172.16.0.0/12 192.168.0.0/16   # RFC 1918
  100.64.0.0/10                             # carrier-grade NAT (Tailscale, some private networks)
  169.254.0.0/16                            # link-local, including the metadata address
  127.0.0.0/8 0.0.0.0/8                     # loopback, "this network"
  192.0.0.0/24 198.18.0.0/15                # IETF protocol assignments, benchmarking
  224.0.0.0/4 240.0.0.0/4                   # multicast, reserved
)

die() { echo "firewall.sh: $*" >&2; exit 1; }

need_root() { [ "$(id -u)" -eq 0 ] || die "run as root (sudo $0)"; }

pool_prefix() {
  # 10.200.0.0/16 -> "10.200"
  [[ "$APP_POOL" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.0\.0/16$ ]] || die "BITPLOY_APP_POOL must look like 10.200.0.0/16, got '$APP_POOL'"
  echo "${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
}

jump_present() { iptables -C "$1" -j "$2" 2>/dev/null; }

# Prints the public port range as "LO:HI" (iptables syntax), or nothing when
# there is none. Same rules as bitploy's config: 1024-65535, at most 1000
# ports, low port first. A bad value opens nothing (a warning, not an error:
# the rest of the isolation must still be applied).
public_ports() {
  local v lo hi
  if [ -n "${BITPLOY_PUBLIC_PORTS+x}" ]; then
    v="$BITPLOY_PUBLIC_PORTS"
  elif [ -r "$ENV_FILE" ]; then
    v="$(grep -E '^BITPLOY_PUBLIC_PORTS=' "$ENV_FILE" | tail -n1 | cut -d= -f2- | tr -d "\"' ")"
  else
    v=""
  fi
  case "$v" in "" | off) return 0 ;; esac
  if [[ "$v" =~ ^([0-9]{4,5})(-([0-9]{4,5}))?$ ]]; then
    lo="${BASH_REMATCH[1]}"
    hi="${BASH_REMATCH[3]:-$lo}"
    lo=$((10#$lo))
    hi=$((10#$hi))
    if [ "$lo" -ge 1024 ] && [ "$hi" -le 65535 ] && [ "$lo" -le "$hi" ] && [ $((hi - lo)) -lt 1000 ]; then
      echo "$lo:$hi"
      return 0
    fi
  fi
  echo "firewall.sh: WARNING: BITPLOY_PUBLIC_PORTS='$v' is not a range like 30000-30099 (1024-65535, at most 1000 ports); no public port is opened" >&2
}

apply() {
  need_root
  command -v iptables >/dev/null || die "iptables not found (apt-get install iptables)"
  command -v iptables-restore >/dev/null || die "iptables-restore not found"
  iptables -nL DOCKER-USER >/dev/null 2>&1 || die "chain DOCKER-USER does not exist: is Docker running?"
  local prefix i r ports p
  prefix="$(pool_prefix)"
  ports="$(public_ports)"

  {
    echo "*filter"
    echo ":$FWD_CHAIN - [0:0]"
    echo ":$IN_CHAIN - [0:0]"
    # --noflush keeps other chains; flush ours so a second run changes nothing.
    echo "-F $FWD_CHAIN"
    echo "-F $IN_CHAIN"
    echo "-A $FWD_CHAIN -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"
    # Published ports into the app subnets, from outside them: only the public
    # port range (and 80/443 for Traefik). From inside the pool the rules
    # below apply, so one app cannot use another app's public port.
    if [ -n "$ports" ]; then
      echo "-A $FWD_CHAIN ! -s $APP_POOL -d $APP_POOL -p tcp -m conntrack --ctstate DNAT --ctproto tcp --ctorigdstport $ports -j RETURN"
    fi
    for p in 80 443; do
      echo "-A $FWD_CHAIN ! -s $APP_POOL -d $APP_POOL -p tcp -m conntrack --ctstate DNAT --ctproto tcp --ctorigdstport $p -j RETURN"
    done
    echo "-A $FWD_CHAIN ! -s $APP_POOL -d $APP_POOL -m conntrack --ctstate DNAT -j DROP"
    # Traefik's internal entrypoint (health checks of candidates, route probes) must
    # only be reachable from the "bitploy" network. Traefik has an address on every
    # app network, so this has to come before the same-/24 rules below.
    echo "-A $FWD_CHAIN -s $APP_POOL -p tcp --dport $TRAEFIK_INTERNAL_PORT -m conntrack --ctstate NEW -j DROP"
    # Each app network is one /24: traffic that stays inside it is Traefik <-> app.
    for i in $(seq 0 255); do
      echo "-A $FWD_CHAIN -s $prefix.$i.0/24 -d $prefix.$i.0/24 -j RETURN"
    done
    echo "-A $FWD_CHAIN -s $APP_POOL -d $METADATA_IP/32 -j DROP"
    for r in "${BLOCKED_RANGES[@]}"; do
      echo "-A $FWD_CHAIN -s $APP_POOL -d $r -j DROP"
    done
    echo "-A $FWD_CHAIN -j RETURN"

    echo "-A $IN_CHAIN -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"
    echo "-A $IN_CHAIN -s $APP_POOL -j DROP"
    echo "-A $IN_CHAIN -j RETURN"
    echo "COMMIT"
  } | iptables-restore --noflush

  jump_present DOCKER-USER "$FWD_CHAIN" || iptables -I DOCKER-USER 1 -j "$FWD_CHAIN"
  jump_present INPUT "$IN_CHAIN" || iptables -I INPUT 1 -j "$IN_CHAIN"
  if [ -n "$ports" ]; then
    echo "bitploy firewall rules applied for $APP_POOL, public ports ${ports/:/-}"
  else
    echo "bitploy firewall rules applied for $APP_POOL, no public ports"
  fi
}

remove() {
  need_root
  local chain parent
  for pair in "DOCKER-USER:$FWD_CHAIN" "INPUT:$IN_CHAIN"; do
    parent="${pair%%:*}"
    chain="${pair##*:}"
    while jump_present "$parent" "$chain"; do iptables -D "$parent" -j "$chain"; done
    if iptables -nL "$chain" >/dev/null 2>&1; then
      iptables -F "$chain"
      iptables -X "$chain"
    fi
  done
  echo "bitploy firewall rules removed"
}

status() {
  need_root
  local ports
  ports="$(public_ports)"
  echo "public port range in the settings: ${ports:-none}${ports:+ (run apply after changing it)}"
  for chain in "$FWD_CHAIN" "$IN_CHAIN"; do
    if iptables -nL "$chain" >/dev/null 2>&1; then
      # Hide the 256 identical "own /24" rules; they only clutter the output.
      iptables -nvL "$chain" | awk '$3 == "RETURN" && $8 ~ /\/24$/ && $9 ~ /\/24$/ { n++; next } { print } END { if (n) print "   (" n " rules that pass traffic inside one app network not shown)" }'
    else
      echo "chain $chain is not installed"
    fi
  done
}

case "${1:-apply}" in
  apply) apply ;;
  remove) remove ;;
  status) status ;;
  ports) public_ports ;;   # the public port range as LO:HI, or nothing (probe-test.sh)
  *) die "usage: $0 [apply|remove|status|ports]" ;;
esac
