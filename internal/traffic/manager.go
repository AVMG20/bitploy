package traffic

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	pollEvery     = 5 * time.Second
	flushEvery    = time.Minute
	pruneEvery    = time.Hour
	routesEvery   = time.Minute // reload the router-to-app table
	routesMissMin = 10 * time.Second
	// Retention is how long stats are kept.
	Retention = 30 * 24 * time.Hour
)

// Manager reads Traefik's access log, counts requests per app and writes the
// counts to SQLite about once a minute.
type Manager struct {
	Store *store.Store
	Log   *slog.Logger
	Now   func() time.Time
	Geo   *Geo // nil: country comes from Cf-Ipcountry only

	tail *Tailer
	agg  *Aggregator

	routes      map[string]string // router name -> app id
	routesAt    time.Time
	routesTried time.Time
	lastWarn    time.Time

	cursorLoaded bool
	savedPos     Position // the position stored with the last flush
}

// New returns a Manager reading logPath.
func New(st *store.Store, log *slog.Logger, logPath string, geo *Geo) *Manager {
	m := &Manager{Store: st, Log: log, Geo: geo, Now: func() time.Time { return time.Now().UTC() }}
	m.tail = NewTailer(logPath)
	m.agg = NewAggregator(func() time.Time { return m.Now() })
	return m
}

// Run polls the log, flushes and prunes until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	if m.Geo != nil {
		go m.Geo.Run(ctx)
	}
	lastFlush, lastPrune := m.Now(), time.Time{}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			m.Poll(bg)
			m.Flush(bg)
			cancel()
			return
		case <-t.C:
		}
		m.Poll(ctx)
		if now := m.Now(); now.Sub(lastFlush) >= flushEvery {
			m.Flush(ctx)
			lastFlush = now
			if now.Sub(lastPrune) >= pruneEvery {
				m.Prune(ctx)
				lastPrune = now
			}
		}
	}
}

// Poll reads the new log lines into memory.
func (m *Manager) Poll(ctx context.Context) {
	if !m.cursorLoaded {
		m.cursorLoaded = true
		c, err := m.Store.Q.TrafficGetCursor(ctx)
		switch {
		case err == nil:
			m.savedPos = Position{Offset: c.Pos, HeadLen: int(c.HeadLen), Head: c.HeadHash}
			m.tail.Resume(m.savedPos)
		case !errors.Is(err, sql.ErrNoRows):
			m.Log.Warn("traffic: reading the saved position failed", "err", err)
		}
	}
	err := m.tail.Poll(func(line []byte) { m.handle(ctx, line) })
	if err != nil && m.Now().Sub(m.lastWarn) > time.Hour {
		m.lastWarn = m.Now()
		m.Log.Warn("traffic: access log", "path", m.tail.Path, "err", err)
	}
}

func (m *Manager) handle(ctx context.Context, line []byte) {
	e, ok := ParseLine(line)
	// App routers are "app-<slug>"; the dashboard, catch-all and ACME routers are not ours.
	// The internal entrypoint only serves bitploy's own health probes.
	if !ok || !strings.HasPrefix(e.Router, "app-") || e.EntryPoint == "internal" {
		return
	}
	id, ok := m.appFor(ctx, e.Router)
	if !ok {
		return
	}
	if e.Country == "" {
		e.Country = m.Geo.Country(e.IP)
	}
	m.agg.Add(id, e)
}

// appFor maps a router name to an app id. The table is reloaded every minute,
// and when a router is not in it (a new app), at most every 10 seconds.
func (m *Manager) appFor(ctx context.Context, router string) (string, bool) {
	now := m.Now()
	if m.routes == nil || now.Sub(m.routesAt) > routesEvery {
		m.loadRoutes(ctx)
	}
	id, ok := m.routes[router]
	if !ok && now.Sub(m.routesTried) > routesMissMin {
		m.loadRoutes(ctx)
		id, ok = m.routes[router]
	}
	return id, ok
}

func (m *Manager) loadRoutes(ctx context.Context) {
	m.routesTried = m.Now()
	apps, err := m.Store.Q.TrafficListApps(ctx)
	if err != nil {
		return
	}
	doms, err := m.Store.Q.TrafficListDomains(ctx)
	if err != nil {
		return
	}
	routes := make(map[string]string, len(apps))
	slugs := make(map[string]string, len(apps))
	for _, a := range apps {
		routes[proxy.RouterName(a.Slug)] = a.ID
		slugs[a.ID] = a.Slug
	}
	for _, d := range doms {
		if slug, ok := slugs[d.AppID]; ok {
			routes[proxy.HostRouterName(slug, d.Hostname)] = d.AppID
		}
	}
	m.routes, m.routesAt = routes, m.Now()
}

// Forget drops the unsaved counts of a deleted app.
func (m *Manager) Forget(appID string) { m.agg.Forget(appID) }

// Flush saves the counts gathered since the last flush, and how far the log has
// been read, in one transaction. If that fails the counts go back into memory
// and are saved with the next flush; the saved position then stays behind, so
// a restart in between reads those lines again instead of losing them.
func (m *Manager) Flush(ctx context.Context) {
	snap := m.agg.Flush()
	m.dropDeleted(ctx, snap)
	pos, havePos := m.tail.Position()
	save := havePos && pos != m.savedPos
	if len(snap) == 0 && !save {
		return
	}
	err := m.Store.Tx(ctx, func(q *db.Queries) error {
		for appID, d := range snap {
			if err := write(ctx, q, appID, d); err != nil {
				return err
			}
		}
		if save {
			return q.TrafficSetCursor(ctx, db.TrafficSetCursorParams{Pos: pos.Offset, HeadLen: int64(pos.HeadLen), HeadHash: pos.Head})
		}
		return nil
	})
	if err != nil {
		m.Log.Warn("traffic: saving counts failed, will try again", "err", err)
		m.agg.Restore(snap)
		return
	}
	if save {
		m.savedPos = pos
	}
}

// dropDeleted removes apps that no longer exist from a snapshot: saving their
// counts would fail on the foreign key and take everyone else's down with it.
func (m *Manager) dropDeleted(ctx context.Context, snap Snapshot) {
	if len(snap) == 0 {
		return
	}
	apps, err := m.Store.Q.TrafficListApps(ctx)
	if err != nil {
		return
	}
	exists := make(map[string]bool, len(apps))
	for _, a := range apps {
		exists[a.ID] = true
	}
	for id := range snap {
		if !exists[id] {
			delete(snap, id)
		}
	}
}

func write(ctx context.Context, q *db.Queries, appID string, d *AppDelta) error {
	for hour, c := range d.Hours {
		if err := q.TrafficAddHourly(ctx, db.TrafficAddHourlyParams{
			AppID: appID, Hour: hour, Requests: c.Requests, Errors4xx: c.Errors4xx, Errors5xx: c.Errors5xx, Visitors: c.Visitors,
		}); err != nil {
			return err
		}
	}
	for day, dd := range d.Days {
		if err := q.TrafficAddDaily(ctx, db.TrafficAddDailyParams{
			AppID: appID, Day: day, Requests: dd.Requests, Errors4xx: dd.Errors4xx, Errors5xx: dd.Errors5xx, Visitors: dd.Visitors,
		}); err != nil {
			return err
		}
		for p, n := range dd.Paths {
			if err := q.TrafficAddPath(ctx, db.TrafficAddPathParams{AppID: appID, Day: day, Path: p, Views: n}); err != nil {
				return err
			}
		}
		for c, n := range dd.Countries {
			if err := q.TrafficAddCountry(ctx, db.TrafficAddCountryParams{AppID: appID, Day: day, Country: c, Requests: n.Requests, Visitors: n.Visitors}); err != nil {
				return err
			}
		}
		for h, n := range dd.Referrers {
			if err := q.TrafficAddReferrer(ctx, db.TrafficAddReferrerParams{AppID: appID, Day: day, Host: h, Visits: n}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Prune deletes stats older than Retention.
func (m *Manager) Prune(ctx context.Context) {
	cut := m.Now().Add(-Retention)
	day := cut.UTC().Format("2006-01-02")
	q := m.Store.Q
	for _, err := range []error{
		q.TrafficPruneHourly(ctx, cut.Unix()),
		q.TrafficPruneDaily(ctx, day),
		q.TrafficPrunePaths(ctx, day),
		q.TrafficPruneCountries(ctx, day),
		q.TrafficPruneReferrers(ctx, day),
	} {
		if err != nil {
			m.Log.Warn("traffic: pruning failed", "err", err)
		}
	}
}
