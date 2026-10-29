SHELL := /bin/bash
GO ?= go
SQLC ?= $(shell command -v sqlc || echo $(HOME)/go/bin/sqlc)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE ?= ghcr.io/avmg20/bitploy

.PHONY: dev build web go test test-integration lint generate release clean

## dev: Go server + Vite (proxying to it) + local Traefik (HTTP only) on http://<slug>.localhost:8000
dev:
	./deploy/dev.sh

## build: one binary with the dashboard embedded
build: web go

web:
	cd web && npm ci --no-audit --no-fund && npm run build

go:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/bitploy ./cmd/bitploy

generate:
	$(SQLC) generate

test:
	$(GO) test ./cmd/... ./internal/... ./web

test-integration:
	$(GO) test -tags=integration -count=1 ./cmd/... ./internal/... ./web

lint:
	$(GO) vet ./cmd/... ./internal/... ./web
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./cmd/... ./internal/... ./web
	cd web && npx vue-tsc --noEmit && npx eslint .

release:
	docker buildx build --build-arg VERSION=$(VERSION) --platform linux/amd64,linux/arm64 -t $(IMAGE):$(VERSION) -t $(IMAGE):latest --push .

clean:
	rm -rf bin web/dist/assets
