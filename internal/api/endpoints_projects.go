package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

type ProjectRef struct {
	ID string `path:"id" doc:"Project id (prj_...) or the project's name"`
}

// projectRoutes registers projects and service templates
// (docs/compose-plan.md, "Phase 4b").
func (a *API) projectRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listProjects", Method: http.MethodGet, Path: "/projects", Tags: []string{"Projects"},
		Summary:     "List your projects",
		Description: "In the order you put them in (PUT /project-order): projects you never placed first, newest first, then the rest. Each with its apps. Every app is in exactly one project; the apps of a project share a private network and can refer to each other's variables as ${{<alias>.VAR}}.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.Project]], error) {
		items, err := a.svc.ListProjects(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "createProject", Method: http.MethodPost, Path: "/projects", Tags: []string{"Projects"},
		Summary:       "Create a project",
		Description:   "Creates an empty project. It is kept while empty, until it is deleted. 400 project_name_invalid, 403 projects_limit (100).",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.CreateProjectInput }) (*Out[app.Project], error) {
		return okOrNil(a.svc.CreateProject(ctx, act, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "orderProjects", Method: http.MethodPut, Path: "/project-order", Tags: []string{"Projects"},
		Summary:       "Put your projects in order",
		Description:   "Saves the order GET /projects lists your projects in, for you alone (each member of a shared project places it for themselves). A project made or shared with you later comes first.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.OrderProjectsInput }) (*Empty, error) {
		if err := a.svc.OrderProjects(ctx, act, in.Body); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getProject", Method: http.MethodGet, Path: "/projects/{id}", Tags: []string{"Projects"},
		Summary: "Get a project with its apps",
	}, func(ctx context.Context, act app.Actor, in *ProjectRef) (*Out[app.Project], error) {
		return okOrNil(a.svc.GetProject(ctx, act, in.ID))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateProject", Method: http.MethodPatch, Path: "/projects/{id}", Tags: []string{"Projects"},
		Summary:     "Rename a project",
		Description: "A renamed project stays when its last app is deleted.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		Body app.UpdateProjectInput
	}) (*Out[app.Project], error) {
		return okOrNil(a.svc.UpdateProject(ctx, act, in.ID, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deleteProject", Method: http.MethodDelete, Path: "/projects/{id}", Tags: []string{"Projects"},
		Summary: "Delete a project",
		Description: "confirm must equal the project's name. 409 project_not_empty while it has apps, unless with_apps=true, " +
			"which deletes every app of the project with its data and backups first.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		Confirm  string `query:"confirm" doc:"The project's name, to confirm"`
		WithApps bool   `query:"with_apps" doc:"Also delete the project's apps, with their data and backups"`
	}) (*Empty, error) {
		if err := a.svc.DeleteProject(ctx, act, in.ID, in.Confirm, in.WithApps); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	for _, pw := range []struct {
		name, path, summary, desc string
		fn                        func(context.Context, app.Actor, string) (app.ProjectPowerResult, error)
	}{
		{"startProject", "start", "Start every app of a project",
			"Starts the stopped apps, databases and services first. Apps that never ran are skipped. An app that cannot start (quota, busy) is listed in failed; the others still start.",
			a.svc.StartProject},
		{"stopProject", "stop", "Stop every app of a project",
			"Stops the running apps, then the databases and services they use. An app that cannot stop is listed in failed; the others still stop.",
			a.svc.StopProject},
	} {
		fn := pw.fn
		reg(a, anyAuth, huma.Operation{
			OperationID: pw.name, Method: http.MethodPost, Path: "/projects/{id}/" + pw.path, Tags: []string{"Projects"},
			Summary: pw.summary, Description: pw.desc,
		}, func(ctx context.Context, act app.Actor, in *ProjectRef) (*Out[app.ProjectPowerResult], error) {
			return okOrNil(fn(ctx, act, in.ID))
		})
	}
	reg(a, anyAuth, huma.Operation{
		OperationID: "listServiceTemplates", Method: http.MethodGet, Path: "/service-templates", Tags: []string{"Projects"},
		Summary:     "List the service templates",
		Description: "Databases, caches and search engines that POST /projects/{id}/services adds to a project, each with a pinned image version and the variables it publishes (connection_vars). Read scope is enough.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.ServiceTemplate]], error) {
		return pageOut(a.svc.ListServiceTemplates(ctx, act), nil, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "addProjectService", Method: http.MethodPost, Path: "/projects/{id}/services", Tags: []string{"Projects"},
		Summary: "Add a database or other service to a project",
		Description: "Creates a private app (no public address, data in /data) from a service template, generates its passwords, deploys it and returns the app. " +
			"Each app in connect_to gets <main_var>=${{<name>.<main_var>}} (like DATABASE_URL) and restarts once the service runs. " +
			"If anything fails before the deployment is queued, the app is removed again. 404 service_template_unknown, 409 alias_taken or connect_to_var_set. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		Body app.AddServiceInput
	}) (*Out[app.App], error) {
		res, err := a.svc.AddService(ctx, act, in.ID, in.Body)
		if err == nil {
			a.svc.PublishAppChange(ctx, act, res.Slug, "", "")
			for _, slug := range in.Body.ConnectTo {
				a.svc.PublishAppChange(ctx, act, slug, "", "")
			}
		}
		return okOrNil(res, err)
	})
}
