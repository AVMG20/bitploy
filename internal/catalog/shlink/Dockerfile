FROM shlinkio/shlink:5.1.8
# Shlink keeps its SQLite database, cache and locks in /etc/shlink/data.
USER root
RUN rm -rf /etc/shlink/data && ln -s /data /etc/shlink/data
# A worker count of 0 means one per host CPU; two of each are plenty here.
ENV DB_DRIVER=sqlite \
    IS_HTTPS_ENABLED=true \
    WEB_WORKER_NUM=2 \
    TASK_WORKER_NUM=2
USER 1001
EXPOSE 8080
# Short links use DEFAULT_DOMAIN; unless it is set, take it from the app's address.
ENTRYPOINT ["/bin/sh", "-c", "u=${BITPLOY_URL#*://}; export DEFAULT_DOMAIN=\"${DEFAULT_DOMAIN:-${u%/}}\"; exec /bin/sh ./docker-entrypoint.sh"]
