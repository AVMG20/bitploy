FROM directus/directus:12.6.1
# The SQLite database, uploaded files and extensions all go to /data.
ENV DB_CLIENT=sqlite3 \
    DB_FILENAME=/data/database.sqlite \
    STORAGE_LOCAL_ROOT=/data/uploads \
    EXTENSIONS_PATH=/data/extensions \
    HOST=0.0.0.0
USER node
EXPOSE 8055
# The first start creates the database schema, which can take over a minute on half a CPU.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8055/server/ping"]
