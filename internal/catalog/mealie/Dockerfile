FROM ghcr.io/mealie-recipes/mealie:v3.30.0
# The image starts as root and switches to uid 911 itself; starting as 911
# skips that step. Data goes to /data through DATA_DIR; the image's own
# /app/data volume is left unused.
ENV DATA_DIR=/data \
    HOME=/tmp
USER 911:911
EXPOSE 9000
# Startup runs database migrations, which can take over a minute on half a CPU;
# the image's own check waits 30 s before its first try.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["/app/healthcheck.sh"]
