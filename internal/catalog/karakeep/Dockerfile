FROM ghcr.io/karakeep-app/karakeep:0.34.1
# Karakeep keeps its SQLite database and saved pages in DATA_DIR. The image's
# s6 supervisor runs the database migrations, the web app and the background
# workers; the image gives /run to the node user so s6 can start as node.
# s6 waits 5 seconds for the migrations by default, too short for a first
# start on half a CPU: 0 waits as long as they take.
# No headless Chrome runs next to it (BROWSER_WEB_URL is not set), so the
# crawler fetches pages over plain HTTP: no screenshots, no JavaScript.
ENV DATA_DIR=/data \
    S6_CMD_WAIT_FOR_SERVICES_MAXTIME=0
USER node
EXPOSE 3000
# The image's own check gives the first start 5 seconds.
HEALTHCHECK --interval=30s --timeout=10s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["wget", "-q", "-O", "/dev/null", "http://127.0.0.1:3000/api/health"]
