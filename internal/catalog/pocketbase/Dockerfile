# PocketBase has no official image: build one from the official release zip.
FROM alpine:3.22 AS fetch
ARG TARGETARCH
ARG PB_VERSION=0.40.6
RUN apk add --no-cache ca-certificates unzip \
 && case "$TARGETARCH" in \
      amd64) SUM=0e1f609158ed945f809f8a02ae012aa0427129c66ebb63c1aee76f2c0309373f ;; \
      arm64) SUM=d22c66ceb67ca92b7d767f36d826e848a276bcfa3c197ead20bf87c42208984f ;; \
      *) echo "PocketBase: no build for $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && wget -q -O /tmp/pb.zip "https://github.com/pocketbase/pocketbase/releases/download/v${PB_VERSION}/pocketbase_${PB_VERSION}_linux_${TARGETARCH}.zip" \
 && echo "$SUM  /tmp/pb.zip" | sha256sum -c - \
 && unzip -q /tmp/pb.zip pocketbase -d /out \
 && chmod 755 /out/pocketbase

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=fetch /out/pocketbase /usr/local/bin/pocketbase
USER app
EXPOSE 8090
CMD ["pocketbase", "serve", "--http=0.0.0.0:8090", "--dir=/data/pb_data", "--publicDir=/data/pb_public", "--migrationsDir=/data/pb_migrations", "--hooksDir=/data/pb_hooks"]
