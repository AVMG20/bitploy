package app

import (
	"context"
	"testing"

	"github.com/AVMG20/bitploy/internal/catalog"
	"github.com/AVMG20/bitploy/internal/servicetpl"
)

// Every service of a multi-service entry is a known template, and every
// reference in its env names one of that template's connection variables.
func TestCatalogServicesAreTemplates(t *testing.T) {
	multi := 0
	for _, e := range catalog.All() {
		tpls := map[string]servicetpl.Template{}
		for _, sv := range e.Services {
			tp, ok := servicetpl.Get(sv.Template)
			if !ok {
				t.Errorf("%s: unknown template %q", e.ID, sv.Template)
				continue
			}
			if _, ae := normalizeAlias(sv.Name); ae != nil || sv.Name == e.ID {
				t.Errorf("%s: service name %q", e.ID, sv.Name)
			}
			tpls[sv.Name] = tp
		}
		if len(e.Services) > 0 {
			multi++
		}
		for _, ev := range e.Env {
			for _, m := range catalog.RefPattern.FindAllStringSubmatch(ev.Value, -1) {
				tp, ok := tpls[m[1]]
				if !ok {
					t.Errorf("%s: %s refers to %s, not a service of the entry", e.ID, ev.Name, m[0])
					continue
				}
				if _, ok := tp.Resolve(m[2], m[1], map[string]string{}); !ok {
					t.Errorf("%s: %s is not a connection variable of %s", e.ID, m[0], tp.ID)
				}
			}
		}
	}
	if multi == 0 {
		t.Error("no multi-service entry")
	}
}

// Deploying umami makes a project with the app and its PostgreSQL; the app
// reads DATABASE_URL through a reference.
func TestDeployCatalogWithServices(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	res, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deployment.State != "queued" || res.App.Project.Name != "Umami" {
		t.Fatalf("app = %+v, deployment %s", res.App.Project, res.Deployment.State)
	}
	p, err := e.s.GetProject(ctx, e.user, res.App.Project.ID)
	if err != nil || len(p.Apps) != 2 {
		t.Fatalf("project = %+v, %v", p, err)
	}
	var db *App
	for i := range p.Apps {
		if p.Apps[i].Template != nil {
			db = &p.Apps[i]
		}
	}
	if db == nil || *db.Template != "postgres" || db.Alias != "db" || db.InternalHost != "db:5432" {
		t.Fatalf("db = %+v", db)
	}
	if got := e.envValue(t, res.App.ID, "DATABASE_URL"); got != "${{db.DATABASE_URL}}" {
		t.Errorf("DATABASE_URL = %q", got)
	}
	if len(res.App.References) != 1 || res.App.References[0] != "db" {
		t.Errorf("references = %v", res.App.References)
	}
	cat, _ := e.s.GetCatalogApp(ctx, e.user, "umami")
	if len(cat.Services) != 1 || cat.Services[0].Image != "postgres:18.6" {
		t.Errorf("catalog services = %+v", cat.Services)
	}

	// A refused deploy leaves neither the project nor its apps behind.
	e.exec(t, `UPDATE users SET quota_apps = 3 WHERE id = ?`, e.user.UserID)
	before := e.countApps(t)
	if _, err := e.s.DeployCatalogApp(ctx, e.user, "umami", CatalogDeployInput{}); depCode(err) != "apps_quota_exceeded" {
		t.Fatalf("over quota: %v", err)
	}
	if n := e.countApps(t); n != before {
		t.Errorf("apps after a refused deploy: %d, want %d", n, before)
	}
	if ps, _ := e.s.ListProjects(ctx, e.user); len(ps) != 1 {
		t.Errorf("projects after a refused deploy: %d", len(ps))
	}
}
