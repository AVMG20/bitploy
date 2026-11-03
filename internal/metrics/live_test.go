package metrics

import (
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

func TestLatestIsLiveFromTheStream(t *testing.T) {
	e := newEnv(t)
	e.m.Now = func() time.Time { return t0 }
	e.m.SetLatestForTest("app_1", Point{TS: t0.Add(-14 * time.Second), CPU: 90, MemBytes: 300 << 20, RxBytes: 1234})

	reading := func(sec int, cpu, sys uint64, mem uint64) dockerx.Stats {
		return dockerx.Stats{Read: t0.Add(time.Duration(sec) * time.Second), CPUTotal: cpu, SystemCPU: sys, OnlineCPUs: 2, MemUsage: mem}
	}
	e.m.tracked["c1"] = &tracked{id: "c1", appID: "app_1", cancel: func() {}, recent: []dockerx.Stats{
		reading(-5, 0, 0, 1<<20),
		reading(-1, 100, 1000, 40<<20), // 10% of the host x 2 cpus = 20
	}}
	e.m.tracked["c2"] = &tracked{id: "c2", appID: "app_1", cancel: func() {}, recent: []dockerx.Stats{
		reading(-4, 0, 0, 1<<20),
		reading(-2, 50, 1000, 10<<20), // 10
	}}

	p, ok := e.m.Latest("app_1")
	if !ok || !near(p.CPU, 30) || p.MemBytes != 50<<20 || !p.TS.Equal(t0.Add(-time.Second)) || p.RxBytes != 1234 {
		t.Errorf("live = %+v, %v", p, ok)
	}
	if all := e.m.LatestAll(); !near(all["app_1"].CPU, 30) {
		t.Errorf("LatestAll = %+v", all)
	}

	// Readings older than liveMaxAge do not count: back to the stored sample.
	e.m.Now = func() time.Time { return t0.Add(time.Minute) }
	if p, _ := e.m.Latest("app_1"); p.CPU != 90 || p.MemBytes != 300<<20 {
		t.Errorf("stale stream: %+v", p)
	}

	// A new app has live numbers before its first stored sample.
	e.m.Now = func() time.Time { return t0 }
	e.m.tracked["c3"] = &tracked{id: "c3", appID: "app_new", cancel: func() {}, recent: []dockerx.Stats{
		reading(-3, 0, 0, 1<<20), reading(-1, 0, 1000, 8<<20),
	}}
	if p, ok := e.m.Latest("app_new"); !ok || p.MemBytes != 8<<20 {
		t.Errorf("new app = %+v, %v", p, ok)
	}
}
