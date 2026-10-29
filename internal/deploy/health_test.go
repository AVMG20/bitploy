package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProberRequiresMarkerAndStatusBelow500(t *testing.T) {
	tests := []struct {
		name   string
		status int
		marker string
		want   bool
	}{
		{"app answers 200", 200, "my-app", true},
		{"app answers 404 (still alive)", 404, "my-app", true},
		{"app answers 302", 302, "my-app", true},
		{"app answers 500", 500, "my-app", false},
		{"proxy 502 with marker", 502, "my-app", false},
		{"proxy 404 without marker (route not loaded)", 404, "", false},
		{"200 without marker is not our route", 200, "", false},
		{"marker of another app", 200, "other", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotHost, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHost, gotPath = r.Host, r.URL.Path
				if tc.marker != "" {
					w.Header().Set("X-Bitploy-Candidate", tc.marker)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			p := Prober{Base: srv.URL, Host: "candidate.my-app.bitploy.internal", Header: "X-Bitploy-Candidate", Marker: "my-app"}
			res := p.Probe(context.Background(), "/healthz")
			if res.OK() != tc.want {
				t.Errorf("OK() = %v, want %v (%+v)", res.OK(), tc.want, res)
			}
			if gotHost != "candidate.my-app.bitploy.internal" || gotPath != "/healthz" {
				t.Errorf("host %q path %q", gotHost, gotPath)
			}
		})
	}
}

func TestProberConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	res := Prober{Base: url, Host: "x"}.Probe(context.Background(), "/")
	if res.OK() || res.Err == nil {
		t.Fatalf("%+v", res)
	}
}

func TestPoll(t *testing.T) {
	var n atomic.Int32
	last, ok := Poll(context.Background(), time.Second, 5*time.Millisecond, func(context.Context) PollResult {
		if n.Add(1) == 3 {
			return PollResult{Done: true}
		}
		return PollResult{Detail: "not yet"}
	})
	if !ok || !last.Done || n.Load() != 3 {
		t.Fatalf("ok=%v n=%d", ok, n.Load())
	}
	last, ok = Poll(context.Background(), 50*time.Millisecond, 5*time.Millisecond, func(context.Context) PollResult { return PollResult{Detail: "nope"} })
	if ok || last.Detail != "nope" {
		t.Fatalf("timeout: ok=%v last=%+v", ok, last)
	}
	n.Store(0)
	_, ok = Poll(context.Background(), time.Second, 5*time.Millisecond, func(context.Context) PollResult {
		n.Add(1)
		return PollResult{Fail: true, Detail: "crashed"}
	})
	if ok || n.Load() != 1 {
		t.Fatalf("fail should stop at once: ok=%v n=%d", ok, n.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := Poll(ctx, time.Second, time.Second, func(context.Context) PollResult { return PollResult{} }); ok {
		t.Fatal("cancelled context must not succeed")
	}
}

func TestMentionsLocalhost(t *testing.T) {
	yes := [][]string{
		{"Server listening on http://127.0.0.1:3000"},
		{"ok", "Listening on localhost:8080"},
		{"LOCALHOST"},
	}
	for _, l := range yes {
		if !MentionsLocalhost(l) {
			t.Errorf("%v should mention localhost", l)
		}
	}
	for _, l := range [][]string{nil, {"Listening on 0.0.0.0:8080"}, {"hello"}} {
		if MentionsLocalhost(l) {
			t.Errorf("%v should not", l)
		}
	}
	if LocalhostHint != "Your app listens on localhost; make it listen on 0.0.0.0:$PORT" {
		t.Errorf("hint text changed: %q", LocalhostHint)
	}
}

func TestLineRing(t *testing.T) {
	r := NewLineRing(3)
	for _, l := range []string{"a", "b"} {
		r.Add(l)
	}
	if !reflect.DeepEqual(r.Lines(), []string{"a", "b"}) {
		t.Fatalf("%v", r.Lines())
	}
	for _, l := range []string{"c", "d", "e"} {
		r.Add(l)
	}
	if !reflect.DeepEqual(r.Lines(), []string{"c", "d", "e"}) {
		t.Fatalf("%v", r.Lines())
	}
}

func TestTailAndReadLines(t *testing.T) {
	if got := TailLines("a\nb\nc\n", 2); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("%v", got)
	}
	if TailLines("", 5) != nil {
		t.Fatal("empty input")
	}
	var got []string
	_ = ReadLines(strings.NewReader("one\n\ntwo\r\nlast"), 1000, func(s string) { got = append(got, s) })
	if !reflect.DeepEqual(got, []string{"one", "", "two", "last"}) {
		t.Fatalf("%q", got)
	}
	// A 300 KB line without newline is split instead of buffered whole.
	got = nil
	long := strings.Repeat("x", 300<<10)
	_ = ReadLines(strings.NewReader(long+"\nend"), 100<<10, func(s string) { got = append(got, s) })
	if len(got) < 3 || got[len(got)-1] != "end" || strings.Join(got[:len(got)-1], "") != long {
		t.Fatalf("long line not split safely: %d parts", len(got))
	}
}

func fakeDocker(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildArgs(t *testing.T) {
	b := BuildSpec{ContextDir: "/ctx", Dockerfile: "/ctx/Dockerfile", Tag: "bitploy/x:3", Labels: map[string]string{"b": "2", "a": "1"}}
	want := []string{"buildx", "build", "--progress=plain", "--load", "--provenance=false", "--sbom=false", "-t", "bitploy/x:3", "-f", "/ctx/Dockerfile", "--label", "a=1", "--label", "b=2", "/ctx"}
	if got := b.BuildArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestRunBuild(t *testing.T) {
	t.Run("streams stdout and stderr", func(t *testing.T) {
		var lines []string
		err := RunBuild(context.Background(), BuildSpec{Docker: fakeDocker(t, `echo "#1 step"; echo "#2 warn" >&2; echo "done"`)}, func(l string) { lines = append(lines, l) })
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 3 {
			t.Fatalf("%q", lines)
		}
	})
	t.Run("failure", func(t *testing.T) {
		var lines []string
		err := RunBuild(context.Background(), BuildSpec{Docker: fakeDocker(t, `echo "ERROR: failed to solve"; exit 1`)}, func(l string) { lines = append(lines, l) })
		if err == nil || len(lines) != 1 {
			t.Fatalf("err=%v lines=%q", err, lines)
		}
		if got := BuildFailureSummary(lines); !strings.Contains(got, "failed to solve") {
			t.Errorf("summary %q", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		start := time.Now()
		err := RunBuild(context.Background(), BuildSpec{Docker: fakeDocker(t, `exec sleep 30`), Timeout: 200 * time.Millisecond}, func(string) {})
		if err != ErrBuildTimeout {
			t.Fatalf("err=%v", err)
		}
		if time.Since(start) > 15*time.Second {
			t.Fatalf("took %v", time.Since(start))
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		err := RunBuild(ctx, BuildSpec{Docker: fakeDocker(t, `exec sleep 30`)}, func(string) {})
		if err == nil || err == ErrBuildTimeout {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("docker missing", func(t *testing.T) {
		err := RunBuild(context.Background(), BuildSpec{Docker: "/nonexistent/docker"}, func(string) {})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
