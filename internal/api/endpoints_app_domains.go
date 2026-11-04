package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

type userAppDomainsBody struct {
	DomainIDs []string `json:"domain_ids" doc:"IDs of the extra app domains this person may use (apd_...). Replaces the list; [] takes all away. The default domain is always allowed."`
}

// appDomainRoutes are the admin's app domains: extra wildcard domains for app
// addresses, and who may use them.
func (a *API) appDomainRoutes() {
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListAppDomains", Method: http.MethodGet, Path: "/admin/app-domains", Tags: []string{"Admin"},
		Summary: "List app domains", Description: "Admin only. The default domain first, then the extra ones, with the number of apps on each and the server IPs for DNS.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.AppDomainList], error) {
		return okOrNil(a.svc.AdminListAppDomains(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminAddAppDomain", Method: http.MethodPost, Path: "/admin/app-domains", Tags: []string{"Admin"},
		Summary: "Add an app domain", Description: "Admin only. Apps can then live at <name>.<domain>. The certificate and key are optional; the key is stored encrypted and never returned.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.AddAppDomainInput }) (*Out[app.AppDomain], error) {
		return okOrNil(a.svc.AdminAddAppDomain(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminSetAppDomainCert", Method: http.MethodPut, Path: "/admin/app-domains/{id}/certificate", Tags: []string{"Admin"},
		Summary: "Set the certificate of an app domain", Description: "Admin only. Adds or replaces the certificate and key.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID   string `path:"id"`
		Body app.AppDomainCertInput
	}) (*Out[app.AppDomain], error) {
		return okOrNil(a.svc.AdminSetAppDomainCert(ctx, act, in.ID, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminRemoveAppDomain", Method: http.MethodDelete, Path: "/admin/app-domains/{id}", Tags: []string{"Admin"},
		Summary: "Remove an app domain", Description: "Admin only. Refused (app_domain_in_use) while any app lives on it.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id"`
	}) (*Empty, error) {
		if err := a.svc.AdminRemoveAppDomain(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminSetUserAppDomains", Method: http.MethodPut, Path: "/admin/users/{id}/app-domains", Tags: []string{"Admin"},
		Summary: "Set which app domains a person may use", Description: "Admin only. Taking a domain away does not move apps that already live on it.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID   string `path:"id"`
		Body userAppDomainsBody
	}) (*Out[app.AdminUser], error) {
		return okOrNil(a.svc.AdminSetUserAppDomains(ctx, act, in.ID, in.Body.DomainIDs))
	})
}
