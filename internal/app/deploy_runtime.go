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
	byUser        map[string]bool               // deployment IDs cancelled on purpose (stop, delete, cancel_deployment)
	cancelReq     map[string]Actor              // deployment ID -> who cancelled it (cancel_deployment)
	committed     map[string]bool               // deployment IDs that passed the point of no return (swapping)
	candidates    map[string]string             // appID -> candidate upstream being health-checked
	draining      map[string]bool               // container names that will be stopped after a swap
	locks         map[string]*sync.Mutex        // per-app lifecycle lock
	restore       map[string]int                // appID -> consecutive failed restore attempts
	afterLive     map[string][]string           // deployment ID -> app IDs to restart once it is live (add_service connect_to)
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
		r.cancelReq = map[string]Actor{}
		r.committed = map[string]bool{}
		r.candidates = map[string]string{}
		r.draining = map[string]bool{}
		r.locks = map[string]*sync.Mutex{}
		r.restore = map[string]int{}
		r.afterLive = map[string][]string{}
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
	delete(r.cancelReq, depID)
	delete(r.committed, depID)
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

// requestCancel cancels the pipeline of a deployment on behalf of a user. found
// is false when no pipeline of that deployment runs in this process; tooLate
// when it passed the point of no return (swapping). Decided under the same
// lock as commitSwap, so a pipeline never both swaps and is cancelled.
func (s *Service) requestCancel(depID string, a Actor) (found, tooLate bool) {
	r := &s.deployRT
	r.mu.Lock()
	r.init()
	cancel, ok := r.cancels[depID]
	if !ok {
		r.mu.Unlock()
		return false, false
	}
	if r.committed[depID] {
		r.mu.Unlock()
		return true, true
	}
	r.byUser[depID] = true
	if _, dup := r.cancelReq[depID]; !dup {
		r.cancelReq[depID] = a
	}
	r.mu.Unlock()
	cancel()
	return true, false
}

// commitSwap is called by a pipeline right before it goes live. It returns
// false when the deployment was cancelled meanwhile; after true no user cancel
// is accepted any more (too_late_to_cancel).
func (s *Service) commitSwap(depID string) bool {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if r.byUser[depID] {
		return false
	}
	r.committed[depID] = true
	return true
}

// cancelRequester is who cancelled the deployment with cancel_deployment.
func (s *Service) cancelRequester(depID string) (Actor, bool) {
	r := &s.deployRT
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	a, ok := r.cancelReq[depID]
	return a, ok
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
