# Decisions

Choices made where SPEC.md was open or ambiguous. Newest at the bottom.

- **Timestamps are unix seconds (INTEGER) in SQLite.** Avoids driver-specific time parsing; the service layer converts to RFC 3339 UTC for JSON.
- **Go module path `github.com/arno/bitploy`.** Placeholder owner; change with `go mod edit -module` when the repo moves.
- **`BITPLOY_DEV=1`, `BITPLOY_APPS_SCHEME`, `BITPLOY_APPS_PORT`, `BITPLOY_LISTEN`, `BITPLOY_TRAEFIK_CONTAINER`, `BITPLOY_INTERNAL_URL` config vars** were added for local development (HTTP, `<slug>.localhost:8000`) and for wiring Traefik to bitploy.
