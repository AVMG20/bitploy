FROM miniflux/miniflux:2.3.5
# Miniflux keeps everything in PostgreSQL and needs no /data. Migrations run on
# every start.
ENV LISTEN_ADDR=0.0.0.0:8080 \
    RUN_MIGRATIONS=1
USER 65534
EXPOSE 8080
# CREATE_ADMIN makes the account from ADMIN_USERNAME and ADMIN_PASSWORD, and
# Miniflux stops at start when it is on without them: turn it on only once a
# password is set.
ENTRYPOINT ["/bin/sh", "-c", "if [ -n \"$ADMIN_PASSWORD\" ]; then export CREATE_ADMIN=1; fi; exec /usr/bin/miniflux"]
