#!/usr/bin/env bash
# Installs bitploy on a fresh Ubuntu LTS server (24.04 or 26.04). Run as root:
#
#   git clone <repo> && cd bitploy && sudo deploy/install.sh
#
# It is safe to run again: it only fills in what is missing and never replaces
# your secret key, your settings or your certificates.
#
# 1. Docker Engine and the buildx plugin from Docker's apt repository
# 2. /etc/docker/daemon.json (address pools, log limits, live-restore)
# 3. firewall.sh as a systemd unit, unattended upgrades, SSH keys only
# 4. /etc/bitploy/bitploy.env (mode 0600, generated secret key) and the Origin CA certificate
# 5. docker compose up -d, wait for /healthz, print the command to create the first admin
#
# Questions are asked on the terminal. To run without questions, set
#   BITPLOY_APPS_DOMAIN, BITPLOY_ACME_EMAIL and either BITPLOY_ORIGIN_CERT_FILE
#   and BITPLOY_ORIGIN_KEY_FILE or put origin.pem and origin.key in /etc/bitploy/origin.
# Optional: BITPLOY_PUBLIC_URL, BITPLOY_EDGE_HOST, BITPLOY_SERVER_IPV4, BITPLOY_SERVER_IPV6,
#   BITPLOY_IMAGE (default ghcr.io/arno/bitploy:latest),
#   BITPLOY_BUILD_LOCAL=1 (build the image from this checkout instead of pulling it),
#   BITPLOY_SKIP_SSH_HARDENING=1, BITPLOY_SKIP_OS_CHECK=1.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$HERE")"
INSTALL_DIR=/opt/bitploy
CONF_DIR=/etc/bitploy
ENV_FILE="$CONF_DIR/bitploy.env"
ORIGIN_DIR="$CONF_DIR/origin"
DATA_DIR="${BITPLOY_DATA_DIR:-/var/lib/bitploy}"

say() { printf '\n==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root: sudo $0"
[ "$(uname -s)" = Linux ] || die "install.sh only runs on Linux"
export DEBIAN_FRONTEND=noninteractive

# Reads $2 (a prompt) into variable $1, unless it is already set in the environment.
# $3 is the default. Without a terminal the default is used, or the script stops if there is none.
ask() {
  local var="$1" prompt="$2" default="${3:-}" reply=""
  if [ -n "${!var:-}" ]; then return; fi
  if [ -r /dev/tty ] && [ -t 0 ]; then
    if [ -n "$default" ]; then read -r -p "$prompt [$default]: " reply </dev/tty; else read -r -p "$prompt: " reply </dev/tty; fi
  fi
  reply="${reply:-$default}"
  [ -n "$reply" ] || die "$var is required (set it in the environment to run without questions)"
  printf -v "$var" '%s' "$reply"
}

# Sets KEY=value in the env file only when KEY is missing, so re-runs keep your edits.
env_default() {
  local key="$1" value="$2"
  if grep -q "^${key}=" "$ENV_FILE"; then return; fi
  printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
}

env_get() { grep "^$1=" "$ENV_FILE" | head -n1 | cut -d= -f2- || true; }

# ---------------------------------------------------------------- 1. Docker
say "1/5 Docker Engine and buildx"
if [ -z "${BITPLOY_SKIP_OS_CHECK:-}" ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  case "${ID:-}:${VERSION_ID:-}" in
    ubuntu:24.04 | ubuntu:26.04) ;;
    *) die "this script supports Ubuntu 24.04 and 26.04 LTS, found ${PRETTY_NAME:-unknown}. Set BITPLOY_SKIP_OS_CHECK=1 to try anyway." ;;
  esac
fi
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg openssl iptables sqlite3 restic >/dev/null
if ! command -v docker >/dev/null || ! docker buildx version >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  # shellcheck disable=SC1091
  . /etc/os-release
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${UBUNTU_CODENAME:-$VERSION_CODENAME} stable" \
    >/etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
fi

# ---------------------------------------------------------------- 2. daemon.json
say "2/5 /etc/docker/daemon.json"
install -d -m 0755 /etc/docker
if ! cmp -s "$HERE/daemon.json" /etc/docker/daemon.json 2>/dev/null; then
  if [ -f /etc/docker/daemon.json ]; then
    cp /etc/docker/daemon.json "/etc/docker/daemon.json.bak.$(date +%Y%m%d%H%M%S)"
    warn "replaced the existing /etc/docker/daemon.json (a backup was kept next to it)"
  fi
  install -m 0644 "$HERE/daemon.json" /etc/docker/daemon.json
  systemctl enable docker >/dev/null 2>&1 || true
  # live-restore keeps running containers up through the restart.
  systemctl restart docker
else
  systemctl enable --now docker >/dev/null 2>&1 || true
fi
for _ in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info >/dev/null 2>&1 || die "the Docker daemon does not answer"

# ---------------------------------------------------------------- 3. firewall, upgrades, SSH
say "3/5 Firewall, unattended upgrades, SSH keys only"
install -d -m 0755 "$INSTALL_DIR"
install -m 0755 "$HERE/firewall.sh" "$INSTALL_DIR/firewall.sh"
cat >/etc/systemd/system/bitploy-firewall.service <<UNIT
[Unit]
Description=bitploy: isolate app containers from the host and private networks
After=docker.service
Requires=docker.service
PartOf=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$INSTALL_DIR/firewall.sh apply
ExecStop=$INSTALL_DIR/firewall.sh remove

[Install]
WantedBy=multi-user.target docker.service
UNIT
systemctl daemon-reload
systemctl enable bitploy-firewall.service >/dev/null 2>&1
systemctl restart bitploy-firewall.service

apt-get install -y -qq unattended-upgrades >/dev/null
cat >/etc/apt/apt.conf.d/20auto-upgrades <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
CONF

harden_ssh() {
  [ -z "${BITPLOY_SKIP_SSH_HARDENING:-}" ] || { echo "skipping SSH hardening (BITPLOY_SKIP_SSH_HARDENING is set)"; return; }
  # Never lock the admin out: require at least one authorized key on root or the user who ran sudo.
  local has_key=0 f
  for f in /root/.ssh/authorized_keys "/home/${SUDO_USER:-nobody}/.ssh/authorized_keys"; do
    if [ -s "$f" ] && grep -qE '^(ssh-|ecdsa-|sk-)' "$f"; then has_key=1; fi
  done
  if [ "$has_key" -eq 0 ]; then
    warn "no SSH public key found for root or ${SUDO_USER:-your user}; leaving password login alone so you are not locked out. Add a key, then run this script again."
    return
  fi
  install -d -m 0755 /etc/ssh/sshd_config.d
  cat >/etc/ssh/sshd_config.d/10-bitploy.conf <<'CONF'
# bitploy: SSH keys only.
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
CONF
  if sshd -t; then
    systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true
  else
    rm -f /etc/ssh/sshd_config.d/10-bitploy.conf
    warn "sshd rejected the hardening file; removed it"
  fi
}
harden_ssh

# ---------------------------------------------------------------- 4. settings
say "4/5 Settings in $CONF_DIR"
install -d -m 0755 "$CONF_DIR"
install -d -m 0755 "$ORIGIN_DIR"
if [ ! -f "$ENV_FILE" ]; then
  ( umask 077; : >"$ENV_FILE" )
fi
chmod 0600 "$ENV_FILE"

if [ -z "$(env_get BITPLOY_SECRET_KEY)" ]; then
  env_default BITPLOY_SECRET_KEY "$(openssl rand -base64 32)"
fi

ask BITPLOY_APPS_DOMAIN "Domain for apps and the dashboard (apps become <name>.THIS)" "$(env_get BITPLOY_APPS_DOMAIN)"
default_url="$(env_get BITPLOY_PUBLIC_URL)"
ask BITPLOY_PUBLIC_URL "Dashboard URL" "${default_url:-https://$BITPLOY_APPS_DOMAIN}"
ask BITPLOY_ACME_EMAIL "Email for Let's Encrypt (custom domains)" "$(env_get BITPLOY_ACME_EMAIL)"
detect_ip4() { curl -4 -fsS --max-time 5 https://ifconfig.me 2>/dev/null || true; }
detect_ip6() { curl -6 -fsS --max-time 5 https://ifconfig.me 2>/dev/null || true; }
default_ip4="$(env_get BITPLOY_SERVER_IPV4)"
ask BITPLOY_SERVER_IPV4 "Public IPv4 address of this server" "${default_ip4:-$(detect_ip4)}"
BITPLOY_SERVER_IPV6="${BITPLOY_SERVER_IPV6:-$(env_get BITPLOY_SERVER_IPV6)}"
BITPLOY_SERVER_IPV6="${BITPLOY_SERVER_IPV6:-$(detect_ip6)}"
BITPLOY_EDGE_HOST="${BITPLOY_EDGE_HOST:-$(env_get BITPLOY_EDGE_HOST)}"
BITPLOY_EDGE_HOST="${BITPLOY_EDGE_HOST:-edge.$BITPLOY_APPS_DOMAIN}"

env_default BITPLOY_PUBLIC_URL "$BITPLOY_PUBLIC_URL"
env_default BITPLOY_APPS_DOMAIN "$BITPLOY_APPS_DOMAIN"
env_default BITPLOY_EDGE_HOST "$BITPLOY_EDGE_HOST"
env_default BITPLOY_SERVER_IPV4 "$BITPLOY_SERVER_IPV4"
[ -z "$BITPLOY_SERVER_IPV6" ] || env_default BITPLOY_SERVER_IPV6 "$BITPLOY_SERVER_IPV6"
env_default BITPLOY_ACME_EMAIL "$BITPLOY_ACME_EMAIL"
env_default BITPLOY_SIGNUP invite
env_default BITPLOY_MAX_PARALLEL_BUILDS 2
env_default BITPLOY_DATA_DIR "$DATA_DIR"

# Cloudflare Origin CA certificate: the default certificate in Traefik.
if [ ! -s "$ORIGIN_DIR/origin.pem" ] || [ ! -s "$ORIGIN_DIR/origin.key" ]; then
  if [ -n "${BITPLOY_ORIGIN_CERT_FILE:-}" ] && [ -n "${BITPLOY_ORIGIN_KEY_FILE:-}" ]; then
    install -m 0644 "$BITPLOY_ORIGIN_CERT_FILE" "$ORIGIN_DIR/origin.pem"
    install -m 0600 "$BITPLOY_ORIGIN_KEY_FILE" "$ORIGIN_DIR/origin.key"
  elif [ -r /dev/tty ] && [ -t 0 ]; then
    cat <<MSG

Cloudflare Origin CA certificate. In the Cloudflare dashboard open SSL/TLS > Origin Server >
Create Certificate, with hostnames $BITPLOY_APPS_DOMAIN and *.$BITPLOY_APPS_DOMAIN, key format PEM.
Paste the certificate, then press Ctrl-D on an empty line. Then the same for the private key.
(To skip this, put origin.pem and origin.key in $ORIGIN_DIR yourself and run this script again.)
MSG
    echo "--- certificate (-----BEGIN CERTIFICATE-----) ---"
    cat </dev/tty >"$ORIGIN_DIR/origin.pem" || true
    echo "--- private key (-----BEGIN PRIVATE KEY-----) ---"
    ( umask 077; cat </dev/tty >"$ORIGIN_DIR/origin.key" ) || true
    chmod 0644 "$ORIGIN_DIR/origin.pem"
    chmod 0600 "$ORIGIN_DIR/origin.key"
  fi
fi
if ! grep -q 'BEGIN CERTIFICATE' "$ORIGIN_DIR/origin.pem" 2>/dev/null || ! grep -q 'PRIVATE KEY' "$ORIGIN_DIR/origin.key" 2>/dev/null; then
  die "no valid Origin CA certificate in $ORIGIN_DIR (origin.pem and origin.key). Create one in Cloudflare, put the files there and run this script again."
fi

# ---------------------------------------------------------------- 5. start
say "5/5 Starting bitploy"
data_dir="$(env_get BITPLOY_DATA_DIR)"
data_dir="${data_dir:-$DATA_DIR}"
install -d -m 0750 "$data_dir" "$data_dir/traefik/dynamic" "$data_dir/uploads" "$data_dir/builds" "$data_dir/logs" "$data_dir/backups"
install -d -m 0700 "$data_dir/acme"

install -d -m 0755 "$INSTALL_DIR/traefik"
install -m 0644 "$HERE/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"
install -m 0644 "$HERE/traefik/traefik.yml" "$INSTALL_DIR/traefik/traefik.yml"
sed -i -E "s|^(\s*email:).*|\1 $(env_get BITPLOY_ACME_EMAIL)|" "$INSTALL_DIR/traefik/traefik.yml"
install -m 0755 "$HERE/backup.sh" "$INSTALL_DIR/backup.sh"
install -m 0755 "$HERE/update-cloudflare-ips.sh" "$INSTALL_DIR/update-cloudflare-ips.sh"

# Static dynamic-config files. The leading underscore keeps them apart from app routes.
install -m 0644 "$HERE/traefik/tls.yml" "$data_dir/traefik/dynamic/_tls.yml"
public_host="$(env_get BITPLOY_PUBLIC_URL)"
public_host="${public_host#*://}"
public_host="${public_host%%/*}"
public_host="${public_host%%:*}"
sed "s|__PUBLIC_HOST__|$public_host|g" "$HERE/traefik/dashboard.yml.tmpl" >"$data_dir/traefik/dynamic/_dashboard.yml"
chmod 0644 "$data_dir/traefik/dynamic/_dashboard.yml"

# Backups run from a systemd timer once /etc/bitploy/backup.env exists (see README).
install -m 0644 "$HERE/systemd/bitploy-backup.service" /etc/systemd/system/bitploy-backup.service
install -m 0644 "$HERE/systemd/bitploy-backup.timer" /etc/systemd/system/bitploy-backup.timer
systemctl daemon-reload
if [ -f "$CONF_DIR/backup.env" ]; then
  systemctl enable --now bitploy-backup.timer >/dev/null 2>&1
fi

if [ -n "${BITPLOY_BUILD_LOCAL:-}" ]; then
  [ -f "$REPO/Dockerfile" ] || die "BITPLOY_BUILD_LOCAL is set but $REPO/Dockerfile does not exist"
  say "Building the bitploy image from $REPO"
  docker build -t bitploy:local "$REPO"
  BITPLOY_IMAGE=bitploy:local
fi
if [ -n "${BITPLOY_IMAGE:-}" ]; then
  if grep -q '^BITPLOY_IMAGE=' "$ENV_FILE"; then
    sed -i "s|^BITPLOY_IMAGE=.*|BITPLOY_IMAGE=$BITPLOY_IMAGE|" "$ENV_FILE"
  else
    echo "BITPLOY_IMAGE=$BITPLOY_IMAGE" >>"$ENV_FILE"
  fi
fi

compose() { docker compose --env-file "$ENV_FILE" -f "$INSTALL_DIR/docker-compose.yml" "$@"; }
if [ -z "${BITPLOY_BUILD_LOCAL:-}" ]; then compose pull; fi
compose up -d

printf 'Waiting for bitploy to answer on /healthz '
ok=0
for _ in $(seq 1 60); do
  if docker exec bitploy wget -q -O /dev/null http://127.0.0.1:8080/healthz 2>/dev/null; then ok=1; break; fi
  printf '.'
  sleep 2
done
echo
if [ "$ok" -ne 1 ]; then
  docker logs --tail 40 bitploy >&2 || true
  die "bitploy did not become healthy within 2 minutes (logs above)"
fi

cat <<MSG

bitploy is running at $(env_get BITPLOY_PUBLIC_URL)

Next:
  1. Create the first admin:
       docker exec -it bitploy bitploy admin create-user --email you@example.com --admin
  2. In Cloudflare add the DNS records and set SSL/TLS to Full (strict):
       $(env_get BITPLOY_APPS_DOMAIN)        A     $(env_get BITPLOY_SERVER_IPV4)   Proxied
       *.$(env_get BITPLOY_APPS_DOMAIN)      A     $(env_get BITPLOY_SERVER_IPV4)   Proxied
       $(env_get BITPLOY_EDGE_HOST)      A     $(env_get BITPLOY_SERVER_IPV4)   DNS only
     (add AAAA records for IPv6 the same way)
  3. In the Hetzner Cloud Firewall allow 22 from your IP only, and 80 and 443 from anywhere.
  4. Check the network isolation once: sudo $HERE/probe-test.sh   (it must end with "All checks passed.")
  5. BACK UP BITPLOY_SECRET_KEY NOW, somewhere that is not this server:
       grep BITPLOY_SECRET_KEY $ENV_FILE
     Without it, saved env vars and GitHub tokens cannot be decrypted after a restore.
MSG
