FROM sissbruecker/linkding:1.47.0-alpine
# The image starts as root, chowns its data folder and runs supervisord as root.
# Run it as www-data instead: the data folder becomes /data, the chown goes,
# supervisord no longer asks for root and keeps its socket in /tmp.
USER root
RUN rm -rf /etc/linkding/data && ln -s /data /etc/linkding/data && \
    chown www-data:www-data /etc/linkding && \
    sed -i '/^chown /d' bootstrap.sh && \
    sed -i -e '/^user=/d' -e 's#/var/run/supervisor.sock#/tmp/supervisor.sock#' supervisord-tasks.conf supervisord-all.conf
ENV LD_SERVER_HOST=0.0.0.0 \
    LD_SERVER_PORT=9090
USER www-data
EXPOSE 9090
# The first start runs database migrations; the image's own check waits 30 s before its first try.
HEALTHCHECK --interval=30s --timeout=5s --start-period=2m --start-interval=2s --retries=3 \
  CMD ["curl", "-fs", "-o", "/dev/null", "http://127.0.0.1:9090/health"]
