package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/proxy"
)

// eventStream reads an open /events response and hands out events by name.
type eventStream struct {
	resp   *http.Response
	events chan string // "name\tdata"
	lines  chan string // every raw line, for comments
}

func openEvents(t *testing.T, srvURL string, set func(*http.Request)) *eventStream {
	t.Helper()
	req, _ := http.NewRequest("GET", srvURL+"/api/v1/events", nil)
	set(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	es := &eventStream{resp: resp, events: make(chan string, 100), lines: make(chan string, 1000)}
	if resp.StatusCode != 200 {
		return es
	}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			l := sc.Text()
			es.lines <- l
			switch {
			case strings.HasPrefix(l, "event: "):
				name = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				es.events <- name + "\t" + strings.TrimPrefix(l, "data: ")
			}
		}
	}()
	return es
}

func (es *eventStream) next(t *testing.T) string {
	t.Helper()
	select {
	case e := <-es.events:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("no event arrived")
		return ""
	}
}

func (es *eventStream) none(t *testing.T) {
	t.Helper()
	select {
	case e := <-es.events:
		t.Fatalf("unexpected event %s", e)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestEventsNeedAuthentication(t *testing.T) {
	e := newEnv(t)
	w := e.req("GET", "/api/v1/events", nil, nil)
	if w.Code != 401 || decode[errResp](t, w).Error.Code != "not_logged_in" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = e.req("GET", "/api/v1/events", nil, map[string]string{"Authorization": "Bearer bp_" + strings.Repeat("a", 40)})
	if w.Code != 401 {
		t.Fatalf("bad token: %d %s", w.Code, w.Body)
	}
}

func TestEventsStreamDeliversChanges(t *testing.T) {
	old := eventsHeartbeat
	eventsHeartbeat = 30 * time.Millisecond
	defer func() { eventsHeartbeat = old }()
	e := newEnv(t)
	e.svc.Routes = &proxy.Writer{Dir: t.TempDir(), EntryPoint: "web"}
	e.login("me@example.nl", false)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	// The cookie is Secure; send it by hand over plain HTTP.
	es := openEvents(t, srv.URL, func(r *http.Request) { r.AddCookie(e.cookie) })
	if es.resp.StatusCode != 200 || es.resp.Header.Get("Content-Type") != "text/event-stream" || es.resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("%d %v", es.resp.StatusCode, es.resp.Header)
	}
	if got := es.next(t); !strings.HasPrefix(got, `hello`+"\t") || !strings.Contains(got, `"type":"hello"`) {
		t.Fatalf("first event: %s", got)
	}

	// Creating an app through the API tells the stream.
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site-one"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if got := es.next(t); got != "app\t"+`{"type":"app","app":"site-one"}` {
		t.Fatalf("create event: %s", got)
	}
	drain := func() {
		for {
			select {
			case <-es.events:
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}
	drain()

	// A settings change tells the stream; a rename (which needs Docker, so it
	// is published by hand here) carries the old slug.
	if w := e.asSession("PATCH", "/api/v1/apps/site-one", map[string]any{"name": "Other name"}); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if got := es.next(t); got != "app\t"+`{"type":"app","app":"site-one"}` {
		t.Fatalf("patch event: %s", got)
	}
	drain()
	e.svc.PublishAppChange(context.Background(), app.Actor{UserID: decodeUserID(t, e)}, "site-one", "site-zero", "")
	if got := es.next(t); got != "app\t"+`{"type":"app","app":"site-one","old_app":"site-zero"}` {
		t.Fatalf("rename event: %s", got)
	}
	drain()

	// An env change, and a delete.
	if w := e.asSession("PUT", "/api/v1/apps/site-one/env", map[string]any{"set": map[string]string{"A": "1"}}); w.Code >= 400 {
		t.Fatalf("env: %d %s", w.Code, w.Body)
	}
	if got := es.next(t); got != "app\t"+`{"type":"app","app":"site-one"}` {
		t.Fatalf("env event: %s", got)
	}
	drain()
	if w := e.asSession("DELETE", "/api/v1/apps/site-one?confirm_slug=site-one", nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if got := es.next(t); got != "app\t"+`{"type":"app","app":"site-one","state":"deleted"}` {
		t.Fatalf("delete event: %s", got)
	}

	// Failed requests send nothing, and the heartbeat keeps coming.
	drain()
	if w := e.asSession("PUT", "/api/v1/apps/missing/env", map[string]any{"set": map[string]string{"A": "1"}}); w.Code < 400 {
		t.Fatalf("env of a missing app: %d", w.Code)
	}
	es.none(t)
	pings := 0
	deadline := time.After(2 * time.Second)
	for pings < 2 {
		select {
		case l := <-es.lines:
			if l == ": ping" {
				pings++
			}
		case <-deadline:
			t.Fatalf("no heartbeat, saw %d", pings)
		}
	}
}

func TestEventsOfOneUserNeverReachAnother(t *testing.T) {
	e := newEnv(t)
	e.login("a@example.nl", false)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	aCookie := e.cookie
	esA := openEvents(t, srv.URL, func(r *http.Request) { r.AddCookie(aCookie) })
	esA.next(t) // hello

	// Second user, own session.
	e.cookie, e.csrf = nil, ""
	e.login("b@example.nl", false)
	esB := openEvents(t, srv.URL, func(r *http.Request) { r.AddCookie(e.cookie) })
	esB.next(t)

	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Mine", "slug": "bs-app"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if got := esB.next(t); !strings.Contains(got, `"bs-app"`) {
		t.Fatalf("owner event: %s", got)
	}
	esA.none(t)
}

func TestEventsTokens(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	w := e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"})
	if w.Code != 201 {
		t.Fatalf("token: %d %s", w.Code, w.Body)
	}
	tok := decode[app.TokenCreated](t, w).Value
	// Any valid token may listen: the stream only carries hints about its owner's apps.
	es := openEvents(t, srv.URL, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
	if es.resp.StatusCode != 200 {
		t.Fatalf("read token: %d", es.resp.StatusCode)
	}
	es.next(t) // hello
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "tok-site"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if got := es.next(t); !strings.Contains(got, `"tok-site"`) {
		t.Fatalf("token stream event: %s", got)
	}
}

func TestEventsStreamIsUnsubscribedOnDisconnect(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	es := openEvents(t, srv.URL, func(r *http.Request) { r.AddCookie(e.cookie) })
	es.next(t)
	if !e.svc.Events().Active() {
		t.Fatal("no subscription while connected")
	}
	es.resp.Body.Close()
	deadline := time.Now().Add(3 * time.Second)
	for e.svc.Events().Active() {
		if time.Now().After(deadline) {
			t.Fatal("subscription still registered after the client left")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func decodeUserID(t *testing.T, e *env) string {
	t.Helper()
	w := e.asSession("GET", "/api/v1/me", nil)
	if w.Code != 200 {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	return decode[app.Me](t, w).User.ID
}
