package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// makeMulti turns an app into one with the given services (as a compose
// deploy would), so it holds <alias>-<service> on the project network.
func (e *depEnv) makeMulti(t *testing.T, appID string, services ...string) {
	t.Helper()
	e.exec(t, `UPDATE apps SET service_layout = 'multi' WHERE id = ?`, appID)
	e.exec(t, `DELETE FROM app_services WHERE app_id = ?`, appID)
	for i, sv := range services {
		if err := e.s.Q.InsertAppService(context.Background(), db.InsertAppServiceParams{
			AppID: appID, Name: sv, Kind: "image", Image: "busybox:1.36", Public: b2i(i == 0), MemoryMb: 64, CpuMillis: 100,
			Position: int64(i), CreatedAt: 1, UpdatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// Names on a project network: an alias may not equal <alias>-<service> of an
// app with several services, in either direction.
func TestProjectHostNameClashes(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "P"})
	web := e.mustApp(t, CreateAppInput{Name: "web", Slug: "web-app", Project: p.ID, Alias: "web"})
	e.makeMulti(t, web.ID, "api", "worker")

	_, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "x", Slug: "other", Project: p.ID, Alias: "web-api"})
	if depCode(err) != "alias_taken" || apperr.As(err).Details["name"] != "web-api" || apperr.As(err).Details["app"] != "web-app" {
		t.Fatalf("alias over a service name: %v", err)
	}
	// A default alias skips the taken name.
	if a := e.mustApp(t, CreateAppInput{Name: "x", Slug: "web-api", Project: p.ID}); a.Alias != "web-api-2" {
		t.Errorf("default alias = %q, want web-api-2", a.Alias)
	}
	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey", Name: "web-worker"}); depCode(err) != "alias_taken" {
		t.Errorf("add_service over a service name: %v", err)
	}
	blog := e.mustApp(t, CreateAppInput{Name: "blog", Slug: "blog", Project: p.ID})
	if _, err := e.s.UpdateApp(ctx, e.user, blog.Slug, UpdateAppInput{Alias: strp("web-worker")}); depCode(err) != "alias_taken" {
		t.Errorf("rename over a service name: %v", err)
	}
	// The other way round: renaming the app with services so that one of its
	// service names becomes another app's alias.
	e.mustApp(t, CreateAppInput{Name: "y", Slug: "shop-api", Project: p.ID, Alias: "shop-api"})
	_, err = e.s.UpdateApp(ctx, e.user, web.Slug, UpdateAppInput{Alias: strp("shop")})
	if depCode(err) != "alias_taken" || apperr.As(err).Details["name"] != "shop-api" {
		t.Fatalf("rename into a clash: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, web.Slug, UpdateAppInput{Alias: strp("site")}); err != nil {
		t.Fatalf("free rename: %v", err)
	}

	// A compose deploy whose service names clash fails before anything is built.
	row, _ := e.s.Q.GetApp(ctx, blog.ID)
	ae := e.s.checkServiceHostNames(ctx, row, []serviceSpec{{Name: "api"}, {Name: "web"}})
	if ae != nil {
		t.Fatalf("no clash expected: %v", ae)
	}
	e.mustApp(t, CreateAppInput{Name: "z", Slug: "blog-db", Project: p.ID, Alias: "blog-db"})
	ae = e.s.checkServiceHostNames(ctx, row, []serviceSpec{{Name: "web"}, {Name: "db"}})
	if ae == nil || ae.Code != "network_name_taken" || ae.Details["service"] != "db" || ae.Details["name"] != "blog-db" || ae.HTTPStatus != 422 {
		t.Fatalf("clash = %+v", ae)
	}

	// Moving an app with services into a project where its names clash picks
	// a free alias.
	q, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Q"})
	e.mustApp(t, CreateAppInput{Name: "q", Slug: "q-one", Project: q.ID, Alias: "site-api"})
	moved, err := e.s.UpdateApp(ctx, e.user, web.Slug, UpdateAppInput{Project: strp(q.ID)})
	if err != nil || moved.Alias != "site-2" {
		t.Fatalf("move = %q, %v", moved.Alias, err)
	}
}

// deploy_catalog_app with a project adds the app and its services there; a
// service name that is taken gets the next free one, and the references
// follow it.
func TestDeployCatalogIntoProject(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Analytics"})
	e.mustApp(t, CreateAppInput{Name: "Old db", Slug: "old-db", Project: p.ID, Alias: "db"})

	res, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{Project: p.ID, Alias: "stats"})
	if err != nil {
		t.Fatal(err)
	}
	if res.App.Project.ID != p.ID || res.App.Alias != "stats" {
		t.Fatalf("app = %+v alias %q", res.App.Project, res.App.Alias)
	}
	got, _ := e.s.GetProject(ctx, e.user, p.ID)
	if len(got.Apps) != 3 {
		t.Fatalf("apps in the project: %d", len(got.Apps))
	}
	var svc *App
	for i := range got.Apps {
		if got.Apps[i].Template != nil {
			svc = &got.Apps[i]
		}
	}
	if svc == nil || svc.Alias != "db-2" || *svc.Template != "postgres" {
		t.Fatalf("service = %+v", svc)
	}
	if v := e.envValue(t, res.App.ID, "DATABASE_URL"); v != "${{db-2.DATABASE_URL}}" {
		t.Errorf("DATABASE_URL = %q", v)
	}
	row, _ := e.s.Q.GetApp(ctx, res.App.ID)
	if _, ae := e.s.containerEnv(ctx, row, 3000); ae != nil {
		t.Errorf("references do not resolve: %v", ae)
	}
	if ps, _ := e.s.ListProjects(ctx, e.user); len(ps) != 1 {
		t.Errorf("projects = %d, want 1 (no new project)", len(ps))
	}

	// A single-container entry goes into the project too; by name works.
	memos, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{Project: "analytics"})
	if err != nil || memos.App.Project.ID != p.ID || memos.App.Alias != "memos" {
		t.Fatalf("memos = %+v %q, %v", memos.App.Project, memos.App.Alias, err)
	}

	// Refusals leave the project as it was.
	before := e.countApps(t)
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{Project: p.ID, Alias: "stats"}); depCode(err) != "alias_taken" {
		t.Errorf("alias taken: %v", err)
	}
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{Project: p.ID, Alias: "-x"}); depCode(err) != "alias_invalid" {
		t.Errorf("alias invalid: %v", err)
	}
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.DeployCatalogApp(ctx, bob, "umami", CatalogDeployInput{Project: p.ID}); depCode(err) != "project_not_found" {
		t.Errorf("another user's project: %v", err)
	}
	// The app is made, its database is over the quota: both go, the project stays.
	e.exec(t, `UPDATE users SET quota_apps = ? WHERE id = ?`, before+1, e.user.UserID)
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{Project: p.ID}); depCode(err) != "apps_quota_exceeded" {
		t.Fatalf("over quota: %v", err)
	}
	if n := e.countApps(t); n != before {
		t.Errorf("apps after refusals: %d, want %d", n, before)
	}
	if got, err := e.s.GetProject(ctx, e.user, p.ID); err != nil || len(got.Apps) != before {
		t.Errorf("project after refusals: %v, %v", got.Apps, err)
	}
}

// The restarts of connect_to apps are stored with the service's deployment:
// they survive a bitploy restart, are done once when it is live and are
// dropped when it failed.
func TestConnectRestartsAreStored(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "P"})
	web := e.mustApp(t, CreateAppInput{Name: "web", Slug: "web", Project: p.ID})
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres", ConnectTo: []string{"web"}})
	if err != nil {
		t.Fatal(err)
	}
	deps, _ := e.s.ListDeployments(ctx, e.user, pg.Slug, 1, "")
	depID := deps.Items[0].ID
	count := func() int {
		var n int
		if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deployment_restarts WHERE deployment_id = ? AND app_id = ?`, depID, web.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("restart not stored")
	}
	// Still queued: the reconciler leaves it.
	e.s.reconcileRestarts(ctx)
	if count() != 1 {
		t.Fatal("a queued deployment's restart was dropped")
	}
	// Live, but no Docker to restart with: kept for the next pass.
	e.exec(t, `UPDATE deployments SET state = 'running' WHERE id = ?`, depID)
	e.s.reconcileRestarts(ctx)
	if count() != 1 {
		t.Fatal("dropped without Docker")
	}
	// The pipeline (or the reconciler) takes it exactly once.
	if ids, err := e.s.Q.TakeDeploymentRestarts(ctx, depID); err != nil || len(ids) != 1 || ids[0] != web.ID {
		t.Fatalf("take = %v, %v", ids, err)
	}
	if ids, _ := e.s.Q.TakeDeploymentRestarts(ctx, depID); len(ids) != 0 {
		t.Fatalf("taken twice: %v", ids)
	}
	// A failed deployment's restarts are dropped.
	e.exec(t, `INSERT INTO deployment_restarts (deployment_id, app_id, created_at) VALUES (?, ?, 1)`, depID, web.ID)
	e.exec(t, `UPDATE deployments SET state = 'failed' WHERE id = ?`, depID)
	e.s.reconcileRestarts(ctx)
	if count() != 0 {
		t.Fatal("a failed deployment's restart was kept")
	}
	// Deleting the connected app removes its rows.
	e.exec(t, `INSERT INTO deployment_restarts (deployment_id, app_id, created_at) VALUES (?, ?, 1)`, depID, web.ID)
	if _, err := e.s.SetEnv(ctx, e.user, "web", nil, []string{"DATABASE_URL"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteApp(ctx, e.user, "web", "web"); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("rows of a deleted app")
	}
}

// A compose service's ${VAR} whose shared variable holds a reference gets
// the resolved value when the container starts.
func TestComposeVariableWithReference(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "P"})
	web := e.mustApp(t, CreateAppInput{Name: "web", Slug: "web", Project: p.ID})
	if _, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey", Name: "cache"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetEnv(ctx, e.user, "web", map[string]string{"CACHE": "${{cache.REDIS_URL}}"}, nil); err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Q.GetApp(ctx, web.ID)
	spec := &serviceSpec{Name: "api", Environment: map[string]string{"REDIS": "x-" + deploy.EnvRef("CACHE")}}
	env, shared, ae := e.s.serviceEnv(ctx, row, spec, nil, 0)
	if ae != nil {
		t.Fatal(ae)
	}
	want := shared["CACHE"]
	if !strings.HasPrefix(want, "redis://") || !containsStr(env, "REDIS=x-"+want) {
		t.Errorf("REDIS not filled from the reference: %q", want)
	}
}

// A template app shows the template's current version and whether it is
// newer than the one it runs; nothing is upgraded.
func TestTemplateUpdateAvailable(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	p, _ := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "P"})
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if pg.TemplateUpdateAvailable || pg.TemplateLatest != pg.TemplateVersion || pg.TemplateLatest == "" {
		t.Fatalf("fresh app: latest %q, runs %q, update %v", pg.TemplateLatest, pg.TemplateVersion, pg.TemplateUpdateAvailable)
	}
	e.exec(t, `UPDATE apps SET template_version = '18.1' WHERE id = ?`, pg.ID)
	got, _ := e.s.GetApp(ctx, e.user, pg.Slug)
	if !got.TemplateUpdateAvailable || got.TemplateVersion != "18.1" || got.TemplateLatest != pg.TemplateLatest {
		t.Errorf("old app: %+v", got)
	}
	e.exec(t, `UPDATE apps SET template_version = '99.0' WHERE id = ?`, pg.ID)
	if got, _ := e.s.GetApp(ctx, e.user, pg.Slug); got.TemplateUpdateAvailable {
		t.Error("a newer running version is not an update")
	}
	plain := e.mustApp(t, CreateAppInput{Name: "web", Slug: "web", Project: p.ID})
	b, _ := json.Marshal(plain)
	if !strings.Contains(string(b), `"template_update_available":false`) || strings.Contains(string(b), "template_latest") {
		t.Errorf("plain app JSON: %s", b)
	}
}

// Members of a project network that are no container of its apps are pruned.
func TestProjectNetworkStrangers(t *testing.T) {
	members := map[string]bool{"bp-web-1": true, "bp-gone-3": true, "bp-db-2": true, "bp-old-1": true}
	owned := map[string]bool{"bp-web-1": true, "bp-db-2": true, "bp-web-2": true}
	if got := strings.Join(strangers(members, owned), ","); got != "bp-gone-3,bp-old-1" {
		t.Errorf("strangers = %s", got)
	}
	if got := strangers(map[string]bool{"a": true}, map[string]bool{"a": true}); len(got) != 0 {
		t.Errorf("all owned: %v", got)
	}
}
