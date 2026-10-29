// Package rules holds the markdown the MCP server and the REST API hand to
// agents: the server instructions and the runtime contract. It lives in its
// own package so the service layer (internal/app) can use it without
// importing internal/mcp, which imports the service layer.
// OWNER: MCP agent.
package rules

import (
	_ "embed"
	"strings"
)

//go:embed instructions.md
var instructions string

// Instructions returns the server instructions with the example address
// replaced by the real app address pattern (for example
// https://<slug>.bitploy.nl). exampleURL is cfg.AppURL("<slug>"), domain is
// cfg.AppsDomain.
func Instructions(exampleURL, domain string) string {
	s := strings.Replace(instructions, "https://<slug>.bitploy.nl", exampleURL, 1)
	return strings.ReplaceAll(s, "bitploy.nl", domain)
}

// RuntimeContract is what every deployed app must follow.
const RuntimeContract = `## Runtime contract

- Listen on 0.0.0.0:$PORT. Bitploy sets PORT, BITPLOY_APP and BITPLOY_URL. The port is the app's port setting, otherwise the single EXPOSE of the last stage of the Dockerfile.
- Log to stdout and stderr; build and runtime logs come from get_deployment and get_logs.
- Write persistent data only to /data, and only after turning the volume on with update_app (volume=true). Everything else in the container is lost on redeploy.
- Apps run as one locked-down container: 512 MB memory and 0.5 CPU by default, no root privileges, no access to the Docker socket or the host.
- A worker (type=worker: bot, queue consumer) has no route and no port check; it counts as healthy when it is still running after 30 seconds.
- A web app is healthy when its port is open and GET health_path (default /) answers with a status below 500 within 60 seconds. A Dockerfile HEALTHCHECK replaces this check.
- A failed deploy never takes a working app down: the old version keeps serving until the new one is healthy.
- With a volume attached, a deploy stops the old container before starting the new one (a few seconds of downtime).
- The last 3 images are kept per app so rollback works.

## Limits

- deploy_files: at most 2 MB in total and 200 files.
- Zip upload: at most 95 MB compressed, 500 MB unpacked, 20,000 files. If everything sits in one top-level folder, that folder is stripped.
- Git: shallow clone of one branch, at most 500 MB, HTTPS only. Private GitHub repositories use the token the user saved in the dashboard under Settings.
- A build takes at most 15 minutes. The built image must be under 2 GB.

## Dockerfile checks (validate_dockerfile)

Errors stop the deploy: dockerfile_missing, dockerfile_parse_error, no_from, port_unknown, port_ambiguous, image_too_large.
Warnings: env_file_in_context, no_dockerignore, unpinned_base, runs_as_root. Info: no_healthcheck.
Fix every error and read every warning before deploying.
`
