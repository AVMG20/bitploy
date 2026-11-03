FROM vaultwarden/server:1.37.5-alpine
ENV DATA_FOLDER=/data \
    ROCKET_ADDRESS=0.0.0.0 \
    ROCKET_PORT=8080
USER 10001:10001
EXPOSE 8080
# bitploy checks /alive itself; the image's own check first runs after 60 s and
# would hold every deploy that long.
HEALTHCHECK NONE
