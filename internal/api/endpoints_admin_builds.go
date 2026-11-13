package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

// adminBuildRoutes: the build history for admins (builds per person, and the
// builds of one person). The builds on the server charts come with
// GET /admin/server/metrics.
func (a *API) adminBuildRoutes() {
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminBuildStats", Method: http.MethodGet, Path: "/admin/builds/stats", Tags: []string{"Admin"},
		Summary: "Builds per person",
		Description: "Admin only. The builds that ended in the window, counted for who started them (the app's owner for a git push), " +
			"most build time first. Build time runs from taking a build slot until the image is built (or the deployment ended). " +
			"peak_cpu and peak_memory_mb are the server's highest during their builds. Builds are kept 90 days.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Window string `query:"window" default:"24h" enum:"24h,7d,30d"`
	}) (*Out[app.BuildStats], error) {
		return okOrNil(a.svc.AdminBuildStats(ctx, act, in.Window))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListBuilds", Method: http.MethodGet, Path: "/admin/builds", Tags: []string{"Admin"},
		Summary:     "Recent builds, of everyone or one person",
		Description: "Admin only. The builds that ended in the window, newest first, at most 100: everyone's, or with user only that person's (started by them, or a git push to their app).",
	}, func(ctx context.Context, act app.Actor, in *struct {
		User   string `query:"user" doc:"The person's user id (usr_...); leave out for everyone's builds"`
		Window string `query:"window" default:"30d" enum:"24h,7d,30d"`
		PageParams
	}) (*Out[app.Page[app.BuildRecord]], error) {
		items, err := a.svc.AdminListBuilds(ctx, act, in.User, in.Window)
		return pageOut(items, err, in.PageParams)
	})
}
