FROM grafana/grafana:13.3.0
# Grafana keeps its SQLite database, dashboards and plugins in
# GF_PATHS_DATA. Logs go to the container log instead of /var/log/grafana.
ENV GF_PATHS_DATA=/data \
    GF_PATHS_PLUGINS=/data/plugins \
    GF_LOG_MODE=console \
    GF_SERVER_HTTP_ADDR=0.0.0.0 \
    GF_SERVER_HTTP_PORT=3000 \
    GF_USERS_ALLOW_SIGN_UP=false \
    GF_ANALYTICS_REPORTING_ENABLED=false \
    GF_ANALYTICS_CHECK_FOR_UPDATES=false
USER 472
EXPOSE 3000
