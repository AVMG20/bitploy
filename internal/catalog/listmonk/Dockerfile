FROM listmonk/listmonk:v6.2.0
# Uploaded images and files go to the uploads folder next to the program (the
# default under Settings, Media), which becomes a link to /data. Settings come
# from the image's config.toml; LISTMONK_* variables win over it.
USER root
RUN ln -s /data /listmonk/uploads
ENV LISTMONK_app__address=0.0.0.0:9000 \
    LISTMONK_db__ssl_mode=disable
USER 65534:65534
EXPOSE 9000
# Create the tables on the first start (--idempotent skips this once they
# exist), apply upgrades, then run. The image's own start script only changes
# file owners, which needs root.
ENTRYPOINT ["/bin/sh", "-c", "./listmonk --install --idempotent --yes && ./listmonk --upgrade --yes && exec ./listmonk"]
