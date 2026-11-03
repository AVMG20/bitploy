FROM louislam/uptime-kuma:2.5.9-slim-rootless
ENV DATA_DIR=/data/ \
    UPTIME_KUMA_DB_TYPE=sqlite \
    UPTIME_KUMA_HOST=0.0.0.0 \
    UPTIME_KUMA_PORT=3001
USER node
EXPOSE 3001
# bitploy checks the health path itself; the image's own check is slow and runs a script every minute.
HEALTHCHECK NONE
