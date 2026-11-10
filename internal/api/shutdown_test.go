package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

// The long-lived requests end as soon as bitploy stops; http.Server.Shutdown
// would otherwise wait for them until its timeout.

func TestSSEEndsWhenBitployStops(t *testing.T) {
	a := &API{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.sse(w, r, func(context.Context) error { return nil }, func(ctx context.Context, emit func(app.LogLine) error) error {
			emit(app.LogLine{Text: "hello"})
			<-ctx.Done()
			return ctx.Err()
		})
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.stop.stop()
	}()
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(resp.Body)
		done <- string(b)
	}()
	select {
	case body := <-done:
		// No "end" (the log tab would stop) and no "error": the browser reconnects.
		if !strings.Contains(body, "hello") || strings.Contains(body, "event: end") || strings.Contains(body, "event: error") {
			t.Fatalf("body:\n%s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the log stream did not end when bitploy stopped")
	}
}

func TestEventsStreamEndsWhenBitployStops(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	es := openEvents(t, srv.URL, func(r *http.Request) { r.AddCookie(e.cookie) })
	if got := es.next(t); !strings.HasPrefix(got, "hello\t") {
		t.Fatalf("first event: %s", got)
	}
	e.stop()
	ended := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, es.resp.Body)
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the event stream did not end when bitploy stopped")
	}
}

func TestDeploymentLongPollEndsWhenBitployStops(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site"})
	if w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	appID := decode[app.App](t, w).ID
	if _, err := e.svc.Store.DB.Exec(`INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES ('dep_q', ?, 1, 'queued', 'files', 'api', ?)`,
		appID, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.stop()
	}()
	start := time.Now()
	w = e.asSession("GET", "/api/v1/deployments/dep_q?wait=30", nil)
	if w.Code != 200 || decode[app.Deployment](t, w).State != "queued" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the long-poll ran %v after bitploy stopped", took)
	}
}

func TestMCPLongPollsEndWhenBitployStops(t *testing.T) {
	a := &API{}
	h := a.mcpLongPolls(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case <-r.Context().Done():
			w.Write([]byte("ended " + string(body)))
		case <-time.After(300 * time.Millisecond):
			w.Write([]byte("finished " + string(body)))
		}
	}))
	call := func(body string) string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", strings.NewReader(body)))
		return w.Body.String()
	}
	a.stop.stop()
	poll := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_deployment","arguments":{"id":"dep_x","wait_seconds":30}}}`
	if got := call(poll); got != "ended "+poll {
		t.Errorf("long-poll: %q", got)
	}
	if got := call("[" + poll + "]"); !strings.HasPrefix(got, "ended ") {
		t.Errorf("batched long-poll: %q", got)
	}
	// Every other call keeps its context and gets its whole body.
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_deployment","arguments":{"id":"dep_x"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","arguments":{"app":"site"}}}`,
		`not json`,
	} {
		if got := call(body); got != "finished "+body {
			t.Errorf("%s: %q", body, got)
		}
	}
}
