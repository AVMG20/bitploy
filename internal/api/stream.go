package api

import (
	"context"
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

var heartbeatEvery = 15 * time.Second

// maxStreamsPerUser caps concurrent ?follow=1 streams per user: each one holds
// a connection, a goroutine and (for runtime logs) a Docker log stream.
const maxStreamsPerUser = 10

// streamCounter counts open streams per user. The zero value is ready to use.
type streamCounter struct {
	mu   sync.Mutex
	open map[string]int
}

// acquire reserves a stream slot; release must be called when the stream ends.
func (c *streamCounter) acquire(user string) (release func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.open == nil {
		c.open = map[string]int{}
	}
	if c.open[user] >= maxStreamsPerUser {
		return nil, false
	}
	c.open[user]++
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.open[user]--; c.open[user] <= 0 {
			delete(c.open, user)
		}
	}, true
}

func tooManyStreams() *apperr.Error {
	return apperr.TooMany("too_many_streams",
		"You already have 10 live log streams open, which is the limit.",
		"Close one (leave the logs page or stop the other follow request), or read logs without follow=1.").
		WithDetail("retry_after_seconds", 5)
}

// follow handles the ?follow=1 variants of the two log endpoints as
// Server-Sent Events. Everything else falls through to the JSON/text handlers.
func (a *API) follow(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !truthy(r.URL.Query().Get("follow")) {
			next.ServeHTTP(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix+"/")
		parts := strings.Split(rest, "/")
		actor, hasActor := auth.ActorFrom(r.Context())
		switch {
		case len(parts) == 3 && parts[0] == "deployments" && parts[2] == "log":
			if !hasActor {
				next.ServeHTTP(w, r) // the normal handler answers 401
				return
			}
			release, ok := a.streams.acquire(actor.UserID)
			if !ok {
				auth.WriteError(w, tooManyStreams())
				return
			}
			defer release()
			id := parts[1]
			a.sse(w, r, func(ctx context.Context) error {
				_, err := a.svc.GetDeployment(ctx, actor, id, 0)
				return err
			}, func(ctx context.Context, emit func(app.LogLine) error) error {
				return a.svc.FollowBuildLog(ctx, actor, id, emit)
			})
		case len(parts) == 3 && parts[0] == "apps" && parts[2] == "logs" && r.URL.Query().Get("kind") != "build":
			if !hasActor {
				next.ServeHTTP(w, r)
				return
			}
			release, ok := a.streams.acquire(actor.UserID)
			if !ok {
				auth.WriteError(w, tooManyStreams())
				return
			}
			defer release()
			ref := parts[1]
			tail := 200
			if v := r.URL.Query().Get("tail"); v != "" {
				tail = atoiDefault(v, 200)
			}
			tail = tailOrDefault(tail, 200)
			a.sse(w, r, func(ctx context.Context) error {
				_, err := a.svc.LoadApp(ctx, actor, ref, false)
				return err
			}, func(ctx context.Context, emit func(app.LogLine) error) error {
				return a.svc.FollowRuntimeLogs(ctx, actor, ref, tail, emit, r.URL.Query().Get("service"))
			})
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func truthy(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
		if n > 1_000_000 {
			return def
		}
	}
	return n
}

// sse runs a log stream. precheck runs before any bytes are sent so a missing
// app or deployment is still a normal JSON error.
func (a *API) sse(w http.ResponseWriter, r *http.Request, precheck func(context.Context) error, run func(context.Context, func(app.LogLine) error) error) {
	if err := precheck(r.Context()); err != nil {
		auth.WriteError(w, err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	rc := http.NewResponseController(w)
	// A stream outlives any read deadline; an expiring one would cancel the request.
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
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
		return rc.Flush()
	}
	_ = write(": ping\n\n")

	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(heartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if write(": ping\n\n") != nil {
					cancel()
					return
				}
			}
		}
	}()

	err := run(ctx, func(l app.LogLine) error {
		l.Text = apperr.Redact(l.Text)
		b, _ := json.Marshal(l)
		return write("data: " + string(b) + "\n\n")
	})
	cancel()
	<-done
	if err != nil && r.Context().Err() == nil {
		e := apperr.As(err)
		b, _ := json.Marshal(map[string]any{"error": errBody{Code: e.Code, Message: apperr.Redact(e.Message), Hint: apperr.Redact(e.Hint), Next: e.Next}})
		_ = write("event: error\ndata: " + string(b) + "\n\n")
		return
	}
	_ = write("event: end\ndata: {}\n\n")
}
