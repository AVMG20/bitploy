//go:build integration

package dockerx

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/ids"
)

// syncBuf collects terminal output from the reader goroutine.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitOutput(t *testing.T, out *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output: %q", want, out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Needs a Docker daemon and busybox (no bash: the shell is sh).
// Run: go test -tags=integration -count=1 -run TestTerminalIntegration ./internal/dockerx/
func TestTerminalIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	if err := c.ensureInitImage(ctx); err != nil {
		t.Fatal(err)
	}
	name := "bp-test-term-" + strings.ToLower(ids.Hex(5))
	if _, err := c.CreateContainer(ctx, RunSpec{
		Name: name, Image: VolumeInitImage, Network: "none", MemoryMB: 64, CPUMillis: 500,
		User: "999:999", Cmd: []string{"sleep", "600"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.RemoveContainer(context.Background(), name) })
	if err := c.StartContainer(ctx, name); err != nil {
		t.Fatal(err)
	}

	// A shell with a TTY of the asked size; the pid marker is not in the output.
	term, err := c.OpenTerminal(ctx, name, TermSize{Cols: 100, Rows: 30}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if term.pid <= 0 {
		t.Fatalf("pid = %d", term.pid)
	}
	out := &syncBuf{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := term.Read(buf)
			_, _ = out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	if _, err := term.Write([]byte("stty size; echo \"T=$TERM\"; id -u\r")); err != nil {
		t.Fatal(err)
	}
	waitOutput(t, out, "30 100")
	waitOutput(t, out, "T=xterm-256color")
	waitOutput(t, out, "999")
	if strings.Contains(out.String(), "bitploy-pid") {
		t.Errorf("the marker leaked: %q", out.String())
	}
	if err := term.Resize(ctx, TermSize{Cols: 132, Rows: 50}); err != nil {
		t.Fatal(err)
	}
	_, _ = term.Write([]byte("stty size\r"))
	waitOutput(t, out, "50 132")

	// Closed while a program runs in the foreground: the shell and the
	// program are gone afterwards.
	_, _ = term.Write([]byte("sleep 300\r"))
	time.Sleep(300 * time.Millisecond)
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	<-done
	var ps bytes.Buffer
	if _, err := c.Exec(ctx, name, []string{"ps"}, ExecOptions{Stdout: &ps}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ps.String(), "sleep 300") || strings.Count(ps.String(), "/bin/sh") > 0 {
		t.Errorf("processes left after Close:\n%s", ps.String())
	}

	// A shell that exits by itself reports its code.
	term, err = c.OpenTerminal(ctx, name, TermSize{Cols: 80, Rows: 24}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = term.Write([]byte("exit 7\r"))
	buf := make([]byte, 4096)
	for {
		if _, err := term.Read(buf); err != nil {
			break
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if code, ok := term.Exit(ctx); ok {
			if code != 7 {
				t.Errorf("exit code %d", code)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no exit code")
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = term.Close()
}
