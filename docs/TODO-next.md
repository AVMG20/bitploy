# Left to do (written 2026-11-06)

The 2026-11-06 audit reports are copied to `docs/audit-2026-11-06/` (security.md, ui.md, features.md).
Three agents worked on it: backend bugs/security, UI fixes, backups + MCP tools. Their work is merged.
Check `git log` for what landed and the notes below for anything they skipped.

## Not started

0. **Remove moving apps between projects**: drop update_app `project` and its UI, MCP and docs. The backend agent was stopped before this.
   Also left from the audits, unverified: compose CPU/memory upper bounds, portproxy per-IP limit and idle timeout, MySQL `--no-data` database name in dumps, `${VAR}` in compose image leaking into logs, and SPEC/README drift.
   Check `docs/audit-2026-11-06/` against `git log` for any other gaps. The UI agent was also stopped before it finished.

1. **Build-time variables**: `VITE_*` / `NEXT_PUBLIC_*` for static sites and auto builds. Pass them as build args, and use BuildKit secrets for secret values. Add them to the API, MCP and dashboard.
2. **MCP `build_mode: auto`**: the MCP schema leaves out `auto`, which the dashboard supports.
3. **Dashboard build fields**: port, health path, type, Dockerfile path, context dir.
4. **Before/after-deploy hooks**: a command run in the app's container.
   - Before: runs in the new container before the swap, e.g. migrations. A failure stops the deploy.
   - After: runs once the swap is live.
   - Show the output in the deploy log. Add them to the API, MCP and dashboard.
5. **Audit rounds**: up to 3. Run an Opus audit+fix on main each time, covering the UI as a beginner, an MCP flow and a code review. Stop when a round comes back clean.

## Decided against or postponed

- No health status based on 5xx rates or dependencies.
- No moving apps between projects. It was removed; users redeploy.
- Notifications: not yet.
- The health check timeout stays fixed at 60 s.

## Known limits

- Docker paths are not integration-tested. This box can't run Docker, and GitHub CI has no minutes. Run `make test-integration` and `deploy/probe-test.sh` on a VPS.
