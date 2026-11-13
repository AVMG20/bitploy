package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/diskuse"
)

func TestDiskLevels(t *testing.T) {
	for _, c := range []struct {
		builds           int
		cancel, critical float64
	}{
		{15, 0.10, 0.05}, // half is 7.5%: capped at 5%
		{4, 0.04, 0.02},  // the cancel level is never above the builds threshold
		{1, 0.01, 0.01},  // half is 0.5%: at least 1%
		{50, 0.10, 0.05}, // a careful admin does not make apps stop at 25% free
	} {
		lv := diskLevelsOf(diskSettings{builds: c.builds})
		if lv.builds != float64(c.builds)/100 || lv.cancel != c.cancel || lv.critical != c.critical {
			t.Errorf("builds %d%%: %+v, want critical %v", c.builds, lv, c.critical)
		}
	}
}

// One guard over a falling, then recovering disk: what each look asks for.
func TestDiskGuardPlan(t *testing.T) {
	lv := diskLevels{builds: 0.15, cancel: 0.10, critical: 0.05}
	var g diskGuard
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		at   time.Duration
		free float64
		want diskGuardPlan
		// handled: the caller acted on the look (no cleanup was running).
		handled bool
	}{
		{0, 0.30, diskGuardPlan{}, false},
		// Below the builds threshold: pause (and ask the cleanup to run), check /data now,
		// measure the apps (baseline). Running builds get one look for the cleanup to help.
		{30 * time.Second, 0.14, diskGuardPlan{pause: true, measureVolumes: true, measureApps: true}, false},
		// Still low but above the cancel level: announce the pause, running builds go on.
		// The cleanup still runs, so the caller waits.
		{time.Minute, 0.13, diskGuardPlan{announce: true}, false},
		{80 * time.Second, 0.13, diskGuardPlan{announce: true}, true},
		// Below the cancel level: stop the builds. Falling fast, but the /data check ran
		// a minute ago. The cleanup runs again, so the caller waits.
		{90 * time.Second, 0.09, diskGuardPlan{stopBuilds: true}, false},
		{100 * time.Second, 0.085, diskGuardPlan{stopBuilds: true}, true},
		// Builds stopped: nothing more until the heavy gap passed.
		{2 * time.Minute, 0.12, diskGuardPlan{}, false},
		// Critical after five minutes: measure and stop the grower; /data was checked under ten minutes ago.
		{5*time.Minute + 30*time.Second, 0.04, diskGuardPlan{measureApps: true, critical: true}, false},
		// Critical again 30 s later: no second app within the gap.
		{6 * time.Minute, 0.03, diskGuardPlan{}, false},
		// Ten minutes on: /data checked again, apps measured again.
		{11 * time.Minute, 0.03, diskGuardPlan{measureVolumes: true, measureApps: true, critical: true}, false},
		// Above the threshold but within the margin: builds stay paused.
		{12 * time.Minute, 0.16, diskGuardPlan{}, false},
		// Above threshold plus margin: builds run again.
		{13 * time.Minute, 0.18, diskGuardPlan{resume: true}, false},
		{14 * time.Minute, 0.18, diskGuardPlan{}, false},
		// A new episode starts from scratch.
		{15 * time.Minute, 0.10, diskGuardPlan{pause: true, measureVolumes: true, measureApps: true}, false},
	}
	for i, st := range steps {
		got := g.plan(t0.Add(st.at), st.free, lv)
		if got != st.want {
			t.Errorf("look %d (%.0f%% free): %+v, want %+v", i, st.free*100, got, st.want)
		}
		if st.handled {
			g.announced = g.announced || got.announce
			g.cancelled = g.cancelled || got.stopBuilds
		}
		if want := st.free < lv.builds+diskuse.Margin && (st.free < lv.builds || g.paused); g.paused != want {
			t.Errorf("look %d: paused = %v", i, g.paused)
		}
	}
}

// Only an app that writes a lot right now is stopped, never one that is
// merely big or has no earlier measurement.
func TestDiskGrower(t *testing.T) {
	const mb = 1 << 20
	cases := []struct {
		name      string
		prev, cur map[string]int64
		want      string
	}{
		{"no baseline", nil, map[string]int64{"app_a": 9000 * mb}, ""},
		{"big but still", map[string]int64{"app_a": 9000 * mb}, map[string]int64{"app_a": 9100 * mb}, ""},
		{"the fastest grower wins over the biggest",
			map[string]int64{"app_big": 9000 * mb, "app_fast": 10 * mb}, map[string]int64{"app_big": 9300 * mb, "app_fast": 900 * mb}, "app_fast"},
		{"new app without a baseline is left alone",
			map[string]int64{"app_a": 0}, map[string]int64{"app_a": 300 * mb, "app_new": 5000 * mb}, "app_a"},
		{"shrinking", map[string]int64{"app_a": 900 * mb}, map[string]int64{"app_a": 100 * mb}, ""},
		{"tie: the first id", map[string]int64{"app_b": 0, "app_a": 0}, map[string]int64{"app_b": 500 * mb, "app_a": 500 * mb}, "app_a"},
	}
	for _, c := range cases {
		if got, _ := diskGrower(c.prev, c.cur); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// Admins hear about a pause once, about the next one no sooner than an hour
// later, and about builds running again only after they heard of the pause.
func TestDiskGuardMessagesOnce(t *testing.T) {
	var g diskGuard
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if !g.sayPause(t0) || g.sayPause(t0.Add(time.Minute)) {
		t.Fatal("first pause: one message")
	}
	if !g.sayResume() || g.sayResume() {
		t.Fatal("resume after a told pause: one message")
	}
	// The disk drops again ten minutes later: silence, and no resume message either.
	if g.sayPause(t0.Add(10 * time.Minute)) {
		t.Fatal("a pause within the hour was announced")
	}
	if g.sayResume() {
		t.Fatal("resume of an unannounced pause was announced")
	}
	if !g.sayPause(t0.Add(61 * time.Minute)) {
		t.Fatal("a pause after the hour was not announced")
	}
}

// The guard end to end with a fake disk: it stops the running build with
// disk_full_build_cancelled, holds the queue, stops the app that writes the
// most at the critical level, and lets builds run again once space is back.
func TestDiskGuardPausesBuildsAndStopsTheGrower(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	clock := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	e.s.Now = func() time.Time { return clock }
	var mu sync.Mutex
	free := uint64(50)
	e.s.disk.space = func() (diskuse.Space, error) {
		mu.Lock()
		defer mu.Unlock()
		return diskuse.Space{Total: 100, Free: free}, nil
	}
	setFree := func(n uint64) { mu.Lock(); free = n; mu.Unlock() }
	const mb = 1 << 20
	written := map[string]int64{}
	measured := 0
	e.s.disk.appBytes = func(context.Context) (map[string]int64, error) {
		measured++
		out := map[string]int64{}
		for k, v := range written {
			out[k] = v
		}
		return out, nil
	}

	web := e.mustApp(t, CreateAppInput{Name: "Web"})
	noisy := e.mustApp(t, CreateAppInput{Name: "Noisy"})
	e.exec(t, `UPDATE apps SET desired_state = 'running', status = 'running'`)
	e.insertDep(t, web, "dep_build", 1, "building")
	done := e.fakePipeline(t, web.ID, "dep_build", true)

	// Plenty of room: nothing happens, nothing is measured.
	e.s.diskGuardOnce(ctx)
	if e.s.buildsPausedForDisk() || measured != 0 {
		t.Fatalf("paused=%v measured=%d with 50%% free", e.s.buildsPausedForDisk(), measured)
	}

	// 8% free (builds pause below 15%, running builds stop below 10%): the
	// queue is held at once; the running build gets one look for the cleanup
	// to help, then it is stopped.
	written[web.ID], written[noisy.ID] = 100*mb, 200*mb
	setFree(8)
	clock = clock.Add(30 * time.Second)
	e.s.diskGuardOnce(ctx)
	if !e.s.buildsPausedForDisk() || e.s.buildSlotsUsed() != 1 {
		t.Fatalf("first low look: paused=%v slots=%d", e.s.buildsPausedForDisk(), e.s.buildSlotsUsed())
	}
	clock = clock.Add(30 * time.Second)
	e.s.diskGuardOnce(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the build was not stopped")
	}
	if state, code, msg := e.depRow(t, "dep_build"); state != "failed" || code != "disk_full_build_cancelled" || !strings.Contains(msg, "disk is almost full") {
		t.Fatalf("stopped build: %s %s %q", state, code, msg)
	}
	if !e.s.buildsPausedForDisk() || e.s.buildSlotsUsed() != 0 {
		t.Fatalf("paused=%v slots=%d", e.s.buildsPausedForDisk(), e.s.buildSlotsUsed())
	}
	b, _ := e.s.ListBuilds(ctx, e.user)
	if !b.Slots.PausedForDisk {
		t.Error("the builds widget does not say builds are paused")
	}
	// Looking again while still low records nothing new.
	clock = clock.Add(30 * time.Second)
	e.s.diskGuardOnce(ctx)
	if n := auditCount(t, e.s, "server.builds_paused", ""); n != 1 {
		t.Errorf("builds_paused audited %d times", n)
	}

	// Critical (3% free) six minutes later: Noisy wrote 900 MB since, Web 10 MB.
	written[web.ID], written[noisy.ID] = 110*mb, 1100*mb
	setFree(3)
	clock = clock.Add(6 * time.Minute)
	e.s.diskGuardOnce(ctx)
	got, _ := e.s.Q.GetApp(ctx, noisy.ID)
	if got.DesiredState != "stopped" || got.Status != "failed" || !strings.Contains(got.StatusMessage, "server disk was almost full") || !strings.Contains(got.StatusMessage, "900.0 MB") {
		t.Fatalf("noisy: %s %s %q", got.DesiredState, got.Status, got.StatusMessage)
	}
	if w, _ := e.s.Q.GetApp(ctx, web.ID); w.DesiredState != "running" {
		t.Fatal("web was stopped too")
	}
	if n := auditCount(t, e.s, "app.disk_stopped", noisy.Slug); n != 1 {
		t.Errorf("disk stop audited %d times", n)
	}
	// Right after: no second app, however low.
	written[web.ID] = 2000 * mb
	clock = clock.Add(30 * time.Second)
	e.s.diskGuardOnce(ctx)
	if w, _ := e.s.Q.GetApp(ctx, web.ID); w.DesiredState != "running" {
		t.Fatal("a second app was stopped within the gap")
	}

	// Space is back above 15% + 2%: builds run again.
	setFree(20)
	clock = clock.Add(30 * time.Second)
	e.s.diskGuardOnce(ctx)
	if e.s.buildsPausedForDisk() {
		t.Fatal("builds still paused at 20% free")
	}
	if n := auditCount(t, e.s, "server.builds_resumed", ""); n != 1 {
		t.Errorf("builds_resumed audited %d times", n)
	}
}
