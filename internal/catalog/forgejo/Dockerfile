FROM codeberg.org/forgejo/forgejo:17.0.1-rootless
# Repositories, the SQLite database and app.ini all live under GITEA_WORK_DIR.
# The image's own /var/lib/gitea volume is left unused. bitploy serves HTTP
# only, so SSH is off: clone and push over HTTPS.
ENV GITEA_WORK_DIR=/data \
    GITEA_CUSTOM=/data/custom \
    GITEA_APP_INI=/data/custom/conf/app.ini \
    HOME=/data/git \
    HTTP_PORT=3000 \
    DISABLE_SSH=true \
    DB_TYPE=sqlite3
USER 1000:1000
EXPOSE 3000
