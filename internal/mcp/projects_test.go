package mcp

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
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

// "Put my repo online with a Postgres": add_service on an app that never ran
// tells the AI to deploy it; get_app of the database says how to connect,
// never to send it code.
func TestAddServiceToNewAppTellsToDeploy(t *testing.T) {
	e := newEnv(t)
	ds := e.connect(t, e.deployToken)

	res := call(t, ds, "create_app", map[string]any{"name": "Shop", "slug": "shop"})
	if res.IsError {
		t.Fatal(text(res))
	}
	ap, _ := structured(t, res)["app"].(map[string]any)
	pr, _ := ap["project"].(map[string]any)
	pid, _ := pr["id"].(string)

	res = call(t, ds, "add_service", map[string]any{"project": pid, "template": "postgres", "connect_to": []string{"shop"}})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	next := strings.Join(toStrings(m["next_steps"]), " ")
	if !strings.Contains(next, "Nothing runs yet in shop") || !strings.Contains(next, "DATABASE_URL=${{postgres.DATABASE_URL}}") || strings.Contains(next, "Restarted by bitploy") {
		t.Errorf("add_service next: %s", next)
	}
	svc, _ := m["app"].(map[string]any)
	slug, _ := svc["slug"].(string)

	res = call(t, ds, "list_projects", nil)
	if !strings.Contains(text(res), "postgres (postgres, slug "+slug+")") {
		t.Errorf("list_projects: %s", text(res))
	}

	res = call(t, ds, "get_app", map[string]any{"app": slug})
	if res.IsError {
		t.Fatal(text(res))
	}
	next = strings.Join(toStrings(structured(t, res)["next_steps"]), " ")
	if strings.Contains(next, "deploy_files with app="+slug) {
		t.Errorf("get_app of a service tells to send files: %s", next)
	}
}

func TestTemplateAppNext(t *testing.T) {
	tpl := "postgres"
	ap := app.App{Slug: "postgres-ab12", Status: "running", Template: &tpl, Alias: "postgres", InternalHost: "postgres:5432",
		MainVar: "DATABASE_URL", ConnectionVars: []string{"DATABASE_URL", "PGHOST"}, TemplateVersion: "17.1", TemplateLatest: "18.0", TemplateUpdateAvailable: true}
	ap.Project.Name = "Shop"
	next := strings.Join(appNext(ap), " ")
	for _, want := range []string{"no code to send", "postgres:5432", `{"DATABASE_URL":"${{postgres.DATABASE_URL}}"}`, "never upgraded automatically"} {
		if !strings.Contains(next, want) {
			t.Errorf("next lacks %q: %s", want, next)
		}
	}
	if strings.Contains(next, "deploy_files") {
		t.Errorf("next: %s", next)
	}
}

// Moving an app was removed. An agent that still sends update_app project=
// gets the answer that says what to do now, not a list of arguments.
func TestUpdateAppProjectSaysAppsDoNotMove(t *testing.T) {
	e := newEnv(t)
	ds := e.connect(t, e.deployToken)
	if res := call(t, ds, "create_app", map[string]any{"name": "Shop", "slug": "move-me"}); res.IsError {
		t.Fatal(text(res))
	}
	m := assertErrorShape(t, "move", call(t, ds, "update_app", map[string]any{"app": "move-me", "project": "prj_other", "name": "Shop 2"}))
	if m == nil {
		return
	}
	if m["code"] != "project_move_unsupported" || m["next_tool"] != "create_app" || !strings.Contains(m["hint"].(string), "create_app") {
		t.Errorf("move: %v", m)
	}
	res := call(t, ds, "get_app", map[string]any{"app": "move-me"})
	if ap := structured(t, res)["app"].(map[string]any); ap["name"] != "Shop" {
		t.Errorf("the other fields changed: %v", ap["name"])
	}
}
