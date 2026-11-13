package mcp

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// list_activity tells an assistant what happened to the user's apps, with a
// read-only token too, and nothing of other users' apps.
func TestListActivity(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	bob, err := e.svc.CreateUser(ctx, "bob@example.com", "Bob", "correct horse battery", false)
	if err != nil {
		t.Fatal(err)
	}
	bobS := app.Actor{UserID: bob.ID, Role: "user", Channel: app.ChannelDashboard, Scope: app.ScopeSession, IP: "198.51.100.9"}
	if _, err := e.svc.CreateApp(ctx, bobS, app.CreateAppInput{Name: "Bob's", Slug: "bobs-app"}); err != nil {
		t.Fatal(err)
	}
	ds := e.connect(t, e.deployToken)
	if res := call(t, ds, "create_app", map[string]any{"name": "Notes", "slug": "notes"}); res.IsError {
		t.Fatalf("create_app: %s", text(res))
	}

	res := call(t, e.connect(t, e.readToken), "list_activity", map[string]any{"app": "notes"})
	if res.IsError {
		t.Fatalf("list_activity: %s", text(res))
	}
	items, _ := structured(t, res)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items: %v", items)
	}
	if it := items[0].(map[string]any); it["action"] != "app.create" || it["actor"] != "you" {
		t.Errorf("entry: %v", it)
	}
	all := call(t, ds, "list_activity", map[string]any{})
	if all.IsError || strings.Contains(text(all), "bobs-app") || strings.Contains(text(all), "198.51.100.9") {
		t.Errorf("list_activity shows bob's app: %s", text(all))
	}
}
