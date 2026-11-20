FROM rustrak/rustrak-server:v0.30.1
# The SQLite database (/data/rustrak.db) and uploaded source maps are in /data
# already. Events wait in INGEST_DIR between being accepted and being stored;
# the default is under /tmp, which a redeploy empties, so it moves to /data.
# SSL_PROXY marks the sign-in cookie secure: bitploy serves every app over HTTPS.
# Anonymous usage reports are off.
ENV HOST=0.0.0.0 \
    PORT=8080 \
    INGEST_DIR=/data/ingest \
    SSL_PROXY=true \
    RUSTRAK_TELEMETRY=off
USER 65532:65532
EXPOSE 8080
