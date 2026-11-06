I found one high-severity issue: a read-only token can write to, or delete from, a PostgreSQL app's database through the new Database tab's SQL console. Everything else is medium or low. Nothing was edited or committed, and none of the Docker-facing code could be run here.

I used two helper agents: one for public ports and dump backups, one for compose validation, references between apps, and authz. I checked their main conclusions against the code I read myself. Findings 2, 3 and 7–12 rest mainly on their reading.

**What holds up well**
- **Database tab:**
  - The helper shares the database container's network, runs as `nobody` with no capabilities, and gets the password in a file, not on the command line.
  - Rows, filters and keys go through checked column names and quoted values.
  - An admin looking at another user's database app takes the SQLite path, not the server login.
- **Highlighting:** there is no `v-html` anywhere. Every token is a text node, so no XSS.
- **Public ports:**
  - The allow list checks the real TCP source address, so forwarded headers can't spoof it.
  - Port allocation is in a locked transaction with UNIQUE columns.
  - Expiry is enforced both by the reconciler and by the forwarder's own `--until`.
  - There is no IPv6 listener.
  - The reveal endpoint is owner-only, POST plus CSRF, and audited.
- **Dump backups:** the scripts are fixed strings, passwords go through env vars or a 0600 option file, and names are escaped.
- **Compose:** it is built from an allowlist (`ComposeService`), so unknown keys are dropped. `include`/`extends`, privileged settings, bind mounts and build secrets/ssh/`additional_contexts` are refused. Paths are resolved through `os.Root`/`ResolveBuildPaths`, and bitploy's own environment is never used for `${VAR}`.
- **References and authz:** `${{alias.VAR}}` stays within one user's project. Moving an app is refused while references would break, and renaming rewrites them in one transaction. Every service method I listed loads the record with an owner check.

## Findings, most severe first

**1. High: a read-only token can write to a PostgreSQL database.**
- **Where:** `internal/dbview/server_split.go:57-63`, with `server_pg.go:139` and `server.go:851-858,897`.
- **Problem:** the console decides "read-only" from its own statement splitter, which ends a `/* */` comment at the first `*/`. PostgreSQL nests block comments. Each split statement is then sent with pgx's simple protocol (`QueryExecModeSimpleProtocol`, no args → `pgConn.Exec`), which runs every statement in the string. I confirmed that path in the pgx source.
- **Scenario:** a read-scope token (for example an AI agent's) posts `{"sql":"SELECT 1 /* /* */ ' */; COMMIT; DELETE FROM users; SELECT ''","write":false}`.
  - The splitter sees one `SELECT`, because the `'` swallows the rest.
  - PostgreSQL runs `SELECT`, then `COMMIT`, which ends the `BEGIN READ ONLY`, then `DELETE` with writes allowed.
  - This also skips the deploy-scope check, the backup lock and the `db.write` audit.
- **Fix:**
  - Run console statements with the extended protocol (for example `QueryExecModeExec`). It refuses more than one statement per call.
  - Also set `default_transaction_read_only=on` in `RuntimeParams` for read sessions.
  - Optionally, handle nested comments in `splitServer`.

**2. Medium: a compose deploy's quota is checked only before the build.**
- **Where:** `internal/app/deploy_pipeline.go:434-438`, where `settleResources` returns nil unless it is a rollback.
- **Problem:** `SumAppResourcesByUser` counts only apps that are already live.
- **Scenario:** two new 2 GB compose apps against a 2 GB quota, building at the same time (the default allows 2 parallel builds). Both pass their check and both go live: 4 GB in use. The server-room check is skipped too.
- **Fix:** run `composeQuotaErr` again in `settleResources` for every compose deploy, ideally inside the swap transaction. Single apps have a smaller version of the same window (`checkStartQuota` is also outside the swap).

**3. Medium: Docker and runc on the host never get security updates automatically.**
- **Where:** `deploy/install.sh:84-91,136-140`.
- **Problem:** `unattended-upgrades` keeps its default origins, which are Ubuntu's archives only. Docker comes from download.docker.com, so a runc escape fix (for example CVE-2024-21626, which is triggered by `docker exec`, as the scheduled dumps do in tenant containers) is never installed.
- **Fix:** add `"Docker:${distro_codename}"` to `Origins-Pattern`, or document that the admin must upgrade these by hand.

**4. Low: read-only MySQL/MariaDB can still write files.**
- **Where:** `server_split.go:52-53` (`--` is always treated as a comment) and `writesFile` (`:12-19`).
- **Problem:** MySQL starts a comment with `--` only when whitespace follows, and it runs the inside of `/*! ... */`. The splitter skips both, so `writesFile` never sees the words.
- **Scenario:** `SELECT 1 --1 INTO OUTFILE '/…/x'` or `SELECT 1 /*! INTO OUTFILE '/…/x' */` passes as read-only. It writes a file inside the database container, as far as `secure_file_priv` allows. Only one statement per call is possible, since `multiStatements` is off.
- **Fix:** treat `--` as a comment only when whitespace follows, scan the words inside `/*!` comments, and refuse `INTO` in read-only mode.

**5. Low: no server-side time limit on viewer queries.**
- **Where:** `server_pg.go:24-43` and `server_mysql.go:32-55`.
- **Problem:** pgx's default on a cancelled context only sets a deadline on the connection, with no cancel request, and the MySQL driver just closes the connection. `QueryTimeout` and the helper kill therefore leave the query running on the server.
- **Scenario:** a read-only token runs `SELECT count(*) FROM generate_series(1,1e12)`. It burns the database's CPU limit for hours and holds a snapshot, so vacuum can't clean up. In write mode, held locks can stall the app.
- **Fix:** set `statement_timeout` (PostgreSQL), and `max_execution_time` / `max_statement_time` (MySQL / MariaDB), to `QueryTimeout`.

**6. Low: side effects of the new overallocate switch.**
- **Where:** `apps.go:540,1040,1090,1103`, `deploy_compose.go:418,474`, and `ServerHasRoom` (`internal/metrics/hoststats.go:54`).
- **Problems:**
  - The switch's own checks hold: it is admin-only and session-only.
  - Such a user's memory reservations still count in `OpsSumMemoryLimitsMB`. Once they pass host RAM, every other user's create or memory raise fails with `server_full`.
  - `AdminSetQuota` (both the `PUT` and `PATCH .../quota` routes) writes `overallocate=false` whenever the field is omitted (`omitempty` bool). Any older client that changes another limit silently turns it off.
  - MCP `whoami` (`mcp/results.go:70`) doesn't mention the switch, so agents still hold to the quota numbers.
- **Fix:** make the field a `*bool` that keeps the old value when omitted, and mention the switch in `whoami`. Optionally, leave such users' reservations out of `server_full` for others, or warn the admin.

**7. Low: compose services have no upper bound on CPU or memory.**
- **Where:** `internal/deploy/compose.go:1155-1179`.
- **Problem:** decisions.md says the 64 GB / 64 CPU sanity bounds still apply, but they don't for compose. Very large `cpus:` values overflow int64 in `composeTotals` and `NanoCPUs`. This matters more for overallocating users, who skip the quota check entirely.
- **Fix:** clamp in `service()` to the same bounds as single apps.

**8. Low: suspending a user leaves their public ports open.**
- **Where:** `accounts_admin.go:115-123`, through `StopAllForUser` (`apps_lifecycle.go:163`), which never calls `closePublicPort`.
- **Scenario:** a port set to never expire keeps listening on the internet. If the admin reactivates the user, the database is exposed again with no action from anyone.
- **Fix:** close the user's public ports when suspending.

**9. Low: anyone allowed to connect can lock the owner out of a public port.**
- **Where:** `portproxy/portproxy.go:183-197,216-237`.
- **Problem:** there is one global cap of 64 connections, with no limit per address and no idle timeout. With an empty `allow_from`, 64 idle connections from anywhere refuse the owner's psql.
- **Fix:** limit connections per source address and add an idle deadline.

**10. Low: `SetPublicPort` reads the current row outside its transaction.**
- **Where:** `public_ports.go:312-319` reads it; `:354-376` writes it.
- **Scenario:** two opens at once give a misleading "taken" error. A change that races the reconciler closing an expired port returns 200 and writes a `public_port_changed` audit entry for a port that is closed.
- **Fix:** read the row inside the transaction and check the rows affected.

**11. Low: a MySQL/MariaDB database named like an option breaks its backup.**
- **Where:** `backups_dump.go:107-112`, the `--databases $(cat "$t")` argument list.
- **Scenario:** the app creates a database named `--no-data`. mysqldump reads it as an option and writes a schema-only dump, which `dumpComplete` still accepts. The backup is silently missing its rows.
- **Fix:** skip names starting with `-`, or end the options before the names.

**12. Low: values filled into a compose file's `image:` leak to read-only tokens and admins.**
- **Where:** the "pulling %s" log line (`deploy_compose_run.go:267`), the invalid-image error (`compose.go:1088`), and the `app_services.image` column.
- **Problem:** `ValidateCompose` deliberately needs deploy scope so filled-in values stay hidden, but a real deploy shows them in places that read-only tokens and admins can read.
- **Fix:** refuse `${VAR}` in `image:` (as is already done for references), or mask those values in logs and errors.

**13. Low (correctness): small issues in the static-site and Traffic changes.**
- **Static sites in Dockerfile mode** (`buildmode.go:257-267`):
  - A push that loses its Dockerfile, where the top folder has an HTML page and no recognised stack, now goes live as a public static site instead of failing. `.env` and `.git` are still excluded.
  - It also resets the app's memory and CPU to the static size.
  - The comment at `:197-198` ("dockerfile mode is always a Dockerfile build") is now wrong.
- **Traffic answer times** (`traffic/agg.go:216`): long-lived responses that aren't websockets, like server-sent events or long polling, count their whole open time and push the p95 figure up.

## Not checked
- Running anything that uses Docker: the integration tests, `probe-test.sh`, and the firewall under nftables or after a dockerd restart with `live-restore`.
- The reconcile, rollback and swap paths for multi-service apps and several apps per project, beyond `deployment_restarts`.
- `DeleteProject` racing with creating an app, and the race between `dependents()` and the move transaction.
- The new Traffic tab, admin and Database tab Vue pages in detail. The field names in `types.go` and `types.ts` match.
- The network setup of the isolated builder (how far image pulls can reach), and whether compose-go accepts `=` inside an `environment` key.
- Database viewer write operations (insert, update, delete) on the server engines.
