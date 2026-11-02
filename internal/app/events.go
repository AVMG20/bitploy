package app

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Live events: small "something changed" hints for the dashboard, pushed over
// one Server-Sent Events stream per signed-in user (GET /api/v1/events). They
// are hints, not state: the browser reacts by refetching the matching query.
//
// Publishing never blocks. Each subscriber has a bounded queue that coalesces
// events on the same thing (latest state wins). If a slow browser still lets
// the queue fill up, the queue is dropped and the subscriber gets one "resync"
// event, which makes the browser refetch everything.

// Event types.
const (
	EventHello      = "hello"      // first event of a stream
	EventResync     = "resync"     // events were dropped: refetch everything
	EventApp        = "app"        // an app changed (status, settings, created, deleted, renamed)
	EventDeployment = "deployment" // a deployment changed state
	EventBackup     = "backup"     // a backup or restore of an app changed
	EventDomain     = "domain"     // a custom domain of an app changed
	EventBuildLog   = "build_log"  // new build log lines of a running deployment
)

// Event is one hint. App is always a slug.
type Event struct {
	Type   string   `json:"type"`
	App    string   `json:"app,omitempty"`
	OldApp string   `json:"old_app,omitempty"` // app events: the slug before a rename
	ID     string   `json:"id,omitempty"`      // deployment id
	State  string   `json:"state,omitempty"`   // deployment state; "deleted" on an app event after a delete
	Lines  []string `json:"lines,omitempty"`   // build_log only
}

const (
	maxPendingEvents = 256 // per subscriber, then a resync
	maxEventLines    = 200 // build log lines kept in one pending event
	maxEventLineLen  = 2000
	buildLogBatch    = 250 * time.Millisecond
)

func (e Event) key() string {
	switch e.Type {
	case EventDeployment, EventBuildLog:
		return e.Type + "|" + e.ID
	}
	return e.Type + "|" + e.App + "|" + e.OldApp + "|" + e.State
}

// Subscription is one open event stream. All methods are safe for concurrent use.
type Subscription struct {
	b    *Broker
	user string

	mu       sync.Mutex
	pending  []Event
	index    map[string]int
	overflow bool
	closed   bool
	wake     chan struct{}
}

// Wake is signalled when Drain has something to return.
func (s *Subscription) Wake() <-chan struct{} { return s.wake }

// Drain returns the queued events in order and empties the queue.
func (s *Subscription) Drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.overflow {
		s.overflow = false
		s.pending, s.index = nil, nil
		return []Event{{Type: EventResync}}
	}
	out := s.pending
	s.pending, s.index = nil, nil
	return out
}

// Close unsubscribes. It is safe to call more than once.
func (s *Subscription) Close() {
	s.mu.Lock()
	s.closed = true
	s.pending, s.index = nil, nil
	s.mu.Unlock()
	s.b.remove(s)
}

func (s *Subscription) push(e Event) {
	s.mu.Lock()
	if s.closed || s.overflow {
		s.mu.Unlock()
		return
	}
	k := e.key()
	if i, ok := s.index[k]; ok {
		if e.Type == EventBuildLog {
			lines := append(s.pending[i].Lines, e.Lines...)
			if len(lines) > maxEventLines {
				lines = append([]string(nil), lines[len(lines)-maxEventLines:]...)
			}
			e.Lines = lines
		}
		s.pending[i] = e
	} else if len(s.pending) >= maxPendingEvents {
		s.overflow = true
		s.pending, s.index = nil, nil
	} else {
		if s.index == nil {
			s.index = map[string]int{}
		}
		s.index[k] = len(s.pending)
		s.pending = append(s.pending, e)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Broker is the in-memory pub/sub of live events, keyed by user id.
type Broker struct {
	mu    sync.RWMutex
	subs  map[string]map[*Subscription]struct{}
	count atomic.Int64

	bmu     sync.Mutex
	batches map[string]*logBatch
}

type logBatch struct {
	user  string
	app   string
	lines []string
	timer *time.Timer
}

func newBroker() *Broker {
	return &Broker{subs: map[string]map[*Subscription]struct{}{}, batches: map[string]*logBatch{}}
}

// Subscribe opens a stream for a user. Close it when the client goes away.
func (b *Broker) Subscribe(user string) *Subscription {
	s := &Subscription{b: b, user: user, wake: make(chan struct{}, 1)}
	b.mu.Lock()
	if b.subs[user] == nil {
		b.subs[user] = map[*Subscription]struct{}{}
	}
	b.subs[user][s] = struct{}{}
	b.mu.Unlock()
	b.count.Add(1)
	return s
}

func (b *Broker) remove(s *Subscription) {
	b.mu.Lock()
	if m, ok := b.subs[s.user]; ok {
		if _, ok := m[s]; ok {
			delete(m, s)
			b.count.Add(-1)
		}
		if len(m) == 0 {
			delete(b.subs, s.user)
		}
	}
	b.mu.Unlock()
}

// Active reports whether anybody listens at all (cheap; publishers skip their
// lookups when it is false).
func (b *Broker) Active() bool { return b.count.Load() > 0 }

func (b *Broker) listening(user string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs[user]) > 0
}

// Publish delivers e to every stream of the user. It never blocks.
func (b *Broker) Publish(user string, e Event) {
	if user == "" || !b.Active() {
		return
	}
	b.mu.RLock()
	for s := range b.subs[user] {
		s.push(e)
	}
	b.mu.RUnlock()
}

// buildLine queues one build log line; lines of the same deployment go out
// together every 250 ms. The cost for a build is one mutex and one append.
func (b *Broker) buildLine(user, app, depID, line string) {
	if !b.Active() || !b.listening(user) {
		return
	}
	if len(line) > maxEventLineLen {
		line = line[:maxEventLineLen]
	}
	b.bmu.Lock()
	bt := b.batches[depID]
	if bt == nil {
		bt = &logBatch{user: user, app: app}
		b.batches[depID] = bt
		bt.timer = time.AfterFunc(buildLogBatch, func() { b.flushBuild(depID) })
	}
	if len(bt.lines) < maxEventLines {
		bt.lines = append(bt.lines, line)
	}
	b.bmu.Unlock()
}

// flushBuild sends the queued lines of a deployment now.
func (b *Broker) flushBuild(depID string) {
	b.bmu.Lock()
	bt := b.batches[depID]
	delete(b.batches, depID)
	b.bmu.Unlock()
	if bt == nil || len(bt.lines) == 0 {
		return
	}
	bt.timer.Stop()
	b.Publish(bt.user, Event{Type: EventBuildLog, App: bt.app, ID: depID, Lines: bt.lines})
}

// ---- Service helpers ----

// Events is the broker of live events; the API opens streams on it.
func (s *Service) Events() *Broker { return s.events }

// publishApp tells the owner of an app (by id) that something about it changed.
func (s *Service) publishApp(ctx context.Context, appID string) {
	s.publishAppState(ctx, EventApp, appID)
}

func (s *Service) publishBackup(ctx context.Context, appID string) {
	s.publishAppState(ctx, EventBackup, appID)
}

func (s *Service) publishDomain(ctx context.Context, appID string) {
	s.publishAppState(ctx, EventDomain, appID)
}

func (s *Service) publishAppState(ctx context.Context, typ, appID string) {
	if !s.events.Active() {
		return
	}
	row, err := s.Q.GetApp(context.WithoutCancel(ctx), appID)
	if err != nil {
		return
	}
	s.events.Publish(row.UserID, Event{Type: typ, App: row.Slug})
}

// publishDeployment tells the owner that a deployment changed. It reads the
// deployment as it is stored now, so call it after the write.
func (s *Service) publishDeployment(depID string) {
	if !s.events.Active() {
		return
	}
	ctx := context.Background()
	d, err := s.Q.GetDeployment(ctx, depID)
	if err != nil {
		return
	}
	row, err := s.Q.GetApp(ctx, d.AppID)
	if err != nil {
		return
	}
	s.events.Publish(row.UserID, Event{Type: EventDeployment, App: row.Slug, ID: d.ID, State: d.State})
}

// PublishAppChange is for the doors (REST, MCP): the actor changed the app
// ref (slug or id; empty for a new app). The owner and the actor both hear
// about it, which differs for an admin acting on someone else's app. When the
// app is gone (delete) the hint says state "deleted". old is the slug before a
// rename.
func (s *Service) PublishAppChange(ctx context.Context, a Actor, ref, old, state string) {
	if !s.events.Active() {
		return
	}
	ref = strings.TrimSpace(ref)
	e := Event{Type: EventApp, App: strings.ToLower(ref), OldApp: old, State: state}
	owner := ""
	if ref != "" {
		var row appRef
		if strings.HasPrefix(ref, "app_") {
			if r, err := s.Q.GetApp(context.WithoutCancel(ctx), ref); err == nil {
				row = appRef{r.UserID, r.Slug}
			}
		} else if r, err := s.Q.GetAppBySlug(context.WithoutCancel(ctx), e.App); err == nil {
			row = appRef{r.UserID, r.Slug}
		}
		if row.slug != "" {
			e.App, owner = row.slug, row.user
		}
	}
	s.events.Publish(a.UserID, e)
	if owner != "" && owner != a.UserID {
		s.events.Publish(owner, e)
	}
}

type appRef struct{ user, slug string }
