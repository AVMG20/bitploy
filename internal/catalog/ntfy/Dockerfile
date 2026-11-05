FROM binwiederhier/ntfy:v2.37.2
# Requests come through Cloudflare and Traefik (subdomains) or Traefik alone
# (custom domains). Cloudflare's ranges (deploy/traefik/traefik.yml) are
# trusted, so the rate limits apply to the real client on both paths.
ENV NTFY_LISTEN_HTTP=0.0.0.0:2586 \
    NTFY_CACHE_FILE=/data/cache.db \
    NTFY_AUTH_FILE=/data/user.db \
    NTFY_ATTACHMENT_CACHE_DIR=/data/attachments \
    NTFY_BEHIND_PROXY=true \
    NTFY_PROXY_TRUSTED_HOSTS=173.245.48.0/20,103.21.244.0/22,103.22.200.0/22,103.31.4.0/22,141.101.64.0/18,108.162.192.0/18,190.93.240.0/20,188.114.96.0/20,197.234.240.0/22,198.41.128.0/17,162.158.0.0/15,104.16.0.0/13,104.24.0.0/14,172.64.0.0/13,131.0.72.0/22,2400:cb00::/32,2606:4700::/32,2803:f800::/32,2405:b500::/32,2405:8100::/32,2a06:98c0::/29,2c0f:f248::/32
USER 10001:10001
EXPOSE 2586
CMD ["serve"]
