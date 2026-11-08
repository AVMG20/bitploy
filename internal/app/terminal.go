package app

// The terminal (docs/decisions.md, "Terminal"): a shell with a TTY in an
// app's running container, opened from the dashboard over a WebSocket
// (internal/api/terminal.go). Only a dashboard session opens one, never an
// API token and never MCP, and only someone who may change the app (its
// owner or a member of its project; LoadApp with write): a shell in the
// container can read every variable and file of the app anyway. Admins do
// not get one on other people's apps. Opening and closing are audited,
// what is typed is not.

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// MaxTerminalsPerUser is how many terminals one user may have open at once.
const MaxTerminalsPerUser = 3

// Terminal sizes bitploy accepts; anything else is clamped.
const (
	maxTerminalCols = 500
	maxTerminalRows = 200
)

// TerminalConn is an open shell in a container.
type TerminalConn interface {
	// Read gives what the shell writes to its terminal, Write sends input.
	io.ReadWriter
	Resize(ctx context.Context, cols, rows int) error
	// Exit reports the shell's exit code once it has exited.
	Exit(ctx context.Context) (code int, exited bool)
	// Close ends the session and stops the shell if it still runs.
	Close() error
}

// TerminalBackend opens shells in containers: Docker, or a fake in tests
// (UseTerminalBackend).
type TerminalBackend interface {
	// Running reports whether the container exists and runs.
	Running(ctx context.Context, container string) (bool, error)
	Open(ctx context.Context, container string, cols, rows int) (TerminalConn, error)
}

// UseTerminalBackend makes terminals open with b instead of Docker (tests).
func (s *Service) UseTerminalBackend(b TerminalBackend) {
	s.term.mu.Lock()
	s.term.backend = b
	s.term.mu.Unlock()
}

// terminalState counts the open terminals per user and keeps every open
// one, so they can be closed when bitploy stops (CloseTerminals).
type terminalState struct {
	mu      sync.Mutex
	open    map[string]int
	all     map[*Terminal]struct{}
	backend TerminalBackend
}

func (t *terminalState) add(term *Terminal) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.all == nil {
		t.all = map[*Terminal]struct{}{}
	}
	t.all[term] = struct{}{}
}

func (t *terminalState) remove(term *Terminal) {
	t.mu.Lock()
	delete(t.all, term)
	t.mu.Unlock()
}

// CloseTerminals ends every open terminal (reason TerminalShutdown) and
// stops their shells, so none is left behind in a container when bitploy
// stops. It returns once all are closed.
func (s *Service) CloseTerminals() {
	s.term.mu.Lock()
	list := make([]*Terminal, 0, len(s.term.all))
	for t := range s.term.all {
		list = append(list, t)
	}
	s.term.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.Close(TerminalShutdown)
		}()
	}
	wg.Wait()
}

func (t *terminalState) acquire(user string) (release func(), ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open == nil {
		t.open = map[string]int{}
	}
	if t.open[user] >= MaxTerminalsPerUser {
		return nil, false
	}
	t.open[user]++
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.open[user]--; t.open[user] <= 0 {
				delete(t.open, user)
			}
		})
	}, true
}

func (s *Service) terminalBackend() TerminalBackend {
	s.term.mu.Lock()
	b := s.term.backend
	s.term.mu.Unlock()
	if b != nil {
		return b
	}
	if s.Docker == nil {
		return nil
	}
	return dockerTerminals{s.Docker}
}

type dockerTerminals struct{ c *dockerx.Client }

func (d dockerTerminals) Running(ctx context.Context, name string) (bool, error) {
	return d.c.ContainerRunning(ctx, name)
}

func (d dockerTerminals) Open(ctx context.Context, name string, cols, rows int) (TerminalConn, error) {
	t, err := d.c.OpenTerminal(ctx, name, dockerx.TermSize{Cols: uint(cols), Rows: uint(rows)}, nil)
	if errors.Is(err, dockerx.ErrNoShell) {
		return nil, errNoShell()
	}
	if err != nil {
		return nil, err
	}
	return dockerTerminal{t}, nil
}

type dockerTerminal struct{ *dockerx.Terminal }

func (d dockerTerminal) Resize(ctx context.Context, cols, rows int) error {
	return d.Terminal.Resize(ctx, dockerx.TermSize{Cols: uint(cols), Rows: uint(rows)})
}

// TerminalOptions pick the container and the terminal's first size.
type TerminalOptions struct {
	// Service is the service of an app with several services; "" is the
	// public (or first) one.
	Service    string
	Cols, Rows int
	// Session is the dashboard session's cookie and IP the client's
	// address. Check reads the session again, so a logout, an expired
	// session or a suspended account ends the terminal.
	Session, IP string
}

// Terminal is an open terminal session. Read, Write and Resize go to the
// shell; Close ends it and must be called once the client is gone.
type Terminal struct {
	// App is the app's slug, Service the service's name ("" for an app
	// with one service).
	App     string
	Service string

	conn      TerminalConn
	backend   TerminalBackend
	s         *Service
	actor     Actor
	session   string
	ip        string
	appID     string
	container string
	started   time.Time
	release   func()
	closeOnce sync.Once

	mu     sync.Mutex
	closed string // why Close was called; "" while open
}

// Why a terminal session ended, in its audit row.
const (
	TerminalExited   = "exited"      // the shell exited
	TerminalClosed   = "closed"      // the browser closed the connection
	TerminalIdle     = "idle"        // no input for too long
	TerminalTooLong  = "max_length"  // open for the longest time allowed
	TerminalNoAccess = "access"      // the user lost access to the app
	TerminalAppGone  = "not_running" // the app was stopped
	// The container stopped under the shell: a deploy, a restart or a crash.
	TerminalContainerGone = "container_gone"
	TerminalLoggedOut     = "session"  // logged out, session expired or account suspended
	TerminalShutdown      = "shutdown" // bitploy stopped (CloseTerminals)
)

// clampTerm keeps a terminal size within what bitploy accepts; 0 gives
// the default.
func clampTerm(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

// ClampTerminalSize is the size a terminal gets for cols × rows.
func ClampTerminalSize(cols, rows int) (int, int) {
	return clampTerm(cols, 80, maxTerminalCols), clampTerm(rows, 24, maxTerminalRows)
}

// OpenTerminal starts a shell in the running container of an app (of the
// service o.Service for an app with several services).
func (s *Service) OpenTerminal(ctx context.Context, a Actor, ref string, o TerminalOptions) (*Terminal, error) {
	if err := RequireSession(a); err != nil {
		return nil, err
	}
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return nil, err
	}
	name, svc, err := s.terminalContainer(ctx, row, o.Service)
	if err != nil {
		return nil, err
	}
	b := s.terminalBackend()
	if b == nil {
		return nil, apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Try again in a minute, or tell the bitploy admin.")
	}
	running, err := b.Running(ctx, name)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !running {
		return nil, appNotRunningErr(svc)
	}
	release, ok := s.term.acquire(a.UserID)
	if !ok {
		return nil, apperr.TooMany("too_many_terminals",
			"You already have 3 terminals open, which is the limit.",
			"Close one of them (its browser tab), then try again.")
	}
	cols, rows := ClampTerminalSize(o.Cols, o.Rows)
	conn, err := b.Open(ctx, name, cols, rows)
	if err != nil {
		release()
		if e := (*apperr.Error)(nil); errors.As(err, &e) {
			return nil, e
		}
		return nil, apperr.Internal(err)
	}
	t := &Terminal{
		App: row.Slug, Service: svc, conn: conn, backend: b, s: s, actor: a, session: o.Session, ip: o.IP,
		appID: row.ID, container: name, started: s.Now(), release: release,
	}
	s.term.add(t)
	s.Audit(ctx, a, "app.terminal_open", "app", row.ID, t.auditDetails(nil))
	return t, nil
}

// terminalContainer picks the container of a terminal like the runtime
// logs do (logContainer), with the errors of a terminal.
func (s *Service) terminalContainer(ctx context.Context, row db.App, service string) (name, svc string, err error) {
	if s.appIsStatic(ctx, row) {
		return "", "", apperr.Unprocessable("terminal_static",
			"This app is a static site that bitploy serves, so it has no container of its own to open a terminal in.",
			"A terminal opens in apps built from a Dockerfile or a compose file.")
	}
	if row.DesiredState == "stopped" {
		return "", "", appNotRunningErr("")
	}
	name, svc, err = s.logContainer(ctx, row, service)
	if e := apperr.As(err); err != nil && e.Code == "no_container" {
		if svc == "" {
			svc = service
		}
		return "", "", appNotRunningErr(svc)
	}
	return name, svc, err
}

// errNoShell is the error for an image without /bin/sh.
func errNoShell() *apperr.Error {
	return apperr.Unprocessable("terminal_no_shell",
		"This app's container has no shell (/bin/sh), so a terminal cannot open in it.",
		"Images built FROM scratch or distroless leave the shell out. Build from an image that has one, such as alpine or debian, to use the terminal.")
}

func appNotRunningErr(service string) *apperr.Error {
	if service != "" {
		return apperr.Conflict("app_not_running", "The "+service+" service is not running, so there is no container to open a terminal in.",
			"Start or deploy the app, then open the terminal again.").WithDetail("service", service)
	}
	return apperr.Conflict("app_not_running", "The app is not running, so there is no container to open a terminal in.",
		"Start or deploy the app, then open the terminal again.")
}

func (t *Terminal) auditDetails(extra map[string]any) map[string]any {
	d := map[string]any{"app": t.App, "container": t.container}
	if t.Service != "" {
		d["service"] = t.Service
	}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// Read gives what the shell writes.
func (t *Terminal) Read(p []byte) (int, error) { return t.conn.Read(p) }

// Write sends input to the shell.
func (t *Terminal) Write(p []byte) (int, error) { return t.conn.Write(p) }

// Resize changes the size of the shell's terminal (clamped).
func (t *Terminal) Resize(ctx context.Context, cols, rows int) error {
	cols, rows = ClampTerminalSize(cols, rows)
	return t.conn.Resize(ctx, cols, rows)
}

// Exit reports the shell's exit code once it has exited.
func (t *Terminal) Exit(ctx context.Context) (int, bool) { return t.conn.Exit(ctx) }

// Check reports whether the user still may use the terminal: their
// session is still valid (TerminalLoggedOut after a logout, when it expired
// or the account was suspended) and the app is still theirs or shared with
// them (a removed member loses it). It returns the reason to end the
// session, or "".
func (t *Terminal) Check(ctx context.Context) string {
	a := t.actor
	if t.session != "" {
		fresh, _, err := t.s.AuthenticateSession(ctx, t.session, t.ip)
		if err != nil {
			if clientErr(err) {
				return TerminalLoggedOut
			}
			return "" // a failed lookup is no reason to end a session
		}
		a = fresh
	}
	row, err := t.s.LoadApp(ctx, a, t.appID, true)
	if err != nil {
		if clientErr(err) {
			return TerminalNoAccess
		}
		return ""
	}
	if row.DesiredState == "stopped" {
		return TerminalAppGone
	}
	return ""
}

func clientErr(err error) bool {
	var ae *apperr.Error
	return errors.As(err, &ae) && ae.HTTPStatus < 500
}

// ContainerRunning reports whether the terminal's container still runs
// (true when Docker cannot say).
func (t *Terminal) ContainerRunning(ctx context.Context) bool {
	ok, err := t.backend.Running(ctx, t.container)
	return ok || err != nil
}

// Ended gives the reason Close was called with, or "" while it is open.
func (t *Terminal) Ended() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// Close ends the session: the shell is stopped if it still runs, the slot
// freed and the end audited with why it ended. Only the first call counts.
func (t *Terminal) Close(reason string) {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = reason
		t.mu.Unlock()
		defer t.s.term.remove(t)
		if err := t.conn.Close(); err != nil {
			t.s.Log.Warn("terminal: could not stop the shell", "app", t.App, "container", t.container, "err", err)
		}
		t.release()
		secs := int64(t.s.Now().Sub(t.started) / time.Second)
		t.s.Audit(context.Background(), t.actor, "app.terminal_close", "app", t.appID,
			t.auditDetails(map[string]any{"reason": reason, "seconds": secs}))
	})
}
