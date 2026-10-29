package metrics

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/store"
)

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// fakeDocker records what the manager asks Docker to do.
type fakeDocker struct {
	mu        sync.Mutex
	stopped   []string
	logs      []dockerx.LogEntry
	state     dockerx.State
	pruned    bool
	diskUsage dockerx.DiskUsage
}

func (f *fakeDocker) ManagedContainers(context.Context, bool) ([]dockerx.Managed, error) {
	return nil, nil
}
func (f *fakeDocker) StreamStats(ctx context.Context, _ string, _ func(dockerx.Stats) error) error {
	<-ctx.Done()
	return nil
}
func (f *fakeDocker) WatchEvents(ctx context.Context, _ time.Time, _ func(dockerx.Event)) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeDocker) InspectState(context.Context, string) (dockerx.State, error) {
	return f.state, nil
}
func (f *fakeDocker) StopContainerGrace(_ context.Context, name string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, name)
	return nil
}
func (f *fakeDocker) TailLogs(context.Context, string, int, time.Time) ([]dockerx.LogEntry, error) {
	return f.logs, nil
}
func (f *fakeDocker) DiskUsage(context.Context, bool) (dockerx.DiskUsage, error) {
	return f.diskUsage, nil
}
func (f *fakeDocker) PruneDanglingImages(context.Context) (uint64, error) {
	f.pruned = true
	return 1, nil
}
func (f *fakeDocker) PruneBuildCache(context.Context, int64) (uint64, error) { return 2, nil }

type auditRow struct{ action, target string }

type env struct {
	m      *Manager
	dk     *fakeDocker
	audits []auditRow
	synced []string
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{dk: &fakeDocker{}, now: t0}
	e.m = New(st, e.dk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.m.Now = func() time.Time { return e.now }
	e.m.DataDir = t.TempDir()
	e.m.Audit = func(_ context.Context, action, _, targetID string, _ map[string]any) {
		e.audits = append(e.audits, auditRow{action, targetID})
	}
	e.m.SyncRoute = func(_ context.Context, appID string) error {
		e.synced = append(e.synced, appID)
		return nil
	}
	return e
}

func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.m.Store.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// addApp inserts a user (once), an app and a current deployment with the given container.
func (e *env) addApp(t *testing.T, id, container, status string) {
	t.Helper()
	e.exec(t, `INSERT OR IGNORE INTO users (id, email, name, password_hash, created_at, updated_at) VALUES ('usr_1','a@b.nl','A','x',1,1)`)
	e.exec(t, `INSERT INTO apps (id, user_id, slug, name, status, current_deployment_id, created_at, updated_at) VALUES (?, 'usr_1', ?, ?, ?, ?, 1, 1)`,
		id, "slug-"+id, id, status, "dep_"+id)
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, container_name, queued_at) VALUES (?, ?, 1, 'running', 'files', 'api', ?, 1)`,
		"dep_"+id, id, container)
}

func (e *env) app(t *testing.T, id string) (status, msg, desired string) {
	t.Helper()
	err := e.m.Store.DB.QueryRow(`SELECT status, status_message, desired_state FROM apps WHERE id = ?`, id).Scan(&status, &msg, &desired)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func (e *env) actions() []string {
	var out []string
	for _, a := range e.audits {
		out = append(out, a.action)
	}
	return out
}

func TestTickWritesOneSamplePerAppAndCountsRunningTime(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")

	// Two containers of one app (old and new during a swap) are summed.
	mk := func(id string, cpu, sys uint64, mem uint64) *tracked {
		return &tracked{
			id: id, name: id, appID: "app_1", cancel: func() {},
			haveCur: true, haveBase: true,
			base: dockerx.Stats{CPUTotal: 0, SystemCPU: 0, OnlineCPUs: 2, RxBytes: 100, TxBytes: 50},
			cur:  dockerx.Stats{CPUTotal: cpu, SystemCPU: sys, OnlineCPUs: 2, MemUsage: mem + 1000, InactiveFile: 1000, RxBytes: 600, TxBytes: 70},
		}
	}
	e.m.tracked["c1"] = mk("c1", 500, 2000, 10<<20) // 25% of the host x 2 cpus = 50
	e.m.tracked["c2"] = mk("c2", 100, 2000, 5<<20)  // 10
	e.m.tracked["gone"] = &tracked{id: "gone", appID: "app_1", cancel: func() {}}

	if err := e.m.Tick(ctx, t0, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	var cpu float64
	var mem, rx, tx int64
	var n int
	err := e.m.Store.DB.QueryRow(`SELECT COUNT(*), SUM(cpu), SUM(mem_bytes), SUM(rx_bytes), SUM(tx_bytes) FROM metric_samples WHERE app_id='app_1'`).Scan(&n, &cpu, &mem, &rx, &tx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !near(cpu, 60) || mem != 15<<20 || rx != 1000 || tx != 40 {
		t.Errorf("sample = n %d cpu %v mem %d rx %d tx %d", n, cpu, mem, rx, tx)
	}
	p, ok := e.m.Latest("app_1")
	if !ok || !near(p.CPU, 60) || p.MemBytes != 15<<20 {
		t.Errorf("latest = %+v, %v", p, ok)
	}

	var secs int64
	if err := e.m.Store.DB.QueryRow(`SELECT running_seconds FROM usage_daily WHERE app_id='app_1' AND day='2026-10-29' AND user_id='usr_1'`).Scan(&secs); err != nil || secs != 15 {
		t.Errorf("running seconds = %d, %v", secs, err)
	}

	// Counters were rebased: the next tick sees only the growth since.
	e.m.tracked["c1"].cur.RxBytes = 700
	e.m.tracked["c2"].cur.RxBytes = 600
	if err := e.m.Tick(ctx, t0.Add(15*time.Second), 15*time.Second); err != nil {
		t.Fatal(err)
	}
	p, _ = e.m.Latest("app_1")
	if p.RxBytes != 100 {
		t.Errorf("second tick rx = %d, want 100", p.RxBytes)
	}
	if err := e.m.Store.DB.QueryRow(`SELECT running_seconds FROM usage_daily WHERE app_id='app_1'`).Scan(&secs); err != nil || secs != 30 {
		t.Errorf("running seconds after two ticks = %d, %v", secs, err)
	}

	// A long pause (laptop sleep) books at most two intervals.
	if err := e.m.Tick(ctx, t0.Add(10*time.Hour), 10*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Store.DB.QueryRow(`SELECT running_seconds FROM usage_daily WHERE app_id='app_1'`).Scan(&secs); err != nil || secs != 60 {
		t.Errorf("running seconds after a pause = %d, %v", secs, err)
	}

	// Once the containers are gone the app disappears from the live view.
	e.m.tracked = map[string]*tracked{}
	if err := e.m.Tick(ctx, t0.Add(10*time.Hour+15*time.Second), 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.m.Latest("app_1"); ok {
		t.Error("stopped app still has a latest sample")
	}
}

func TestRollupAndSeries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")

	// Samples every 15 s for the last 2 hours.
	e.now = t0
	start := t0.Add(-2 * time.Hour).Unix()
	err := e.m.Store.Tx(ctx, func(q *dbQ) error {
		for ts := start; ts < t0.Unix(); ts += 15 {
			if err := insertSample(ctx, q, "app_1", ts, 10, 100, 1, 2); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.m.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	var rolls int
	var last int64
	if err := e.m.Store.DB.QueryRow(`SELECT COUNT(*), MAX(ts) FROM metric_rollups`).Scan(&rolls, &last); err != nil {
		t.Fatal(err)
	}
	// 2 hours = 24 buckets; t0 is a bucket boundary so all of them are complete.
	if rolls != 24 || last != BucketStart(t0.Unix())-BucketSeconds {
		t.Errorf("rollups = %d, last = %d", rolls, last)
	}
	var rx int64
	e.m.Store.DB.QueryRow(`SELECT rx_bytes FROM metric_rollups LIMIT 1`).Scan(&rx)
	if rx != 20 { // 20 samples per bucket x 1 byte
		t.Errorf("rollup rx = %d, want 20", rx)
	}

	// Running it again changes nothing.
	if err := e.m.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	e.m.Store.DB.QueryRow(`SELECT COUNT(*) FROM metric_rollups`).Scan(&rolls)
	if rolls != 24 {
		t.Errorf("second rollup gave %d rows", rolls)
	}

	// Time passes; 7 minutes of fresh raw samples are not rolled up yet.
	e.now = t0.Add(7 * time.Minute)
	e.m.Store.Tx(ctx, func(q *dbQ) error {
		for ts := t0.Unix(); ts < e.now.Unix(); ts += 15 {
			insertSample(ctx, q, "app_1", ts, 50, 200, 1, 1)
		}
		return nil
	})

	raw, err := e.m.Series(ctx, "app_1", "1h")
	if err != nil {
		t.Fatal(err)
	}
	// 1 h of history = 240 samples, of which 28 are the fresh ones.
	if len(raw) != 240 || raw[0].CPUMax != 0 {
		t.Errorf("1h series: %d points", len(raw))
	}

	day, err := e.m.Series(ctx, "app_1", "24h")
	if err != nil {
		t.Fatal(err)
	}
	// 24 rollups + the fresh raw samples folded into 2 buckets (one complete, one running).
	if len(day) != 26 {
		t.Fatalf("24h series: %d points, want 26", len(day))
	}
	for i := 1; i < len(day); i++ {
		if !day[i].TS.After(day[i-1].TS) {
			t.Fatalf("series not ascending at %d", i)
		}
	}
	if day[25].CPU != 50 || day[25].CPUMax != 50 || day[0].CPU != 10 {
		t.Errorf("points: first %+v last %+v", day[0], day[25])
	}

	week, err := e.m.Series(ctx, "app_1", "7d")
	if err != nil || len(week) != 26 {
		t.Errorf("7d series: %d points, %v", len(week), err)
	}
	if _, err := e.m.Series(ctx, "app_1", "30d"); err == nil {
		t.Error("expected an error for an unknown window")
	}
	empty, err := e.m.Series(ctx, "app_nope", "24h")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("unknown app: %v, %v", empty, err)
	}
}

func TestPruneMetrics(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	e.exec(t, `INSERT INTO metric_samples VALUES ('app_1', ?, 1, 1, 1, 1), ('app_1', ?, 1, 1, 1, 1), ('app_gone', ?, 1, 1, 1, 1)`,
		t0.Add(-25*time.Hour).Unix(), t0.Add(-23*time.Hour).Unix(), t0.Unix())
	e.exec(t, `INSERT INTO metric_rollups VALUES ('app_1', ?, 1, 1, 1, 1, 1, 1), ('app_1', ?, 1, 1, 1, 1, 1, 1)`,
		t0.Add(-31*24*time.Hour).Unix(), t0.Add(-29*24*time.Hour).Unix())
	e.exec(t, `INSERT INTO app_disk VALUES ('app_gone', 1, 1, 1)`)
	if err := e.m.PruneMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) { e.m.Store.DB.QueryRow(q).Scan(&n); return }
	if n := count(`SELECT COUNT(*) FROM metric_samples`); n != 1 {
		t.Errorf("samples left = %d, want 1 (only the fresh sample of a live app)", n)
	}
	if n := count(`SELECT COUNT(*) FROM metric_rollups`); n != 1 {
		t.Errorf("rollups left = %d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM app_disk`); n != 0 {
		t.Errorf("orphan app_disk rows = %d", n)
	}
}

func TestBackupDBKeepsSeven(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	for i := 0; i < 9; i++ {
		e.now = t0.AddDate(0, 0, i)
		if err := e.m.BackupDB(ctx); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(e.m.BackupDir())
	if len(entries) != 7 {
		t.Fatalf("%d backups kept, want 7", len(entries))
	}
	if entries[0].Name() != "bitploy-20261031T120000Z.db" {
		t.Errorf("oldest kept = %s", entries[0].Name())
	}
	// The copy is a usable database.
	b, err := sql.Open("sqlite", "file:"+filepath.Join(e.m.BackupDir(), entries[6].Name())+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var n int
	if err := b.QueryRow(`SELECT COUNT(*) FROM apps`).Scan(&n); err != nil || n != 1 {
		t.Errorf("backup has %d apps, %v", n, err)
	}
}

func TestDeleteOldUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	dir := filepath.Join(e.m.DataDir, "uploads")
	os.MkdirAll(dir, 0o750)
	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("zip"), 0o640)
		old := e.now.Add(-age)
		os.Chtimes(p, old, old)
	}
	e.m.Now = func() time.Time { return time.Now() }
	e.now = time.Now()
	write("upl_old.zip", 30*time.Hour)
	write("upl_new.zip", time.Hour)
	write("upl_orphan.zip", 48*time.Hour)
	e.exec(t, `INSERT INTO uploads (id, app_id, user_id, expires_at, created_at) VALUES ('upl_old','app_1','usr_1',1,?), ('upl_new','app_1','usr_1',1,?)`,
		time.Now().Add(-30*time.Hour).Unix(), time.Now().Add(-time.Hour).Unix())

	if err := e.m.DeleteOldUploads(ctx); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"upl_old.zip": false, "upl_orphan.zip": false, "upl_new.zip": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
	var n int
	e.m.Store.DB.QueryRow(`SELECT COUNT(*) FROM uploads`).Scan(&n)
	if n != 1 {
		t.Errorf("%d upload rows left, want 1", n)
	}
}

func TestPruneAudit(t *testing.T) {
	e := newEnv(t)
	e.exec(t, `INSERT INTO audit_log (ts, channel, action) VALUES (?, 'api', 'old'), (?, 'api', 'fresh')`,
		t0.Add(-91*24*time.Hour).Unix(), t0.Add(-89*24*time.Hour).Unix())
	if err := e.m.PruneAudit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var a string
	if err := e.m.Store.DB.QueryRow(`SELECT action FROM audit_log`).Scan(&a); err != nil || a != "fresh" {
		t.Errorf("audit rows left: %q, %v", a, err)
	}
}

func TestMeasureDiskAndDockerPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	e.addApp(t, "app_2", "bp-b-1", "running")
	e.dk.diskUsage = dockerx.DiskUsage{
		Images: []dockerx.DiskImage{
			{AppID: "app_1", Size: 100 << 20}, {AppID: "app_1", Size: 150 << 20}, {AppID: "", Size: 9 << 30},
		},
		Volumes: []dockerx.DiskVolume{{AppID: "app_1", Name: "bp-slug-app_1-data", Size: 7 << 20}, {Name: "bp-slug-app_2-data", Size: 3 << 20}},
	}
	if err := e.m.MeasureDisk(ctx); err != nil {
		t.Fatal(err)
	}
	get := func(id string) (img, vol int64) {
		e.m.Store.DB.QueryRow(`SELECT image_bytes, volume_bytes FROM app_disk WHERE app_id=?`, id).Scan(&img, &vol)
		return
	}
	if img, vol := get("app_1"); img != 150<<20 || vol != 7<<20 {
		t.Errorf("app_1 disk = %d / %d", img, vol)
	}
	if img, vol := get("app_2"); img != 0 || vol != 3<<20 {
		t.Errorf("app_2 disk = %d / %d (volume found by name)", img, vol)
	}

	e.m.Dev = true
	if err := e.m.PruneDocker(ctx); err != nil || e.dk.pruned {
		t.Errorf("dev mode must not prune: pruned=%v err=%v", e.dk.pruned, err)
	}
	e.m.Dev = false
	if err := e.m.PruneDocker(ctx); err != nil || !e.dk.pruned {
		t.Errorf("prune: pruned=%v err=%v", e.dk.pruned, err)
	}
}

// ---- Docker events ----

func die(name, appID, exit string, at time.Time) dockerx.Event {
	return dockerx.Event{Action: "die", ID: "id-" + name, Name: name, AppID: appID, ExitCode: exit, Time: at}
}

func TestEventOOM(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")

	e.m.HandleEvent(ctx, dockerx.Event{Action: "oom", ID: "c", Name: "bp-a-1", AppID: "app_1", Time: t0})
	st, msg, _ := e.app(t, "app_1")
	if st != "failed" || msg != MsgOOM {
		t.Fatalf("after oom: %s / %q", st, msg)
	}
	// The die event that follows keeps the OOM message even if Docker no longer reports OOMKilled.
	e.m.HandleEvent(ctx, die("bp-a-1", "app_1", "137", t0))
	if st, msg, _ = e.app(t, "app_1"); st != "failed" || msg != MsgOOM {
		t.Errorf("after die: %s / %q", st, msg)
	}
	// Docker restarts the container: running again, but the owner still sees why.
	e.m.HandleEvent(ctx, dockerx.Event{Action: "start", ID: "c", Name: "bp-a-1", AppID: "app_1", Time: t0})
	if st, msg, _ = e.app(t, "app_1"); st != "running" || msg != MsgOOM {
		t.Errorf("after restart: %s / %q", st, msg)
	}
	if got := strings.Join(e.actions(), ","); !strings.Contains(got, "app.oom") {
		t.Errorf("audit actions = %s", got)
	}
}

func TestEventDieAndRestart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")

	e.m.HandleEvent(ctx, die("bp-a-1", "app_1", "1", t0))
	st, msg, _ := e.app(t, "app_1")
	if st != "failed" || !strings.Contains(msg, "exit code 1") {
		t.Fatalf("after die: %s / %q", st, msg)
	}
	e.m.HandleEvent(ctx, dockerx.Event{Action: "start", ID: "c", Name: "bp-a-1", AppID: "app_1", Time: t0})
	if st, msg, _ = e.app(t, "app_1"); st != "running" || msg != "" {
		t.Errorf("after restart: %s / %q", st, msg)
	}
	if got := e.actions(); len(got) != 1 || got[0] != "app.container_died" {
		t.Errorf("audit actions = %v", got)
	}
}

func TestEventOOMDetectedByInspect(t *testing.T) {
	e := newEnv(t)
	e.addApp(t, "app_1", "bp-a-1", "running")
	e.dk.state = dockerx.State{OOMKilled: true}
	e.m.HandleEvent(context.Background(), die("bp-a-1", "app_1", "137", t0))
	if _, msg, _ := e.app(t, "app_1"); msg != MsgOOM {
		t.Errorf("message = %q", msg)
	}
}

func TestEventsIgnoredWhenNotTheCurrentRunningContainer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-2", "running")
	e.addApp(t, "app_2", "bp-b-1", "deploying") // being redeployed: the old container dies on purpose
	e.addApp(t, "app_3", "bp-c-1", "running")
	e.exec(t, `UPDATE apps SET desired_state='stopped' WHERE id='app_3'`) // user stopped it

	e.m.HandleEvent(ctx, die("bp-a-1", "app_1", "0", t0))    // superseded container after a swap
	e.m.HandleEvent(ctx, die("bp-b-1", "app_2", "0", t0))    // status deploying
	e.m.HandleEvent(ctx, die("bp-c-1", "app_3", "0", t0))    // desired state stopped
	e.m.HandleEvent(ctx, die("bp-x-1", "app_nope", "1", t0)) // app was deleted

	for _, id := range []string{"app_1", "app_2", "app_3"} {
		st, msg, _ := e.app(t, id)
		want := map[string]string{"app_1": "running", "app_2": "deploying", "app_3": "running"}[id]
		if st != want || msg != "" {
			t.Errorf("%s changed to %s / %q", id, st, msg)
		}
	}
	if len(e.audits) != 0 {
		t.Errorf("unexpected audit rows: %v", e.audits)
	}
}

func TestEventUnhealthyAndHealthy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	ev := func(h string) dockerx.Event {
		return dockerx.Event{Action: "health_status", Health: h, ID: "c", Name: "bp-a-1", AppID: "app_1", Time: t0}
	}
	e.m.HandleEvent(ctx, ev("unhealthy"))
	if st, msg, _ := e.app(t, "app_1"); st != "failed" || !strings.HasPrefix(msg, "Health check failing") {
		t.Fatalf("unhealthy: %s / %q", st, msg)
	}
	e.m.HandleEvent(ctx, ev("healthy"))
	if st, msg, _ := e.app(t, "app_1"); st != "running" || msg != "" {
		t.Errorf("healthy again: %s / %q", st, msg)
	}
	// A deploy failure message must not be cleared by a healthy event.
	e.exec(t, `UPDATE apps SET status='failed', status_message='Build failed' WHERE id='app_1'`)
	e.m.HandleEvent(ctx, ev("healthy"))
	if st, msg, _ := e.app(t, "app_1"); st != "failed" || msg != "Build failed" {
		t.Errorf("deploy failure was overwritten: %s / %q", st, msg)
	}
}

func TestCrashLoopStopsContainerAndMarksCrashed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "bp-a-1", "running")
	e.dk.logs = []dockerx.LogEntry{
		{Text: "starting"}, {Text: "panic: cannot open /data/db token=bp_" + strings.Repeat("a", 30)},
	}

	for i := 0; i < 5; i++ {
		at := t0.Add(time.Duration(i) * 40 * time.Second)
		e.now = at
		e.m.HandleEvent(ctx, die("bp-a-1", "app_1", "1", at))
		if i < 4 {
			e.m.HandleEvent(ctx, dockerx.Event{Action: "start", ID: "c", Name: "bp-a-1", AppID: "app_1", Time: at})
		}
	}
	st, msg, desired := e.app(t, "app_1")
	if st != "crashed" || desired != "stopped" {
		t.Fatalf("status %s, desired %s", st, desired)
	}
	if !strings.Contains(msg, "5 times in 5 minutes") || !strings.Contains(msg, "panic: cannot open /data/db") {
		t.Errorf("message lacks the reason or log tail: %q", msg)
	}
	if strings.Contains(msg, strings.Repeat("a", 30)) {
		t.Errorf("token leaked into the status message: %q", msg)
	}
	if len(e.dk.stopped) != 1 || e.dk.stopped[0] != "bp-a-1" {
		t.Errorf("stopped = %v", e.dk.stopped)
	}
	if len(e.synced) != 1 || e.synced[0] != "app_1" {
		t.Errorf("route syncs = %v", e.synced)
	}
	if a := e.actions(); a[len(a)-1] != "app.crashed" {
		t.Errorf("audit actions = %v", a)
	}
	// Further events of a crashed app change nothing.
	e.m.HandleEvent(ctx, die("bp-a-1", "app_1", "1", e.now))
	if st, _, _ = e.app(t, "app_1"); st != "crashed" {
		t.Errorf("crashed app changed to %s", st)
	}
}

func TestRunWithoutDockerReturnsOnCancel(t *testing.T) {
	e := newEnv(t)
	e.m.Docker = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.m.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	// None of these may panic without Docker.
	e.m.HandleEvent(context.Background(), die("x", "app_1", "1", t0))
	if err := e.m.MeasureDisk(context.Background()); err != nil {
		t.Error(err)
	}
	if du, err := e.m.DockerDisk(context.Background()); err != nil || du.ImagesBytes != 0 {
		t.Error(du, err)
	}
}
