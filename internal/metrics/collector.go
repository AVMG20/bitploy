package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
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
	service         string // the app service it runs, see ServiceOf
	cancel          context.CancelFunc

	cur, base         dockerx.Stats
	haveCur, haveBase bool

	// recent are the last few stream readings (about one per second), for
	// the live CPU and memory between two stored samples.
	recent []dockerx.Stats
}

const (
	// liveReadings is how many stream readings the live view spans: about
	// five seconds, enough to smooth the jitter of one-second readings.
	liveReadings = 6
	// liveMaxAge is how old the newest reading may be before the live view
	// falls back to the stored sample.
	liveMaxAge = 10 * time.Second
)

// SingleService is the name of the one service of an app with the single
// layout (every app from before services). Its containers may lack the
// nl.bitploy.service label.
const SingleService = "app"

// ServiceOf is the service a container runs, from its nl.bitploy.service label.
func ServiceOf(label string) string {
	if label == "" {
		return SingleService
	}
	return label
}

func svcKey(appID, service string) string { return appID + "\x00" + service }

// Latest returns what a running app uses right now: CPU and memory over the
// last few seconds of the stats stream, network from the newest stored
// sample (bytes during that sample's interval). Without fresh stream readings
// it is the newest stored sample.
func (m *Manager) Latest(appID string) (Point, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latestLocked(appID)
}

// LatestAll returns Latest for all running apps.
func (m *Manager) LatestAll() map[string]Point {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := map[string]bool{}
	for id := range m.latest {
		ids[id] = true
	}
	for _, t := range m.tracked {
		ids[t.appID] = true
	}
	out := make(map[string]Point, len(ids))
	for id := range ids {
		if p, ok := m.latestLocked(id); ok {
			out[id] = p
		}
	}
	return out
}

// LatestService is Latest for one service of an app.
func (m *Manager) LatestService(appID, service string) (Point, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latestOf(appID, service)
}

func (m *Manager) latestLocked(appID string) (Point, bool) { return m.latestOf(appID, "") }

// latestOf is the live view of an app (service "") or of one of its services.
func (m *Manager) latestOf(appID, service string) (Point, bool) {
	p, ok := m.latest[appID]
	if service != "" {
		p, ok = m.latestSvc[svcKey(appID, service)]
	}
	var cpu float64
	var mem int64
	var newest time.Time
	live := false
	for _, t := range m.tracked {
		if t.appID != appID || len(t.recent) < 2 || (service != "" && t.service != service) {
			continue
		}
		first, last := t.recent[0], t.recent[len(t.recent)-1]
		if m.now().Sub(last.Read) > liveMaxAge {
			continue
		}
		live = true
		cpu += CPUPercent(first, last)
		mem += int64(last.MemWorkingSet())
		if last.Read.After(newest) {
			newest = last.Read
		}
	}
	if !live {
		return p, ok
	}
	if !ok {
		p = Point{}
	}
	p.TS, p.CPU, p.MemBytes = newest.UTC(), cpu, mem
	return p, true
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
	t := &tracked{id: c.ID, name: c.Name, appID: c.AppID, service: ServiceOf(c.Service), cancel: cancel}
	m.tracked[c.ID] = t
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := m.Docker.StreamStats(sctx, c.ID, func(s dockerx.Stats) error {
			m.mu.Lock()
			t.cur, t.haveCur = s, true
			t.recent = append(t.recent, s)
			if len(t.recent) > liveReadings {
				t.recent = append(t.recent[:0], t.recent[len(t.recent)-liveReadings:]...)
			}
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

func (a *appAgg) add(o appAgg) {
	a.cpu += o.cpu
	a.mem += o.mem
	a.rx += o.rx
	a.tx += o.tx
	a.containerCount += o.containerCount
}

// Tick turns the newest reading of every tracked container into one sample
// per app service, stores them in a single transaction and counts running
// time per app.
// elapsed is the time since the previous tick (capped at twice the interval,
// so a laptop that slept does not book hours of uptime).
func (m *Manager) Tick(ctx context.Context, now time.Time, elapsed time.Duration) error {
	if max := 2 * m.SampleInterval; elapsed > max {
		elapsed = max
	}
	type key struct{ app, service string }
	perSvc := map[key]*appAgg{}
	agg := map[string]*appAgg{} // per app: the sum of its services
	m.mu.Lock()
	for _, t := range m.tracked {
		if !t.haveCur {
			continue
		}
		k := key{t.appID, ServiceOf(t.service)}
		a := perSvc[k]
		if a == nil {
			a = &appAgg{}
			perSvc[k] = a
		}
		one := appAgg{
			containerCount: 1,
			cpu:            CPUPercent(t.base, t.cur),
			mem:            int64(t.cur.MemWorkingSet()),
			rx:             int64(CounterDelta(t.base.RxBytes, t.cur.RxBytes)),
			tx:             int64(CounterDelta(t.base.TxBytes, t.cur.TxBytes)),
		}
		a.add(one)
		if agg[t.appID] == nil {
			agg[t.appID] = &appAgg{}
		}
		agg[t.appID].add(one)
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
	for k := range m.latestSvc {
		app, svc, _ := strings.Cut(k, "\x00")
		if _, ok := perSvc[key{app, svc}]; !ok {
			delete(m.latestSvc, k)
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
		for k, a := range perSvc {
			if err := q.OpsInsertSample(ctx, db.OpsInsertSampleParams{
				AppID: k.app, Service: k.service, Ts: ts, Cpu: a.cpu, MemBytes: a.mem, RxBytes: a.rx, TxBytes: a.tx,
			}); err != nil {
				return err
			}
		}
		for id := range agg {
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
	for k, a := range perSvc {
		m.latestSvc[svcKey(k.app, k.service)] = Point{TS: now, CPU: a.cpu, MemBytes: a.mem, RxBytes: a.rx, TxBytes: a.tx}
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
