package dockerx

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// LogEntry is one runtime log line.
type LogEntry struct {
	Time   time.Time
	Stream string // stdout | stderr
	Text   string
}

// LogOptions selects runtime log lines.
type LogOptions struct {
	Tail   int       // lines from the end; <= 0 means all
	Since  time.Time // zero = no limit
	Follow bool
}

// StreamLogs reads a container's logs (stdout and stderr demultiplexed) and
// calls fn for every line in order. With Follow it runs until ctx ends or the
// container stops.
func (c *Client) StreamLogs(ctx context.Context, name string, o LogOptions, fn func(LogEntry) error) error {
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: o.Follow}
	if o.Tail > 0 {
		opts.Tail = strconv.Itoa(o.Tail)
	}
	if !o.Since.IsZero() {
		opts.Since = strconv.FormatInt(o.Since.Unix(), 10)
	}
	rc, err := c.API.ContainerLogs(ctx, name, opts)
	if err != nil {
		return err
	}
	defer rc.Close()

	var mu sync.Mutex
	var fnErr error
	emit := func(e LogEntry) {
		mu.Lock()
		defer mu.Unlock()
		if fnErr == nil {
			fnErr = fn(e)
		}
	}
	out := &lineWriter{stream: "stdout", emit: emit}
	errw := &lineWriter{stream: "stderr", emit: emit}
	_, err = stdcopy.StdCopy(out, errw, rc)
	out.flush()
	errw.flush()
	if fnErr != nil {
		return fnErr
	}
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// TailLogs returns the last n lines (both streams) in order.
func (c *Client) TailLogs(ctx context.Context, name string, n int, since time.Time) ([]LogEntry, error) {
	var out []LogEntry
	err := c.StreamLogs(ctx, name, LogOptions{Tail: n, Since: since}, func(e LogEntry) error {
		out = append(out, e)
		return nil
	})
	return out, err
}

type lineWriter struct {
	stream string
	buf    []byte
	emit   func(LogEntry)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 64*1024 { // a runaway line without newline
		w.line(w.buf)
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.line(w.buf)
		w.buf = nil
	}
}

func (w *lineWriter) line(b []byte) {
	s := strings.TrimRight(string(b), "\r")
	e := LogEntry{Stream: w.stream, Text: s}
	if sp := strings.IndexByte(s, ' '); sp > 0 {
		if t, err := time.Parse(time.RFC3339Nano, s[:sp]); err == nil {
			e.Time = t.UTC()
			e.Text = s[sp+1:]
		}
	}
	w.emit(e)
}

var _ io.Writer = (*lineWriter)(nil)
