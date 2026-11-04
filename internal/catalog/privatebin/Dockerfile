FROM privatebin/nginx-fpm-alpine:2.1.0
# nginx and PHP-FPM already run unprivileged on port 8080. Pastes go to /data
# through the config file; the image's own /srv/data volume is left unused.
# PrivateBin needs the [main], [model] and [model_options] sections; everything
# else keeps its default. Every request reaches PHP from Traefik, so the
# 10-second flood limit keys on X-Forwarded-For rather than one shared address.
USER root
RUN mkdir -p /srv/cfg && \
    printf ';<?php http_response_code(403); /*\n[main]\n\n[traffic]\nheader = "X_FORWARDED_FOR"\n\n[model]\nclass = Filesystem\n\n[model_options]\ndir = "/data"\n' > /srv/cfg/conf.php && \
    chmod 644 /srv/cfg/conf.php
USER 65534:82
EXPOSE 8080
