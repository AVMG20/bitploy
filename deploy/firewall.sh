#!/usr/bin/env bash
# Network isolation for app containers. Run as root on the bitploy server.
#
#   firewall.sh [apply]   install the rules (default; safe to run again)
#   firewall.sh remove    take the rules out again
#   firewall.sh status    show the rules and their packet counters
#
# What it does, for traffic that comes from the app subnets (default
# 10.200.0.0/16, set in /etc/docker/daemon.json as /24 blocks, one per app):
#
#   FORWARD, via the DOCKER-USER chain (Docker never touches this chain)
#     - replies to connections that were already allowed: pass
#     - the app's own /24: pass. Traefik is a member of every app network, so
#       Traefik <-> app traffic stays inside one /24 and must keep working.
#     - 169.254.169.254 (Hetzner metadata service) and every private, link-local,
#       loopback and multicast range: DROP. That covers the host's other
#       networks, the "bitploy" network with bitploy and Traefik (10.201.0.0/24),
#       and every other app. Docker already isolates bridges from each other;
#       this is the second lock.
#     - everything else (the internet): pass
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
FWD_CHAIN="BITPLOY-APPS"
IN_CHAIN="BITPLOY-INPUT"
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

need_root() { [ "$(id -u)" -eq 0 ] || die "run as root (sudo $0 $*)"; }

pool_prefix() {
  # 10.200.0.0/16 -> "10.200"
  [[ "$APP_POOL" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.0\.0/16$ ]] || die "BITPLOY_APP_POOL must look like 10.200.0.0/16, got '$APP_POOL'"
  echo "${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
}

jump_present() { iptables -C "$1" -j "$2" 2>/dev/null; }

apply() {
  need_root
  command -v iptables >/dev/null || die "iptables not found (apt-get install iptables)"
  command -v iptables-restore >/dev/null || die "iptables-restore not found"
  iptables -nL DOCKER-USER >/dev/null 2>&1 || die "chain DOCKER-USER does not exist: is Docker running?"
  local prefix i r
  prefix="$(pool_prefix)"

  {
    echo "*filter"
    echo ":$FWD_CHAIN - [0:0]"
    echo ":$IN_CHAIN - [0:0]"
    # --noflush keeps other chains; flush ours so a second run changes nothing.
    echo "-F $FWD_CHAIN"
    echo "-F $IN_CHAIN"
    echo "-A $FWD_CHAIN -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN"
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
  echo "bitploy firewall rules applied for $APP_POOL"
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
  *) die "usage: $0 [apply|remove|status]" ;;
esac
