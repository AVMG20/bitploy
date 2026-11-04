FROM ghcr.io/avmg20/arnotes:0.1.0
# Requests come through Cloudflare and Traefik (subdomains) or Traefik alone
# (custom domains). Traefik drops X-Forwarded-For from anyone but Cloudflare and
# appends the peer, so the second address from the right is the real client on
# both paths (on the direct path the header holds only the client).
ENV DATABASE_URL=/data/arnotes.db \
    ARNOTES_TRUST_PROXY=2
USER 10001:10001
EXPOSE 3000
# bitploy checks /api/ready itself; the image's own check first runs after 30 s
# and would hold every deploy that long.
HEALTHCHECK NONE
