package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

type dockerfileRequest struct {
	Content string `json:"content" doc:"The whole Dockerfile, at most 64 KB"`
}

// catalogRoutes registers the one-click catalog and the Dockerfile editor.
func (a *API) catalogRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listCatalog", Method: http.MethodGet, Path: "/catalog", Tags: []string{"Catalog"},
		Summary:     "List the one-click apps",
		Description: "Popular self-contained tools that keep their data in /data, each with a pinned version. Deploy one with POST /catalog/{id}/deploy. Read scope is enough.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.CatalogApp]], error) {
		return pageOut(a.svc.ListCatalog(ctx, act), nil, in.PageParams)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getCatalogApp", Method: http.MethodGet, Path: "/catalog/{id}", Tags: []string{"Catalog"},
		Summary: "Get one one-click app",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id" doc:"Catalog id, for example memos"`
	}) (*Out[app.CatalogApp], error) {
		return okOrNil(a.svc.GetCatalogApp(ctx, act, in.ID))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deployCatalogApp", Method: http.MethodPost, Path: "/catalog/{id}/deploy", Tags: []string{"Catalog"},
		Summary: "Create an app from the catalog and deploy it",
		Description: "Creates the app (the usual quotas and slug rules apply), sets its environment, deploys the catalog Dockerfile and returns the app, the deployment and the first steps to take. " +
			"If anything fails, the app is removed again. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID   string `path:"id" doc:"Catalog id, for example memos"`
		Body app.CatalogDeployInput
	}) (*Out[app.CatalogDeployResult], error) {
		res, err := a.svc.DeployCatalogApp(ctx, act, in.ID, in.Body)
		if err == nil {
			a.svc.PublishAppChange(ctx, act, res.App.Slug, "", "")
		}
		return okOrNil(res, err)
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateCatalogApp", Method: http.MethodPost, Path: "/apps/{app}/catalog-update", Tags: []string{"Catalog"},
		Summary:       "Update an app to the catalog's current version",
		Description:   "Replaces the app's Dockerfile with the catalog's current one and queues a deployment. 409 catalog_up_to_date, not_catalog_app or git_source_linked.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.UpdateCatalogApp(ctx, act, in.App))
	})

	reg(a, anyAuth, huma.Operation{
		OperationID: "getDockerfile", Method: http.MethodGet, Path: "/apps/{app}/dockerfile", Tags: []string{"Deployments"},
		Summary:     "Read the app's Dockerfile",
		Description: "From the stored source of the current deployment (else the newest). `editable` is false, with a `reason`, for an app that builds from a repository, one that was never deployed from files, or one whose files have no Dockerfile at that path.",
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.DockerfileView], error) {
		return okOrNil(a.svc.GetAppDockerfile(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "putDockerfile", Method: http.MethodPut, Path: "/apps/{app}/dockerfile", Tags: []string{"Deployments"},
		Summary:       "Replace the app's Dockerfile and deploy",
		Description:   "Takes the current stored source, replaces (or adds) the Dockerfile, runs the same checks as deploy_files and queues a deployment. Max 64 KB. 409 git_source_linked or source_missing, 400 dockerfile_empty, 413 dockerfile_too_large.",
		DefaultStatus: http.StatusAccepted, MaxBodyBytes: 256 << 10, BodyReadTimeout: 30 * time.Second,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body dockerfileRequest
	}) (*Out[app.Deployment], error) {
		return okOrNil(a.svc.PutAppDockerfile(ctx, act, in.App, in.Body.Content))
	})
}
