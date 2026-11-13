package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

func insertHostSample(ctx context.Context, q *db.Queries, ts int64, cpu float64, used, total int64) error {
	return q.OpsInsertHostSample(ctx, db.OpsInsertHostSampleParams{Ts: ts, Cpu: cpu, MemUsedBytes: used, MemTotalBytes: total})
}

func TestHostTickStoresCPUBetweenTicksAndMemory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const gb = 1 << 30
	cpu := CPUTimes{Busy: 1000, Total: 10000}
	mem := MemInfo{Total: 16 * gb, Available: 12 * gb}
	var readErr error
	e.m.host.read = func() (CPUTimes, MemInfo, error) { return cpu, mem, readErr }
	count := func() (n int) { e.m.Store.DB.QueryRow(`SELECT COUNT(*) FROM host_samples`).Scan(&n); return }

	// The first reading is only the base.
	if err := e.m.HostTick(ctx, t0); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Fatalf("first tick stored %d samples", n)
	}

	// 15 s later: 300 of 1000 jiffies busy is 30% of the whole server.
	cpu = CPUTimes{Busy: 1300, Total: 11000}
	if err := e.m.HostTick(ctx, t0.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	var ts, used, total int64
	var pct float64
	if err := e.m.Store.DB.QueryRow(`SELECT ts, cpu, mem_used_bytes, mem_total_bytes FROM host_samples`).Scan(&ts, &pct, &used, &total); err != nil {
		t.Fatal(err)
	}
	if ts != t0.Unix()+15 || !near(pct, 30) || used != 4*gb || total != 16*gb {
		t.Errorf("sample = ts %d, cpu %v, used %d, total %d", ts, pct, used, total)
	}

	// A counter that went backwards gives 0, never a negative or huge value.
	cpu = CPUTimes{Busy: 10, Total: 20}
	if err := e.m.HostTick(ctx, t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	e.m.Store.DB.QueryRow(`SELECT cpu FROM host_samples ORDER BY ts DESC LIMIT 1`).Scan(&pct)
	if pct != 0 {
		t.Errorf("cpu after a counter reset = %v", pct)
	}

	// A failed read stores nothing.
	readErr = errors.New("no /proc")
	if err := e.m.HostTick(ctx, t0.Add(45*time.Second)); err == nil {
		t.Error("expected the read error")
	}
	if n := count(); n != 2 {
		t.Errorf("samples = %d, want 2", n)
	}
}

func TestRollupHost(t *testing.T) {
	samples := []HostSample{
		{TS: 0, CPU: 10, MemUsed: 100, MemTotal: 1000},
		{TS: 15, CPU: 30, MemUsed: 300, MemTotal: 2000},
		{TS: 300, CPU: 50, MemUsed: 500, MemTotal: 1000},
	}
	got := RollupHost(samples)
	if len(got) != 2 {
		t.Fatalf("buckets = %d", len(got))
	}
	if b := got[0]; b.TS.Unix() != 0 || !near(b.CPU, 20) || b.MemUsed != 200 || b.MemTotal != 2000 || b.CPUMax != 30 || b.MemMax != 300 {
		t.Errorf("first bucket = %+v", b)
	}
	if b := got[1]; b.TS.Unix() != 300 || b.CPU != 50 || b.MemTotal != 1000 {
		t.Errorf("second bucket = %+v", b)
	}
	if len(RollupHost(nil)) != 0 {
		t.Error("buckets from nothing")
	}
}

func TestHostRollupAndSeries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const total = 8 << 30

	// Samples every 15 s for the last 2 hours.
	start := t0.Add(-2 * time.Hour).Unix()
	err := e.m.Store.Tx(ctx, func(q *dbQ) error {
		for ts := start; ts < t0.Unix(); ts += 15 {
			if err := insertHostSample(ctx, q, ts, 10, 1<<30, total); err != nil {
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
	if err := e.m.Store.DB.QueryRow(`SELECT COUNT(*), MAX(ts) FROM host_rollups`).Scan(&rolls, &last); err != nil {
		t.Fatal(err)
	}
	if rolls != 24 || last != BucketStart(t0.Unix())-BucketSeconds {
		t.Errorf("host rollups = %d, last = %d", rolls, last)
	}
	// Running it again changes nothing.
	if err := e.m.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	e.m.Store.DB.QueryRow(`SELECT COUNT(*) FROM host_rollups`).Scan(&rolls)
	if rolls != 24 {
		t.Errorf("second rollup gave %d rows", rolls)
	}

	// Time passes; 7 minutes of fresh raw samples are not rolled up yet.
	e.now = t0.Add(7 * time.Minute)
	e.m.Store.Tx(ctx, func(q *dbQ) error {
		for ts := t0.Unix(); ts < e.now.Unix(); ts += 15 {
			insertHostSample(ctx, q, ts, 50, 2<<30, total)
		}
		return nil
	})

	raw, err := e.m.HostSeries(ctx, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 240 || raw[0].CPUMax != 0 || raw[0].MemTotal != total {
		t.Errorf("1h series: %d points, first %+v", len(raw), raw[0])
	}

	day, err := e.m.HostSeries(ctx, "24h")
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
	if day[0].CPU != 10 || day[0].MemUsed != 1<<30 || day[25].CPU != 50 || day[25].CPUMax != 50 || day[25].MemMax != 2<<30 || day[25].MemTotal != total {
		t.Errorf("points: first %+v last %+v", day[0], day[25])
	}

	if week, err := e.m.HostSeries(ctx, "7d"); err != nil || len(week) != 26 {
		t.Errorf("7d series: %d points, %v", len(week), err)
	}
	if _, err := e.m.HostSeries(ctx, "30d"); err == nil {
		t.Error("expected an error for an unknown window")
	}
}

func TestHostSeriesEmpty(t *testing.T) {
	e := newEnv(t)
	for _, w := range []string{"1h", "24h", "7d"} {
		got, err := e.m.HostSeries(context.Background(), w)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("%s: %v, %v", w, got, err)
		}
	}
}

func TestPruneHostMetrics(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(t, `INSERT INTO host_samples (ts, cpu, mem_used_bytes, mem_total_bytes) VALUES (?, 1, 1, 1), (?, 1, 1, 1)`,
		t0.Add(-25*time.Hour).Unix(), t0.Add(-23*time.Hour).Unix())
	e.exec(t, `INSERT INTO host_rollups (ts, cpu, mem_used_bytes, mem_total_bytes, cpu_max, mem_max) VALUES (?, 1, 1, 1, 1, 1), (?, 1, 1, 1, 1, 1)`,
		t0.Add(-31*24*time.Hour).Unix(), t0.Add(-29*24*time.Hour).Unix())
	if err := e.m.PruneMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (n int) { e.m.Store.DB.QueryRow(q).Scan(&n); return }
	if n := count(`SELECT COUNT(*) FROM host_samples`); n != 1 {
		t.Errorf("host samples left = %d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM host_rollups`); n != 1 {
		t.Errorf("host rollups left = %d, want 1", n)
	}
}

func TestRunHostCollectorWritesOnTheSampleInterval(t *testing.T) {
	e := newEnv(t)
	e.m.SampleInterval = 10 * time.Millisecond
	busy := uint64(0)
	e.m.host.read = func() (CPUTimes, MemInfo, error) {
		busy += 10
		return CPUTimes{Busy: busy, Total: busy * 4}, MemInfo{Total: 4 << 30, Available: 3 << 30}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.m.runHostCollector(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		e.m.Store.DB.QueryRow(`SELECT COUNT(*) FROM host_samples`).Scan(&n)
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if n == 0 {
		t.Fatal("the host collector stored nothing")
	}
	var pct float64
	e.m.Store.DB.QueryRow(`SELECT cpu FROM host_samples LIMIT 1`).Scan(&pct)
	if !near(pct, 25) {
		t.Errorf("cpu = %v, want 25", pct)
	}
}

func TestPruneBuildHistory(t *testing.T) {
	e := newEnv(t)
	ins := func(id string, finished time.Time) {
		e.exec(t, `INSERT INTO build_history (deployment_id, app_id, app_slug, app_name, owner_id, person_id, channel, source_kind,
			queued_at, started_at, finished_at, result) VALUES (?, 'app_1', 'web', 'Web', 'usr_1', 'usr_1', 'api', 'files', ?, ?, ?, 'live')`,
			id, finished.Unix()-60, finished.Unix()-50, finished.Unix())
	}
	ins("dep_old", t0.Add(-91*24*time.Hour))
	ins("dep_kept", t0.Add(-89*24*time.Hour))
	if err := e.m.PruneMetrics(context.Background()); err != nil {
		t.Fatal(err)
	}
	var left string
	if err := e.m.Store.DB.QueryRow(`SELECT group_concat(deployment_id) FROM build_history`).Scan(&left); err != nil || left != "dep_kept" {
		t.Errorf("left = %q, %v; want dep_kept", left, err)
	}
}
