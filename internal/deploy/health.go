package deploy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeResult is the outcome of one HTTP health probe.
type ProbeResult struct {
	Status int  // HTTP status, 0 when the request failed
	Marked bool // the response carried the candidate marker header (it came from the app route)
	Err    error
}

// OK reports a healthy answer: the route is live (marker header present) and
// the app answered with a status below 500.
func (p ProbeResult) OK() bool { return p.Err == nil && p.Marked && p.Status > 0 && p.Status < 500 }

// Prober sends GET requests to a base URL with a fixed Host header.
type Prober struct {
	Client *http.Client
	Base   string // e.g. http://traefik:8081
	Host   string // Host header selecting the candidate router
	Header string // marker header that proves the response went through the candidate router
	Marker string // expected marker value ("" accepts any non-empty value)
}

// Probe does one GET of path.
func (p Prober) Probe(ctx context.Context, path string) ProbeResult {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Base, "/")+path, nil)
	if err != nil {
		return ProbeResult{Err: err}
	}
	req.Host = p.Host
	req.Header.Set("User-Agent", "bitploy-healthcheck")
	c := p.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := c.Do(req)
	if err != nil {
		return ProbeResult{Err: err}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	marked := true
	if p.Header != "" {
		v := resp.Header.Get(p.Header)
		marked = v != "" && (p.Marker == "" || v == p.Marker)
	}
	return ProbeResult{Status: resp.StatusCode, Marked: marked}
}

// PollResult is what one poll step reports to Poll.
type PollResult struct {
	Done   bool   // success: stop polling
	Fail   bool   // permanent failure: stop polling now
	Detail string // short reason for the last attempt, kept for messages
}

// Poll calls step every interval until it reports Done or Fail, the timeout
// passes or ctx ends. It returns the last result and whether it succeeded.
func Poll(ctx context.Context, timeout, interval time.Duration, step func(context.Context) PollResult) (PollResult, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	t := time.NewTicker(interval)
	defer t.Stop()
	var last PollResult
	for {
		last = step(ctx)
		if last.Done {
			return last, true
		}
		if last.Fail {
			return last, false
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-deadline.C:
			return last, false
		case <-t.C:
		}
	}
}

// LocalhostHint is the exact hint for the most common noob failure.
const LocalhostHint = "Your app listens on localhost; make it listen on 0.0.0.0:$PORT"

// MentionsLocalhost reports whether runtime log lines mention 127.0.0.1 or localhost.
func MentionsLocalhost(lines []string) bool {
	for _, l := range lines {
		l = strings.ToLower(l)
		if strings.Contains(l, "127.0.0.1") || strings.Contains(l, "localhost") {
			return true
		}
	}
	return false
}
