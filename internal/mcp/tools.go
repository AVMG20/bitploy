package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
)

// ---- input types ----
//
// Every field has a jsonschema description: models read these as the manual
// for the argument, so each says what to pass, in what format, with an example.

type emptyIn struct{}

type appIn struct {
	App string `json:"app" jsonschema:"The app's slug (the part before the domain, like quiet-otter-3f9a) or its ID (app_...). Not sure? Look in the project's .bitploy file or call list_apps."`
}

type templateIn struct {
	Stack string `json:"stack" jsonschema:"One of: static, node, node-spa, python, go, php, generic."`
}

type validateIn struct {
	Dockerfile string   `json:"dockerfile" jsonschema:"The full text of the Dockerfile (not a path). Use an empty string if the project has none: you then get the template to use."`
	FileList   []string `json:"file_list,omitempty" jsonschema:"Paths of all files in the project folder, relative with forward slashes, like [\"package.json\",\"src/index.js\",\".env\"]. Lets bitploy name the right stack and warn about .env files and a missing .dockerignore."`
	App        string   `json:"app,omitempty" jsonschema:"Optional app slug. Uses that app's port setting and skips port checks for a worker."`
}

type createAppIn struct {
	Name     string `json:"name" jsonschema:"Short human name, like My Portfolio. You will match the app by this name later, so make it readable."`
	Type     string `json:"type,omitempty" jsonschema:"web (default): site or API with an HTTP port. worker: a bot or queue consumer with no web port (Discord bot, cron-like loop)."`
	Slug     string `json:"slug,omitempty" jsonschema:"Subdomain: 3-40 characters of a-z, 0-9 and -, no dots, not starting or ending with -, like my-portfolio. Leave out to get a random one like quiet-otter-3f9a."`
	Port     *int64 `json:"port,omitempty" jsonschema:"Only when the Dockerfile has no EXPOSE or several: the port the app listens on, like 3000. Leave out otherwise."`
	Volume   bool   `json:"volume,omitempty" jsonschema:"true gives the app a persistent folder at /data that survives redeploys (SQLite, uploads). The app must then write its files to /data."`
	VolumeMB *int64 `json:"volume_mb,omitempty" jsonschema:"Storage limit of /data in MB, default 5120 (5 GB). Taken from the account's storage allowance (whoami) while the volume is on."`
}

type updateAppIn struct {
	App            string  `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Name           *string `json:"name,omitempty" jsonschema:"New display name, 1-80 characters."`
	Slug           *string `json:"slug,omitempty" jsonschema:"Rename the subdomain (the URL changes, old URL stops working, a few seconds of downtime). 3-40 characters of a-z, 0-9 and -. Files in /data are kept."`
	Type           *string `json:"type,omitempty" jsonschema:"web or worker."`
	Port           *int64  `json:"port,omitempty" jsonschema:"The port the app listens on, like 3000. 0 clears it so the Dockerfile EXPOSE is used. Applies on the next deploy."`
	HealthPath     *string `json:"health_path,omitempty" jsonschema:"Path bitploy requests to see the app is healthy, like /healthz. Starts with /. Default /. Any status below 500 passes. Applies on the next deploy."`
	DockerfilePath *string `json:"dockerfile_path,omitempty" jsonschema:"Path of the Dockerfile inside the project, default Dockerfile. Example: docker/Dockerfile.prod."`
	ContextDir     *string `json:"context_dir,omitempty" jsonschema:"Folder used as the build context, default . (the project root). Example: backend."`
	Volume         *bool   `json:"volume,omitempty" jsonschema:"true turns on the persistent /data volume, false turns it off. Call restart_app afterwards."`
	VolumeMB       *int64  `json:"volume_mb,omitempty" jsonschema:"Storage limit of /data in MB, like 10240 for 10 GB (default 5120, minimum 100), within the account's storage allowance (whoami). Applies at once, no restart. Bitploy checks /data hourly and stops an app that is over its limit."`
	MemoryMB       *int64  `json:"memory_mb,omitempty" jsonschema:"Memory limit in MB, like 1024 (default 512, minimum 64), within the account quota. Call restart_app afterwards."`
	CPUMillis      *int64  `json:"cpu_millis,omitempty" jsonschema:"CPU limit in thousandths of a CPU: 500 is half a CPU (default), 1000 is one. Call restart_app afterwards."`
}

type fileIn struct {
	Path     string `json:"path" jsonschema:"Relative path with forward slashes, like index.html or src/app.js. No leading slash, no .., no backslashes."`
	Content  string `json:"content" jsonschema:"The file content: plain text for utf8, the base64 text for base64."`
	Encoding string `json:"encoding,omitempty" jsonschema:"utf8 (default) for text files, base64 for binary files such as images or fonts."`
}

type deployFilesIn struct {
	App   string   `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Files []fileIn `json:"files" jsonschema:"EVERY file of the project, including Dockerfile and .dockerignore: this list replaces the previous source completely. At most 200 files and 2 MB in total. Leave out node_modules, .git, .env and .bitploy."`
}

type gitSourceIn struct {
	App     string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	RepoURL string `json:"repo_url" jsonschema:"HTTPS address of the repository, like https://github.com/owner/repo (no .git needed, no token in the URL, no git@ addresses)."`
	Branch  string `json:"branch,omitempty" jsonschema:"Branch to deploy, like main. Leave out for the repository's default branch."`
	Subdir  string `json:"subdir,omitempty" jsonschema:"Folder inside the repository that holds the project and its Dockerfile, like apps/web. Leave out when the Dockerfile is at the top."`
}

type deployIn struct {
	App      string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	UploadID string `json:"upload_id,omitempty" jsonschema:"The upload_id (upl_...) from create_upload, after the curl command succeeded. Leave out to deploy the app's git source, or to rebuild the last deployed source."`
}

type getDeploymentIn struct {
	ID          string `json:"id,omitempty" jsonschema:"The deployment ID (dep_...) from deploy_files, deploy or list_deployments. Leave out and pass app instead to get the latest deployment."`
	App         string `json:"app,omitempty" jsonschema:"Instead of id: an app slug or ID. Returns that app's latest deployment."`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"Wait up to this many seconds (max 50; larger counts as 50) until the deployment is running or failed. Use 30 while a deploy is in progress; leave out for an instant answer."`
}

type listDeploymentsIn struct {
	App    string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Limit  int    `json:"limit,omitempty" jsonschema:"How many to return, newest first. Default 10, max 100."`
	Cursor string `json:"cursor,omitempty" jsonschema:"The next_cursor value of the previous page. Leave out for the first page."`
}

type getLogsIn struct {
	App   string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Kind  string `json:"kind" jsonschema:"build: the log of the latest build (use when a deploy failed while validating or building). runtime: what the running app prints (use when it crashes, errors or is unhealthy)."`
	Tail  int    `json:"tail,omitempty" jsonschema:"Number of last lines. Default 200, max 1000."`
	Since string `json:"since,omitempty" jsonschema:"Runtime logs only: lines since an age like 10m, 2h or 1d, or a time like 2026-10-29T12:00:00Z."`
}

type setEnvIn struct {
	App   string            `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Set   map[string]string `json:"set,omitempty" jsonschema:"Variables to add or change, as an object of name to value, like {\"DISCORD_TOKEN\":\"abc\",\"NODE_ENV\":\"production\"}. Names: letters, digits and _, not starting with a digit. Not PORT or BITPLOY_*."`
	Unset []string          `json:"unset,omitempty" jsonschema:"Names of variables to remove, like [\"OLD_KEY\"]. A name must not also be in set."`
}

type rollbackIn struct {
	App          string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"The earlier deployment (dep_...) to run again, from list_deployments (state running or superseded, one of the newest 3). Leave out to use the newest older deployment that ran."`
}

type deleteAppIn struct {
	App         string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	ConfirmSlug string `json:"confirm_slug" jsonschema:"The app's slug typed out again, like my-portfolio. Must match exactly. Ask the user before deleting."`
}

type domainIn struct {
	App      string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Hostname string `json:"hostname" jsonschema:"The custom domain only, no https:// and no path, like www.example.com or example.com."`
}

type metricsIn struct {
	App    string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Window string `json:"window,omitempty" jsonschema:"1h (15-second samples), 24h (default) or 7d."`
}

// enums limits string arguments to the values the tool understands, so the
// schema itself tells a model the choices.
func enums(vals map[string][]string) func(*jsonschema.Schema) {
	return func(sc *jsonschema.Schema) {
		for prop, list := range vals {
			p := sc.Properties[prop]
			if p == nil {
				panic("enums: no property " + prop)
			}
			for _, v := range list {
				p.Enum = append(p.Enum, v)
			}
		}
	}
}

// ---- registration ----

func (s *srv) registerTools() {
	svc := s.svc
	settingsURL := strings.TrimRight(svc.Cfg.PublicURL, "/") + "/settings"

	addTool(s, "whoami", "Who am I",
		"Start here. Returns the signed-in user, the token scope (deploy can change things, read cannot), the base domain apps are served from (https://<slug>.<domain>), quotas, current usage and hard limits (inline 2 MB/200 files, zip 95 MB). Next: list_apps.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			me, err := svc.Me(ctx, a)
			if err != nil {
				return nil, err
			}
			d := toMap(me)
			d["app_url_pattern"] = svc.Cfg.AppURL("<slug>")
			d["token_can_deploy"] = me.Scope != app.ScopeRead
			d["limits"] = limitsInfo()
			d["how_changes_apply"] = changeRules()
			next := []string{"Call list_apps to see the existing apps, or create_app to make a new one (but first: a .bitploy file in the project names its app; no .bitploy means create a NEW app unless the user says it is already online)."}
			if me.Scope == app.ScopeRead {
				next = []string{"This token is read-only: you can look but not deploy. Ask the user for a token with the deploy scope (dashboard, Tokens), then call list_apps to look around."}
			}
			return &result{
				Summary: fmt.Sprintf("Signed in as %s (%s), token scope %s; apps live at %s; %d of %d apps used, %d MB of %d MB memory reserved. Limits: deploy_files 200 files/2 MB, zip 95 MB. Env var changes need restart_app or deploy.",
					me.User.Name, me.User.Email, me.Scope, svc.Cfg.AppURL("<slug>"), me.Usage.Apps, me.Quota.Apps, me.Usage.MemoryReservedMB, me.Quota.MemoryMB),
				Data: d,
				Next: next,
			}, nil
		})

	addTool(s, "get_deploy_rules", "Deploy rules",
		"The full rulebook in one document: Dockerfile rule, runtime contract (0.0.0.0:$PORT, /data), limits, how to update an app later, how env and setting changes are applied, a fix for each deploy error code, custom domains. Read it once before your first deploy.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			md, err := s.rulesDoc(ctx, a)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: "Bitploy deploy rules and runtime contract.",
				Data:    data("rules", md, "base_domain", svc.Cfg.AppsDomain, "app_url_pattern", svc.Cfg.AppURL("<slug>"), "limits", limitsInfo()),
				Next:    []string{"Check which app this project is: a .bitploy file names it; otherwise create a NEW app (list_apps only when the user says it is already online). If the project has no Dockerfile, call get_template with the stack that fits, then validate_dockerfile."},
			}, nil
		})

	addTool(s, "get_template", "Get a Dockerfile template",
		"Returns a ready Dockerfile and .dockerignore for a stack. static: only HTML/CSS/JS with an index.html. node-spa: Vite/React/Vue/Svelte site built with npm run build. node: Node server (Express, Fastify, Next.js) or bot. python: Flask/FastAPI/Django. go. php: index.php or Composer site. generic: anything else (Ruby, Java, Deno). Save both files in the project root, adapt CMD and EXPOSE, then call validate_dockerfile.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in templateIn) (*result, error) {
			t, err := svc.GetTemplate(ctx, a, in.Stack)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Template %q: a Dockerfile and a .dockerignore.", t.Stack),
				Data:    data("stack", t.Stack, "dockerfile", t.Dockerfile, "dockerignore", t.Dockerignore, "notes", t.Readme),
				Next: []string{
					"Save dockerfile as Dockerfile and dockerignore as .dockerignore in the top folder of the project; read notes for what to change.",
					"Adapt it: the start command (CMD), and make the app listen on 0.0.0.0 at the EXPOSE port ($PORT), never on localhost. A bot or other app without a web port: use create_app with type=worker.",
					"Call validate_dockerfile with the Dockerfile text and the list of project files.",
				},
			}, nil
		})

	addTool(s, "validate_dockerfile", "Validate a Dockerfile",
		"Checks a Dockerfile without building. Call it before every deploy and fix every finding with severity error. Pass the Dockerfile text and file_list (all project paths) so it can name the stack and warn about .env files. Returns ok, the detected port and findings (code, severity, message, hint, line). If ok is true, send the code next.",
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

	addTool(s, "list_apps", "List apps",
		"Lists all the user's apps, one compact entry each: slug, name, url, status, healthy, problem, latest deployment. Use it to answer \"what do I have and is it healthy\", and to find an app the user says is already online (confirm by name AND url; ask the user if several could match). Do not use it to hunt for a similar name: other projects may own it. status: running (serving), deploying, new (nothing deployed yet), stopped (stopped on purpose), failed (deploy failed, nothing live), crashed (stopped on its own). A non-empty problem means look closer.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			apps, err := svc.ListApps(ctx, a)
			if err != nil {
				return nil, err
			}
			briefs := make([]appBrief, 0, len(apps))
			for _, ap := range apps {
				var latest *deploymentBrief
				if page, err := svc.ListDeployments(ctx, a, ap.Slug, 1, ""); err == nil && len(page.Items) > 0 {
					latest = briefOf(page.Items[0])
				}
				briefs = append(briefs, newAppBrief(ap, latest))
			}
			return listAppsResult(briefs), nil
		})

	addTool(s, "get_app", "Get an app",
		"One app in full: url, status and status_message, settings (port, health_path, volume, memory, cpu), git source, current deployment, domains, live CPU and memory, restart count. Use it to check an app or to start diagnosing; if something is wrong follow with get_deployment (app=slug) and get_logs.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			ap, err := svc.GetApp(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			return &result{Summary: appLine(ap), Data: data("app", ap), Next: appNext(ap)}, nil
		})

	addTool(s, "create_app", "Create an app",
		"Reserves a name and URL for a NEW app; it serves nothing until the first deploy. BEFORE calling this: if the project has a .bitploy file, that app is the one to update (skip create_app, use deploy_files). If the user says the project is already online, find it with list_apps and confirm by name and url. Otherwise create a NEW app: never reuse an app just because the name is similar. After creating, save {\"app\":\"<slug>\",\"url\":\"<url>\"} as .bitploy in the project (do not deploy that file). Use type=worker for bots with no web port.",
		write(), true,
		enums(map[string][]string{"type": {"web", "worker"}}),
		func(ctx context.Context, a app.Actor, in createAppIn) (*result, error) {
			ap, err := svc.CreateApp(ctx, a, app.CreateAppInput{Name: in.Name, Type: in.Type, Slug: in.Slug, Port: in.Port, Volume: in.Volume, VolumeMB: in.VolumeMB})
			if err != nil {
				return nil, err
			}
			url := ap.URL
			if url == "" {
				url = "(a worker has no web address)"
			}
			return &result{
				Summary: fmt.Sprintf("Created app %s (%s) at %s; nothing is deployed yet.", ap.Slug, ap.Name, url),
				Data:    data("app", ap),
				Next: []string{
					bitployFileStep(ap),
					"If the project has no Dockerfile, call get_template and add one, then call validate_dockerfile.",
					"Send the code: deploy_files with app=" + ap.Slug + " for up to 2 MB and 200 files, create_upload for a zip, or set_git_source then deploy for a git repository. To publish changes later, call deploy_files again with app=" + ap.Slug + " and ALL files (it replaces the whole source).",
				},
			}, nil
		})

	addTool(s, "update_app", "Change app settings",
		"Changes settings of an existing app; only the fields you pass change. volume, memory_mb, cpu_millis apply on restart_app; volume_mb applies at once; port, health_path, dockerfile_path, context_dir, type apply on the next deploy (call deploy with only app=); slug (rename) applies at once and changes the URL. To turn on persistent files: volume=true, restart_app, and the app must write to /data.",
		idempotent(), true, nil,
		func(ctx context.Context, a app.Actor, in updateAppIn) (*result, error) {
			ap, err := svc.UpdateApp(ctx, a, in.App, app.UpdateAppInput{
				Name: in.Name, Slug: in.Slug, Type: in.Type, Port: in.Port, HealthPath: in.HealthPath,
				DockerfilePath: in.DockerfilePath, ContextDir: in.ContextDir, Volume: in.Volume,
				VolumeMB: in.VolumeMB, MemoryMB: in.MemoryMB, CPUMillis: in.CPUMillis,
			})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: "Updated " + ap.Slug + ".",
				Data:    data("app", ap),
				Next:    updateAppNext(in, ap),
			}, nil
		})

	addTool(s, "deploy_files", "Deploy files",
		"Publishes the project from inline files and returns at once (then poll get_deployment). This is a FULL SNAPSHOT, not a patch: send every file of the project every time, including Dockerfile and .dockerignore; the previous source is replaced. Use it for first deploys and for later updates (same app, same URL). Which app: the one in the project's .bitploy file; no .bitploy means create_app first (never pick an app by similar name). At most 200 files and 2 MB; bigger: create_upload. Never send node_modules, .git, .env or .bitploy. No Dockerfile? You get the template back as an error.",
		write(), true,
		nil,
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
		"For folders over 2 MB or 200 files: returns a one-time upload address (valid 15 minutes) and the exact curl command that uploads app.zip (max 95 MB, with the Dockerfile inside, without node_modules and .git). After curl succeeds, call deploy with the upload_id.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			u, err := svc.CreateUpload(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			next := []string{
				"In the project folder run: zip -r app.zip . -x 'node_modules/*' '*/node_modules/*' '.git/*' '.env' '.bitploy' (Windows: tar -a -c -f app.zip --exclude node_modules --exclude .git .). Keep the Dockerfile inside.",
				"In that same folder run the curl command from the curl field (it uploads ./app.zip). On success it prints the upload as JSON; on an error it prints an error object with code, message and hint: follow the hint. Then delete app.zip.",
				"Then call deploy with app=" + in.App + " and upload_id=" + u.ID + ". If more than 15 minutes pass first, call create_upload again.",
			}
			return &result{
				Summary: fmt.Sprintf("Upload %s is ready until %s. Run: %s", u.ID, u.ExpiresAt.UTC().Format(time.RFC3339), u.Curl),
				Data:    toMap(u),
				Next:    next,
			}, nil
		})

	addTool(s, "set_git_source", "Set a git source",
		"Uses a git repository as the app's source: saves repo_url, branch and subdir and checks the repo can be read. Then call deploy. Public repos work as is. A private GitHub repo needs a read-only token the USER saves in the dashboard under Settings ("+settingsURL+"); you cannot set it, so when this fails with github_token_missing tell the user to do that. The repo must contain the Dockerfile (inside subdir if set).",
		openWorld(idempotent()), true, nil,
		func(ctx context.Context, a app.Actor, in gitSourceIn) (*result, error) {
			ap, err := svc.SetGitSource(ctx, a, in.App, app.GitSource{RepoURL: in.RepoURL, Branch: in.Branch, Subdir: in.Subdir})
			if err != nil {
				return nil, err
			}
			next := []string{"Call deploy with app=" + ap.Slug + " to build the latest commit. The repository must have a Dockerfile (in the subfolder, if you set one); if it has none, add one to the repository first (get_template) and push it."}
			if ap.Current == nil {
				next = append(next, bitployFileStep(ap))
			}
			return &result{
				Summary: fmt.Sprintf("Git source for %s saved and readable.", ap.Slug),
				Data:    data("app", ap),
				Next:    next,
			}, nil
		})

	addTool(s, "deploy", "Deploy",
		"Queues a build and returns at once (then poll get_deployment). With upload_id: deploys the zip from create_upload. Without: builds the latest commit of the app's git source; if the app has none, rebuilds the last deployed files or zip. That is how to apply new env vars or settings to an app that is not running, without resending files. To publish changed files, use deploy_files instead.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in deployIn) (*result, error) {
			d, err := svc.Deploy(ctx, a, in.App, in.UploadID)
			if err != nil && in.UploadID == "" && apperr.As(err).Code == "no_source" {
				// No git source: rebuild the last stored files or upload.
				if page, lerr := svc.ListDeployments(ctx, a, in.App, 1, ""); lerr == nil && len(page.Items) > 0 {
					d, err = svc.RetryLatest(ctx, a, in.App)
				}
			}
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued deployment", d), nil
		})

	addTool(s, "get_deployment", "Get a deployment",
		"State of one deployment: queued, fetching, validating, building, starting, health_check, swapping, running (live, see url), failed (see error and log_tail) or superseded. Pass the id from deploy/deploy_files, or just app to get its latest. With wait_seconds=30 it waits for the end state; call again until running or failed. Also the way to see why a deploy failed: error {code, message, hint} and the last 50 log lines.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in getDeploymentIn) (*result, error) {
			wait := in.WaitSeconds
			if wait < 0 {
				wait = 0
			}
			if wait > maxWaitSec {
				wait = maxWaitSec
			}
			id := strings.TrimSpace(in.ID)
			if id == "" {
				if strings.TrimSpace(in.App) == "" {
					return nil, apperr.BadRequest("invalid_arguments", "get_deployment needs id (dep_...) or app.",
						"Pass the deployment id from deploy_files/deploy, or pass app=<slug> for the latest deployment of that app. Not sure which apps exist? Call list_apps.").WithNext("list_apps")
				}
				page, err := svc.ListDeployments(ctx, a, in.App, 1, "")
				if err != nil {
					return nil, err
				}
				if len(page.Items) == 0 {
					return nil, apperr.NotFound("deployment_not_found", "App "+in.App+" has no deployments yet.",
						"Send the code first with deploy_files, create_upload or set_git_source.").WithNext("deploy_files")
				}
				id = page.Items[0].ID
			}
			d, err := svc.GetDeployment(ctx, a, id, time.Duration(wait)*time.Second)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Deployment", d), nil
		})

	addTool(s, "list_deployments", "List deployments",
		"Deployment history of one app, newest first: id, state, error code, source, time. Use it to see which deploy failed, or to pick one to roll back to (state running or superseded; only the newest 3 still have an image).",
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
			next := []string{"Call get_deployment with an id for the error and log tail of one deployment."}
			if page.NextCursor != "" {
				next = append(next, "More exist: call list_deployments again with cursor="+page.NextCursor+".")
			}
			if len(page.Items) == 0 {
				next = []string{"Nothing was deployed yet. Send the code with deploy_files, create_upload or set_git_source."}
			}
			return &result{
				Summary: fmt.Sprintf("%d deployment(s) of %s.", len(page.Items), in.App),
				Data:    toMap(page),
				Next:    next,
			}, nil
		})

	addTool(s, "get_logs", "Get logs",
		"Log lines of an app (at most 1,000, default last 200). kind=build is the log of the latest build: use it when a deploy failed before the app started. kind=runtime is what the running app prints to stdout/stderr: use it when the app crashes, errors or is unhealthy. Runtime logs exist only once a container has been started.",
		readOnly(), false,
		enums(map[string][]string{"kind": {"build", "runtime"}}),
		func(ctx context.Context, a app.Actor, in getLogsIn) (*result, error) {
			opts := app.LogOptions{Kind: in.Kind, Tail: in.Tail}
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
				next = append(next, "Look for the error (stack trace, missing environment variable, EADDRINUSE, listening on localhost). A missing variable: set_env then restart_app. A code bug: fix it and deploy_files again with ALL files.")
			} else {
				next = append(next, "Find the first error line (the failing step). Fix the Dockerfile or the code, then deploy again (deploy_files with ALL files, or deploy for git/zip).")
			}
			if len(lines) == 0 {
				next = []string{"No lines yet. For a deploy that failed early call get_deployment; try the other kind (build or runtime)."}
			}
			return &result{
				Summary: fmt.Sprintf("%d %s log line(s) for %s.", len(lines), opts.Kind, in.App),
				Data:    data("app", in.App, "kind", opts.Kind, "lines", nonNil(lines)),
				Next:    next,
			}, nil
		})

	addTool(s, "set_env", "Set environment variables",
		"Adds, changes or removes environment variables (API keys, DISCORD_TOKEN, DATABASE_URL). Values are stored encrypted and never shown again; variables you do not mention stay. NOT applied to a running app until restart_app (or a deploy for an app that never ran or failed). Never put secrets in files or the Dockerfile.",
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
			var cur *app.App
			if ap, err := svc.GetApp(ctx, a, in.App); err == nil {
				cur = &ap
			}
			return &result{
				Summary: fmt.Sprintf("%s now has %d variable(s): %s.", in.App, len(vars), strings.Join(names, ", ")),
				Data:    data("env", nonNil(vars)),
				Next:    setEnvNext(in.App, cur),
			}, nil
		})

	lifecycle := func(name, title, desc, verb string, ann *mcp.ToolAnnotations, call func(ctx context.Context, a app.Actor, ref string) (app.App, error)) {
		addTool(s, name, title, desc, ann, true, nil,
			func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
				ap, err := call(ctx, a, in.App)
				if err != nil {
					return nil, err
				}
				next := []string{"Call get_app in a few seconds: status should be running. If it says crashed, call get_logs with kind=runtime."}
				if name == "stop_app" {
					next = []string{"Tell the user the app is stopped and its address shows 'This app is stopped'. start_app brings it back."}
				}
				return &result{
					Summary: fmt.Sprintf("%s %s: status %s.", verb, ap.Slug, ap.Status),
					Data:    data("app", ap),
					Next:    next,
				}, nil
			})
	}
	lifecycle("restart_app", "Restart an app",
		"Recreates the container of a deployed app from its existing image (no rebuild, a few seconds of downtime) so it picks up new env vars (set_env), volume, memory and CPU settings (update_app). Fails with app_not_deployed if no deploy ever succeeded: call deploy instead.",
		"Restarted", write(), svc.RestartApp)
	lifecycle("start_app", "Start an app",
		"Starts an app that was stopped (stop_app). Only if the user wants it running again.",
		"Started", write(), svc.StartApp)
	lifecycle("stop_app", "Stop an app",
		"Stops an app on purpose: its address then shows 'This app is stopped'; data and settings stay. Only stop an app the user named.",
		"Stopped", destructive(), svc.StopApp)

	addTool(s, "rollback", "Roll back",
		"Runs an earlier deployment's image again, with no rebuild: the way back to the last working version. Without deployment_id it picks the newest older deployment that ran. Only when the user asks (or a new version broke a live app and they want the old one). Returns a deployment: poll it with get_deployment.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in rollbackIn) (*result, error) {
			target := strings.TrimSpace(in.DeploymentID)
			if target == "" {
				page, err := svc.ListDeployments(ctx, a, in.App, 30, "")
				if err != nil {
					return nil, err
				}
				target = rollbackTarget(page.Items)
				if target == "" {
					return nil, apperr.Conflict("rollback_unavailable", "There is no older deployment of "+in.App+" that ran, so there is nothing to roll back to.",
						"Call list_deployments to see the history. If every earlier deploy failed, fix the code and deploy again.").WithNext("list_deployments")
				}
			}
			d, err := svc.Rollback(ctx, a, in.App, target)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued rollback", d), nil
		})

	addTool(s, "delete_app", "Delete an app",
		"Permanently deletes an app: containers, images, address and its /data volume. Cannot be undone. Needs confirm_slug equal to the app's slug. Ask the user first, and only delete an app they named.",
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
				Next:    []string{"Tell the user the app and its data are gone. If the project has a .bitploy file for this app, delete that file too."},
			}, nil
		})

	addTool(s, "add_domain", "Add a custom domain",
		"Attaches your own domain (www.example.com) to a web app and returns the DNS records the USER must create at their domain provider. Give the user the tell_the_user text from the result, then call check_domain after they say it is done.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			d, err := svc.AddDomain(ctx, a, in.App, in.Hostname)
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Domain %s added, status %s.", d.Hostname, d.Status),
				Data:    data("domain", d, "tell_the_user", dnsMessage(d)),
				Next: []string{
					"Tell the user, in plain words, what to do: use the tell_the_user text. It takes 5 to 30 minutes (rarely longer) before DNS works.",
					"When the user says the records are in, call check_domain with the same app and hostname. Repeat until status is active.",
				},
			}, nil
		})

	addTool(s, "check_domain", "Check a custom domain",
		"Looks up the DNS records of a custom domain and moves it toward active (pending_dns, issuing_cert, active, failed). Call it after the user created the records, and again every minute or so while issuing_cert. Returns what is still wrong.",
		openWorld(write()), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			d, err := svc.CheckDomain(ctx, a, in.App, in.Hostname)
			if err != nil {
				return nil, err
			}
			var next []string
			switch d.Status {
			case "active":
				next = []string{"The domain works. Tell the user it is live at https://" + d.Hostname + "."}
			case "issuing_cert":
				next = []string{"The DNS is right and the secure certificate is being issued (a minute or two). Call check_domain again in a minute."}
			case "failed":
				next = []string{"Bitploy stopped waiting. Re-check the records with the user (tell_the_user text from add_domain, or the records in this result), fix them, then call check_domain again."}
			default:
				next = []string{"The records are not visible yet. Ask the user to check they match exactly, wait a few minutes (DNS can take up to an hour), then call check_domain again."}
			}
			if d.Status != "active" {
				next = append(next, "Records needed:")
				next = append(next, domainRecordLines(d)...)
			}
			summary := fmt.Sprintf("Domain %s: %s.", d.Hostname, d.Status)
			if d.LastError != "" {
				summary += " " + oneLine(d.LastError)
			}
			return &result{Summary: summary, Data: data("domain", d, "tell_the_user", dnsMessage(d)), Next: next}, nil
		})

	addTool(s, "remove_domain", "Remove a custom domain",
		"Removes a custom domain from an app. The app stays reachable at its bitploy address. Only when the user asks.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in domainIn) (*result, error) {
			if err := svc.RemoveDomain(ctx, a, in.App, in.Hostname); err != nil {
				return nil, err
			}
			return &result{
				Summary: "Removed domain " + in.Hostname + ".",
				Data:    data("removed", in.Hostname),
				Next:    []string{"The app is still reachable at its bitploy address. The user can delete the DNS records at their domain provider."},
			}, nil
		})

	addTool(s, "get_metrics", "Get metrics",
		"CPU %, memory (MB, against the memory limit) and network of one app over a window (1h, 24h or 7d), plus peaks. Use it for \"how much does it use\", slow apps and suspected out-of-memory. Account totals (apps, memory reserved, deploys, disk) come from whoami.",
		readOnly(), false,
		enums(map[string][]string{"window": {"1h", "24h", "7d"}}),
		func(ctx context.Context, a app.Actor, in metricsIn) (*result, error) {
			w := in.Window
			if w == "" {
				w = "24h"
			}
			m, err := svc.GetMetrics(ctx, a, in.App, w)
			if err != nil {
				return nil, err
			}
			return metricsResult(in.App, w, m), nil
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

// rollbackTarget picks the newest deployment that ran a version older than
// the live one (items are newest first). Versions are told apart by image: a
// rollback runs its target's image, so after rolling back from #5 to #4 the
// next rollback goes to #3, not back to #5.
func rollbackTarget(items []app.Deployment) string {
	built := map[string]int64{} // image -> seq of the deployment that built it
	for _, it := range items {
		if it.Image != nil && it.SourceKind != "rollback" {
			built[*it.Image] = it.Seq
		}
	}
	version := func(it app.Deployment) int64 {
		if it.Image != nil {
			if seq, ok := built[*it.Image]; ok {
				return seq
			}
		}
		return it.Seq
	}
	var live *app.Deployment
	for i := range items {
		if items[i].State == "running" {
			live = &items[i]
			break
		}
	}
	for _, it := range items {
		if it.State != "superseded" {
			continue
		}
		if live != nil && (it.Image != nil && live.Image != nil && *it.Image == *live.Image || version(it) >= version(*live)) {
			continue
		}
		return it.ID
	}
	return ""
}
