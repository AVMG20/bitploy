package mcp

import (
	"strings"
	"testing"
)

func TestNewAppAndProjectTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	ro := e.connect(t, e.readToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Notes", "slug": "notes-app", "volume": true}); res.IsError {
		t.Fatal(text(res))
	}
	if res := call(t, cs, "set_env", map[string]any{"app": "notes-app", "set": map[string]any{"API_KEY": "s3cret-value"}}); res.IsError {
		t.Fatal(text(res))
	}

	// list_env: names, never values; read scope.
	res := call(t, ro, "list_env", map[string]any{"app": "notes-app", "names": []string{"API_KEY", "DATABASE_URL"}})
	if res.IsError || strings.Contains(text(res), "s3cret") {
		t.Fatalf("list_env: %s", text(res))
	}
	if m := structured(t, res); !strings.Contains(text(res), "Not set: DATABASE_URL") || m["set"].(map[string]any)["API_KEY"] != true {
		t.Errorf("list_env = %v", m)
	}

	// Backups are on by default; set_backup_schedule needs the deploy scope.
	res = call(t, ro, "list_backups", map[string]any{"app": "notes-app"})
	if res.IsError || !strings.Contains(text(res), "every night at") || !strings.Contains(text(res), "kept 14 days") {
		t.Errorf("list_backups: %s", text(res))
	}
	if m := assertErrorShape(t, "read token", call(t, ro, "set_backup_schedule", map[string]any{"app": "notes-app", "schedule": "off"})); m != nil && m["code"] != "token_read_only" {
		t.Errorf("read token: %v", m)
	}
	res = call(t, cs, "set_backup_schedule", map[string]any{"app": "notes-app", "schedule": "weekly", "keep_mode": "last", "keep": 3})
	if res.IsError || !strings.Contains(text(res), "every Sunday at") || !strings.Contains(text(res), "newest 3") {
		t.Errorf("set_backup_schedule: %s", text(res))
	}
	if m := assertErrorShape(t, "bad schedule", call(t, cs, "set_backup_schedule", map[string]any{"app": "notes-app", "schedule": "hourly"})); m == nil {
		t.Error("hourly accepted")
	}

	// get_traffic: read scope, empty for a new app.
	res = call(t, ro, "get_traffic", map[string]any{"app": "notes-app", "window": "7d"})
	if res.IsError || !strings.Contains(text(res), "0 request(s)") {
		t.Errorf("get_traffic: %s", text(res))
	}

	// update_catalog_app on an app not from the catalog.
	if m := assertErrorShape(t, "not catalog", call(t, cs, "update_catalog_app", map[string]any{"app": "notes-app"})); m != nil && m["code"] != "not_catalog_app" {
		t.Errorf("update_catalog_app: %v", m)
	}

	// rename_project and delete_project.
	res = call(t, cs, "create_project", map[string]any{"name": "Old name"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if res := call(t, ro, "rename_project", map[string]any{"project": "Old name", "name": "Shop"}); !res.IsError {
		t.Error("read token renamed a project")
	}
	if res := call(t, cs, "rename_project", map[string]any{"project": "Old name", "name": "Shop"}); res.IsError || !strings.Contains(text(res), "now called Shop") {
		t.Errorf("rename_project: %s", text(res))
	}
	if m := assertErrorShape(t, "no confirm", call(t, cs, "delete_project", map[string]any{"project": "Shop", "confirm": ""})); m != nil && m["next_tool"] != "delete_project" {
		t.Errorf("no confirm: %v", m)
	}
	if m := assertErrorShape(t, "wrong confirm", call(t, cs, "delete_project", map[string]any{"project": "Shop", "confirm": "Shops"})); m != nil && m["code"] != "confirm_mismatch" {
		t.Errorf("wrong confirm: %v", m)
	}
	if res := call(t, cs, "delete_project", map[string]any{"project": "Shop", "confirm": "Shop"}); res.IsError || !strings.Contains(text(res), "Deleted the project Shop") {
		t.Errorf("delete_project: %s", text(res))
	}
}
