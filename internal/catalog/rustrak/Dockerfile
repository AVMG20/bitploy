FROM rustrak/rustrak-server:v0.34.1
# The SQLite database (/data/rustrak.db) and uploaded source maps are in /data
# already. Events wait in INGEST_DIR between being accepted and being stored,
# and installed plugins with their databases live in RUSTRAK_PLUGINS_DIR; both
# must be in /data, or a redeploy empties them. The image sets /data/ingest
# (since v0.33.0) and /data/plugins (since v0.34.0); they stay set here so the
# catalog does not depend on that.
# SSL_PROXY marks the sign-in cookie secure: bitploy serves every app over HTTPS.
# Anonymous usage reports are off.
ENV HOST=0.0.0.0 \
    PORT=8080 \
    INGEST_DIR=/data/ingest \
    RUSTRAK_PLUGINS_DIR=/data/plugins \
    SSL_PROXY=true \
    RUSTRAK_TELEMETRY=off
USER 65532:65532
EXPOSE 8080
