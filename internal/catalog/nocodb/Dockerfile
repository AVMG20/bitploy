FROM nocodb/nocodb:2026.10.1
# NocoDB keeps its SQLite database and uploads in NC_TOOL_DIR. The image runs
# as root; its node user (uid 1000) is enough.
ENV NC_TOOL_DIR=/data/ \
    NC_DISABLE_TELE=true
USER node
EXPOSE 8080
# Startup runs migrations and builds its metadata cache; on half a CPU that can take minutes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["curl", "-fs", "-o", "/dev/null", "http://127.0.0.1:8080/api/v1/health"]
