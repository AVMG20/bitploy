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
		OperationID: "adminServer", Method: http.MethodGet, Path: "/admin/server", Tags: []string{"Admin"},
		Summary: "Server CPU, RAM, disk, Docker disk use and build queue", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.ServerStats], error) {
		return okOrNil(a.svc.AdminServerStats(ctx, act))
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
