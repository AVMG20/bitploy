# syntax=docker/dockerfile:1

# ---- 1. Dashboard: Vue app built with Vite into web/dist
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- 2. Server: one static Go binary with web/dist embedded (go:embed)
FROM golang:1.26-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/bitploy ./cmd/bitploy

# ---- 3. Runtime: the Docker CLI with the buildx plugin, plus the binary
# bitploy builds user images with `docker buildx build` and starts containers
# through the Docker API, so the image needs the CLI and the socket.
FROM docker:29-cli
RUN apk add --no-cache ca-certificates tzdata
COPY --from=server /out/bitploy /usr/local/bin/bitploy

# This image runs as root on purpose. It mounts /var/run/docker.sock, and the
# socket is owned by root:docker with a group ID that differs per host, so a
# non-root user could not talk to Docker without a host-specific group mapping.
# Access to the socket is root-equivalent on the host in any case, so a non-root
# user inside the container would not make it safer. What protects the host is
# that only this container mounts the socket and that user code never runs here:
# apps run in their own locked-down containers.
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["bitploy"]
CMD ["serve"]
