package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/catalog"
	"github.com/AVMG20/bitploy/internal/deploy"
)

func (e *depEnv) envValue(t *testing.T, appID, name string) string {
	t.Helper()
	rows, err := e.s.Q.ListEnvVars(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == name {
			b, err := e.s.Crypto.Open(r.ValueEnc, envAAD(appID, name))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	return ""
}

func (e *depEnv) countApps(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM apps`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeployCatalogApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	res, err := e.s.DeployCatalogApp(ctx, e.user, "vaultwarden", CatalogDeployInput{})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Get("vaultwarden")
	app := res.App
	if app.Slug != "vaultwarden" || app.Name != "Vaultwarden" {
		t.Errorf("slug/name: %q %q", app.Slug, app.Name)
	}
	if app.Port == nil || *app.Port != int64(entry.Port) || app.HealthPath != entry.HealthPath || !app.Volume ||
		app.VolumeMB != int64(entry.VolumeMB) || app.MemoryMB != int64(entry.MemoryMB) || app.BuildMode != "dockerfile" || app.Type != "web" {
		t.Errorf("app settings: %+v", app)
	}
	if app.CatalogID != "vaultwarden" || app.CatalogVersion != entry.Version || app.CatalogUpdate != nil {
		t.Errorf("catalog fields: %q %q %v", app.CatalogID, app.CatalogVersion, app.CatalogUpdate)
	}
	if res.Deployment.State != "queued" || res.Deployment.SourceKind != "files" {
		t.Errorf("deployment: %+v", res.Deployment)
	}
	if got := e.envValue(t, app.ID, "DOMAIN"); got != app.URL || !strings.HasPrefix(got, "https://") {
		t.Errorf("DOMAIN = %q, want %q", got, app.URL)
	}
	for _, st := range res.FirstSteps {
		if strings.Contains(st, "{") {
			t.Errorf("first step with a placeholder: %q", st)
		}
	}
	if !strings.Contains(strings.Join(res.FirstSteps, " "), app.URL) {
		t.Errorf("no first step names the address: %v", res.FirstSteps)
	}
	// The stored source is the catalog Dockerfile.
	v, err := e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if err != nil || !v.Editable || v.Content != entry.Dockerfile {
		t.Errorf("stored Dockerfile: %+v %v", v, err)
	}
	// A second app of the same kind gets a random suffix.
	res2, err := e.s.DeployCatalogApp(ctx, e.user, "vaultwarden", CatalogDeployInput{Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res2.App.Slug, "vaultwarden-") || len(res2.App.Slug) != len("vaultwarden-")+4 || res2.App.Name != "Second" {
		t.Errorf("second slug/name: %q %q", res2.App.Slug, res2.App.Name)
	}
	// A slug the caller chose is never changed.
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{Slug: "vaultwarden"}); depCode(err) != "slug_taken" {
		t.Errorf("taken slug: %v", err)
	}
	// Memory and volume can be changed.
	mem, vol := int64(512), int64(256)
	res3, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{MemoryMB: &mem, VolumeMB: &vol})
	if err != nil || res3.App.MemoryMB != 512 || res3.App.VolumeMB != 256 {
		t.Errorf("custom size: %+v %v", res3.App, err)
	}
	// A static entry has no volume.
	res4, err := e.s.DeployCatalogApp(ctx, e.user, "it-tools", CatalogDeployInput{})
	if err != nil || res4.App.Volume {
		t.Errorf("it-tools: %+v %v", res4.App, err)
	}
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "nope", CatalogDeployInput{}); depCode(err) != "catalog_unknown" {
		t.Errorf("unknown: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.DeployCatalogApp(ctx, ro, "memos", CatalogDeployInput{}); depCode(err) != "token_read_only" {
		t.Errorf("read token: %v", err)
	}
}

func TestDeployCatalogAppFailureLeavesNothing(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 0 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{}); depCode(err) != "deploy_rate_limited" {
		t.Fatalf("want deploy_rate_limited, got %v", err)
	}
	if n := e.countApps(t); n != 0 {
		t.Errorf("the half-made app stayed: %d apps", n)
	}
	var envs int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM env_vars`).Scan(&envs)
	if envs != 0 {
		t.Errorf("env vars stayed: %d", envs)
	}
	// A refusal from CreateApp (quota) is passed on and creates nothing.
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 100, quota_apps = 0 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{}); depCode(err) != "apps_quota_exceeded" {
		t.Errorf("apps quota: %v", err)
	}
}

func TestGeneratedSecretsAreFilledAndNotListed(t *testing.T) {
	// No entry needs a generated secret today, so the logic is checked with the view.
	v := catalogView(catalog.Entry{ID: "x", Env: []catalog.EnvSpec{{Name: "SECRET_KEY", Generate: "secret", Description: "d"}, {Name: "A", Value: "b", Description: "d"}}})
	if len(v.Env) != 2 || !v.Env[0].Generated || v.Env[1].Generated {
		t.Errorf("env view: %+v", v.Env)
	}
}

func TestUpdateCatalogApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	res, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Get("memos")
	if d, _ := e.s.Q.GetDeployment(ctx, res.Deployment.ID); d.CatalogVersion != entry.Version {
		t.Errorf("first deployment catalog_version = %q", d.CatalogVersion)
	}
	if _, err := e.s.UpdateCatalogApp(ctx, e.user, res.App.Slug); depCode(err) != "catalog_up_to_date" {
		t.Errorf("up to date: %v", err)
	}
	// A newer version than the catalog's (bitploy was downgraded): no update offered.
	e.exec(t, `UPDATE apps SET catalog_version = '99.0.0' WHERE id = ?`, res.App.ID)
	if a, _ := e.s.GetApp(ctx, e.user, res.App.Slug); a.CatalogUpdate != nil {
		t.Errorf("downgrade offered: %v", *a.CatalogUpdate)
	}
	if _, err := e.s.UpdateCatalogApp(ctx, e.user, res.App.Slug); depCode(err) != "catalog_up_to_date" {
		t.Errorf("downgrade: %v", err)
	}
	// An older version, as if the catalog moved on.
	e.exec(t, `UPDATE apps SET catalog_version = '0.1.0' WHERE id = ?`, res.App.ID)
	a, _ := e.s.GetApp(ctx, e.user, res.App.Slug)
	if a.CatalogUpdate == nil || *a.CatalogUpdate != entry.Version {
		t.Fatalf("catalog_update = %v", a.CatalogUpdate)
	}
	e.exec(t, `UPDATE deployments SET state = 'failed' WHERE app_id = ?`, res.App.ID)
	dep, err := e.s.UpdateCatalogApp(ctx, e.user, res.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if dep.State != "queued" || dep.SourceKind != "files" {
		t.Errorf("deployment: %+v", dep)
	}
	// Queued is not live: the app still says what it runs, and the update is still offered.
	a, _ = e.s.GetApp(ctx, e.user, res.App.Slug)
	if a.CatalogVersion != "0.1.0" || a.CatalogUpdate == nil {
		t.Errorf("version recorded before the deployment is live: %q %v", a.CatalogVersion, a.CatalogUpdate)
	}
	// The deployment goes live: now the app runs the catalog version.
	e.exec(t, `UPDATE apps SET type = 'worker' WHERE id = ?`, res.App.ID) // a worker swaps without a proxy
	row, _ := e.s.Q.GetApp(ctx, res.App.ID)
	d, _ := e.s.Q.GetDeployment(ctx, dep.ID)
	if d.CatalogVersion != entry.Version {
		t.Fatalf("update deployment catalog_version = %q", d.CatalogVersion)
	}
	p := &pipeline{s: e.s, app: row, dep: d, ring: deploy.NewLineRing(10)}
	if ae := p.swap(ctx); ae != nil {
		t.Fatalf("swap: %v", ae)
	}
	a, _ = e.s.GetApp(ctx, e.user, res.App.Slug)
	if a.CatalogVersion != entry.Version || a.CatalogUpdate != nil {
		t.Errorf("after going live: %q %v", a.CatalogVersion, a.CatalogUpdate)
	}
	// A rollback to the older deployment carries the older version back.
	if old, _ := e.s.Q.GetDeployment(ctx, res.Deployment.ID); old.CatalogVersion != entry.Version {
		t.Errorf("old deployment version = %q", old.CatalogVersion)
	}

	plain := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.UpdateCatalogApp(ctx, e.user, plain.Slug); depCode(err) != "not_catalog_app" {
		t.Errorf("plain app: %v", err)
	}
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://example.com/r.git', git_branch = 'main', catalog_version = '0.1.0' WHERE id = ?`, res.App.ID)
	if _, err := e.s.UpdateCatalogApp(ctx, e.user, res.App.Slug); depCode(err) != "git_source_linked" {
		t.Errorf("git linked: %v", err)
	}
	if a, _ := e.s.GetApp(ctx, e.user, res.App.Slug); a.CatalogUpdate != nil {
		t.Errorf("update offered for a git-linked app")
	}
}

// A cancelled update leaves nothing claimed; with the stored source gone the
// catalog Dockerfile alone is deployed.
func TestUpdateCatalogAppWithoutSnapshot(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	res, err := e.s.DeployCatalogApp(ctx, e.user, "memos", CatalogDeployInput{})
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE apps SET catalog_version = '0.1.0' WHERE id = ?`, res.App.ID)
	e.exec(t, `UPDATE deployments SET state = 'failed' WHERE app_id = ?`, res.App.ID)
	if err := os.Remove(e.s.sourceZip(res.Deployment.ID)); err != nil {
		t.Fatal(err)
	}
	dep, err := e.s.UpdateCatalogApp(ctx, e.user, res.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Get("memos")
	if got, ok := readZipFile(t, e.s.sourceZip(dep.ID), "Dockerfile"); !ok || got != entry.Dockerfile {
		t.Errorf("Dockerfile = %q %v", got, ok)
	}
}

func TestCatalogNewer(t *testing.T) {
	for _, c := range []struct {
		cat, running string
		want         bool
	}{
		{"1.37.5", "1.37.4", true},
		{"1.37.5", "1.37.5", false},
		{"1.10.0", "1.9.3", true},
		{"1.9.3", "1.10.0", false},
		{"26.11.0", "26.9.1", true},
		{"2024.10.22", "2024.10.3", true},
		{"0.40.6", "0.40", true},
		{"0.40", "0.40.6", false},
		{"2.0.0", "2.0.0-beta.1", false},
		{"v1.2.0", "1.1.9", true},
		{"rc", "beta", true},
		{"1.0", "", true},
	} {
		if got := catalogNewer(c.cat, c.running); got != c.want {
			t.Errorf("catalogNewer(%q, %q) = %v, want %v", c.cat, c.running, got, c.want)
		}
	}
}

// A secret the catalog generates is saved hidden; the other values stay readable.
func TestDeployCatalogAppHidesGeneratedSecrets(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	res, err := e.s.DeployCatalogApp(ctx, e.user, "n8n", CatalogDeployInput{})
	if err != nil {
		t.Fatal(err)
	}
	vars, err := e.s.ListEnv(ctx, e.user, res.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	m := envByName(vars)
	if v := m["N8N_ENCRYPTION_KEY"]; !v.Hidden || v.Value != nil {
		t.Errorf("generated: %+v", v)
	}
	if v := m["WEBHOOK_URL"]; v.Hidden || v.Value == nil || *v.Value != res.App.URL {
		t.Errorf("filled in: %+v", v)
	}
}
