//go:build integration

package app

// Projects and service templates on a real Docker daemon:
//
//	go test -tags=integration -count=1 -run 'TestIntegrationServiceTemplates|TestIntegrationProjectReference' ./internal/app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/servicetpl"
)

// waitService waits for the first deployment of a service app to finish.
func (e *itEnv) waitService(t *testing.T, slug string) Deployment {
	t.Helper()
	page, err := e.s.ListDeployments(context.Background(), e.user, slug, 1, "")
	if err != nil || len(page.Items) == 0 {
		t.Fatalf("%s has no deployment: %v", slug, err)
	}
	return e.wait(t, page.Items[0].ID)
}

// Every template, hidden ones too, builds and passes its health check as a
// non-root user with its generated passwords.
func TestIntegrationServiceTemplates(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	for _, tp := range servicetpl.All() {
		t.Run(tp.ID, func(t *testing.T) {
			p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "tpl " + tp.ID + " " + e.suffix})
			if err != nil {
				t.Fatal(err)
			}
			sv, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: tp.ID})
			if err != nil {
				t.Fatal(err)
			}
			e.slugs = append(e.slugs, sv.Slug)
			if d := e.waitService(t, sv.Slug); d.State != "running" {
				t.Fatalf("%s: %s %+v %v", tp.ID, d.State, d.Error, d.LogTail)
			}
			got, err := e.s.GetApp(ctx, e.user, sv.Slug)
			if err != nil || got.Current == nil {
				t.Fatalf("get: %+v %v", got.Current, err)
			}
			info, err := e.dk.InspectContainer(ctx, got.Current.toContainer(sv.Slug))
			if err != nil || info.State == nil || !info.State.Running {
				t.Fatalf("container: %v", err)
			}
			if info.Config.User != tp.User {
				t.Errorf("user = %q, want %q", info.Config.User, tp.User)
			}
			if tp.ShmMB > 0 && info.HostConfig.ShmSize < tp.ShmMB*1024*1024 {
				t.Errorf("shm = %d", info.HostConfig.ShmSize)
			}
			if err := e.s.DeleteProject(ctx, e.user, p.ID, p.Name, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A worker in the same project reaches PostgreSQL through
// DATABASE_URL=${{db.DATABASE_URL}}, on the project network by the alias db.
func TestIntegrationProjectReference(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "ref " + e.suffix})
	if err != nil {
		t.Fatal(err)
	}
	w := e.newApp(t, CreateAppInput{Type: "worker", Project: p.ID, Alias: "api"})
	db, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres", Name: "db", ConnectTo: []string{w.Slug}})
	if err != nil {
		t.Fatal(err)
	}
	e.slugs = append(e.slugs, db.Slug)
	if got := e.envValue(t, w.ID, "DATABASE_URL"); got != "${{db.DATABASE_URL}}" {
		t.Fatalf("DATABASE_URL = %q", got)
	}

	// The worker's deployment waits for db, then connects with psql.
	d, err := e.s.DeployFiles(ctx, e.user, w.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM postgres:18.6\nUSER 999:999\n" +
		`CMD ["sh", "-c", "until psql \"$DATABASE_URL\" -tAc 'select 40+2'; do sleep 1; done; echo reached-db; sleep 3600"]` + "\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v %v", d.State, d.Error, d.LogTail)
	}
	if sd := e.waitService(t, db.Slug); sd.State != "running" {
		t.Fatalf("db: %s %+v", sd.State, sd.Error)
	}
	waitFor(t, 60*time.Second, "worker reaching db", func() bool {
		lines, _ := e.s.Logs(ctx, e.user, w.Slug, LogOptions{Kind: "runtime"})
		for _, l := range lines {
			if strings.Contains(l.Text, "reached-db") {
				return true
			}
		}
		return false
	})

	// Both containers are on bp-p-<id>, db under its alias.
	net := dockerx.ProjectNetworkName(p.ID)
	members, err := e.dk.NetworkMembers(ctx, net)
	if err != nil || len(members) == 0 {
		t.Fatalf("project network: %v", err)
	}
	dbApp, _ := e.s.GetApp(ctx, e.user, db.Slug)
	wApp, _ := e.s.GetApp(ctx, e.user, w.Slug)
	for _, a := range []App{dbApp, wApp} {
		name := a.Current.toContainer(a.Slug)
		if !members[name] {
			t.Errorf("%s is not on %s: %v", name, net, members)
		}
	}
	info, _ := e.dk.InspectContainer(ctx, dbApp.Current.toContainer(db.Slug))
	if n := info.NetworkSettings.Networks[net]; n == nil || !contains(n.Aliases, "db") {
		t.Errorf("db aliases on %s: %+v", net, n)
	}
	if dbApp.InternalHost != "db:5432" {
		t.Errorf("internal_host = %q", dbApp.InternalHost)
	}

	// The password never shows in the worker's env as a value.
	if v := e.envValue(t, w.ID, "DATABASE_URL"); strings.Contains(v, "postgres://") {
		t.Errorf("the stored value was resolved: %q", v)
	}

	// Removing the project removes its apps and the network.
	if err := e.s.DeleteProject(ctx, e.user, p.ID, p.Name, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "project network removed", func() bool {
		ns, _ := e.dk.ListManagedNetworks(ctx)
		for _, n := range ns {
			if n.Name == net {
				return false
			}
		}
		return true
	})
}
