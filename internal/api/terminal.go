package api

// GET /api/v1/apps/{app}/terminal: a WebSocket to a shell in the app's
// running container (app.OpenTerminal). The protocol is small:
//
//   - binary frames carry terminal data both ways (keystrokes in, output out);
//   - text frames carry one JSON control message each. From the browser:
//     {"type":"resize","cols":120,"rows":40}. From bitploy:
//     {"type":"ready","app","service"} once the shell runs,
//     {"type":"exit","code"} when the shell exited,
//     {"type":"end","reason","message"} when bitploy ended the session
//     (reason idle, max_length, access, session, not_running, container_gone
//     or shutdown), and
//     {"type":"error","error":{code,message,hint}} when it could not open.
//
// The query gives the first size (cols, rows) and, for an app with several
// services, service. Only a dashboard session opens one, and only from the
// dashboard's own origin: a WebSocket handshake is a GET that a browser
// sends with cookies from any website, so the Origin check is what stops
// cross-site WebSocket hijacking.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
)

// Limits of a terminal session; variables so tests can shorten them.
var (
	// terminalIdle ends a session without input for this long.
	terminalIdle = 30 * time.Minute
	// terminalMaxAge ends every session after this long.
	terminalMaxAge = 8 * time.Hour
	// terminalCheckEvery is how often access, the timeouts and the app's
	// state are checked again.
	terminalCheckEvery = 15 * time.Second
	// terminalPingEvery keeps proxies (Cloudflare cuts a WebSocket after
	// 100 s without traffic) from closing a quiet session.
	terminalPingEvery = 25 * time.Second
)

const (
	terminalReadLimit    = 64 << 10 // the biggest frame the browser may send
	terminalWriteTimeout = 30 * time.Second
)

func (a *API) terminalRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	mux.Handle("GET "+prefix+"/apps/{app}/terminal", wrap(http.HandlerFunc(a.terminal)))
}

// terminalControl is a JSON message of the protocol.
type terminalControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// parseResize reads a resize message from the browser. ok is false for
// anything else (other types are ignored, so the protocol can grow).
func parseResize(b []byte) (cols, rows int, ok bool) {
	var m terminalControl
	if err := json.Unmarshal(b, &m); err != nil || m.Type != "resize" || m.Cols <= 0 || m.Rows <= 0 {
		return 0, 0, false
	}
	cols, rows = app.ClampTerminalSize(m.Cols, m.Rows)
	return cols, rows, true
}

var terminalEndMessages = map[string]string{
	app.TerminalIdle:      "The terminal closed after 30 minutes without input.",
	app.TerminalTooLong:   "The terminal closed after 8 hours, the longest a session stays open.",
	app.TerminalNoAccess:  "The terminal closed because you no longer have access to this app.",
	app.TerminalAppGone:   "The terminal closed because the app stopped.",
	app.TerminalLoggedOut: "The terminal closed because you were logged out.",
	app.TerminalContainerGone: "The terminal closed because the app was restarted or deployed again. " +
		"Reconnect to open a shell in the new container.",
	app.TerminalShutdown: "The terminal closed because bitploy restarted.",
}

func (a *API) terminal(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimRight(a.cfg.PublicURL, "/")
	if r.Header.Get("Origin") != origin {
		auth.WriteError(w, apperr.Forbidden("csrf_origin",
			"A terminal opens only from the bitploy dashboard itself, so this request was blocked.",
			"Open the app's Terminal tab in the dashboard."))
		return
	}
	act, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	if err := app.RequireSession(act); err != nil {
		auth.WriteError(w, err)
		return
	}
	// The session is read again while the terminal is open (Terminal.Check).
	session := auth.SessionCookieFrom(r.Context())
	if session == "" {
		auth.WriteError(w, app.RequireSession(app.Actor{}))
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		auth.WriteError(w, apperr.BadRequest("websocket_required",
			"The terminal speaks WebSocket; this request did not ask to upgrade.",
			"Open the app's Terminal tab in the dashboard."))
		return
	}
	var patterns []string
	if u, err := url.Parse(origin); err == nil && u.Host != "" {
		patterns = []string{u.Host}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  patterns, // checked exactly above; this only covers a proxy that rewrites Host
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return // Accept has answered
	}
	defer c.CloseNow()
	c.SetReadLimit(terminalReadLimit)

	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	send := func(v any) error {
		b, _ := json.Marshal(v)
		wctx, wcancel := context.WithTimeout(ctx, terminalWriteTimeout)
		defer wcancel()
		return c.Write(wctx, websocket.MessageText, b)
	}

	q := r.URL.Query()
	t, err := a.svc.OpenTerminal(ctx, act, r.PathValue("app"), app.TerminalOptions{
		Service: q.Get("service"), Cols: atoiDefault(q.Get("cols"), 0), Rows: atoiDefault(q.Get("rows"), 0),
		Session: session, IP: auth.ClientIP(r, a.cfg),
	})
	if err != nil {
		e := apperr.As(err)
		_ = send(map[string]any{"type": "error", "error": errBody{Code: e.Code, Message: apperr.Redact(e.Message), Hint: apperr.Redact(e.Hint), Next: e.Next, Details: e.Details}})
		_ = c.Close(websocket.StatusNormalClosure, e.Code)
		return
	}
	if err := send(map[string]any{"type": "ready", "app": t.App, "service": t.Service}); err != nil {
		t.Close(app.TerminalClosed)
		return
	}

	// The session ends once; the first reason wins. exitCode and exited
	// are the shell's, when it exited by itself.
	var (
		mu       sync.Mutex
		reason   string
		exitCode int
		exited   bool
	)
	// done tells the loop below to end the session. The connection's
	// context stays alive until the last message is sent: a Read whose
	// context ends closes the connection at once.
	done := make(chan struct{})
	var doneOnce sync.Once
	stop := func() { doneOnce.Do(func() { close(done) }) }
	end := func(why string) {
		mu.Lock()
		if reason == "" {
			reason = why
		}
		mu.Unlock()
		stop()
	}

	var lastInput sync.Mutex
	last := time.Now()
	touch := func() {
		lastInput.Lock()
		last = time.Now()
		lastInput.Unlock()
	}
	idleFor := func() time.Duration {
		lastInput.Lock()
		defer lastInput.Unlock()
		return time.Since(last)
	}

	var wg sync.WaitGroup
	// Output: the shell to the browser. It ends when the shell exits (or
	// its streams are closed by t.Close).
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32<<10)
		for {
			n, rerr := t.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, terminalWriteTimeout)
				werr := c.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					end(app.TerminalClosed)
					return
				}
			}
			if rerr != nil {
				select {
				case <-done: // closed by the loop below
					return
				default:
				}
				if why := t.Ended(); why != "" { // closed by bitploy (CloseTerminals)
					end(why)
					return
				}
				// The shell exited, or its container stopped under it.
				code, ok := waitExit(ctx, t)
				why := app.TerminalExited
				if !t.ContainerRunning(ctx) {
					if why = t.Check(ctx); why == "" {
						why = app.TerminalContainerGone
					}
				}
				mu.Lock()
				if reason == "" {
					reason, exitCode, exited = why, code, ok
				}
				mu.Unlock()
				stop()
				return
			}
		}
	}()

	// Input: the browser to the shell.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			typ, b, rerr := c.Read(ctx)
			if rerr != nil {
				end(app.TerminalClosed)
				return
			}
			switch typ {
			case websocket.MessageBinary:
				touch()
				if _, werr := t.Write(b); werr != nil {
					end(app.TerminalClosed)
					return
				}
			case websocket.MessageText:
				if cols, rows, ok := parseResize(b); ok {
					if err := t.Resize(ctx, cols, rows); err != nil {
						a.svc.Log.Debug("terminal resize failed", "app", t.App, "err", err)
					}
				}
			}
		}
	}()

	// Keepalive, timeouts and access.
	started := time.Now()
	check := time.NewTicker(terminalCheckEvery)
	ping := time.NewTicker(terminalPingEvery)
	func() {
		defer check.Stop()
		defer ping.Stop()
		for {
			select {
			case <-done:
				return
			case <-ping.C:
				pctx, pcancel := context.WithTimeout(ctx, terminalWriteTimeout)
				err := c.Ping(pctx)
				pcancel()
				if err != nil {
					end(app.TerminalClosed)
				}
			case <-check.C:
				switch {
				case idleFor() >= terminalIdle:
					end(app.TerminalIdle)
				case time.Since(started) >= terminalMaxAge:
					end(app.TerminalTooLong)
				default:
					if why := t.Check(ctx); why != "" {
						end(why)
					}
				}
			}
		}
	}()

	// Tell the browser why, stop the shell, close, and wait for both pumps.
	mu.Lock()
	why, code, codeKnown := reason, exitCode, exited
	mu.Unlock()
	if why == "" {
		why = app.TerminalClosed
	}
	fin, fcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer fcancel()
	finish := func(v any) {
		b, _ := json.Marshal(v)
		_ = c.Write(fin, websocket.MessageText, b)
	}
	switch {
	case why == app.TerminalExited:
		m := map[string]any{"type": "exit"}
		if codeKnown {
			m["code"] = code
		}
		finish(m)
	case terminalEndMessages[why] != "":
		finish(map[string]any{"type": "end", "reason": why, "message": terminalEndMessages[why]})
	}
	// The shell first: the close handshake below waits for the browser.
	t.Close(why)
	if why != app.TerminalClosed {
		_ = c.Close(websocket.StatusNormalClosure, why)
	}
	_ = c.CloseNow() // ends a Read still waiting
	cancel()
	wg.Wait()
}

// waitExit waits briefly for Docker to have the exit code of a shell whose
// output ended.
func waitExit(ctx context.Context, t *app.Terminal) (int, bool) {
	for i := 0; i < 20; i++ {
		if code, ok := t.Exit(ctx); ok {
			return code, true
		}
		select {
		case <-ctx.Done():
			return 0, false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return 0, false
}
