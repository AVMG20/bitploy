FROM n8nio/n8n:2.46.6
# n8n keeps its SQLite database, settings and binary data in $N8N_USER_FOLDER/.n8n.
# Requests come through Cloudflare and Traefik (subdomains) or Traefik alone
# (custom domains); with two trusted hops the real client is found on both paths.
ENV N8N_USER_FOLDER=/data \
    N8N_LISTEN_ADDRESS=0.0.0.0 \
    N8N_PORT=5678 \
    N8N_PROXY_HOPS=2
USER node
EXPOSE 5678
# The first start runs database migrations, which can take over a minute on half a CPU.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:5678/healthz').then(r => process.exit(r.ok ? 0 : 1), () => process.exit(1))"]
