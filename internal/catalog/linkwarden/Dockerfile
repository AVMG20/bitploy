# Linkwarden's image keeps the whole app in /data, where bitploy mounts the
# app's volume. The app moves to /app; its archive folder (STORAGE_FOLDER,
# "data" next to apps/ and packages/) becomes a link to /data.
FROM ghcr.io/linkwarden/linkwarden:v2.16.3 AS linkwarden
RUN test ! -e /app && mv /data /app && mkdir /data && ln -s /data /app/data

# Moving a folder copies it into a new layer: with the app (about 750 MB of
# node_modules) and its browser stored twice, the image would pass bitploy's
# 2 GB limit. Copying the moved file system into an empty image keeps one copy.
# The settings of the image's config are repeated below.
FROM scratch
COPY --from=linkwarden / /
ENV PATH=/app/node_modules/.bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    NODE_VERSION=22.23.2 \
    YARN_VERSION=1.22.22 \
    NODE_ENV=production \
    PRISMA_HIDE_UPDATE_MESSAGE=1 \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0
WORKDIR /app
USER node
EXPOSE 3000
# Startup applies database migrations, then starts the web app and the worker
# that archives pages; on half a CPU that can take minutes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=3m --start-interval=2s --retries=3 \
  CMD ["curl", "-fs", "-o", "/dev/null", "http://127.0.0.1:3000/"]
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["sh", "-c", "prisma migrate deploy --schema=/app/packages/prisma/schema.prisma && exec concurrently -k -n web,worker \"cd /app/apps/web && exec next start\" \"cd /app/apps/worker && exec tsx worker.ts\""]
