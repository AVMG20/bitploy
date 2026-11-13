package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// histRow is a build_history row as the tests read it.
type histRow struct {
	appSlug, owner, person, channel, result, code string
	startedBy                                     *string
	builtAt                                       *int64
	started, finished                             int64
	cpu, mem                                      *float64
}

func (e *depEnv) histRow(t *testing.T, depID string) (histRow, bool) {
	t.Helper()
	var r histRow
	var code sql.NullString
	err := e.s.Store.DB.QueryRow(`SELECT app_slug, owner_id, person_id, channel, result, error_code, started_by, built_at, started_at, finished_at, peak_cpu, peak_mem_mb
		FROM build_history WHERE deployment_id = ?`, depID).Scan(&r.appSlug, &r.owner, &r.person, &r.channel, &r.result, &code, &r.startedBy, &r.builtAt,
		&r.started, &r.finished, &r.cpu, &r.mem)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	r.code = code.String
	return r, true
}

// histDep inserts a deployment that took a build slot at started, and returns
// a pipeline for it (as runPipeline has it after the claim).
func (e *depEnv) histDep(t *testing.T, app App, id, kind, createdBy string, queued, started int64) *pipeline {
	t.Helper()
	var by any
	if createdBy != "" {
		by = createdBy
	}
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, created_by, queued_at, started_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM deployments WHERE app_id = ?), 'building', ?, 'mcp', ?, ?, ?)`,
		id, app.ID, app.ID, kind, by, queued, started)
	ctx := context.Background()
	dep, err := e.s.Q.GetDeployment(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.s.Q.GetApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &pipeline{s: e.s, ring: deploy.NewLineRing(10), dep: dep, app: row}
}

// adminSession is an admin signed in to the dashboard (admin endpoints need a session).
func (e *depEnv) adminSession(t *testing.T) Actor {
	t.Helper()
	a := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	a.Channel, a.Scope = ChannelDashboard, ScopeSession
	return a
}

func (e *depEnv) hostSample(t *testing.T, ts int64, cpu float64, memMB int64) {
	t.Helper()
	e.exec(t, `INSERT INTO host_samples (ts, cpu, mem_used_bytes, mem_total_bytes) VALUES (?, ?, ?, ?)`, ts, cpu, memMB<<20, int64(8<<30))
}

func TestBuildHistoryOnEveryEnd(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	app := e.mustApp(t, CreateAppInput{Name: "Shop"})
	now := time.Now().Unix()

	t.Run("live", func(t *testing.T) {
		p := e.histDep(t, app, "dep_live", "files", bob.UserID, now-100, now-90)
		e.exec(t, `UPDATE deployments SET built_at = ?, state = 'running', finished_at = ? WHERE id = 'dep_live'`, now-60, now-50)
		p.dep.BuiltAt = sql.NullInt64{Int64: now - 60, Valid: true}
		p.finish(ctx, nil)
		r, ok := e.histRow(t, "dep_live")
		if !ok {
			t.Fatal("no history row")
		}
		if r.result != "live" || r.code != "" || r.appSlug != app.Slug || r.owner != e.user.UserID || r.person != bob.UserID ||
			r.startedBy == nil || *r.startedBy != bob.UserID || r.channel != "mcp" || r.builtAt == nil || *r.builtAt != now-60 ||
			r.started != now-90 || r.finished != now-50 {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("failed", func(t *testing.T) {
		p := e.histDep(t, app, "dep_fail", "git", "", now-40, now-40)
		p.finish(ctx, apperr.Unprocessable("build_failed", "The build failed.", "Fix it."))
		r, ok := e.histRow(t, "dep_fail")
		if !ok {
			t.Fatal("no history row")
		}
		// A git push has no starter: it counts for the owner.
		if r.result != "failed" || r.code != "build_failed" || r.builtAt != nil || r.startedBy != nil || r.person != e.user.UserID || r.finished < now {
			t.Errorf("row = %+v", r)
		}
	})
	t.Run("cancelled by a user", func(t *testing.T) {
		e.histDep(t, app, "dep_cancel", "files", e.user.UserID, now-30, now-30)
		done := e.fakePipeline(t, app.ID, "dep_cancel", false)
		if _, err := e.s.CancelDeployment(ctx, e.user, "dep_cancel"); err != nil {
			t.Fatal(err)
		}
		<-done
		if r, ok := e.histRow(t, "dep_cancel"); !ok || r.result != "cancelled" || r.code != "" {
			t.Errorf("row = %+v, %v", r, ok)
		}
	})
	t.Run("cancelled by the disk guard", func(t *testing.T) {
		p := e.histDep(t, app, "dep_disk", "files", "", now-20, now-20)
		e.s.deployRT.mu.Lock()
		e.s.deployRT.init()
		e.s.deployRT.diskCancel["dep_disk"] = true
		e.s.deployRT.mu.Unlock()
		p.finish(ctx, errCancelled)
		if r, ok := e.histRow(t, "dep_disk"); !ok || r.result != "failed" || r.code != "disk_full_build_cancelled" {
			t.Errorf("row = %+v, %v", r, ok)
		}
	})
	t.Run("shutdown, then the reconciler", func(t *testing.T) {
		p := e.histDep(t, app, "dep_stop", "files", "", now-10, now-10)
		stopped, cancel := context.WithCancel(ctx)
		cancel()
		p.finish(stopped, errCancelled)
		r, ok := e.histRow(t, "dep_stop")
		if !ok || r.result != "failed" || r.code != "interrupted" || r.finished < now {
			t.Fatalf("row = %+v, %v", r, ok)
		}
		// After the restart the reconciler fails the deployment; the row stays one.
		e.s.reconcileDeployments(ctx)
		if state, code, _ := e.depRow(t, "dep_stop"); state != "failed" || code != "interrupted" {
			t.Errorf("deployment %s %s", state, code)
		}
		var n int
		e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM build_history WHERE deployment_id = 'dep_stop'`).Scan(&n)
		if n != 1 {
			t.Errorf("%d rows", n)
		}
	})
	t.Run("crash: the reconciler writes it", func(t *testing.T) {
		e.histDep(t, app, "dep_crash", "files", "", now-5, now-5)
		e.s.reconcileDeployments(ctx)
		if r, ok := e.histRow(t, "dep_crash"); !ok || r.result != "failed" || r.code != "interrupted" {
			t.Errorf("row = %+v, %v", r, ok)
		}
	})
	t.Run("rollback builds nothing", func(t *testing.T) {
		p := e.histDep(t, app, "dep_rb", "rollback", "", now, now)
		p.finish(ctx, apperr.Unprocessable("start_failed", "x", "y"))
		if _, ok := e.histRow(t, "dep_rb"); ok {
			t.Error("a rollback has a history row")
		}
	})
	t.Run("the app is gone", func(t *testing.T) {
		p := e.histDep(t, app, "dep_gone", "files", "", now-8, now-8)
		p.dep.BuiltAt = sql.NullInt64{Int64: now - 4, Valid: true}
		e.exec(t, `DELETE FROM deployments WHERE id = 'dep_gone'`)
		p.finish(ctx, errCancelled)
		r, ok := e.histRow(t, "dep_gone")
		if !ok || r.result != "cancelled" || r.builtAt == nil || *r.builtAt != now-4 || r.appSlug != app.Slug {
			t.Errorf("row = %+v, %v", r, ok)
		}
	})
}

func TestBuildHistoryPeak(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{Name: "Shop"})
	base := time.Now().Unix() - 1000
	// The build runs from base to base+100 (built). A sample covers the 15
	// seconds before it, so base+115 counts and base+130 does not.
	e.hostSample(t, base-15, 99, 7000) // before
	e.hostSample(t, base+15, 40, 3000)
	e.hostSample(t, base+60, 85.25, 2000)
	e.hostSample(t, base+115, 20, 4000)
	e.hostSample(t, base+130, 95, 7500) // after
	p := e.histDep(t, app, "dep_1", "files", "", base-5, base)
	e.exec(t, `UPDATE deployments SET built_at = ?, state = 'failed', finished_at = ? WHERE id = 'dep_1'`, base+100, base+200)
	p.finish(ctx, apperr.Unprocessable("health_check_failed", "x", "y"))
	r, ok := e.histRow(t, "dep_1")
	if !ok || r.cpu == nil || r.mem == nil || *r.cpu != 85.3 || *r.mem != 4000 {
		t.Fatalf("peak = %+v", r)
	}

	// No samples (off Linux): no peak.
	cpu, mem := e.s.hostPeak(ctx, base+2000, base+2100)
	if cpu.Valid || mem.Valid {
		t.Errorf("peak without samples = %v %v", cpu, mem)
	}
}

func TestBuildStatsPerPerson(t *testing.T) {
	row := func(person, name, app, result string, started, built, finished int64, cpu float64) db.ListBuildHistorySinceRow {
		h := db.BuildHistory{DeploymentID: app + result, PersonID: person, AppSlug: app, Result: result, QueuedAt: started - 5, StartedAt: started, FinishedAt: finished}
		if built > 0 {
			h.BuiltAt = sql.NullInt64{Int64: built, Valid: true}
		}
		if cpu > 0 {
			h.PeakCpu = sql.NullFloat64{Float64: cpu, Valid: true}
			h.PeakMemMb = sql.NullFloat64{Float64: cpu * 100, Valid: true}
		}
		return db.ListBuildHistorySinceRow{BuildHistory: h, PersonName: name}
	}
	got := buildStatsPerPerson([]db.ListBuildHistorySinceRow{
		row("usr_a", "Ann", "shop", "live", 1000, 1060, 1100, 50),   // 60 s
		row("usr_b", "Bob", "blog", "failed", 2000, 0, 2300, 0),     // 300 s, failed before built
		row("usr_a", "Ann", "api", "failed", 3000, 0, 3120, 70.5),   // 120 s
		row("usr_a", "Ann", "shop", "cancelled", 4000, 0, 4010, 30), // 10 s
		row("usr_c", "Cy", "docs", "live", 5000, 5100, 5200, 0),     // 100 s
		row("usr_c", "Cy", "docs", "live", 6000, 6100, 6200, 0),     // 100 s
		row("usr_d", "", "gone", "live", 7000, 7001, 7002, 0),       // 1 s, account gone
		row("usr_e", "Eve", "zero", "failed", 8000, 0, 8000, 0),     // 0 s
	})
	if len(got) != 5 {
		t.Fatalf("%d people: %+v", len(got), got)
	}
	order := []string{"usr_b", "usr_c", "usr_a", "usr_d", "usr_e"}
	for i, id := range order {
		if got[i].UserID != id {
			t.Fatalf("order %d = %s, want %s (%+v)", i, got[i].UserID, id, got)
		}
	}
	a := got[2]
	if a.Builds != 3 || a.Failed != 1 || a.TotalBuildSeconds != 190 || a.AvgBuildSeconds != 63 || a.LongestBuildSeconds != 120 || a.LongestApp != "api" ||
		a.PeakCPU == nil || *a.PeakCPU != 70.5 || a.PeakMemoryMB == nil || *a.PeakMemoryMB != 7050 || a.LastBuildAt == nil || a.LastBuildAt.Unix() != 4000 {
		t.Errorf("ann = %+v", a)
	}
	if b := got[0]; b.Failed != 1 || b.TotalBuildSeconds != 300 || b.LongestApp != "blog" || b.PeakCPU != nil || b.PeakMemoryMB != nil {
		t.Errorf("bob = %+v", b)
	}
	if c := got[1]; c.Builds != 2 || c.AvgBuildSeconds != 100 {
		t.Errorf("cy = %+v", c)
	}
	if e := got[4]; e.LongestApp != "zero" || e.LongestBuildSeconds != 0 {
		t.Errorf("eve = %+v", e)
	}
}

func TestAdminBuildStatsAndList(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	admin := e.adminSession(t)
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	app := e.mustApp(t, CreateAppInput{Name: "Shop"})
	now := time.Now().Unix()
	ins := func(id, person string, started, finished int64, result string) {
		e.exec(t, `INSERT INTO build_history (deployment_id, app_id, app_slug, app_name, owner_id, person_id, channel, source_kind,
			queued_at, started_at, finished_at, result) VALUES (?, ?, ?, 'Shop', ?, ?, 'api', 'files', ?, ?, ?, ?)`,
			id, app.ID, app.Slug, e.user.UserID, person, started-10, started, finished, result)
	}
	ins("dep_1", bob.UserID, now-3600, now-3500, "live")
	ins("dep_2", bob.UserID, now-2*86400, now-2*86400+50, "failed")
	ins("dep_3", e.user.UserID, now-600, now-570, "live")
	ins("dep_4", bob.UserID, now-40*86400, now-40*86400+10, "live") // older than 30 days

	for _, a := range []Actor{e.user, bob} {
		if _, err := e.s.AdminBuildStats(ctx, a, "24h"); depCode(err) != "admin_only" {
			t.Errorf("stats as %s: %v", a.UserID, err)
		}
		if _, err := e.s.AdminListBuilds(ctx, a, bob.UserID, "7d"); depCode(err) != "admin_only" {
			t.Errorf("list as %s: %v", a.UserID, err)
		}
	}
	if _, err := e.s.AdminBuildStats(ctx, admin, "1y"); depCode(err) != "invalid_window" {
		t.Errorf("window: %v", err)
	}

	st, err := e.s.AdminBuildStats(ctx, admin, "")
	if err != nil || st.Window != "24h" || len(st.People) != 2 || st.People[0].UserID != bob.UserID || st.People[0].Builds != 1 ||
		st.People[0].Name != "usr_bob" || st.People[0].Email != "bob@example.com" {
		t.Errorf("24h = %+v, %v", st, err)
	}
	st, _ = e.s.AdminBuildStats(ctx, admin, "7d")
	if len(st.People) != 2 || st.People[0].Builds != 2 || st.People[0].Failed != 1 || st.People[0].TotalBuildSeconds != 150 {
		t.Errorf("7d = %+v", st)
	}

	list, err := e.s.AdminListBuilds(ctx, admin, bob.UserID, "30d")
	if err != nil || len(list) != 2 || list[0].DeploymentID != "dep_1" || list[1].DeploymentID != "dep_2" || list[0].WaitSeconds != 10 ||
		list[0].BuildSeconds != 100 || list[0].Person != "usr_bob" || list[0].App != app.Slug {
		t.Errorf("list = %+v, %v", list, err)
	}
	if _, err := e.s.AdminListBuilds(ctx, admin, "", "30d"); depCode(err) != "user_required" {
		t.Errorf("no user: %v", err)
	}
}

func TestServerMetricsBuilds(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	admin := e.adminSession(t)
	app := e.mustApp(t, CreateAppInput{Name: "Shop"})
	now := time.Now().Unix()
	ins := func(id string, started, built, finished int64) {
		var b any
		if built > 0 {
			b = built
		}
		e.exec(t, `INSERT INTO build_history (deployment_id, app_id, app_slug, app_name, owner_id, person_id, channel, source_kind,
			queued_at, started_at, built_at, finished_at, result) VALUES (?, ?, ?, 'Shop', ?, ?, 'api', 'files', ?, ?, ?, ?, 'live')`,
			id, app.ID, app.Slug, e.user.UserID, e.user.UserID, started-3, started, b, finished)
	}
	ins("dep_old", now-7200, now-7100, now-3000)              // built before the hour: outside 1h although it finished in it
	ins("dep_edge", now-3700, 0, now-3500)                    // failed build that ends inside the hour
	ins("dep_recent", now-600, now-500, now-480)              // inside
	e.histDep(t, app, "dep_now", "files", "", now-30, now-20) // building now

	m, err := e.s.AdminServerMetrics(ctx, admin, "1h")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, b := range m.Builds {
		ids = append(ids, b.DeploymentID)
	}
	if len(m.Builds) != 3 || ids[0] != "dep_edge" || ids[1] != "dep_recent" || ids[2] != "dep_now" {
		t.Fatalf("builds = %v", ids)
	}
	if b := m.Builds[2]; b.Result != "in_progress" || b.EndedAt != nil || b.FinishedAt != nil || b.WaitSeconds != 10 || b.BuildSeconds < 20 || b.Person != e.user.UserID {
		t.Errorf("running = %+v", b)
	}
	if b := m.Builds[1]; b.EndedAt == nil || b.EndedAt.Unix() != now-500 || b.BuildSeconds != 100 || b.WaitSeconds != 3 || b.Result != "live" {
		t.Errorf("recent = %+v", b)
	}
	m, _ = e.s.AdminServerMetrics(ctx, admin, "24h")
	if len(m.Builds) != 4 || m.Builds[0].DeploymentID != "dep_old" {
		t.Errorf("24h = %+v", m.Builds)
	}
}

func TestAdminListUserAuditPage(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	admin := e.adminSession(t)
	for _, u := range []string{"usr_alice", "usr_bob", "usr_alice"} {
		e.exec(t, `INSERT INTO audit_log (ts, user_id, channel, action) VALUES (?, ?, 'api', 'app.create')`, time.Now().Unix(), u)
	}
	if _, _, err := e.s.AdminListUserAuditPage(ctx, e.user, "usr_alice", 10, 0); depCode(err) != "admin_only" {
		t.Errorf("as user: %v", err)
	}
	items, more, err := e.s.AdminListUserAuditPage(ctx, admin, "usr_alice", 1, 0)
	if err != nil || len(items) != 1 || !more || items[0].UserID == nil || *items[0].UserID != "usr_alice" {
		t.Fatalf("page 1 = %+v %v %v", items, more, err)
	}
	items, more, _ = e.s.AdminListUserAuditPage(ctx, admin, "usr_alice", 10, items[0].ID)
	if len(items) != 1 || more || *items[0].UserID != "usr_alice" {
		t.Errorf("page 2 = %+v %v", items, more)
	}
}
