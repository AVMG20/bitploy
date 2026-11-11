package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"

	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
)

type fakeSysContainer struct {
	err       error
	status    container.ContainerState
	running   bool
	prev, cur dockerx.Stats
}

type fakeSysDocker struct {
	mu         sync.Mutex
	containers map[string]fakeSysContainer // by name or ID
	inspects   int
	snapshots  int
}

func (f *fakeSysDocker) InspectContainer(_ context.Context, name string) (container.InspectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspects++
	c, ok := f.containers[name]
	if !ok {
		return container.InspectResponse{}, fmt.Errorf("no such container %s: %w", name, cerrdefs.ErrNotFound)
	}
	if c.err != nil {
		return container.InspectResponse{}, c.err
	}
	return container.InspectResponse{ID: name, State: &container.State{Status: c.status, Running: c.running}}, nil
}

func (f *fakeSysDocker) StatsSnapshot(_ context.Context, id string) (dockerx.Stats, dockerx.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots++
	c := f.containers[id]
	return c.prev, c.cur, nil
}

func (f *fakeSysDocker) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspects, f.snapshots
}

// busy is a stats pair where the container used a quarter of the host's CPU
// time on 4 cores (100% of one core) and has a working set of memMB.
func busy(memMB uint64) (dockerx.Stats, dockerx.Stats) {
	prev := dockerx.Stats{CPUTotal: 1000, SystemCPU: 10_000, OnlineCPUs: 4}
	cur := dockerx.Stats{CPUTotal: 1000 + 250, SystemCPU: 10_000 + 1000, OnlineCPUs: 4, MemUsage: (memMB + 10) << 20, InactiveFile: 10 << 20}
	return prev, cur
}

func newSysService(f *fakeSysDocker, self string, now *time.Time) *Service {
	s := &Service{Cfg: &config.Config{TraefikContainer: "bitploy-traefik"}, Now: func() time.Time { return *now }}
	s.sys.docker = f
	s.sys.selfID = func() string { return self }
	return s
}

func TestSystemContainersMapping(t *testing.T) {
	bp, bc := busy(180)
	pp, pc := busy(40)
	f := &fakeSysDocker{containers: map[string]fakeSysContainer{
		"selfid":                {status: "running", running: true, prev: bp, cur: bc},
		"bitploy-traefik":       {status: "running", running: true, prev: pp, cur: pc},
		deploy.BuilderContainer: {status: "exited"},
	}}
	now := time.Unix(1000, 0)
	got := newSysService(f, "selfid", &now).systemContainers(context.Background())
	want := []SystemContainer{
		{Name: "bitploy", State: "running", CPUPercent: 100, MemoryMB: 180},
		{Name: "proxy", State: "running", CPUPercent: 100, MemoryMB: 40},
		{Name: "builder", State: "exited"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, snaps := f.counts(); snaps != 2 {
		t.Errorf("snapshots = %d, want 2 (a stopped builder is not read)", snaps)
	}
}

func TestSystemContainersOutsideDockerAndMissing(t *testing.T) {
	f := &fakeSysDocker{containers: map[string]fakeSysContainer{
		"bitploy-traefik": {err: errors.New("daemon hiccup")},
	}}
	now := time.Unix(1000, 0)
	got := newSysService(f, "", &now).systemContainers(context.Background())
	want := []SystemContainer{{Name: "proxy", State: "unknown"}, {Name: "builder", State: "missing"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestSystemContainersWithoutDocker(t *testing.T) {
	s := &Service{Cfg: &config.Config{}, Now: time.Now}
	if got := s.systemContainers(context.Background()); got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want an empty slice", got)
	}
}

func TestSystemContainersCache(t *testing.T) {
	f := &fakeSysDocker{containers: map[string]fakeSysContainer{}}
	now := time.Unix(1000, 0)
	s := newSysService(f, "", &now)
	ctx := context.Background()

	s.systemContainers(ctx)
	first, _ := f.counts() // proxy + builder
	if first != 2 {
		t.Fatalf("inspects = %d, want 2", first)
	}
	now = now.Add(5 * time.Second)
	s.systemContainers(ctx)
	if n, _ := f.counts(); n != first {
		t.Fatalf("a fresh reading was read again (%d inspects)", n)
	}

	// Older than sysRefreshAfter: the stale rows come back at once and a
	// refresh runs in the background.
	now = now.Add(sysRefreshAfter)
	if got := s.systemContainers(ctx); len(got) != 2 {
		t.Fatalf("stale rows = %+v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.sys.mu.Lock()
		done := !s.sys.refreshing
		s.sys.mu.Unlock()
		if n, _ := f.counts(); done && n == 2*first {
			break
		}
		if time.Now().After(deadline) {
			n, _ := f.counts()
			t.Fatalf("background refresh did not run (%d inspects)", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Older than sysMaxAge: the request reads again itself.
	now = now.Add(sysMaxAge)
	s.systemContainers(ctx)
	if n, _ := f.counts(); n != 3*first {
		t.Fatalf("inspects = %d, want %d", n, 3*first)
	}
}

func TestIsShortContainerID(t *testing.T) {
	for in, want := range map[string]bool{"3f1c2a9b8d7e": true, "arnos-mbp": false, "3F1C2A9B8D7E": false, "3f1c2a9b8d7": false} {
		if got := isShortContainerID(in); got != want {
			t.Errorf("isShortContainerID(%q) = %v", in, got)
		}
	}
}
