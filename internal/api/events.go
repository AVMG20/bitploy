package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
)

// eventsHeartbeat keeps the stream alive through proxies that close idle connections.
var eventsHeartbeat = 25 * time.Second

// eventsRetryMs is the reconnect delay the browser is told to use.
const eventsRetryMs = 3000

// events is GET /api/v1/events: one Server-Sent Events stream per signed-in
// user with small "something changed" hints (see docs/CONTRACT.md). A session
// or any API token may open it; it only carries hints about the caller's own
// apps. The stream has no deadline, like the log streams: bitploy sets no
// server WriteTimeout, and Traefik passes text/event-stream through unbuffered.
func (a *API) events(w http.ResponseWriter, r *http.Request) {
	actor, err := auth.Require(r.Context())
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	release, ok := a.eventStreams.acquire(actor.UserID)
	if !ok {
		auth.WriteError(w, apperr.TooMany("too_many_streams",
			"You already have 10 live update streams open, which is the limit.",
			"Close a bitploy tab you are not using.").WithDetail("retry_after_seconds", 5))
		return
	}
	defer release()

	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	var mu sync.Mutex
	write := func(s string) error {
		mu.Lock()
		defer mu.Unlock()
		// A browser that stopped reading must not hold this goroutine for long.
		// The deadline is lifted again after the write: over HTTP/2 an expired
		// deadline resets the stream even when nothing is being written.
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
		return rc.Flush()
	}

	sub := a.svc.Events().Subscribe(actor.UserID)
	defer sub.Close()

	if write(": bitploy events\nretry: "+itoa(eventsRetryMs)+"\n\n") != nil {
		return
	}
	if write(frame(app.Event{Type: app.EventHello})) != nil {
		return
	}

	t := time.NewTicker(eventsHeartbeat)
	defer t.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if write(": ping\n\n") != nil {
				return
			}
		case <-sub.Wake():
			var b strings.Builder
			for _, e := range sub.Drain() {
				b.WriteString(frame(e))
			}
			if b.Len() > 0 && write(b.String()) != nil {
				return
			}
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// frame renders one event: the type is the SSE event name and also in the data.
func frame(e app.Event) string {
	b, _ := json.Marshal(e)
	return "event: " + e.Type + "\ndata: " + string(b) + "\n\n"
}

// ---- hints for changes made through this door ----

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// hints publishes an "app" event after a successful change below
// /api/v1/apps/{app}/..., so the dashboard of the owner (and of an admin
// acting for them) updates at once. Background changes publish from the
// service layer; creating and renaming an app publish from their handlers
// because only they know the new slug.
func (a *API) hints(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status < 200 || sw.status >= 400 {
			return
		}
		actor, ok := auth.ActorFrom(r.Context())
		if !ok {
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix+"/"), "/")
		if len(parts) < 2 || parts[0] != "apps" || parts[1] == "" {
			return
		}
		state := ""
		if len(parts) == 2 && r.Method == http.MethodDelete {
			state = "deleted"
		}
		a.svc.PublishAppChange(r.Context(), actor, parts[1], "", state)
	})
}
