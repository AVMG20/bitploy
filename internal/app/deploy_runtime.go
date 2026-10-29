package app

// OWNER: deploy agent. In-memory coordination for the deploy worker,
// lifecycle actions and the reconciler. Everything durable lives in SQLite;
// this only says "which pipelines run right now in this process".

import (
	"context"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/deploy"
)

type deployRuntime struct {
	mu            sync.Mutex
	busy          map[string]string             // appID -> deployment ID whose pipeline is running
	cancels       map[string]context.CancelFunc // deployment ID -> cancel
	byUser        map[string]bool               // deployment IDs cancelled on purpose (stop, delete)
	candidates    map[string]string             // appID -> candidate upstream being health-checked
	draining      map[string]bool               // container names that will be stopped after a swap
	locks         map[string]*sync.Mutex        // per-app lifecycle lock
	restore       map[string]int                // appID -> consecutive failed restore attempts
	foreignLogged bool
	builderOnce   sync.Once
	builder       *deploy.Builder
	slotsOnce     sync.Once
	slots         chan struct{} // build slots (fetch, validate, build)
	wg            sync.WaitGroup
}

func (r *deployRuntime) init() {
	if r.busy == nil {
		r.busy = map[string]string{}
		r.cancels = map[string]context.CancelFunc{}
		r.byUser = map[string]bool{}
		r.candidates = map[string]string{}
		r.draining = map[string]bool{}
		r.locks = map[string]*sync.Mutex{}
		r.restore = map[string]int{}
	}
}

// appLock serialises lifecycle actions and route changes of one app.
func (s *Service) appLock(appID string) *sync.Mutex {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	l := r.locks[appID]
	if l == nil {
		l = &sync.Mutex{}
		r.locks[appID] = l
	}
	return l
}

// buildBuilder is the isolated buildx builder all builds run in.
func (s *Service) buildBuilder() *deploy.Builder {
	r := &s.deployRT
	r.builderOnce.Do(func() {
		r.builder = &deploy.Builder{ConfigDir: s.Cfg.BuildxDir(), CPUs: s.Cfg.BuilderCPUs, Memory: s.Cfg.BuilderMemory}
	})
	return r.builder
}

func (s *Service) buildSlots() chan struct{} {
	r := &s.deployRT
	r.slotsOnce.Do(func() {
		n := s.Cfg.MaxParallelBuilds
		if n < 1 {
			n = 2
		}
		r.slots = make(chan struct{}, n)
	})
	return r.slots
}

// tryBusy marks an app as running a pipeline; false when one already runs.
func (s *Service) tryBusy(appID, depID string, cancel context.CancelFunc) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if _, ok := r.busy[appID]; ok {
		return false
	}
	r.busy[appID] = depID
	r.cancels[depID] = cancel
	return true
}

func (s *Service) clearBusy(appID, depID string) {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if r.busy[appID] == depID {
		delete(r.busy, appID)
	}
	delete(r.cancels, depID)
	delete(r.byUser, depID)
	delete(r.candidates, appID)
}

func (s *Service) appBusy(appID string) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	_, ok := r.busy[appID]
	return ok
}

func (s *Service) activeDeployment(depID string) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	_, ok := r.cancels[depID]
	return ok
}

func (s *Service) activeDeploymentIDs() map[string]bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	out := make(map[string]bool, len(r.cancels))
	for id := range r.cancels {
		out[id] = true
	}
	return out
}

func (s *Service) cancelledByUser(depID string) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.byUser[depID]
}

// cancelInflight cancels the running pipeline of an app (stop, delete) and
// waits until it has cleaned up (at most wait).
func (s *Service) cancelInflight(appID string, wait time.Duration) {
	r := &s.deployRT
	r.mu.Lock()
	r.init()
	depID, ok := r.busy[appID]
	var cancel context.CancelFunc
	if ok {
		r.byUser[depID] = true
		cancel = r.cancels[depID]
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	if cancel != nil {
		cancel()
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !s.appBusy(appID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// settled waits up to 5 s for a pipeline that is wrapping up (its deployment
// is already running or failed, cleanup is still going) and reports whether
// the app is free. A deploy that is really in progress still reports false.
func (s *Service) settled(appID string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for s.appBusy(appID) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func (s *Service) setCandidate(appID, upstream string) {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if upstream == "" {
		delete(r.candidates, appID)
	} else {
		r.candidates[appID] = upstream
	}
}

func (s *Service) candidate(appID string) string {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.candidates[appID]
}

func (s *Service) setDraining(name string, on bool) {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if on {
		r.draining[name] = true
	} else {
		delete(r.draining, name)
	}
}

func (s *Service) isDraining(name string) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.draining[name]
}
