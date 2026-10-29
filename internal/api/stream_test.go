package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
)

func TestSSEStreamsLinesHeartbeatAndEnd(t *testing.T) {
	old := heartbeatEvery
	heartbeatEvery = 20 * time.Millisecond
	defer func() { heartbeatEvery = old }()
	a := &API{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.sse(w, r, func(context.Context) error { return nil }, func(ctx context.Context, emit func(app.LogLine) error) error {
			emit(app.LogLine{Stream: "build", Text: "step 1 token bp_" + strings.Repeat("a", 40)})
			time.Sleep(70 * time.Millisecond) // long enough for heartbeats
			return emit(app.LogLine{Stream: "build", Text: "step 2"})
		})
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, `data: {"stream":"build","text":"step 1 token bp_[REDACTED]"}`) {
		t.Errorf("first line missing or token not redacted:\n%s", all)
	}
	if strings.Count(all, ": ping") < 2 {
		t.Errorf("no heartbeat comments:\n%s", all)
	}
	if !strings.Contains(all, "step 2") || !strings.HasSuffix(strings.TrimSpace(all), "data: {}") || !strings.Contains(all, "event: end") {
		t.Errorf("stream did not finish with event: end:\n%s", all)
	}
}

func TestSSEPrecheckErrorIsPlainJSON(t *testing.T) {
	a := &API{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	a.sse(w, r, func(context.Context) error {
		return apperr.NotFound("app_not_found", "no such app", "check the name")
	}, func(context.Context, func(app.LogLine) error) error { t.Fatal("run must not start"); return nil })
	if w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") || !strings.Contains(w.Body.String(), "app_not_found") {
		t.Fatalf("%d %s %s", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
}

func TestSSEMidStreamErrorBecomesEvent(t *testing.T) {
	a := &API{}
	w := httptest.NewRecorder()
	a.sse(w, httptest.NewRequest("GET", "/x", nil), func(context.Context) error { return nil },
		func(ctx context.Context, emit func(app.LogLine) error) error {
			emit(app.LogLine{Text: "hello"})
			return errors.New("docker went away")
		})
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "event: error") || strings.Contains(body, "docker went away") {
		t.Fatalf("%d %s", w.Code, body)
	}
}

func TestFollowRoutingFallsThroughWithoutFlag(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	// Unknown deployment with follow=1: precheck fails before streaming, so a normal JSON 404/501 error comes back.
	w := e.asSession("GET", "/api/v1/deployments/dep_nope/log?follow=1", nil)
	if strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("stream started for an unknown deployment: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/apps/nope/logs?follow=1", nil); strings.Contains(w.Header().Get("Content-Type"), "event-stream") {
		t.Fatalf("stream started for an unknown app: %d %s", w.Code, w.Body)
	}
	// Anonymous follow gets a 401, not a stream.
	if w := e.req("GET", "/api/v1/apps/nope/logs?follow=1", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous follow: %d %s", w.Code, w.Body)
	}
}

func TestStreamCapPerUser(t *testing.T) {
	var c streamCounter
	var rels []func()
	for i := 0; i < maxStreamsPerUser; i++ {
		rel, ok := c.acquire("usr_a")
		if !ok {
			t.Fatalf("stream %d refused", i)
		}
		rels = append(rels, rel)
	}
	if _, ok := c.acquire("usr_a"); ok {
		t.Fatal("11th stream of one user must be refused")
	}
	if rel, ok := c.acquire("usr_b"); !ok {
		t.Fatal("another user is not affected")
	} else {
		rel()
	}
	rels[0]()
	if rel, ok := c.acquire("usr_a"); !ok {
		t.Fatal("a closed stream frees its slot")
	} else {
		rel()
	}
}

func TestFollowRefusesStreamsOverTheCap(t *testing.T) {
	a := &API{}
	for i := 0; i < maxStreamsPerUser; i++ {
		if _, ok := a.streams.acquire("usr_a"); !ok {
			t.Fatal("setup")
		}
	}
	h := a.follow(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("must not fall through") }))
	for _, path := range []string{"/api/v1/apps/x/logs?follow=1", "/api/v1/deployments/dep_1/log?follow=1"} {
		r := httptest.NewRequest("GET", path, nil)
		r = r.WithContext(auth.WithActor(r.Context(), app.Actor{UserID: "usr_a", Scope: app.ScopeSession}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 429 || !strings.Contains(w.Body.String(), "too_many_streams") || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestReadDeadlineCutsSlowBodiesButNotSlowResponses(t *testing.T) {
	errs := make(chan error, 2)
	srv := httptest.NewServer(readDeadline(150*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, bodyErr := io.ReadAll(r.Body)
		errs <- bodyErr
		if bodyErr != nil {
			return
		}
		time.Sleep(400 * time.Millisecond) // a long-poll style answer, past the deadline
		if r.Context().Err() != nil {
			t.Error("request context was cancelled after the body was read")
		}
		w.Write([]byte("done"))
	})))
	defer srv.Close()

	// A body that arrives in time, followed by a slow response, works.
	resp, err := http.Post(srv.URL, "text/plain", strings.NewReader("fast"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if bodyErr := <-errs; string(b) != "done" || bodyErr != nil {
		t.Fatalf("fast body: %q %v", b, bodyErr)
	}

	// A body that trickles in too slowly is cut off.
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("slow"))
		time.Sleep(600 * time.Millisecond)
		pw.Close()
	}()
	if resp, err := http.Post(srv.URL, "text/plain", pr); err == nil {
		resp.Body.Close()
	}
	if bodyErr := <-errs; bodyErr == nil {
		t.Fatal("slow body was not cut off by the read deadline")
	}
}
