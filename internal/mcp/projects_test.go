package mcp

import (
	"strings"
	"testing"
)

// deploy_catalog_app takes project and alias; delete_app on a database that
// an app uses answers app_in_use with the dependents.
func TestCatalogIntoProjectAndAppInUse(t *testing.T) {
	e := newEnv(t)
	ds := e.connect(t, e.deployToken)

	res := call(t, ds, "create_project", map[string]any{"name": "Stats"})
	if res.IsError {
		t.Fatal(text(res))
	}
	proj, _ := structured(t, res)["project"].(map[string]any)
	pid, _ := proj["id"].(string)
	if pid == "" {
		t.Fatalf("create_project: %s", text(res))
	}

	res = call(t, ds, "deploy_catalog_app", map[string]any{"id": "umami", "project": pid, "alias": "stats"})
	if res.IsError {
		t.Fatal(text(res))
	}
	ap, _ := structured(t, res)["app"].(map[string]any)
	pr, _ := ap["project"].(map[string]any)
	if pr["id"] != pid || ap["alias"] != "stats" {
		t.Fatalf("app: %v", ap)
	}

	res = call(t, ds, "get_project", map[string]any{"project": pid})
	proj, _ = structured(t, res)["project"].(map[string]any)
	apps, _ := proj["apps"].([]any)
	dbSlug := ""
	for _, a := range apps {
		m := a.(map[string]any)
		if m["alias"] == "db" {
			dbSlug, _ = m["slug"].(string)
		}
	}
	if len(apps) != 2 || dbSlug == "" {
		t.Fatalf("project apps: %v", apps)
	}

	res = call(t, ds, "delete_app", map[string]any{"app": dbSlug, "confirm_slug": dbSlug})
	m := structured(t, res)
	if !res.IsError || m["code"] != "app_in_use" || m["next_tool"] != "set_env" || !strings.Contains(m["hint"].(string), "set_env") {
		t.Fatalf("delete_app: %v", m)
	}
	details, _ := m["details"].(map[string]any)
	deps, _ := details["dependents"].([]any)
	if len(deps) != 1 {
		t.Fatalf("dependents: %v", details)
	}
	d := deps[0].(map[string]any)
	if d["slug"] != ap["slug"] || d["alias"] != "stats" || len(d["vars"].([]any)) != 1 || d["vars"].([]any)[0] != "DATABASE_URL" {
		t.Errorf("dependent: %v", d)
	}
}
