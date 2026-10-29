package metrics

import (
	"context"
	"time"

	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/store/db"
)

// Point is the newest sample of an app, kept in memory for the app lists.
type Point struct {
	TS       time.Time
	CPU      float64 // % of one core
	MemBytes int64
	RxBytes  int64
	TxBytes  int64
}

// tracked is one running container with an open stats stream.
type tracked struct {
	id, name, appID string
	cancel          context.CancelFunc

	cur, base         dockerx.Stats
	haveCur, haveBase bool
}

// Latest returns the newest sample of a running app.
func (m *Manager) Latest(appID string) (Point, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.latest[appID]
	return p, ok
}

// LatestAll returns the newest samples of all running apps.
func (m *Manager) LatestAll() map[string]Point {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Point, len(m.latest))
	for k, v := range m.latest {
		out[k] = v
	}
	return out
}

func (m *Manager) runCollector(ctx context.Context) {
	if m.Docker == nil {
		return
	}
	m.syncContainers(ctx)
	tick := time.NewTicker(m.SampleInterval)
	resync := time.NewTicker(time.Minute)
	defer tick.Stop()
	defer resync.Stop()
	last := m.now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-resync.C:
			m.syncContainers(ctx)
		case <-tick.C:
			now := m.now()
			if err := m.Tick(ctx, now, now.Sub(last)); err != nil {
				m.Log.Warn("writing metric samples failed", "err", err)
			}
			last = now
		}
	}
}

// syncContainers opens a stats stream for every running bitploy container
// and closes the ones whose container is gone.
func (m *Manager) syncContainers(ctx context.Context) {
	list, err := m.Docker.ManagedContainers(ctx, false)
	if err != nil {
		m.Log.Warn("listing containers for metrics failed", "err", err)
		return
	}
	running := map[string]bool{}
	for _, c := range list {
		if c.State != "running" {
			continue
		}
		running[c.ID] = true
		m.Track(ctx, c)
	}
	m.mu.Lock()
	for id, t := range m.tracked {
		if !running[id] {
			t.cancel()
			delete(m.tracked, id)
		}
	}
	m.mu.Unlock()
}

// Track opens a stats stream for a container (a no-op when one is open).
func (m *Manager) Track(ctx context.Context, c dockerx.Managed) {
	if m.Docker == nil {
		return
	}
	m.mu.Lock()
	if _, ok := m.tracked[c.ID]; ok {
		m.mu.Unlock()
		return
	}
	sctx, cancel := context.WithCancel(ctx)
	t := &tracked{id: c.ID, name: c.Name, appID: c.AppID, cancel: cancel}
	m.tracked[c.ID] = t
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := m.Docker.StreamStats(sctx, c.ID, func(s dockerx.Stats) error {
			m.mu.Lock()
			t.cur, t.haveCur = s, true
			if !t.haveBase {
				t.base, t.haveBase = s, true
			}
			m.mu.Unlock()
			return nil
		})
		if err != nil && sctx.Err() == nil {
			m.Log.Debug("stats stream ended", "container", c.Name, "err", err)
		}
		m.mu.Lock()
		if m.tracked[c.ID] == t {
			delete(m.tracked, c.ID)
		}
		m.mu.Unlock()
	}()
}

// Untrack closes the stats stream of a container.
func (m *Manager) Untrack(id string) {
	m.mu.Lock()
	if t, ok := m.tracked[id]; ok {
		t.cancel()
		delete(m.tracked, id)
	}
	m.mu.Unlock()
}

type appAgg struct {
	cpu            float64
	mem, rx, tx    int64
	containerCount int
}

// Tick turns the newest reading of every tracked container into one sample
// per app, stores them in a single transaction and counts running time.
// elapsed is the time since the previous tick (capped at twice the interval,
// so a laptop that slept does not book hours of uptime).
func (m *Manager) Tick(ctx context.Context, now time.Time, elapsed time.Duration) error {
	if max := 2 * m.SampleInterval; elapsed > max {
		elapsed = max
	}
	agg := map[string]*appAgg{}
	m.mu.Lock()
	for _, t := range m.tracked {
		if !t.haveCur {
			continue
		}
		a := agg[t.appID]
		if a == nil {
			a = &appAgg{}
			agg[t.appID] = a
		}
		a.containerCount++
		a.cpu += CPUPercent(t.base, t.cur)
		a.mem += int64(t.cur.MemWorkingSet())
		a.rx += int64(CounterDelta(t.base.RxBytes, t.cur.RxBytes))
		a.tx += int64(CounterDelta(t.base.TxBytes, t.cur.TxBytes))
		t.base = t.cur
	}
	m.mu.Unlock()

	// Apps that stopped running drop out of the in-memory view.
	m.mu.Lock()
	for id := range m.latest {
		if _, ok := agg[id]; !ok {
			delete(m.latest, id)
		}
	}
	m.mu.Unlock()
	if len(agg) == 0 {
		return nil
	}

	day := now.UTC().Format("2006-01-02")
	secs := int64(elapsed.Seconds() + 0.5)
	ts := now.Unix()
	users := map[string]string{}
	for id := range agg {
		users[id] = m.userOf(ctx, id)
	}
	err := m.Store.Tx(ctx, func(q *db.Queries) error {
		for id, a := range agg {
			if err := q.OpsInsertSample(ctx, db.OpsInsertSampleParams{
				AppID: id, Ts: ts, Cpu: a.cpu, MemBytes: a.mem, RxBytes: a.rx, TxBytes: a.tx,
			}); err != nil {
				return err
			}
			if secs > 0 && users[id] != "" {
				if err := q.OpsAddRunningSeconds(ctx, db.OpsAddRunningSecondsParams{
					AppID: id, UserID: users[id], Day: day, RunningSeconds: secs,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	for id, a := range agg {
		m.latest[id] = Point{TS: now, CPU: a.cpu, MemBytes: a.mem, RxBytes: a.rx, TxBytes: a.tx}
	}
	m.mu.Unlock()
	return nil
}

// userOf returns the owner of an app (cached); "" when the app is gone.
func (m *Manager) userOf(ctx context.Context, appID string) string {
	m.mu.RLock()
	u, ok := m.appUser[appID]
	m.mu.RUnlock()
	if ok {
		return u
	}
	app, err := m.Store.Q.GetApp(ctx, appID)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	m.appUser[appID] = app.UserID
	m.mu.Unlock()
	return app.UserID
}

// SetLatestForTest stores a sample as if the collector had written it.
// It exists for tests of code that reads the live view.
func (m *Manager) SetLatestForTest(appID string, p Point) {
	m.mu.Lock()
	m.latest[appID] = p
	m.mu.Unlock()
}
