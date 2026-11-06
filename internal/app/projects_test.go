package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

func TestProjectsCRUD(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()

	// An app without a project gets one of its own, named after it.
	solo := e.mustApp(t, CreateAppInput{Name: "Blog", Slug: "blog"})
	if solo.Project.ID == "" || solo.Project.Name != "Blog" || solo.Alias != "blog" || solo.Template != nil || len(solo.References) != 0 {
		t.Fatalf("solo = project %+v alias %q template %v refs %v", solo.Project, solo.Alias, solo.Template, solo.References)
	}
	b, _ := json.Marshal(solo)
	for _, k := range []string{`"project":{"id":"prj_`, `"alias":"blog"`, `"template":null`, `"internal_host":`, `"references":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("App JSON lacks %s", k)
		}
	}

	shop, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: " Shop "})
	if err != nil || shop.Name != "Shop" || !strings.HasPrefix(shop.ID, "prj_") || shop.Network != "bp-p-"+shop.ID || len(shop.Apps) != 0 {
		t.Fatalf("create = %+v, %v", shop, err)
	}
	if _, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "  "}); depCode(err) != "project_name_invalid" {
		t.Errorf("empty name: %v", err)
	}
	web := e.mustApp(t, CreateAppInput{Name: "Shop web", Slug: "shop", Project: shop.ID, Alias: "Web", Port: ptr(int64(3000))})
	if web.Project.ID != shop.ID || web.Alias != "web" || web.InternalHost != "web:3000" {
		t.Errorf("web = %+v %q %q", web.Project, web.Alias, web.InternalHost)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "x", Slug: "shop-two", Project: shop.ID, Alias: "web"}); depCode(err) != "alias_taken" {
		t.Errorf("duplicate alias: %v", err)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "x", Slug: "shop-two", Project: shop.ID, Alias: "-bad"}); depCode(err) != "alias_invalid" {
		t.Errorf("bad alias: %v", err)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "x", Slug: "shop-two", Project: "prj_nope"}); depCode(err) != "project_not_found" {
		t.Errorf("unknown project: %v", err)
	}

	// Another user's project is not found.
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.GetProject(ctx, bob, shop.ID); depCode(err) != "project_not_found" {
		t.Errorf("bob sees alice's project: %v", err)
	}
	if _, err := e.s.CreateApp(ctx, bob, CreateAppInput{Name: "x", Slug: "bobs", Project: shop.ID}); depCode(err) != "project_not_found" {
		t.Errorf("bob adds to alice's project: %v", err)
	}

	got, err := e.s.GetProject(ctx, e.user, "shop") // by name, case-insensitive
	if err != nil || got.ID != shop.ID || len(got.Apps) != 1 || got.Apps[0].Slug != "shop" {
		t.Fatalf("get by name = %+v, %v", got, err)
	}
	all, err := e.s.ListProjects(ctx, e.user)
	if err != nil || len(all) != 2 {
		t.Fatalf("list = %d, %v", len(all), err)
	}

	renamed, err := e.s.UpdateProject(ctx, e.user, shop.ID, UpdateProjectInput{Name: strp("Store")})
	if err != nil || renamed.Name != "Store" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}

	// Apps do not move between projects: project is refused, with or without
	// other fields, and nothing changes.
	_, err = e.s.UpdateApp(ctx, e.user, "blog", UpdateAppInput{Project: strp(shop.ID), Alias: strp("news"), Name: strp("News")})
	if ae := apperr.As(err); depCode(err) != "project_move_unsupported" || ae.HTTPStatus != 422 || ae.Next != "create_app" {
		t.Fatalf("move = %v", err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, "blog"); got.Project.ID != solo.Project.ID || got.Alias != "blog" || got.Name != "Blog" {
		t.Errorf("a refused move changed the app: %+v %q %q", got.Project, got.Alias, got.Name)
	}
	if err := e.s.DeleteApp(ctx, e.user, "blog", "blog"); err != nil {
		t.Fatal(err)
	}
	e.mustApp(t, CreateAppInput{Name: "Shop blog", Slug: "shop-blog", Project: shop.ID})
	if _, err := e.s.UpdateApp(ctx, e.user, "shop-blog", UpdateAppInput{Alias: strp("web")}); depCode(err) != "alias_taken" {
		t.Errorf("alias clash: %v", err)
	}

	// Delete: confirm, then refused with apps, then with_apps.
	if err := e.s.DeleteProject(ctx, e.user, shop.ID, "Shop", false); depCode(err) != "confirm_mismatch" {
		t.Errorf("confirm: %v", err)
	}
	if err := e.s.DeleteProject(ctx, e.user, shop.ID, "Store", false); depCode(err) != "project_not_empty" {
		t.Errorf("not empty: %v", err)
	}
	if err := e.s.DeleteProject(ctx, e.user, shop.ID, "Store", true); err != nil {
		t.Fatalf("with_apps: %v", err)
	}
	if apps, _ := e.s.ListApps(ctx, e.user); len(apps) != 0 {
		t.Errorf("apps left: %d", len(apps))
	}

	// A project created on purpose stays when its last app goes; one made for
	// an app goes with it.
	keep, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Keep"})
	k := e.mustApp(t, CreateAppInput{Name: "k", Slug: "k-app", Project: keep.ID})
	if err := e.s.DeleteApp(ctx, e.user, k.Slug, k.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetProject(ctx, e.user, keep.ID); err != nil {
		t.Errorf("an explicit project must stay: %v", err)
	}
	auto := e.mustApp(t, CreateAppInput{Name: "Auto", Slug: "auto-app"})
	if err := e.s.DeleteApp(ctx, e.user, auto.Slug, auto.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetProject(ctx, e.user, auto.Project.ID); depCode(err) != "project_not_found" {
		t.Errorf("an auto project must go with its app: %v", err)
	}
}

func TestAddServiceAndReferences(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Shop"})
	web := e.mustApp(t, CreateAppInput{Name: "Shop", Slug: "shop", Project: p.ID, Port: ptr(int64(3000))})

	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "oracle"}); depCode(err) != "service_template_unknown" {
		t.Errorf("unknown template: %v", err)
	}
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres", ConnectTo: []string{"shop"}})
	if err != nil {
		t.Fatalf("add postgres: %v", err)
	}
	if pg.Template == nil || *pg.Template != "postgres" || pg.TemplateVersion != "18.6" || pg.Alias != "postgres" || pg.Type != "worker" || !pg.Volume ||
		pg.InternalHost != "postgres:5432" || pg.MainVar != "DATABASE_URL" || pg.Project.ID != p.ID || pg.URL != "" || pg.Status != "deploying" {
		t.Fatalf("postgres app = %+v", pg)
	}
	if pgDeps, err := e.s.ListDeployments(ctx, e.user, pg.Slug, 1, ""); err != nil || len(pgDeps.Items) != 1 || pgDeps.Items[0].State != "queued" {
		t.Errorf("deployments = %+v, %v", pgDeps, err)
	}

	// The connected app holds a reference, shown as such; the value resolves
	// from postgres's generated password and alias.
	env, _ := e.s.ListEnv(ctx, e.user, "shop")
	if len(env) != 1 || env[0].Name != "DATABASE_URL" || len(env[0].References) != 1 || env[0].References[0] != "${{postgres.DATABASE_URL}}" {
		t.Fatalf("shop env = %+v", env)
	}
	pgVars, _ := e.s.sharedVars(ctx, pg.ID)
	if len(pgVars["POSTGRES_PASSWORD"]) != 32 || pgVars["POSTGRES_USER"] != "app" {
		t.Fatalf("postgres vars: %d names", len(pgVars))
	}
	// The generated password is hidden, the user name readable.
	if pgEnv, _ := e.s.ListEnv(ctx, e.user, pg.Slug); !envByName(pgEnv)["POSTGRES_PASSWORD"].Hidden || envByName(pgEnv)["POSTGRES_USER"].Hidden {
		t.Errorf("postgres env = %+v", pgEnv)
	}
	row, _ := e.s.Q.GetApp(ctx, web.ID)
	envList, _, ae := e.s.serviceEnv(ctx, row, nil, nil, 3000)
	if ae != nil {
		t.Fatal(ae)
	}
	want := "DATABASE_URL=postgresql://app:" + pgVars["POSTGRES_PASSWORD"] + "@postgres:5432/app"
	if !containsStr(envList, want) {
		t.Errorf("env lacks the resolved DATABASE_URL")
	}
	if shop, _ := e.s.GetApp(ctx, e.user, "shop"); len(shop.References) != 1 || shop.References[0] != "postgres" {
		t.Errorf("shop references = %v", shop.References)
	}

	// The template's runtime: /dev/shm and the project network (two apps).
	pgRow, _ := e.s.Q.GetApp(ctx, pg.ID)
	if tp, ok := templateOf(pgRow); !ok || tp.ShmMB != 128 {
		t.Errorf("template of the app: %v", ok)
	}
	at := e.s.projectAttach(ctx, row, singleSpec(row))
	if len(at) != 1 || at[0].Network != "bp-p-"+p.ID || len(at[0].Aliases) != 1 || at[0].Aliases[0] != "shop" {
		t.Errorf("attach = %+v", at)
	}

	// A second postgres gets the next free name; connect_to refuses a set variable.
	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres", ConnectTo: []string{"shop"}}); depCode(err) != "connect_to_var_set" {
		t.Errorf("connect over a set variable: %v", err)
	}
	pg2, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres"})
	if err != nil || pg2.Alias != "postgres-2" {
		t.Fatalf("second postgres = %q, %v", pg2.Alias, err)
	}
	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey", Name: "postgres"}); depCode(err) != "alias_taken" {
		t.Errorf("name clash: %v", err)
	}
	if apps, _ := e.s.ListApps(ctx, e.user); len(apps) != 3 {
		t.Errorf("a refused add_service left an app behind: %d apps", len(apps))
	}

	// set_env checks references.
	for set, code := range map[string]string{
		"${{nope.X}}":              "variable_reference_unknown",
		"${{postgres.NOPE}}":       "variable_reference_unknown",
		"${{shop.DATABASE_URL}}":   "variable_reference_self",
		"x ${{postgres.PGHOST}} y": "",
	} {
		_, err := e.s.SetEnv(ctx, e.user, "shop", map[string]string{"V": set}, nil)
		if depCode(err) != code {
			t.Errorf("set %s: %v, want %s", set, err, code)
		}
	}

	// Renaming postgres rewrites the reference in shop.
	if _, err := e.s.UpdateApp(ctx, e.user, pg.Slug, UpdateAppInput{Alias: strp("db")}); err != nil {
		t.Fatalf("rename alias: %v", err)
	}
	env, _ = e.s.ListEnv(ctx, e.user, "shop")
	refs := map[string]string{}
	for _, v := range env {
		if len(v.References) > 0 {
			refs[v.Name] = v.References[0]
		}
	}
	if refs["DATABASE_URL"] != "${{db.DATABASE_URL}}" || refs["V"] != "${{db.PGHOST}}" {
		t.Errorf("after rename: %v", refs)
	}
	row, _ = e.s.Q.GetApp(ctx, web.ID)
	envList, _, ae = e.s.serviceEnv(ctx, row, nil, nil, 3000)
	if ae != nil || !containsStr(envList, "V=x db y") {
		t.Errorf("resolved after rename: %v", ae)
	}

	// Deleting a referenced app is refused with the apps and variables that
	// use it.
	err = e.s.DeleteApp(ctx, e.user, pg.Slug, pg.Slug)
	if depCode(err) != "app_in_use" {
		t.Fatalf("delete a referenced app: %v", err)
	}
	ae = apperr.As(err)
	deps, _ := ae.Details["dependents"].([]Dependent)
	if ae.HTTPStatus != 409 || ae.Next != "set_env" || len(deps) != 1 || deps[0].Slug != "shop" || deps[0].Alias != "shop" ||
		strings.Join(deps[0].Vars, ",") != "DATABASE_URL,V" || !strings.Contains(ae.Hint, "set_env") {
		t.Fatalf("app_in_use = %+v", ae)
	}
	b, _ := json.Marshal(ae.Details)
	if string(b) != `{"dependents":[{"slug":"shop","alias":"shop","vars":["DATABASE_URL","V"]}]}` {
		t.Errorf("details = %s", b)
	}
	if _, err := e.s.GetApp(ctx, e.user, pg.Slug); err != nil {
		t.Fatalf("the refused delete removed the app: %v", err)
	}
	// A dangling reference (here written straight into the database, as an
	// older bitploy allowed) makes the start fail with the name.
	if err := e.s.deleteApp(ctx, e.user, pg.Slug, pg.Slug, false); err != nil {
		t.Fatal(err)
	}
	row, _ = e.s.Q.GetApp(ctx, web.ID)
	if _, _, ae := e.s.serviceEnv(ctx, row, nil, nil, 3000); ae == nil || ae.Code != "variable_reference_unknown" || ae.Details["variable"] == nil {
		t.Errorf("start with a dangling reference: %+v", ae)
	}
}

func TestReferenceChainsAndLoops(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "P"})
	e.mustApp(t, CreateAppInput{Name: "a", Slug: "app-a", Project: p.ID, Alias: "a"})
	e.mustApp(t, CreateAppInput{Name: "b", Slug: "app-b", Project: p.ID, Alias: "b"})
	e.mustApp(t, CreateAppInput{Name: "c", Slug: "app-c", Project: p.ID, Alias: "c"})
	set := func(slug, k, v string) {
		t.Helper()
		if _, err := e.s.SetEnv(ctx, e.user, slug, map[string]string{k: v}, nil); err != nil {
			t.Fatalf("set %s %s: %v", slug, k, err)
		}
	}
	set("app-c", "KEY", "secret-value")
	set("app-b", "KEY", "${{c.KEY}}")
	set("app-a", "KEY", "${{b.KEY}}")
	row, _ := e.s.Q.GetAppBySlug(ctx, "app-a")
	env, _, ae := e.s.serviceEnv(ctx, row, nil, nil, 0)
	if ae != nil || !containsStr(env, "KEY=secret-value") {
		t.Errorf("chain: %v", ae)
	}
	// A loop: c.KEY -> a.KEY -> b.KEY -> c.KEY.
	set("app-c", "KEY", "${{a.KEY}}")
	if _, _, ae := e.s.serviceEnv(ctx, row, nil, nil, 0); ae == nil || ae.Code != "variable_reference_loop" {
		t.Errorf("loop: %+v", ae)
	}
	if !strings.Contains(refUnknownApp("X", varRef{"q", "Y"}).Message, "${{q.Y}}") {
		t.Error("the error names the reference")
	}
}

// delete_app is refused while another app uses the app's variables, works
// once they are gone, and delete_project with_apps removes both.
func TestDeleteReferencedApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Shop"})
	e.mustApp(t, CreateAppInput{Name: "Shop", Slug: "shop", Project: p.ID})
	e.mustApp(t, CreateAppInput{Name: "Back office", Slug: "office", Project: p.ID})
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres", Name: "db", ConnectTo: []string{"shop"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetEnv(ctx, e.user, "office", map[string]string{"PGHOST": "${{db.PGHOST}}", "X": "plain"}, nil); err != nil {
		t.Fatal(err)
	}
	err = e.s.DeleteApp(ctx, e.user, pg.Slug, pg.Slug)
	deps, _ := apperr.As(err).Details["dependents"].([]Dependent)
	if depCode(err) != "app_in_use" || len(deps) != 2 || deps[0].Slug != "office" || deps[0].Vars[0] != "PGHOST" || len(deps[0].Vars) != 1 || deps[1].Slug != "shop" {
		t.Fatalf("delete = %v, dependents %+v", err, deps)
	}
	// A wrong confirmation is still the first answer.
	if err := e.s.DeleteApp(ctx, e.user, pg.Slug, "nope"); depCode(err) != "confirm_mismatch" {
		t.Errorf("confirm: %v", err)
	}
	if _, err := e.s.SetEnv(ctx, e.user, "office", nil, []string{"PGHOST"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetEnv(ctx, e.user, "shop", map[string]string{"DATABASE_URL": "postgres://elsewhere/db"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteApp(ctx, e.user, pg.Slug, pg.Slug); err != nil {
		t.Fatalf("delete after the variables changed: %v", err)
	}

	// with_apps deletes referenced apps along with the apps that use them.
	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey", Name: "cache", ConnectTo: []string{"office"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteProject(ctx, e.user, p.ID, "Shop", true); err != nil {
		t.Fatalf("delete_project with_apps: %v", err)
	}
	if n := e.countApps(t); n != 0 {
		t.Errorf("%d apps left", n)
	}
}
