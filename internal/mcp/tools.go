package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
)

// ---- input types ----

type emptyIn struct{}

type appIn struct {
	App string `json:"app" jsonschema:"The app: its slug (like quiet-otter-3f9a) or its ID (app_...)."`
}

type templateIn struct {
	Stack string `json:"stack" jsonschema:"static, node, node-spa, python, go, php or generic."`
}

type validateIn struct {
	Dockerfile string   `json:"dockerfile" jsonschema:"The full text of the Dockerfile."`
	FileList   []string `json:"file_list,omitempty" jsonschema:"Paths of the files in the project folder (relative, with forward slashes). Lets bitploy warn about .env files and missing .dockerignore, and name the right template when the Dockerfile is missing."`
	App        string   `json:"app,omitempty" jsonschema:"Optional app slug. Uses that app's port setting, and skips port checks for a worker."`
}

type createAppIn struct {
	Name   string `json:"name" jsonschema:"A short human name, like My Portfolio."`
	Type   string `json:"type,omitempty" jsonschema:"web (default: a site or API with an HTTP port) or worker (a bot or queue consumer with no port)."`
	Slug   string `json:"slug,omitempty" jsonschema:"Subdomain, 3 to 40 characters of a-z, 0-9 and -. Leave out to get a random one like quiet-otter-3f9a."`
	Port   *int64 `json:"port,omitempty" jsonschema:"The port the app listens on. Leave out to use the single EXPOSE of the Dockerfile."`
	Volume bool   `json:"volume,omitempty" jsonschema:"true gives the app a persistent folder at /data that survives redeploys."`
}

type updateAppIn struct {
	App            string  `json:"app" jsonschema:"The app slug or ID."`
	Name           *string `json:"name,omitempty" jsonschema:"New display name."`
	Type           *string `json:"type,omitempty" jsonschema:"web or worker."`
	Port           *int64  `json:"port,omitempty" jsonschema:"App port. 0 clears it so the Dockerfile EXPOSE is used."`
	HealthPath     *string `json:"health_path,omitempty" jsonschema:"Path bitploy requests to check the app is healthy, default /. Any answer below 500 passes."`
	DockerfilePath *string `json:"dockerfile_path,omitempty" jsonschema:"Path of the Dockerfile in the project, default Dockerfile."`
	ContextDir     *string `json:"context_dir,omitempty" jsonschema:"Folder used as the build context, default . (the project root)."`
	Volume         *bool   `json:"volume,omitempty" jsonschema:"true turns on the persistent /data volume, false turns it off."`
	MemoryMB       *int64  `json:"memory_mb,omitempty" jsonschema:"Memory limit in MB (default 512), within the account quota."`
	CPUMillis      *int64  `json:"cpu_millis,omitempty" jsonschema:"CPU limit in thousandths of a CPU (500 = half a CPU), within the account quota."`
}

type fileIn struct {
	Path     string `json:"path" jsonschema:"Relative path such as index.html or src/app.js. No leading slash and no .."`
	Content  string `json:"content" jsonschema:"The file content."`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf8 (default) for text, or base64 for binary files such as images."`
}

type deployFilesIn struct {
	App   string   `json:"app" jsonschema:"The app slug or ID."`
	Files []fileIn `json:"files" jsonschema:"All project files including the Dockerfile and .dockerignore. At most 200 files and 2 MB in total."`
}

type gitSourceIn struct {
	App     string `json:"app" jsonschema:"The app slug or ID."`
	RepoURL string `json:"repo_url" jsonschema:"HTTPS address of the repository, like https://github.com/owner/repo."`
	Branch  string `json:"branch,omitempty" jsonschema:"Branch to deploy. Leave out for the repository's default branch."`
	Subdir  string `json:"subdir,omitempty" jsonschema:"Folder inside the repository that holds the project (and its Dockerfile), like apps/web."`
}

type deployIn struct {
	App      string `json:"app" jsonschema:"The app slug or ID."`
	UploadID string `json:"upload_id,omitempty" jsonschema:"The upload_id from create_upload after the zip was sent. Leave out to deploy the app's git source."`
}

type getDeploymentIn struct {
	ID          string `json:"id" jsonschema:"The deployment ID (dep_...)."`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Wait up to this many seconds (max 50; larger values count as 50) for the state to become running or failed. Use 30 while a deploy is in progress."`
}

type listDeploymentsIn struct {
	App    string `json:"app" jsonschema:"The app slug or ID."`
	Limit  int    `json:"limit,omitempty" jsonschema:"How many to return, newest first (default 10, max 100)."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor of the previous page."`
}

type getLogsIn struct {
	App   string `json:"app" jsonschema:"The app slug or ID."`
	Kind  string `json:"kind" jsonschema:"build (the latest build log) or runtime (what the running app prints)."`
	Tail  int    `json:"tail,omitempty" jsonschema:"Number of last lines, default 200, max 1000."`
	Since string `json:"since,omitempty" jsonschema:"Runtime logs only: a time like 2026-10-29T12:00:00Z or an age like 10m or 2h."`
}

type setEnvIn struct {
	App   string            `json:"app" jsonschema:"The app slug or ID."`
	Set   map[string]string `json:"set,omitempty" jsonschema:"Variables to add or change, name to value. Values are stored encrypted and never shown again."`
	Unset []string          `json:"unset,omitempty" jsonschema:"Names of variables to remove."`
}

type rollbackIn struct {
	App          string `json:"app" jsonschema:"The app slug or ID."`
	DeploymentID string `json:"deployment_id" jsonschema:"The earlier deployment (dep_...) to run again. Only the last 3 images are kept."`
}

type deleteAppIn struct {
	App         string `json:"app" jsonschema:"The app slug or ID."`
	ConfirmSlug string `json:"confirm_slug" jsonschema:"The app's slug, typed out again, to confirm. Ask the user before deleting."`
}

type domainIn struct {
	App      string `json:"app" jsonschema:"The app slug or ID."`
	Hostname string `json:"hostname" jsonschema:"The custom domain, like www.example.com."`
}

type metricsIn struct {
	App    string `json:"app" jsonschema:"The app slug or ID."`
	Window string `json:"window,omitempty" jsonschema:"1h, 24h (default) or 7d."`
}

// ---- registration ----

func (s *srv) registerTools() {
	svc := s.svc

	addTool(s, "whoami", "Who am I", "Shows the signed-in user, quotas, usage and the base domain apps are served from.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			me, err := svc.Me(ctx, a)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Signed in as %s (%s), token scope %s; %d of %d apps used; apps live at https://<slug>.%s.",
					me.User.Name, me.User.Email, me.Scope, me.Usage.Apps, me.Quota.Apps, me.AppsDomain),
				Data: toMap(me),
				Next: []string{"Call list_apps to see existing apps, or create_app to make a new one."},
			}, nil
		})

	addTool(s, "get_deploy_rules", "Deploy rules", "The rules every deploy follows plus the runtime contract (port, logs, /data). Read it once per session.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			md, err := svc.DeployRules(ctx, a)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: "Bitploy deploy rules and runtime contract.",
				Data:    data("rules", md),
				Next:    []string{"If the project has no Dockerfile, call get_template with the stack that fits.", "Call validate_dockerfile before every deploy."},
			}, nil
		})

	addTool(s, "get_template", "Get a Dockerfile template",
		"Returns a ready Dockerfile and .dockerignore for a stack: static (plain HTML/CSS/JS), node (Node.js server), node-spa (Vue/React/Vite site), python, go, php, generic (anything else).",
		readOnly(), false,
		nil,
		func(ctx context.Context, a app.Actor, in templateIn) (*result, error) {
			t, err := svc.GetTemplate(ctx, a, in.Stack)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Template %q: a Dockerfile and a .dockerignore.", t.Stack),
				Data:    data("stack", t.Stack, "dockerfile", t.Dockerfile, "dockerignore", t.Dockerignore, "notes", t.Readme),
				Next: []string{
					"Save dockerfile as Dockerfile and dockerignore as .dockerignore in the top folder of the project.",
					"Adapt it: the start command, and make the app listen on 0.0.0.0 at the EXPOSE port ($PORT), never on localhost.",
					"Call validate_dockerfile with the Dockerfile text and the list of project files.",
				},
			}, nil
		})

	addTool(s, "validate_dockerfile", "Validate a Dockerfile",
		"Static checks on a Dockerfile without building: parse errors, missing FROM, port, .env files, unpinned base image, running as root. Call it before every deploy and fix all errors.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in validateIn) (*result, error) {
			var res app.ValidateResult
			var err error
			if strings.TrimSpace(in.App) != "" {
				res, err = svc.ValidateDockerfileFor(ctx, a, in.App, in.Dockerfile, in.FileList)
			} else {
				res, err = svc.ValidateDockerfile(ctx, a, in.Dockerfile, in.FileList, 0)
			}
			if err != nil {
				return nil, err
			}
			var errs, warns int
			var first string
			for _, f := range res.Findings {
				switch f.Severity {
				case "error":
					errs++
					if first == "" {
						first = f.Code
					}
				case "warning":
					warns++
				}
			}
			summary := fmt.Sprintf("Dockerfile is valid (port %d, %d warnings).", res.Port, warns)
			if !res.OK {
				summary = fmt.Sprintf("Dockerfile has %d error(s) and %d warning(s); first: %s.", errs, warns, first)
			}
			d := toMap(res)
			delete(d, "next_steps")
			return &result{Summary: summary, Data: d, Next: res.NextSteps}, nil
		})

	addTool(s, "list_apps", "List apps", "Lists the user's apps with URL, state and current deployment.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			apps, err := svc.ListApps(ctx, a)
			if err != nil {
				return nil, err
			}
			next := []string{"Call get_app for details of one app."}
			if len(apps) == 0 {
				next = []string{"There are no apps yet. Call create_app to make one."}
			}
			return &result{Summary: fmt.Sprintf("%d app(s).", len(apps)), Data: data("apps", nonNil(apps)), Next: next}, nil
		})

	addTool(s, "get_app", "Get an app", "One app with its URL, status, settings, current deployment and domains.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			ap, err := svc.GetApp(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			return &result{Summary: appLine(ap), Data: data("app", ap), Next: appNext(ap)}, nil
		})

	addTool(s, "create_app", "Create an app",
		"Reserves a subdomain and returns the app URL. It serves nothing until the first deploy.",
		write(), true,
		nil,
		func(ctx context.Context, a app.Actor, in createAppIn) (*result, error) {
			ap, err := svc.CreateApp(ctx, a, app.CreateAppInput{Name: in.Name, Type: in.Type, Slug: in.Slug, Port: in.Port, Volume: in.Volume})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Created app %s at %s (nothing is deployed yet).", ap.Slug, ap.URL),
				Data:    data("app", ap),
				Next: []string{
					"If the project has no Dockerfile, call get_template and add one, then call validate_dockerfile.",
					"Send the code: deploy_files for up to 2 MB and 200 files, create_upload for a zip, or set_git_source and deploy for a git repository.",
				},
			}, nil
		})

	addTool(s, "update_app", "Change app settings",
		"Changes the port, health path, volume, memory or CPU of an app. Only the fields you pass change.",
		idempotent(), true, nil,
		func(ctx context.Context, a app.Actor, in updateAppIn) (*result, error) {
			ap, err := svc.UpdateApp(ctx, a, in.App, app.UpdateAppInput{
				Name: in.Name, Type: in.Type, Port: in.Port, HealthPath: in.HealthPath,
				DockerfilePath: in.DockerfilePath, ContextDir: in.ContextDir, Volume: in.Volume,
				MemoryMB: in.MemoryMB, CPUMillis: in.CPUMillis,
			})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: "Updated " + ap.Slug + ".",
				Data:    data("app", ap),
				Next:    []string{"Settings that change how the app runs (port, volume, resources) apply from the next deploy. Call deploy or deploy_files to apply them, then get_deployment."},
			}, nil
		})

	addTool(s, "deploy_files", "Deploy files",
		"Deploys inline files (at most 2 MB and 200 files, one of them a Dockerfile). Validates, queues a deployment and returns at once.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in deployFilesIn) (*result, error) {
			files := make([]app.FileInput, len(in.Files))
			for i, f := range in.Files {
				files[i] = app.FileInput{Path: f.Path, Content: f.Content, Encoding: f.Encoding}
			}
			d, err := svc.DeployFiles(ctx, a, in.App, files)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued deployment", d), nil
		})

	addTool(s, "create_upload", "Create a zip upload",
		"For folders bigger than deploy_files allows: returns a one-time upload address (valid 15 minutes) and the exact curl command that uploads a zip.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			u, err := svc.CreateUpload(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			next := append([]string{
				"Zip the project folder (for example: cd the-folder && zip -r ../app.zip .), without node_modules and .git.",
				"Run the curl command from the curl field, with app.zip at the path it names.",
				"Then call deploy with app=" + in.App + " and upload_id=" + u.ID + ".",
			}, u.NextSteps...)
			return &result{
				Summary: fmt.Sprintf("Upload %s is ready until %s. Run: %s", u.ID, u.ExpiresAt.UTC().Format(time.RFC3339), u.Curl),
				Data:    toMap(u),
				Next:    dedupe(next),
			}, nil
		})

	addTool(s, "set_git_source", "Set a git source",
		"Saves a git repository (HTTPS), branch and optional subfolder as the app's source and checks access with ls-remote. Private GitHub repositories use the token the user saved in the bitploy dashboard under Settings.",
		openWorld(idempotent()), true, nil,
		func(ctx context.Context, a app.Actor, in gitSourceIn) (*result, error) {
			ap, err := svc.SetGitSource(ctx, a, in.App, app.GitSource{RepoURL: in.RepoURL, Branch: in.Branch, Subdir: in.Subdir})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Git source for %s saved and readable.", ap.Slug),
				Data:    data("app", ap),
				Next:    []string{"The repository needs a Dockerfile (in the subfolder, if you set one). Call deploy with app=" + ap.Slug + " to build it."},
			}, nil
		})

	addTool(s, "deploy", "Deploy", "Queues a deployment from an uploaded zip (upload_id) or from the app's git source. Returns at once.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in deployIn) (*result, error) {
			d, err := svc.Deploy(ctx, a, in.App, in.UploadID)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued deployment", d), nil
		})

	addTool(s, "get_deployment", "Get a deployment",
		"State, timings, error and the last 50 log lines of a deployment. With wait_seconds it waits (up to 50 s) for the state to change.",
		readOnly(), false,
		nil,
		func(ctx context.Context, a app.Actor, in getDeploymentIn) (*result, error) {
			wait := in.WaitSeconds
			if wait < 0 {
				wait = 0
			}
			if wait > maxWaitSec {
				wait = maxWaitSec
			}
			d, err := svc.GetDeployment(ctx, a, in.ID, time.Duration(wait)*time.Second)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Deployment", d), nil
		})

	addTool(s, "list_deployments", "List deployments", "Deployment history of an app, newest first.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in listDeploymentsIn) (*result, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 10
			}
			if limit > 100 {
				limit = 100
			}
			page, err := svc.ListDeployments(ctx, a, in.App, limit, in.Cursor)
			if err != nil {
				return nil, err
			}
			page.Items = nonNil(page.Items)
			for i := range page.Items {
				page.Items[i].LogTail = nil
			}
			return &result{
				Summary: fmt.Sprintf("%d deployment(s) of %s.", len(page.Items), in.App),
				Data:    toMap(page),
				Next:    []string{"Call get_deployment with an id for the error and log tail of one deployment."},
			}, nil
		})

	addTool(s, "get_logs", "Get logs", "Build or runtime log lines of an app (at most 1,000 lines).",
		readOnly(), false,
		nil,
		func(ctx context.Context, a app.Actor, in getLogsIn) (*result, error) {
			opts := app.LogOptions{Kind: in.Kind, Tail: in.Tail}
			if opts.Kind != "build" && opts.Kind != "runtime" {
				return nil, apperr.BadRequest("log_kind_invalid", "kind must be build or runtime.",
					"Use kind=build for the latest build log, or kind=runtime for what the running app prints.")
			}
			if opts.Tail <= 0 {
				opts.Tail = 200
			}
			if opts.Tail > 1000 {
				opts.Tail = 1000
			}
			if in.Since != "" {
				t, err := parseSince(in.Since, time.Now())
				if err != nil {
					return nil, err
				}
				opts.Since = &t
			}
			lines, err := svc.Logs(ctx, a, in.App, opts)
			if err != nil {
				return nil, err
			}
			next := []string{}
			if opts.Kind == "runtime" {
				next = append(next, "If the app misbehaves, fix the code and deploy again.")
			} else {
				next = append(next, "Fix the cause shown in the build log, then deploy again.")
			}
			return &result{
				Summary: fmt.Sprintf("%d %s log line(s) for %s.", len(lines), opts.Kind, in.App),
				Data:    data("app", in.App, "kind", opts.Kind, "lines", nonNil(lines)),
				Next:    next,
			}, nil
		})

	addTool(s, "set_env", "Set environment variables",
		"Adds, changes or removes encrypted environment variables. Values are never returned. Use this for secrets, never the Dockerfile.",
		idempotent(), true, nil,
		func(ctx context.Context, a app.Actor, in setEnvIn) (*result, error) {
			vars, err := svc.SetEnv(ctx, a, in.App, in.Set, in.Unset)
			if err != nil {
				return nil, err
			}
			names := make([]string, len(vars))
			for i, v := range vars {
				names[i] = v.Name
			}
			sort.Strings(names)
			return &result{
				Summary: fmt.Sprintf("%s now has %d variable(s): %s.", in.App, len(vars), strings.Join(names, ", ")),
				Data:    data("env", nonNil(vars)),
				Next:    []string{"A running app reads new values when it next starts: call restart_app, or deploy again."},
			}, nil
		})

	lifecycle := func(name, title, desc, verb string, ann *mcp.ToolAnnotations, call func(ctx context.Context, a app.Actor, ref string) (app.App, error)) {
		addTool(s, name, title, desc, ann, true, nil,
			func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
				ap, err := call(ctx, a, in.App)
				if err != nil {
					return nil, err
				}
				return &result{
					Summary: fmt.Sprintf("%s %s: status %s.", verb, ap.Slug, ap.Status),
					Data:    data("app", ap),
					Next:    []string{"Call get_app to check the state, or get_logs with kind=runtime if it does not come up."},
				}, nil
			})
	}
	lifecycle("restart_app", "Restart an app", "Restarts the running container of an app.", "Restarted", write(), svc.RestartApp)
	lifecycle("start_app", "Start an app", "Starts a stopped app.", "Started", write(), svc.StartApp)
	lifecycle("stop_app", "Stop an app", "Stops an app: its address then shows 'This app is stopped'. Only stop an app the user named.", "Stopped", destructive(), svc.StopApp)

	addTool(s, "rollback", "Roll back", "Runs a previous deployment's image again (the last 3 are kept). Only roll back an app the user named.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in rollbackIn) (*result, error) {
			d, err := svc.Rollback(ctx, a, in.App, in.DeploymentID)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued rollback", d), nil
		})

	addTool(s, "delete_app", "Delete an app",
		"Permanently removes an app: containers, images, routes and its volume. Needs confirm_slug equal to the app's slug. Only delete an app the user named, and ask first.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in deleteAppIn) (*result, error) {
			if strings.TrimSpace(in.ConfirmSlug) == "" {
				return nil, apperr.BadRequest("confirm_slug_required", "delete_app needs confirm_slug, the app's slug typed out again.",
					"Ask the user to confirm, then call delete_app again with confirm_slug set to the slug of the app.").WithNext("delete_app")
			}
			if err := svc.DeleteApp(ctx, a, in.App, in.ConfirmSlug); err != nil {
				return nil, err
			}
			return &result{
				Summary: "Deleted " + in.App + ".",
				Data:    data("deleted", in.App),
				Next:    []string{"Tell the user the app and its data are gone."},
			}, nil
		})

	addTool(s, "add_domain", "Add a custom domain",
		"Adds a custom domain to an app and returns the DNS records the user must create.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			d, err := svc.AddDomain(ctx, a, in.App, in.Hostname)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Domain %s added, status %s.", d.Hostname, d.Status),
				Data:    data("domain", d),
				Next:    append(domainNext(d), "Once the user created the records, call check_domain."),
			}, nil
		})

	addTool(s, "check_domain", "Check a custom domain", "Checks the DNS records of a custom domain and moves it toward active.",
		openWorld(write()), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			d, err := svc.CheckDomain(ctx, a, in.App, in.Hostname)
			if err != nil {
				return nil, err
			}
			next := domainNext(d)
			switch d.Status {
			case "active":
				next = []string{"The domain works. Tell the user it is live at https://" + d.Hostname + "."}
			case "failed", "pending_dns":
				next = append(next, "Fix the records, wait a few minutes for DNS, then call check_domain again.")
			case "issuing_cert":
				next = []string{"The certificate is being issued. Call check_domain again in a minute."}
			}
			summary := fmt.Sprintf("Domain %s: %s.", d.Hostname, d.Status)
			if d.LastError != "" {
				summary += " " + oneLine(d.LastError)
			}
			return &result{Summary: summary, Data: data("domain", d), Next: next}, nil
		})

	addTool(s, "remove_domain", "Remove a custom domain", "Removes a custom domain from an app.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			if err := svc.RemoveDomain(ctx, a, in.App, in.Hostname); err != nil {
				return nil, err
			}
			return &result{
				Summary: "Removed domain " + in.Hostname + ".",
				Data:    data("removed", in.Hostname),
				Next:    []string{"The app is still reachable at its bitploy address."},
			}, nil
		})

	addTool(s, "get_metrics", "Get metrics", "CPU, memory and network samples of an app for 1h, 24h or 7d.",
		readOnly(), false,
		nil,
		func(ctx context.Context, a app.Actor, in metricsIn) (*result, error) {
			w := in.Window
			if w == "" {
				w = "24h"
			}
			m, err := svc.GetMetrics(ctx, a, in.App, w)
			if err != nil {
				return nil, err
			}
			m.Points = nonNil(m.Points)
			summary := fmt.Sprintf("%d sample(s) for %s over %s.", len(m.Points), in.App, w)
			if m.Latest != nil {
				summary += fmt.Sprintf(" Now: CPU %.1f%%, memory %.0f of %d MB.", m.Latest.CPU, m.Latest.MemoryMB, m.MemoryLimitMB)
			}
			return &result{Summary: summary, Data: toMap(m), Next: []string{"If memory is near the limit, raise it with update_app (memory_mb) or use less."}}, nil
		})
}

// ---- result helpers ----

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func appLine(ap app.App) string {
	s := fmt.Sprintf("%s (%s): status %s, %s", ap.Slug, ap.Type, ap.Status, ap.URL)
	if ap.Current != nil {
		s += ", deployment " + ap.Current.ID + " " + ap.Current.State
	}
	if ap.StatusMessage != "" {
		s += ". " + oneLine(ap.StatusMessage)
	}
	return s + "."
}

func appNext(ap app.App) []string {
	switch ap.Status {
	case "new":
		return []string{"Nothing is deployed yet. Send the code with deploy_files, create_upload or set_git_source."}
	case "failed", "crashed":
		return []string{"Call get_deployment for the error and log tail, or get_logs with kind=runtime, fix the cause and deploy again."}
	case "stopped":
		return []string{"Call start_app to bring it back, only if the user asked for it."}
	case "deploying":
		if ap.Current != nil {
			return []string{"Call get_deployment with id=" + ap.Current.ID + " and wait_seconds=30."}
		}
	}
	return []string{"The app is at " + ap.URL + ". To change it, deploy again."}
}

func domainNext(d app.Domain) []string {
	var out []string
	for _, r := range d.Records {
		out = append(out, fmt.Sprintf("Ask the user to create this DNS record: %s %s = %s", r.Type, r.Name, r.Value))
	}
	out = append(out, d.Notes...)
	return out
}

// parseSince accepts RFC 3339 times and ages like 10m, 2h or 1d.
func parseSince(v string, now time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if strings.HasSuffix(v, "d") {
		var n int
		if _, err := fmt.Sscanf(v, "%dd", &n); err == nil && n > 0 {
			return now.Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, apperr.BadRequest("since_invalid", fmt.Sprintf("%q is not a time or an age.", v),
		"Use a time like 2026-10-29T12:00:00Z or an age like 10m, 2h or 1d.")
}

// deploymentResult renders a deployment with state-specific next steps.
func deploymentResult(verb string, d app.Deployment) *result {
	if d.Error != nil && d.Error.Code == "unhealthy" && !strings.Contains(d.Error.Hint, "0.0.0.0") && deploy.MentionsLocalhost(d.LogTail) {
		// Safety net for the most common failure; the pipeline normally sets this hint itself.
		e := *d.Error
		e.Hint = deploy.LocalhostHint + ". For example Node: app.listen(process.env.PORT || 3000, \"0.0.0.0\"); Flask: app.run(host=\"0.0.0.0\", port=int(os.environ.get(\"PORT\", 8000))). Then deploy again."
		d.Error = &e
	}
	var next []string
	summary := fmt.Sprintf("%s %s of %s (#%d): %s", verb, d.ID, d.AppSlug, d.Seq, d.State)
	poll := "Call get_deployment with id=" + d.ID + " and wait_seconds=30 until the state is running or failed."
	switch d.State {
	case "running":
		summary += ". Live at " + d.URL
		next = []string{
			"Give the user the URL " + d.URL + " and one or two plain sentences on what you did.",
		}
		if len(d.Warnings) > 0 {
			next = append(next, "Mention warnings only if they matter to the user.")
		}
	case "failed":
		if d.Error != nil {
			summary += " with " + d.Error.Code + ": " + oneLine(d.Error.Message)
		}
		next = []string{
			"Read error.hint and log_tail, fix the cause, then deploy again (deploy_files, or deploy for an upload or git source).",
			"After 3 failures with the same error code, stop and explain the problem to the user in plain words.",
		}
		if d.Error != nil && d.Error.Next != "" {
			next = append([]string{"Suggested next tool: " + d.Error.Next + "."}, next...)
		}
	case "superseded", "cancelled":
		next = []string{"A newer deployment replaced this one. Call get_app to find the current deployment."}
	default:
		next = []string{poll}
	}
	if len(d.NextSteps) > 0 && !d.Terminal() {
		next = dedupe(append(next, d.NextSteps...))
	}
	d.NextSteps = nil
	return &result{Summary: summary + ".", Data: data("deployment", d), Next: next}
}
