# Left to do (updated 2026-11-06)

The 2026-11-06 audit reports are in `docs/audit-2026-11-06/` (security.md, ui.md, features.md).
Everything from the earlier list has landed: app moves removed, the smaller audit fixes, the leftover UI items,
build-time variables, MCP `build_mode: auto`, dashboard build fields, before/after-deploy commands,
and one audit round covering a code review, the MCP flows and a beginner UI pass. Check `git log` for details.

## Open

- **Compose deploy commands**: before/after-deploy commands are refused for compose apps (`deploy_commands_unsupported`). Per-service commands would need an `x-bitploy` key and an order across services.
- **Database tab for a compose `db` service**: the `/apps/{app}/db` endpoints only reach SQLite files in volumes and template database servers.
- **After-deploy timeout**: Docker can't kill an exec. At 5 minutes bitploy stops waiting, but a hung command keeps running in the live container until it is replaced.
- **Build secrets reach only the build script** in bitploy's own Node builds, not the dependency install (a private npm token isn't covered).
- **Laravel template** still sets `DB_CONNECTION=sqlite` in the image. Its README says to override it with `set_env` when using a database service.
- **`query_db` with a service alias** (`postgres`) instead of its slug gives a plain `app_not_found`.
- **Dashboard leftovers from the UI audit**: the secret build variable help still says `RUN --mount=type=secret` (shown only for apps with their own Dockerfile); deploy progress on the Overview has no step for deploy commands; phone-light and desktop-dark were only spot-checked after the last fixes; mk-worker's overview showed blank once while the mock restarted (not reproduced).
- **More audit rounds**: only one was run this time, by choice.

## Decided against or postponed

- No health status based on 5xx rates or dependencies.
- No moving apps between projects. It was removed; users redeploy.
- Notifications: not yet.
- The health check timeout stays fixed at 60 s.

## Known limits

- Docker paths are not integration-tested. This box can't run Docker, and GitHub CI has no minutes. Run `make test-integration` and `deploy/probe-test.sh` on a VPS. This now matters more: the before-deploy one-off container, after-deploy exec output, BuildKit `--secret` builds and the public-port forwarder limits were all unit-tested only.
