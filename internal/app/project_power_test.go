package app

import (
	"context"
	"testing"
)

// Stop all stops the apps before the database they use, skips what already
// stopped, and is open to members; start all skips apps that never ran.
func TestStopAndStartProject(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	db := m.mustApp(t, CreateAppInput{Name: "db", Slug: "shop-db", Project: m.shop.ID})
	m.exec(t, `UPDATE apps SET template = 'postgres' WHERE id = ?`, db.ID)
	idle := m.mustApp(t, CreateAppInput{Name: "Idle", Slug: "shop-idle", Project: m.shop.ID})
	m.seedRunning(t, m.app, 1)
	m.seedRunning(t, db, 1)
	m.exec(t, `UPDATE apps SET desired_state = 'running' WHERE id IN (?, ?)`, m.app.ID, db.ID)
	m.exec(t, `UPDATE apps SET desired_state = 'stopped' WHERE id = ?`, idle.ID)

	// A stranger gets nothing; a member may.
	if _, err := s.StopProject(ctx, m.carol, m.shop.ID); depCode(err) != "project_not_found" {
		t.Fatalf("stranger: %v", err)
	}
	m.join(t, m.bob)
	res, err := s.StopProject(ctx, m.bob, m.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 2 || res.Changed[0] != "shop" || res.Changed[1] != "shop-db" {
		t.Errorf("stopped %v, want the app before its database", res.Changed)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].App != "shop-idle" || res.Skipped[0].Reason != "already_stopped" || len(res.Failed) != 0 {
		t.Errorf("skipped %+v, failed %+v", res.Skipped, res.Failed)
	}
	for _, a := range res.Project.Apps {
		if a.DesiredState != "stopped" {
			t.Errorf("%s is %s after stop all", a.Slug, a.DesiredState)
		}
	}
	if n := auditCount(t, s, "project.stop_all", ""); n != 1 {
		t.Errorf("project.stop_all audited %d times", n)
	}

	// Start all: the never-deployed app is skipped. Without Docker the
	// deployed ones cannot start here; each failure is listed, not fatal.
	res, err = s.StartProject(ctx, m.alice, m.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]string{}
	for _, sk := range res.Skipped {
		skipped[sk.App] = sk.Reason
	}
	if skipped["shop-idle"] != "not_deployed" {
		t.Errorf("skipped %+v", res.Skipped)
	}
	if got := len(res.Changed) + len(res.Failed); got != 2 {
		t.Errorf("start all touched %d deployed apps, want 2: %+v %+v", got, res.Changed, res.Failed)
	}
	if len(res.Failed) > 0 && res.Failed[0].App != "shop-db" {
		t.Errorf("start all began with %s, want the database first", res.Failed[0].App)
	}
}
