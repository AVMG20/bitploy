package metrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Status messages written by the event handler. Restores on `start` only
// touch messages that begin with one of these prefixes, so a real deploy
// failure is never overwritten.
const (
	MsgOOM       = "Out of memory: raise the memory limit or use less"
	msgExited    = "The app stopped by itself and Docker is restarting it"
	msgUnhealthy = "Health check failing"
	msgCrashed   = "Crashed"

	crashMaxRestarts = 5
	crashWindow      = 5 * time.Minute
	logTailLines     = 20
)

// crashTracker counts container deaths inside a sliding window.
type crashTracker struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	deaths map[string][]time.Time
}

func newCrashTracker(max int, window time.Duration) *crashTracker {
	return &crashTracker{max: max, window: window, deaths: map[string][]time.Time{}}
}

// Add records a death and reports how many happened inside the window and
// whether that reaches the limit.
func (c *crashTracker) Add(key string, at time.Time) (count int, loop bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := c.deaths[key][:0]
	for _, t := range c.deaths[key] {
		if at.Sub(t) < c.window {
			keep = append(keep, t)
		}
	}
	keep = append(keep, at)
	c.deaths[key] = keep
	return len(keep), len(keep) >= c.max
}

// Forget drops the history of a container.
func (c *crashTracker) Forget(key string) {
	c.mu.Lock()
	delete(c.deaths, key)
	c.mu.Unlock()
}

// runEvents follows the Docker event stream and reconnects when it breaks.
func (m *Manager) runEvents(ctx context.Context) {
	if m.Docker == nil {
		return
	}
	var since time.Time
	backoff := time.Second
	for ctx.Err() == nil {
		started := m.now()
		err := m.Docker.WatchEvents(ctx, since, func(ev dockerx.Event) {
			since = ev.Time.Add(time.Nanosecond)
			m.HandleEvent(ctx, ev)
		})
		if ctx.Err() != nil {
			return
		}
		m.Log.Warn("docker event stream ended; reconnecting", "err", err)
		if since.IsZero() {
			since = started // do not replay the whole history on the first reconnect
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// HandleEvent reacts to one container event of an app container. Events of
// containers that are not the app's current one (the old container after a
// swap, a candidate under health check) are ignored, as are events of apps
// that are not supposed to be running.
func (m *Manager) HandleEvent(ctx context.Context, ev dockerx.Event) {
	if m.Docker == nil {
		return
	}
	q := m.Store.Q
	switch ev.Action {
	case "start":
		m.Track(ctx, dockerx.Managed{ID: ev.ID, Name: ev.Name, AppID: ev.AppID, Slug: ev.Slug, State: "running"})
	case "destroy":
		m.Untrack(ev.ID)
		m.crashes.Forget(ev.Name)
		return
	case "die":
		m.Untrack(ev.ID)
	}

	app, err := q.GetApp(ctx, ev.AppID)
	if err != nil {
		return // app deleted
	}
	cur, err := q.OpsGetCurrentContainer(ctx, ev.AppID)
	if err != nil || !cur.Valid || cur.String != ev.Name {
		return
	}
	if app.DesiredState != "running" {
		return
	}

	switch ev.Action {
	case "oom":
		m.markOOM(ev.Name)
		if app.Status == "running" || app.Status == "failed" {
			m.setStatus(ctx, app, "failed", MsgOOM)
			m.audit(ctx, "app.oom", "app", app.ID, map[string]any{"container": ev.Name})
		}
	case "die":
		m.onDie(ctx, app, ev)
	case "start":
		m.onStart(ctx, app)
	case "health_status":
		m.onHealth(ctx, app, ev)
	}
}

func (m *Manager) markOOM(name string) {
	m.oomMu.Lock()
	m.oomAt[name] = m.now()
	m.oomMu.Unlock()
}

func (m *Manager) recentOOM(name string) bool {
	m.oomMu.Lock()
	defer m.oomMu.Unlock()
	t, ok := m.oomAt[name]
	return ok && m.now().Sub(t) < 15*time.Second
}

func (m *Manager) onDie(ctx context.Context, app db.App, ev dockerx.Event) {
	if app.Status != "running" && app.Status != "failed" {
		return // deploying, stopped, crashed already
	}
	oom := m.recentOOM(ev.Name)
	if !oom {
		if st, err := m.Docker.InspectState(ctx, ev.ID); err == nil && st.OOMKilled {
			oom = true
		}
	}
	msg := msgExited
	if ev.ExitCode != "" && ev.ExitCode != "0" {
		msg += " (exit code " + ev.ExitCode + ")"
	}
	if oom {
		msg = MsgOOM
	}

	count, loop := m.crashes.Add(ev.Name, ev.Time)
	if loop {
		m.crashLoop(ctx, app, ev, count)
		return
	}
	m.setStatus(ctx, app, "failed", msg)
	m.audit(ctx, "app.container_died", "app", app.ID, map[string]any{
		"container": ev.Name, "exit_code": ev.ExitCode, "oom": oom, "deaths_in_window": count,
	})
}

// crashLoop stops a container that died too often and marks the app crashed.
func (m *Manager) crashLoop(ctx context.Context, app db.App, ev dockerx.Event, count int) {
	_ = m.Docker.StopContainerGrace(ctx, ev.Name, 10) // also cancels the restart policy
	tail := m.logTail(ctx, ev.Name)
	msg := fmt.Sprintf("%s: the app stopped %d times in %d minutes, so bitploy stopped it. "+
		"Fix the error in the log below and redeploy, or start the app again.", msgCrashed, count, int(crashWindow.Minutes()))
	if tail != "" {
		msg += "\n\nLast log lines:\n" + tail
	}
	m.setStatus(ctx, app, "crashed", msg)
	if err := m.Store.Q.OpsSetAppDesiredState(ctx, db.OpsSetAppDesiredStateParams{
		DesiredState: "stopped", UpdatedAt: m.now().Unix(), ID: app.ID,
	}); err != nil {
		m.Log.Error("marking crashed app as stopped failed", "app", app.ID, "err", err)
	}
	m.crashes.Forget(ev.Name)
	m.audit(ctx, "app.crashed", "app", app.ID, map[string]any{
		"container": ev.Name, "deaths_in_window": count, "log_tail": tail,
	})
	if m.SyncRoute != nil {
		if err := m.SyncRoute(ctx, app.ID); err != nil {
			m.Log.Warn("route sync after crash failed", "app", app.ID, "err", err)
		}
	}
}

func (m *Manager) logTail(ctx context.Context, name string) string {
	lines, err := m.Docker.TailLogs(ctx, name, logTailLines, time.Time{})
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	s := apperr.Redact(strings.TrimRight(b.String(), "\n"))
	if len(s) > 4000 {
		s = "…" + s[len(s)-4000:]
	}
	return s
}

// onStart flips an app we marked failed back to running once Docker has
// restarted its container. An out-of-memory message is kept so the owner
// still sees why the app restarted.
func (m *Manager) onStart(ctx context.Context, app db.App) {
	if app.Status != "failed" {
		return
	}
	switch {
	case strings.HasPrefix(app.StatusMessage, MsgOOM):
		m.setStatus(ctx, app, "running", MsgOOM)
	case strings.HasPrefix(app.StatusMessage, msgExited):
		m.setStatus(ctx, app, "running", "")
	}
}

func (m *Manager) onHealth(ctx context.Context, app db.App, ev dockerx.Event) {
	switch ev.Health {
	case "unhealthy":
		if app.Status != "running" {
			return
		}
		m.setStatus(ctx, app, "failed", msgUnhealthy+": the container's HEALTHCHECK reports unhealthy")
		m.audit(ctx, "app.unhealthy", "app", app.ID, map[string]any{"container": ev.Name})
	case "healthy":
		if app.Status == "failed" && strings.HasPrefix(app.StatusMessage, msgUnhealthy) {
			m.setStatus(ctx, app, "running", "")
			m.audit(ctx, "app.healthy", "app", app.ID, map[string]any{"container": ev.Name})
		}
	}
}

func (m *Manager) setStatus(ctx context.Context, app db.App, status, msg string) {
	if app.Status == status && app.StatusMessage == msg {
		return
	}
	err := m.Store.Q.SetAppStatus(ctx, db.SetAppStatusParams{
		Status: status, StatusMessage: msg, UpdatedAt: m.now().Unix(), ID: app.ID,
	})
	if err != nil {
		m.Log.Error("setting app status failed", "app", app.ID, "status", status, "err", err)
	}
}
