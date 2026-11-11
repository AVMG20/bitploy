package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
)

type adminUserPatch struct {
	Status *string         `json:"status,omitempty" enum:"active,suspended" doc:"suspended stops all of the user's apps"`
	Quota  *app.QuotaInput `json:"quota,omitempty" doc:"Replaces all quota values; overallocate left out stays as it is"`
}

func (a *API) adminRoutes() {
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListUsers", Method: http.MethodGet, Path: "/admin/users", Tags: []string{"Admin"},
		Summary: "List users with usage and quotas", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.AdminUser]], error) {
		items, err := a.svc.AdminListUsers(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminUpdateUser", Method: http.MethodPatch, Path: "/admin/users/{id}", Tags: []string{"Admin"},
		Summary: "Suspend, reactivate or change the quota of a user", Description: "Admin only. Send status and/or quota.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID   string `path:"id"`
		Body adminUserPatch
	}) (*Out[app.AdminUser], error) {
		if in.Body.Status == nil && in.Body.Quota == nil {
			return nil, apperr.BadRequest("nothing_to_change", "Send a status and/or a quota.", "Example: {\"status\": \"suspended\"}.")
		}
		if in.Body.Status != nil {
			if err := a.svc.AdminSetUserStatus(ctx, act, in.ID, *in.Body.Status); err != nil {
				return nil, err
			}
		}
		if in.Body.Quota != nil {
			return okOrNil(a.svc.AdminSetQuota(ctx, act, in.ID, *in.Body.Quota))
		}
		users, err := a.svc.AdminListUsers(ctx, act)
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if u.ID == in.ID {
				return &Out[app.AdminUser]{Body: u}, nil
			}
		}
		return nil, apperr.NotFound("user_not_found", "There is no user with that ID.", "List users with GET /api/v1/admin/users.")
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminSetQuota", Method: http.MethodPut, Path: "/admin/quotas/{user_id}", Tags: []string{"Admin"},
		Summary: "Set a user's quota", Description: "Admin only. Replaces all quota values; overallocate left out stays as it is.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		UserID string `path:"user_id"`
		Body   app.QuotaInput
	}) (*Out[app.AdminUser], error) {
		return okOrNil(a.svc.AdminSetQuota(ctx, act, in.UserID, in.Body))
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListInvites", Method: http.MethodGet, Path: "/admin/invites", Tags: []string{"Admin"},
		Summary: "List invites", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.Invite]], error) {
		items, err := a.svc.ListInvites(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminCreateInvite", Method: http.MethodPost, Path: "/admin/invites", Tags: []string{"Admin"},
		Summary: "Create a single-use invite link", Description: "Admin only. Valid 7 days. The url is returned once. Accepts an Idempotency-Key header.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.CreateInviteInput }) (*Out[app.Invite], error) {
		return okOrNil(a.svc.CreateInvite(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminRevokeInvite", Method: http.MethodDelete, Path: "/admin/invites/{id}", Tags: []string{"Admin"},
		Summary: "Revoke an unused invite", Description: "Admin only.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id"`
	}) (*Empty, error) {
		if err := a.svc.RevokeInvite(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListProjectInvites", Method: http.MethodGet, Path: "/admin/project-invites", Tags: []string{"Admin"},
		Summary: "List the open project invites of every user", Description: "Admin only. Unused and not expired, newest first.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.AdminProjectInvite]], error) {
		items, err := a.svc.AdminListProjectInvites(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminRevokeProjectInvite", Method: http.MethodDelete, Path: "/admin/project-invites/{id}", Tags: []string{"Admin"},
		Summary: "Revoke anyone's open project invite", Description: "Admin only. 404 invite_not_found.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id" doc:"The invite's id (pin_...)"`
	}) (*Empty, error) {
		if err := a.svc.AdminRevokeProjectInvite(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminGetSettings", Method: http.MethodGet, Path: "/admin/settings", Tags: []string{"Admin"},
		Summary: "Who can make an account, and the limits new accounts get", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.ServerSettings], error) {
		return okOrNil(a.svc.GetServerSettings(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminUpdateSettings", Method: http.MethodPatch, Path: "/admin/settings", Tags: []string{"Admin"},
		Summary: "Change who can make an account, or the limits new accounts get",
		Description: "Admin only. What is left out stays. The limits apply to accounts made from now on; existing accounts keep theirs. " +
			"400 invalid_signups, invalid_quota (also when a limit is more than the server has).",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.UpdateServerSettingsInput }) (*Out[app.ServerSettings], error) {
		return okOrNil(a.svc.UpdateServerSettings(ctx, act, in.Body))
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminServer", Method: http.MethodGet, Path: "/admin/server", Tags: []string{"Admin"},
		Summary: "Server CPU, RAM, disk, Docker disk use and build queue", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.ServerStats], error) {
		return okOrNil(a.svc.AdminServerStats(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminServerMetrics", Method: http.MethodGet, Path: "/admin/server/metrics", Tags: []string{"Admin"},
		Summary: "Server CPU and memory over time",
		Description: "Admin only. cpu is % of the whole server (0-100); memory_mb is used memory without reclaimable cache. " +
			"1h is 15-second samples, 24h and 7d are 5-minute averages with cpu_max and memory_max_mb.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Window string `query:"window" default:"24h" enum:"1h,24h,7d"`
	}) (*Out[app.ServerMetrics], error) {
		return okOrNil(a.svc.AdminServerMetrics(ctx, act, in.Window))
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminDisk", Method: http.MethodGet, Path: "/admin/disk", Tags: []string{"Admin"},
		Summary: "Where the server's disk space goes, and what each cleanup would free",
		Description: "Admin only. Measured at most every two minutes (measuring sizes every volume); refresh=true measures now. " +
			"auto is the automatic cleanup's settings and its last runs.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Refresh bool `query:"refresh" doc:"Measure now instead of using a measurement of the last two minutes"`
	}) (*Out[app.DiskReport], error) {
		return okOrNil(a.svc.AdminDiskReport(ctx, act, in.Refresh))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminDiskCleanup", Method: http.MethodPost, Path: "/admin/disk/cleanup", Tags: []string{"Admin"},
		Summary: "Run one disk cleanup now",
		Description: "Admin only. Nothing a container uses, no app's live version and nothing of an app that is deploying is removed. " +
			"orphan_volumes deletes only the named volumes, and only while they are bitploy volumes of deleted apps. " +
			"409 disk_cleanup_running; 503 disk_cleanup_failed.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.DiskCleanupInput }) (*Out[app.DiskCleanupResult], error) {
		return okOrNil(a.svc.AdminDiskCleanup(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminUpdateDiskAuto", Method: http.MethodPatch, Path: "/admin/disk/auto", Tags: []string{"Admin"},
		Summary: "Turn the automatic disk cleanup on or off, or change its threshold",
		Description: "Admin only. Below min_free_percent free, bitploy deletes build cache, unused images, leftover containers, " +
			"older app versions and unused images that are not its own, in that order, until free space is back above it. It never deletes volumes.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.UpdateDiskCleanupInput }) (*Out[app.DiskAutoCleanup], error) {
		return okOrNil(a.svc.AdminUpdateDiskCleanup(ctx, act, in.Body))
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListApps", Method: http.MethodGet, Path: "/admin/apps", Tags: []string{"Admin"},
		Summary: "List every app on the server", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.App]], error) {
		if err := app.RequireAdmin(act); err != nil {
			return nil, err
		}
		items, err := a.svc.ListAllApps(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminStopApp", Method: http.MethodPost, Path: "/admin/apps/{app}/stop", Tags: []string{"Admin"},
		Summary: "Stop any app", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.App], error) {
		if err := app.RequireAdmin(act); err != nil {
			return nil, err
		}
		return okOrNil(a.svc.StopApp(ctx, act, in.App))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListPublicPorts", Method: http.MethodGet, Path: "/admin/public-ports", Tags: []string{"Admin"},
		Summary: "Public port range and the open ports", Description: "Admin only. enabled is false when BITPLOY_PUBLIC_PORTS is not set.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.PublicPortsOverview], error) {
		return okOrNil(a.svc.ListPublicPorts(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminClosePublicPort", Method: http.MethodDelete, Path: "/admin/public-ports/{app}", Tags: []string{"Admin"},
		Summary: "Close the public port of any app", Description: "Admin only.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Empty, error) {
		if err := a.svc.AdminClosePublicPort(ctx, act, in.App); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminAudit", Method: http.MethodGet, Path: "/admin/audit", Tags: []string{"Admin"},
		Summary: "Audit log, newest first", Description: "Admin only. Kept 90 days.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.AuditEntry]], error) {
		var before int64
		if in.Cursor != "" {
			n, err := strconv.ParseInt(strings.TrimPrefix(in.Cursor, "a:"), 10, 64)
			if err != nil || !strings.HasPrefix(in.Cursor, "a:") || n <= 0 {
				return nil, apperr.BadRequest("invalid_cursor", "The cursor is not valid.", "Use the next_cursor value from the previous page, unchanged.")
			}
			before = n
		}
		limit := pageLimit(in.Limit)
		items, more, err := a.svc.AdminListAuditPage(ctx, act, limit, before)
		if err != nil {
			return nil, err
		}
		pg := app.Page[app.AuditEntry]{Items: items}
		if more && len(items) > 0 {
			pg.NextCursor = "a:" + strconv.FormatInt(items[len(items)-1].ID, 10)
		}
		return &Out[app.Page[app.AuditEntry]]{Body: pg}, nil
	})
}
