package app

// What bitploy's own containers use (bitploy, the Traefik proxy, the BuildKit
// builder), for the card on Admin > Server. The metrics collector only
// streams app containers, so this reads one stats snapshot per container and
// keeps the result for a few seconds: the page polls every 5 s and a snapshot
// takes about a second.

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/metrics"
)

const (
	// sysRefreshAfter is when a cached reading is refreshed in the background
	// (the stale one is still returned).
	sysRefreshAfter = 10 * time.Second
	// sysMaxAge is how old a reading may be before a request waits for a new one.
	sysMaxAge = time.Minute
	// sysTimeout caps reading all containers (they are read in parallel).
	sysTimeout = 2500 * time.Millisecond
)

// sysDocker is the part of the Docker client the system card needs; tests fake it.
type sysDocker interface {
	InspectContainer(ctx context.Context, name string) (container.InspectResponse, error)
	StatsSnapshot(ctx context.Context, id string) (prev, cur dockerx.Stats, err error)
}

// systemState caches the readings of bitploy's own containers.
type systemState struct {
	mu         sync.Mutex
	rows       []SystemContainer
	at         time.Time
	refreshing bool

	self     string // bitploy's own container ID, "" when it does not run in Docker
	selfDone bool

	docker sysDocker     // tests replace it; nil means s.Docker
	selfID func() string // tests replace it; nil means the real lookup
}

// sysTarget is one container to read: its display name and Docker name or ID.
type sysTarget struct{ name, ref string }

func (s *Service) sysDocker() sysDocker {
	if s.sys.docker != nil {
		return s.sys.docker
	}
	if s.Docker == nil {
		return nil
	}
	return s.Docker
}

// systemContainers returns what bitploy's own containers use, from a cache
// that is at most a minute old. Empty without Docker.
func (s *Service) systemContainers(ctx context.Context) []SystemContainer {
	dk := s.sysDocker()
	if dk == nil {
		return []SystemContainer{}
	}
	now := s.Now()
	st := &s.sys
	st.mu.Lock()
	rows, age := st.rows, now.Sub(st.at)
	cached := !st.at.IsZero() && age < sysMaxAge
	if cached && age >= sysRefreshAfter && !st.refreshing {
		st.refreshing = true
		go s.refreshSystem(context.WithoutCancel(ctx), dk)
	}
	st.mu.Unlock()
	if cached {
		return rows
	}
	return s.refreshSystem(ctx, dk)
}

func (s *Service) refreshSystem(ctx context.Context, dk sysDocker) []SystemContainer {
	ctx, cancel := context.WithTimeout(ctx, sysTimeout)
	defer cancel()
	targets := []sysTarget{{"proxy", s.Cfg.TraefikContainer}, {"builder", deploy.BuilderContainer}}
	if self := s.selfContainer(ctx, dk); self != "" {
		targets = append([]sysTarget{{"bitploy", self}}, targets...)
	}
	rows := readSystem(ctx, dk, targets)
	st := &s.sys
	st.mu.Lock()
	st.rows, st.at, st.refreshing = rows, s.Now(), false
	st.mu.Unlock()
	return rows
}

// readSystem reads every target in parallel. A container that does not exist
// is "missing"; one that is not running has no CPU or memory.
func readSystem(ctx context.Context, dk sysDocker, targets []sysTarget) []SystemContainer {
	rows := make([]SystemContainer, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = readOne(ctx, dk, t)
		}()
	}
	wg.Wait()
	return rows
}

func readOne(ctx context.Context, dk sysDocker, t sysTarget) SystemContainer {
	row := SystemContainer{Name: t.name, State: "missing"}
	info, err := dk.InspectContainer(ctx, t.ref)
	if err != nil {
		if !dockerx.IsNotFound(err) {
			row.State = "unknown"
		}
		return row
	}
	if info.State == nil {
		row.State = "unknown"
		return row
	}
	row.State = string(info.State.Status)
	if !info.State.Running {
		return row
	}
	prev, cur, err := dk.StatsSnapshot(ctx, info.ID)
	if err != nil {
		return row
	}
	row.CPUPercent = round1(metrics.CPUPercent(prev, cur))
	row.MemoryMB = round1(float64(cur.MemWorkingSet()) / mib)
	return row
}

// selfContainer finds the container bitploy runs in, once. The container ID
// comes from /proc/self/mountinfo; else from the hostname, which Docker sets
// to the short container ID unless the compose file sets another. Outside
// Docker (local development) it is "".
func (s *Service) selfContainer(ctx context.Context, dk sysDocker) string {
	st := &s.sys
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.selfDone {
		return st.self
	}
	if st.selfID != nil {
		st.self, st.selfDone = st.selfID(), true
		return st.self
	}
	if id := dockerx.SelfContainerID(); id != "" {
		st.self, st.selfDone = id, true
		return id
	}
	host, err := os.Hostname()
	if err != nil || !isShortContainerID(host) {
		st.selfDone = true
		return ""
	}
	info, err := dk.InspectContainer(ctx, host)
	switch {
	case err == nil && strings.HasPrefix(info.ID, host):
		st.self, st.selfDone = info.ID, true
	case err == nil || dockerx.IsNotFound(err):
		st.selfDone = true
	}
	return st.self // on another error, try again next time
}

// isShortContainerID reports whether s looks like Docker's 12-hex short ID.
func isShortContainerID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
