package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// fakeTerminals stands in for Docker: every container in running runs, and
// each session is a pair of pipes the test drives.
type fakeTerminals struct {
	mu      sync.Mutex
	running map[string]bool
	opened  chan *fakeTermConn
}

func newFakeTerminals(running ...string) *fakeTerminals {
	f := &fakeTerminals{running: map[string]bool{}, opened: make(chan *fakeTermConn, 10)}
	for _, n := range running {
		f.running[n] = true
	}
	return f
}

func (f *fakeTerminals) Running(_ context.Context, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[name], nil
}

func (f *fakeTerminals) Open(_ context.Context, name string, cols, rows int) (app.TerminalConn, error) {
	r, w := io.Pipe()
	c := &fakeTermConn{container: name, size: [2]int{cols, rows}, out: r, outW: w,
		input: make(chan []byte, 10), resized: make(chan [2]int, 10), closed: make(chan struct{})}
	f.opened <- c
	return c, nil
}

type fakeTermConn struct {
	container string
	size      [2]int
	out       *io.PipeReader
	outW      *io.PipeWriter
	input     chan []byte
	resized   chan [2]int
	closed    chan struct{}

	mu       sync.Mutex
	exitCode int
	exited   bool
	once     sync.Once
}

func (c *fakeTermConn) Read(p []byte) (int, error) { return c.out.Read(p) }
func (c *fakeTermConn) Write(p []byte) (int, error) {
	c.input <- append([]byte(nil), p...)
	return len(p), nil
}
func (c *fakeTermConn) Resize(_ context.Context, cols, rows int) error {
	c.resized <- [2]int{cols, rows}
	return nil
}
func (c *fakeTermConn) Exit(context.Context) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitCode, c.exited
}

// exit makes the shell exit with code.
func (c *fakeTermConn) exit(code int) {
	c.mu.Lock()
	c.exitCode, c.exited = code, true
	c.mu.Unlock()
	_ = c.outW.Close()
}

func (c *fakeTermConn) Close() error {
	c.once.Do(func() {
		_ = c.outW.Close()
		close(c.closed)
	})
	return nil
}

// loginAs logs another user in without replacing e's own session.
func (e *env) loginAs(email string, admin bool) (*http.Cookie, string) {
	e.t.Helper()
	old, oldCSRF := e.cookie, e.csrf
	e.cookie = nil
	e.login(email, admin)
	c, csrf := e.cookie, e.csrf
	e.cookie, e.csrf = old, oldCSRF
	return c, csrf
}

// runningApp creates an app as the session user and records a live
// deployment whose container is bp-<slug>-1.
func (e *env) runningApp(slug string) string {
	e.t.Helper()
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": slug, "slug": slug}); w.Code != 201 {
		e.t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	ctx := context.Background()
	row, err := e.svc.Q.GetAppBySlug(ctx, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	dep := "dep_" + slug
	now := time.Now().Unix()
	if err := e.svc.Q.InsertDeployment(ctx, db.InsertDeploymentParams{ID: dep, AppID: row.ID, Seq: 1, SourceKind: "upload", Channel: "dashboard", BuildKind: "dockerfile", QueuedAt: now}); err != nil {
		e.t.Fatal(err)
	}
	name := "bp-" + slug + "-1"
	if err := e.svc.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{ContainerName: sql.NullString{String: name, Valid: true}, ID: dep}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.svc.Q.SetAppCurrentDeployment(ctx, db.SetAppCurrentDeploymentParams{CurrentDeploymentID: sql.NullString{String: dep, Valid: true}, UpdatedAt: now, ID: row.ID}); err != nil {
		e.t.Fatal(err)
	}
	return name
}

// dialTerminal opens the terminal WebSocket with a session cookie and the
// dashboard's origin.
func dialTerminal(t *testing.T, srv *httptest.Server, cookie *http.Cookie, path string) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	h.Set("Origin", pub)
	if cookie != nil {
		h.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, status)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

type termMsg struct {
	Type    string
	App     string
	Service string
	Code    *int
	Reason  string
	Message string
	Error   struct{ Code, Message string }
}

// readMsg reads the next text message (skipping terminal data) and decodes it.
func readMsg(t *testing.T, c *websocket.Conn) termMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var m termMsg
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("bad control message %q: %v", b, err)
		}
		return m
	}
}

func readData(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil || typ != websocket.MessageBinary {
		t.Fatalf("read data: %v %v %q", typ, err, b)
	}
	return string(b)
}

func auditCount(t *testing.T, e *env, action string) int {
	t.Helper()
	var n int
	if err := e.svc.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTerminalRefusedBeforeTheUpgrade(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	e.svc.UseTerminalBackend(newFakeTerminals("bp-shop-1"))
	path := "/api/v1/apps/shop/terminal"
	upgrade := map[string]string{"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ=="}
	with := func(extra map[string]string) map[string]string {
		h := map[string]string{}
		for k, v := range upgrade {
			h[k] = v
		}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	cookie := e.cookie.Name + "=" + e.cookie.Value

	// Not logged in.
	if w := e.req("GET", path, nil, with(map[string]string{"Origin": pub})); w.Code != 401 {
		t.Errorf("anonymous: %d %s", w.Code, w.Body)
	}
	// Another website (cross-site WebSocket hijacking), an app's own address, or no Origin at all.
	for _, origin := range []string{"https://evil.example", "https://shop.bitploy.test", "http://bitploy.test", ""} {
		w := e.req("GET", path, nil, with(map[string]string{"Origin": origin, "Cookie": cookie}))
		if w.Code != 403 || decode[errResp](t, w).Error.Code != "csrf_origin" {
			t.Errorf("origin %q: %d %s", origin, w.Code, w.Body)
		}
	}
	// An API token, even one that deploys.
	w := e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "laptop", "scope": "deploy"})
	tok := decode[app.TokenCreated](t, w)
	if w := e.req("GET", path, nil, with(map[string]string{"Origin": pub, "Authorization": "Bearer " + tok.Value})); w.Code != 403 || decode[errResp](t, w).Error.Code != "session_required" {
		t.Errorf("token: %d %s", w.Code, w.Body)
	}
	// A plain GET.
	if w := e.req("GET", path, nil, map[string]string{"Origin": pub, "Cookie": cookie}); w.Code != 400 || decode[errResp](t, w).Error.Code != "websocket_required" {
		t.Errorf("no upgrade: %d %s", w.Code, w.Body)
	}
	if n := auditCount(t, e, "app.terminal_open"); n != 0 {
		t.Errorf("%d sessions audited", n)
	}
}

func TestTerminalSession(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal?cols=120&rows=40")
	if m := readMsg(t, c); m.Type != "ready" || m.App != "shop" || m.Service != "" {
		t.Fatalf("first message: %+v", m)
	}
	sh := <-fake.opened
	if sh.container != "bp-shop-1" || sh.size != [2]int{120, 40} {
		t.Fatalf("opened %s at %v", sh.container, sh.size)
	}

	ctx := context.Background()
	// Keystrokes go in as binary frames.
	if err := c.Write(ctx, websocket.MessageBinary, []byte("ls -la\r")); err != nil {
		t.Fatal(err)
	}
	if got := string(<-sh.input); got != "ls -la\r" {
		t.Errorf("input %q", got)
	}
	// A resize is a JSON text frame; it is clamped.
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":9999,"rows":50}`)); err != nil {
		t.Fatal(err)
	}
	if got := <-sh.resized; got != [2]int{500, 50} {
		t.Errorf("resize %v", got)
	}
	// Other text is not input.
	_ = c.Write(ctx, websocket.MessageText, []byte(`echo not a command`))
	// Output comes back as binary frames.
	go func() { _, _ = sh.outW.Write([]byte("total 0\r\n")) }()
	if got := readData(t, c); got != "total 0\r\n" {
		t.Errorf("output %q", got)
	}
	select {
	case b := <-sh.input:
		t.Errorf("a text frame reached the shell: %q", b)
	default:
	}
	if n := auditCount(t, e, "app.terminal_open"); n != 1 {
		t.Errorf("open audited %d times", n)
	}

	// The shell exits: the browser hears the code, and the session is closed and audited.
	sh.exit(3)
	if m := readMsg(t, c); m.Type != "exit" || m.Code == nil || *m.Code != 3 {
		t.Fatalf("exit message: %+v", m)
	}
	waitFor(t, func() bool { return auditCount(t, e, "app.terminal_close") == 1 })
	var details string
	_ = e.svc.Store.DB.QueryRow(`SELECT details FROM audit_log WHERE action = 'app.terminal_close'`).Scan(&details)
	if !strings.Contains(details, `"reason":"exited"`) || !strings.Contains(details, `"container":"bp-shop-1"`) || strings.Contains(details, "ls -la") {
		t.Errorf("close audit: %s", details)
	}
}

func TestTerminalClosedByTheBrowserStopsTheShell(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	readMsg(t, c)
	sh := <-fake.opened
	if sh.size != [2]int{80, 24} {
		t.Errorf("default size %v", sh.size)
	}
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-sh.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the shell was not stopped when the browser went away")
	}
	waitFor(t, func() bool { return auditCount(t, e, "app.terminal_close") == 1 })
}

func TestTerminalErrorsAfterTheUpgrade(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	e.runningApp("halted") // its container does not run
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "new", "slug": "new"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	other, _ := e.loginAs("other@example.nl", false)
	admin, _ := e.loginAs("admin@example.nl", true)
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		path   string
		code   string
	}{
		{"someone else's app", other, "/api/v1/apps/shop/terminal", "app_not_found"},
		{"an admin on someone else's app", admin, "/api/v1/apps/shop/terminal", "app_not_found"},
		{"no such app", e.cookie, "/api/v1/apps/nope/terminal", "app_not_found"},
		{"never deployed", e.cookie, "/api/v1/apps/new/terminal", "app_not_running"},
		{"container not running", e.cookie, "/api/v1/apps/halted/terminal", "app_not_running"},
		{"unknown service", e.cookie, "/api/v1/apps/shop/terminal?service=db", "service_not_found"},
	} {
		c := dialTerminal(t, srv, tc.cookie, tc.path)
		if m := readMsg(t, c); m.Type != "error" || m.Error.Code != tc.code || m.Error.Message == "" {
			t.Errorf("%s: %+v", tc.name, m)
		}
	}
	select {
	case <-fake.opened:
		t.Error("a shell was opened")
	default:
	}

	// Stopped by its owner.
	e.stopApp("shop")
	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	if m := readMsg(t, c); m.Type != "error" || m.Error.Code != "app_not_running" {
		t.Errorf("stopped app: %+v", m)
	}
}

func TestTerminalLimitPerUser(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	var open []*websocket.Conn
	for i := 0; i < app.MaxTerminalsPerUser; i++ {
		c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
		if m := readMsg(t, c); m.Type != "ready" {
			t.Fatalf("session %d: %+v", i, m)
		}
		open = append(open, c)
	}
	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	if m := readMsg(t, c); m.Type != "error" || m.Error.Code != "too_many_terminals" {
		t.Fatalf("one too many: %+v", m)
	}
	// Closing one frees its slot.
	open[0].Close(websocket.StatusNormalClosure, "")
	waitFor(t, func() bool { return auditCount(t, e, "app.terminal_close") == 1 })
	c = dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	if m := readMsg(t, c); m.Type != "ready" {
		t.Fatalf("after closing one: %+v", m)
	}
}

func TestTerminalIdleEndsTheSession(t *testing.T) {
	oldIdle, oldCheck := terminalIdle, terminalCheckEvery
	terminalIdle, terminalCheckEvery = 150*time.Millisecond, 20*time.Millisecond
	defer func() { terminalIdle, terminalCheckEvery = oldIdle, oldCheck }()

	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	readMsg(t, c)
	sh := <-fake.opened
	if m := readMsg(t, c); m.Type != "end" || m.Reason != "idle" || m.Message == "" {
		t.Fatalf("idle: %+v", m)
	}
	<-sh.closed
}

func TestTerminalEndsWhenTheAppStops(t *testing.T) {
	oldCheck := terminalCheckEvery
	terminalCheckEvery = 20 * time.Millisecond
	defer func() { terminalCheckEvery = oldCheck }()

	e := newEnv(t)
	e.login("owner@example.nl", false)
	e.runningApp("shop")
	fake := newFakeTerminals("bp-shop-1")
	e.svc.UseTerminalBackend(fake)
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	c := dialTerminal(t, srv, e.cookie, "/api/v1/apps/shop/terminal")
	readMsg(t, c)
	sh := <-fake.opened
	e.stopApp("shop")
	if m := readMsg(t, c); m.Type != "end" || m.Reason != "not_running" {
		t.Fatalf("stopped: %+v", m)
	}
	<-sh.closed
}

func TestParseResize(t *testing.T) {
	for _, tc := range []struct {
		in         string
		cols, rows int
		ok         bool
	}{
		{`{"type":"resize","cols":120,"rows":40}`, 120, 40, true},
		{`{"type":"resize","cols":100000,"rows":100000}`, 500, 200, true},
		{`{"type":"resize","cols":0,"rows":40}`, 0, 0, false},
		{`{"type":"resize","cols":-5,"rows":40}`, 0, 0, false},
		{`{"type":"resize","cols":"80","rows":40}`, 0, 0, false},
		{`{"type":"ping"}`, 0, 0, false},
		{`{"cols":80,"rows":24}`, 0, 0, false},
		{`ls -la`, 0, 0, false},
		{``, 0, 0, false},
	} {
		cols, rows, ok := parseResize([]byte(tc.in))
		if cols != tc.cols || rows != tc.rows || ok != tc.ok {
			t.Errorf("parseResize(%q) = %d, %d, %v; want %d, %d, %v", tc.in, cols, rows, ok, tc.cols, tc.rows, tc.ok)
		}
	}
}

// stopApp marks an app stopped by its owner (stopping it for real needs Docker).
func (e *env) stopApp(slug string) {
	e.t.Helper()
	if _, err := e.svc.Store.DB.Exec(`UPDATE apps SET desired_state = 'stopped', status = 'stopped' WHERE slug = ?`, slug); err != nil {
		e.t.Fatal(err)
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
