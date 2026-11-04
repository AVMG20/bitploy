package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestDomainArgument(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := os.MkdirAll(e.svc.Routes.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	admin := app.Actor{UserID: "usr_admin", Role: "admin", Channel: app.ChannelDashboard, Scope: app.ScopeSession}
	d, err := e.svc.AdminAddAppDomain(ctx, admin, app.AddAppDomainInput{Domain: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	ds := e.connect(t, e.deployToken)

	// Not allowed yet.
	res := call(t, ds, "create_app", map[string]any{"name": "Shop", "slug": "shop", "domain": "example.com"})
	if !res.IsError || structured(t, res)["code"] != "domain_not_allowed" {
		t.Fatalf("create_app without access: %s", text(res))
	}
	res = call(t, ds, "deploy_catalog_app", map[string]any{"id": "memos", "domain": "example.com"})
	if !res.IsError || structured(t, res)["code"] != "domain_not_allowed" {
		t.Fatalf("deploy_catalog_app without access: %s", text(res))
	}

	users, err := e.svc.Q.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatal(users, err)
	}
	if _, err := e.svc.AdminSetUserAppDomains(ctx, admin, users[0].ID, []string{d.ID}); err != nil {
		t.Fatal(err)
	}

	res = call(t, ds, "whoami", nil)
	if res.IsError || !strings.Contains(text(res), "example.com") {
		t.Fatalf("whoami: %s", text(res))
	}
	doms, _ := structured(t, res)["app_domains"].([]any)
	if len(doms) != 2 || doms[1].(map[string]any)["domain"] != "example.com" {
		t.Fatalf("whoami app_domains: %v", doms)
	}

	res = call(t, ds, "create_app", map[string]any{"name": "Shop", "slug": "shop", "domain": "example.com"})
	if res.IsError {
		t.Fatal(text(res))
	}
	ap := structured(t, res)["app"].(map[string]any)
	if ap["url"] != "https://shop.example.com" || !strings.Contains(text(res), "https://shop.example.com") {
		t.Fatalf("create_app: %s", text(res))
	}

	res = call(t, ds, "update_app", map[string]any{"app": "shop", "domain": "bitploy.test"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if ap := structured(t, res)["app"].(map[string]any); ap["url"] != "https://shop.bitploy.test" {
		t.Fatalf("update_app: %v", ap)
	}
}
