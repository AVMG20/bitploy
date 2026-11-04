package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/templates"
)

type AppRef struct {
	App string `path:"app" doc:"App slug or app ID (app_...)"`
}

type validateRequest struct {
	Dockerfile string   `json:"dockerfile" doc:"Full Dockerfile text"`
	FileList   []string `json:"file_list,omitempty" doc:"Paths of the project files, to improve the checks (.dockerignore, .env, node_modules...)"`
	Port       int      `json:"port,omitempty" doc:"The app's port setting, if you set one"`
}

type filesRequest struct {
	Files []app.FileInput `json:"files" doc:"Inline files: at most 2 MB in total and 200 files"`
}

type deployRequest struct {
	UploadID string `json:"upload_id,omitempty" doc:"Upload from create_upload. Leave empty to deploy the app's git source."`
}

type rollbackRequest struct {
	DeploymentID string `json:"deployment_id" doc:"The earlier deployment (dep_...) whose image to run"`
}

type envRequest struct {
	Set   map[string]string `json:"set,omitempty" doc:"Variables to set. Values are encrypted and never returned."`
	Unset []string          `json:"unset,omitempty" doc:"Variable names to remove"`
	// Service limits the change to one service of an app with several services.
	Service string `json:"service,omitempty" doc:"For an app with several services: set or unset the variables of this service only (they win over shared ones of the same name). Leave it out for variables every service gets."`
}

type domainRequest struct {
	Hostname string `json:"hostname" doc:"Custom domain, e.g. www.example.com"`
}

func (a *API) appRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listTemplates", Method: http.MethodGet, Path: "/templates", Tags: []string{"Templates"},
		Summary: "List Dockerfile templates",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.Template]], error) {
		items, err := a.svc.ListTemplates(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getTemplate", Method: http.MethodGet, Path: "/templates/{stack}", Tags: []string{"Templates"},
		Summary: "Get a Dockerfile template",
		Description: "Stacks: " + strings.Join(templates.Stacks, ", ") + ". " +
			"Pass the project's file paths in `file` (repeat it) so the Dockerfile matches the package manager (npm, pnpm, yarn, bun; pip, uv, Poetry).",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Stack string   `path:"stack"`
		Files []string `query:"file,explode" doc:"A project file path, relative with forward slashes; repeat for each file"`
	}) (*Out[app.Template], error) {
		return okOrNil(a.svc.GetTemplateForFiles(ctx, act, in.Stack, in.Files))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "validateDockerfile", Method: http.MethodPost, Path: "/dockerfile/validate", Tags: []string{"Templates"},
		Summary: "Validate a Dockerfile", Description: "Static checks, no build. Read scope is enough.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body validateRequest }) (*Out[app.ValidateResult], error) {
		return okOrNil(a.svc.ValidateDockerfile(ctx, act, in.Body.Dockerfile, in.Body.FileList, in.Body.Port))
	})

	// ---- apps ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "listApps", Method: http.MethodGet, Path: "/apps", Tags: []string{"Apps"},
		Summary: "List your apps",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.App]], error) {
		items, err := a.svc.ListApps(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "createApp", Method: http.MethodPost, Path: "/apps", Tags: []string{"Apps"},
		Summary: "Create an app", Description: "Reserves a subdomain and returns the URL. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.CreateAppInput }) (*Out[app.App], error) {
		created, err := a.svc.CreateApp(ctx, act, in.Body)
		if err == nil {
			a.svc.PublishAppChange(ctx, act, created.Slug, "", "")
		}
		return okOrNil(created, err)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getApp", Method: http.MethodGet, Path: "/apps/{app}", Tags: []string{"Apps"},
		Summary: "Get an app",
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.App], error) {
		return okOrNil(a.svc.GetApp(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateApp", Method: http.MethodPatch, Path: "/apps/{app}", Tags: []string{"Apps"},
		Summary: "Update app settings", Description: "Port, health path, volume, resources, name, slug. Fields left out stay unchanged. volume=false deletes /data and all backups: it needs delete_data=true when there is anything to delete (else 409 volume_has_data). A static site has a fixed size and no volume.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.UpdateAppInput
	}) (*Out[app.App], error) {
		updated, err := a.svc.UpdateApp(ctx, act, in.App, in.Body)
		if err == nil && updated.Slug != "" && !strings.EqualFold(updated.Slug, in.App) && !strings.HasPrefix(in.App, "app_") {
			a.svc.PublishAppChange(ctx, act, updated.Slug, strings.ToLower(in.App), "") // renamed: the old slug is in old_app
		}
		return okOrNil(updated, err)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deleteApp", Method: http.MethodDelete, Path: "/apps/{app}", Tags: []string{"Apps"},
		Summary: "Delete an app", Description: "Removes containers, images, routes and the volume. confirm_slug must equal the app's slug.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		ConfirmSlug string `query:"confirm_slug" doc:"The app's slug, to confirm (or send {\"confirm_slug\": ...} as the body)"`
		Body        *struct {
			ConfirmSlug string `json:"confirm_slug,omitempty"`
		}
	}) (*Empty, error) {
		confirm := in.ConfirmSlug
		if confirm == "" && in.Body != nil {
			confirm = in.Body.ConfirmSlug
		}
		if err := a.svc.DeleteApp(ctx, act, in.App, confirm); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- sources ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "deployFiles", Method: http.MethodPost, Path: "/apps/{app}/files", Tags: []string{"Deployments"},
		Summary: "Deploy inline files", Description: "Validates, then queues a deployment. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted, MaxBodyBytes: 4 << 20, BodyReadTimeout: 30 * time.Second,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body filesRequest
	}) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.DeployFiles(ctx, act, in.App, in.Body.Files))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "createUpload", Method: http.MethodPost, Path: "/apps/{app}/uploads", Tags: []string{"Deployments"},
		Summary: "Create a one-time upload URL", Description: "POST the raw zip to the returned URL (95 MB max, 15 minutes). Accepts an Idempotency-Key header. " +
			"With a session or token you can skip this and the deploy call: POST the raw zip to /apps/{app}/upload, which stores it and queues the deployment (202, a deployment).",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.Upload], error) {
		return okOrNil(a.svc.CreateUpload(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "setGitSource", Method: http.MethodPut, Path: "/apps/{app}/git-source", Tags: []string{"Deployments"},
		Summary: "Set the git source", Description: "Saves repo, branch and subdirectory and checks access with ls-remote.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.GitSource
	}) (*Out[app.App], error) {
		return okOrNil(a.svc.SetGitSource(ctx, act, in.App, in.Body))
	})

	// ---- deployments ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "listDeployments", Method: http.MethodGet, Path: "/apps/{app}/deployments", Tags: []string{"Deployments"},
		Summary: "Deployment history, newest first",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		PageParams
	}) (*Out[app.Page[app.Deployment]], error) {
		return okOrNil(a.svc.ListDeployments(ctx, act, in.App, pageLimit(in.Limit), in.Cursor))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deploy", Method: http.MethodPost, Path: "/apps/{app}/deployments", Tags: []string{"Deployments"},
		Summary: "Queue a deployment", Description: "From an upload (upload_id) or the app's git source. Returns at once; poll GET /deployments/{id}?wait=30. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body *deployRequest
	}) (*Out[app.Deployment], error) {
		uploadID := ""
		if in.Body != nil {
			uploadID = in.Body.UploadID
		}
		return okOrNil(a.svc.Deploy(ctx, act, in.App, uploadID))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "redeploy", Method: http.MethodPost, Path: "/apps/{app}/redeploy", Tags: []string{"Deployments"},
		Summary: "Rebuild the current source", Description: "Git: the latest commit. Files and uploads: the stored snapshot if it is still there. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.Redeploy(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "cancelDeployment", Method: http.MethodPost, Path: "/deployments/{id}/cancel", Tags: []string{"Deployments"},
		Summary: "Cancel a deployment",
		Description: "Needs the deploy scope and your own app. A queued deployment is cancelled at once. One that is fetching, validating, building, starting or in its health check is stopped (the build process ends, the candidate container is removed) and the call waits up to 5 seconds, so the answer usually already says `cancelled`; otherwise it shows the state as it is and you poll GET /deployments/{id}. " +
			"A deployment that is swapping is already going live: 409 too_late_to_cancel. A finished one: 409 deployment_finished. The version that is live keeps running. Returns the deployment.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id"`
	}) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.CancelDeployment(ctx, act, in.ID))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getDeployment", Method: http.MethodGet, Path: "/deployments/{id}", Tags: []string{"Deployments"},
		Summary: "Get a deployment", Description: "With ?wait=30 (max 50 seconds) it waits until the deployment reaches a final state.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID   string `path:"id"`
		Wait int    `query:"wait" minimum:"0" doc:"Long-poll up to this many seconds (capped at 50)"`
	}) (*Out[app.Deployment], error) {
		wait := in.Wait
		if wait > 50 {
			wait = 50
		}
		return okOrNil(a.svc.GetDeployment(ctx, act, in.ID, time.Duration(wait)*time.Second))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "rollback", Method: http.MethodPost, Path: "/apps/{app}/rollback", Tags: []string{"Lifecycle"},
		Summary: "Roll back to a previous deployment", Description: "Reruns that deployment's image. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body rollbackRequest
	}) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.Rollback(ctx, act, in.App, in.Body.DeploymentID))
	})

	// ---- logs ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "getBuildLog", Method: http.MethodGet, Path: "/deployments/{id}/log", Tags: []string{"Logs"},
		Summary: "Build log", Description: "Plain text by default, or JSON {\"items\": [LogLine]} when the request has `Accept: application/json`. With ?follow=1 it becomes a Server-Sent Events stream (a `: ping` comment every 15 s, `event: end` when the deployment is final).",
		Responses: map[string]*huma.Response{"200": {Description: "Build log", Content: map[string]*huma.MediaType{
			"text/plain":        {Schema: &huma.Schema{Type: "string"}},
			"application/json":  {Schema: &huma.Schema{Type: "object"}},
			"text/event-stream": {Schema: &huma.Schema{Type: "string"}},
		}}},
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID     string `path:"id"`
		Tail   int    `query:"tail" minimum:"0" doc:"Last N lines (default and max 1000)"`
		Follow bool   `query:"follow" doc:"1 = stream as Server-Sent Events"`
		Accept string `header:"Accept"`
	}) (*struct {
		ContentType string `header:"Content-Type"`
		Body        []byte
	}, error) {
		lines, err := a.svc.BuildLog(ctx, act, in.ID, tailOrDefault(in.Tail, 1000))
		if err != nil {
			return nil, err
		}
		out := &struct {
			ContentType string `header:"Content-Type"`
			Body        []byte
		}{}
		if strings.Contains(in.Accept, "application/json") {
			if lines == nil {
				lines = []app.LogLine{}
			}
			out.ContentType = "application/json"
			out.Body, _ = json.Marshal(app.Page[app.LogLine]{Items: redactLines(lines)})
			return out, nil
		}
		out.ContentType = "text/plain; charset=utf-8"
		out.Body = []byte(joinLines(redactLines(lines)))
		return out, nil
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getLogs", Method: http.MethodGet, Path: "/apps/{app}/logs", Tags: []string{"Logs"},
		Summary: "Build or runtime logs", Description: "Max 1000 lines. With follow=1 it becomes a Server-Sent Events stream of runtime logs (comment heartbeat every 15 s).",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Kind    string `query:"kind" default:"runtime" enum:"build,runtime" doc:"build (latest deployment) or runtime"`
		Tail    int    `query:"tail" minimum:"0" doc:"Last N lines (default 200, max 1000)"`
		Since   string `query:"since" doc:"RFC 3339 time; runtime logs only"`
		Follow  bool   `query:"follow" doc:"1 = stream as Server-Sent Events"`
		Service string `query:"service" doc:"Runtime logs of one service of an app with several (see services in get app). Default: the public service, else the first."`
	}) (*Out[app.Page[app.LogLine]], error) {
		opts := app.LogOptions{Kind: in.Kind, Tail: tailOrDefault(in.Tail, 200), Service: in.Service}
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, apperr.BadRequest("invalid_since", "since is not an RFC 3339 time.", "Use a time like 2026-10-29T12:00:00Z.")
			}
			opts.Since = &t
		}
		lines, err := a.svc.Logs(ctx, act, in.App, opts)
		if err != nil {
			return nil, err
		}
		if lines == nil {
			lines = []app.LogLine{}
		}
		return &Out[app.Page[app.LogLine]]{Body: app.Page[app.LogLine]{Items: redactLines(lines)}}, nil
	})

	// ---- env ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "listEnv", Method: http.MethodGet, Path: "/apps/{app}/env", Tags: []string{"Environment"},
		Summary: "Environment variable names", Description: "Names and masked values only. Nobody can read values back.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		PageParams
	}) (*Out[app.Page[app.EnvVar]], error) {
		items, err := a.svc.ListEnv(ctx, act, in.App)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "setEnv", Method: http.MethodPut, Path: "/apps/{app}/env", Tags: []string{"Environment"},
		Summary: "Set and unset environment variables", Description: "They apply on the next deploy or restart.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body envRequest
	}) (*Out[app.Page[app.EnvVar]], error) {
		items, err := a.svc.SetServiceEnv(ctx, act, in.App, in.Body.Service, in.Body.Set, in.Body.Unset)
		return pageOut(items, err, PageParams{Limit: 100})
	})

	reg(a, anyAuth, huma.Operation{
		OperationID: "setPasswordProtection", Method: http.MethodPut, Path: "/apps/{app}/password-protection", Tags: []string{"Apps"},
		Summary: "Turn password protection on or off",
		Description: "A login prompt (username and password) in front of the app's web addresses. enabled=true needs a username and a password " +
			"the first time; send either one alone to change just that while it stays on. enabled=false removes the prompt and forgets the login. " +
			"The password is never returned. Takes effect within a second or two. Needs the deploy scope.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.ProtectionInput
	}) (*Out[app.App], error) {
		return okOrNil(a.svc.SetPasswordProtection(ctx, act, in.App, in.Body))
	})

	// ---- lifecycle ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "restartApp", Method: http.MethodPost, Path: "/apps/{app}/restart", Tags: []string{"Lifecycle"},
		Summary: "Restart the app", Description: "With service, only that service of a running app with several services restarts.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Service string `query:"service" doc:"Name of one service of an app with several (see services in get app). Leave it out for a single-service app."`
	}) (*Out[app.App], error) {
		return okOrNil(a.svc.RestartApp(ctx, act, in.App, in.Service))
	})
	for _, lc := range []struct {
		name, path, summary string
		fn                  func(context.Context, app.Actor, string) (app.App, error)
	}{
		{"startApp", "start", "Start a stopped app", a.svc.StartApp},
		{"stopApp", "stop", "Stop the app", a.svc.StopApp},
	} {
		fn := lc.fn
		reg(a, anyAuth, huma.Operation{
			OperationID: lc.name, Method: http.MethodPost, Path: "/apps/{app}/" + lc.path, Tags: []string{"Lifecycle"},
			Summary: lc.summary,
		}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.App], error) {
			return okOrNil(fn(ctx, act, in.App))
		})
	}

	// ---- domains ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "listDomains", Method: http.MethodGet, Path: "/apps/{app}/domains", Tags: []string{"Domains"},
		Summary: "List custom domains",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		PageParams
	}) (*Out[app.Page[app.Domain]], error) {
		items, err := a.svc.ListDomains(ctx, act, in.App)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "addDomain", Method: http.MethodPost, Path: "/apps/{app}/domains", Tags: []string{"Domains"},
		Summary: "Add a custom domain", Description: "Returns the DNS records to create. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body domainRequest
	}) (*Out[app.Domain], error) {
		return okOrNil(a.svc.AddDomain(ctx, act, in.App, in.Body.Hostname))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "checkDomain", Method: http.MethodPost, Path: "/apps/{app}/domains/{host}/check", Tags: []string{"Domains"},
		Summary: "Check a custom domain's DNS now",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Host string `path:"host"`
	}) (*Out[app.Domain], error) {
		return okOrNil(a.svc.CheckDomain(ctx, act, in.App, in.Host))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "removeDomain", Method: http.MethodDelete, Path: "/apps/{app}/domains/{host}", Tags: []string{"Domains"},
		Summary: "Remove a custom domain", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Host string `path:"host"`
	}) (*Empty, error) {
		if err := a.svc.RemoveDomain(ctx, act, in.App, in.Host); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- metrics ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "getMetrics", Method: http.MethodGet, Path: "/apps/{app}/metrics", Tags: []string{"Metrics"},
		Summary: "CPU, memory and network",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Window  string `query:"window" default:"24h" enum:"1h,24h,7d"`
		Service string `query:"service" doc:"Metrics of one service of an app with several (see services in get app). Default: the sum over all services."`
	}) (*Out[app.Metrics], error) {
		return okOrNil(a.svc.GetMetrics(ctx, act, in.App, in.Window, in.Service))
	})

	// ---- traffic ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "getTraffic", Method: http.MethodGet, Path: "/apps/{app}/traffic", Tags: []string{"Traffic"},
		Summary: "Visitors, top pages, countries, referrers and 404 paths",
		Description: "Lists hold the top 10. With list, only that list comes back, in full (up to 500 rows); " +
			"totals, points and the other lists are then empty.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Window string `query:"window" default:"24h" enum:"24h,7d,30d,today"`
		List   string `query:"list" doc:"pages, countries, referrers or not_found: return that list in full"`
	}) (*Out[app.Traffic], error) {
		if in.List != "" {
			return okOrNil(a.svc.GetTrafficList(ctx, act, in.App, in.Window, in.List))
		}
		return okOrNil(a.svc.GetTraffic(ctx, act, in.App, in.Window))
	})
}

func tailOrDefault(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > 1000 {
		return 1000
	}
	return n
}

func redactLines(lines []app.LogLine) []app.LogLine {
	out := make([]app.LogLine, len(lines))
	for i, l := range lines {
		l.Text = apperr.Redact(l.Text)
		out[i] = l
	}
	return out
}

func joinLines(lines []app.LogLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
