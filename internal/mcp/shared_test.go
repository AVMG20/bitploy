package mcp

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// A project another user shared shows in list_projects and list_apps,
// marked shared with its owner, and its apps answer like the user's own; an
// app of that user outside the project stays hidden.
func TestSharedProjectsInLists(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	bob, err := e.svc.CreateUser(ctx, "bob@example.com", "Bob", "correct horse battery", false)
	if err != nil {
		t.Fatal(err)
	}
	bobS := app.Actor{UserID: bob.ID, Role: "user", Channel: app.ChannelDashboard, Scope: app.ScopeSession}
	shop, err := e.svc.CreateProject(ctx, bobS, app.CreateProjectInput{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateApp(ctx, bobS, app.CreateAppInput{Name: "Shop", Slug: "shop", Project: shop.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateApp(ctx, bobS, app.CreateAppInput{Name: "Private", Slug: "private"}); err != nil {
		t.Fatal(err)
	}
	ds := e.connect(t, e.deployToken)
	if res := call(t, ds, "get_app", map[string]any{"app": "shop"}); !res.IsError {
		t.Fatalf("ada sees bob's app before the invite: %s", text(res))
	}

	inv, err := e.svc.CreateProjectInvite(ctx, bobS, shop.ID, app.CreateProjectInviteInput{})
	if err != nil {
		t.Fatal(err)
	}
	ada, err := e.svc.AuthenticateToken(ctx, e.deployToken, "")
	if err != nil {
		t.Fatal(err)
	}
	ada.Channel, ada.Scope, ada.TokenID = app.ChannelDashboard, app.ScopeSession, ""
	if _, err := e.svc.AcceptProjectInvite(ctx, ada, inv.URL[strings.LastIndex(inv.URL, "/")+1:]); err != nil {
		t.Fatal(err)
	}

	res := call(t, ds, "list_projects", map[string]any{})
	if res.IsError || !strings.Contains(text(res), "shared by Bob (bob@example.com)") {
		t.Fatalf("list_projects: %s", text(res))
	}
	items, _ := structured(t, res)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["role"] != "member" {
		t.Fatalf("items: %v", items)
	}
	res = call(t, ds, "list_apps", map[string]any{})
	if res.IsError || !strings.Contains(text(res), "shop") || strings.Contains(text(res), "private") {
		t.Fatalf("list_apps: %s", text(res))
	}
	if res := call(t, ds, "set_env", map[string]any{"app": "shop", "set": map[string]any{"GREETING": "hi"}}); res.IsError {
		t.Fatalf("set_env on the shared app: %s", text(res))
	}
	if res := call(t, ds, "delete_project", map[string]any{"project": shop.ID, "confirm": "Shop", "with_apps": true}); !res.IsError || structured(t, res)["code"] != "project_owner_only" {
		t.Fatalf("delete_project: %s", text(res))
	}
	if res := call(t, ds, "get_app", map[string]any{"app": "private"}); !res.IsError {
		t.Fatalf("ada sees bob's other app: %s", text(res))
	}
	// Deleting apps and backups is up to the owner; the error says who that is.
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"delete_app", map[string]any{"app": "shop", "confirm_slug": "shop"}},
		{"delete_backup", map[string]any{"app": "shop", "id": "bak_x"}},
	} {
		res := call(t, ds, c.tool, c.args)
		m := structured(t, res)
		if !res.IsError || m["code"] != "owner_only" || m["next_tool"] != "get_app" || !strings.Contains(text(res), "Only the project owner (Bob)") {
			t.Errorf("%s: %s", c.tool, text(res))
		}
	}
	if res := call(t, ds, "get_app", map[string]any{"app": "shop"}); res.IsError {
		t.Fatalf("shop is gone: %s", text(res))
	}
}
