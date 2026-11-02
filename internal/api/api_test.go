package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/auth"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/store"
)

const pub = "https://bitploy.test"

type env struct {
	t      *testing.T
	svc    *app.Service
	h      http.Handler
	cookie *http.Cookie
	csrf   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{PublicURL: pub, AppsDomain: "bitploy.test", AppsScheme: "https", DataDir: dir, Signup: "invite"}
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	crypto, _ := store.NewCrypto([]byte("0123456789abcdef0123456789abcdef"))
	svc := app.New(cfg, st, crypto, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	web := fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>spa</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
	}
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("mcp ok")) })
	return &env{t: t, svc: svc, h: New(svc, mcp, web)}
}

func (e *env) req(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.7:1000"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// asSession sends the request as the logged-in dashboard user, with CSRF.
func (e *env) asSession(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.7:1000"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", pub)
	r.Header.Set("X-CSRF-Token", e.csrf)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) login(email string, admin bool) {
	e.t.Helper()
	if _, err := e.svc.CreateUser(context.Background(), email, "Tester", "password-1234", admin); err != nil {
		e.t.Fatal(err)
	}
	w := e.req("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "password-1234"}, nil)
	if w.Code != 200 {
		e.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			e.cookie = c
		}
	}
	if e.cookie == nil {
		e.t.Fatal("login did not set the session cookie")
	}
	var info app.SessionInfo
	json.Unmarshal(w.Body.Bytes(), &info)
	e.csrf = info.CSRFToken
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
	return v
}

type errResp struct {
	Error struct {
		Code, Message, Hint, Next string
		Details                   map[string]any
	}
}

func TestLoginSetsHardenedCookieAndSessionEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	c := e.cookie
	if c.Name != "__Host-bitploy_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie: %+v", c)
	}
	w := e.asSession("GET", "/api/v1/session", nil)
	if w.Code != 200 {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	info := decode[app.SessionInfo](t, w)
	if info.CSRFToken != e.csrf || info.User.Email != "me@example.nl" {
		t.Fatalf("session info: %+v", info)
	}
	// Wrong password: 401 in the standard error shape.
	w = e.req("POST", "/api/v1/auth/login", map[string]string{"email": "me@example.nl", "password": "wrong-wrong"}, nil)
	er := decode[errResp](t, w)
	if w.Code != 401 || er.Error.Code != "invalid_credentials" || er.Error.Hint == "" {
		t.Fatalf("bad login: %d %s", w.Code, w.Body)
	}
	// Logout deletes the session and clears the cookie.
	w = e.asSession("POST", "/api/v1/auth/logout", nil)
	if w.Code != 204 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/session", nil); w.Code != 401 {
		t.Fatalf("session after logout: %d", w.Code)
	}
}

func TestLoginRateLimitReturns429WithRetryAfter(t *testing.T) {
	e := newEnv(t)
	e.svc.CreateUser(context.Background(), "me@example.nl", "Me", "password-1234", false)
	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = e.req("POST", "/api/v1/auth/login", map[string]string{"email": "me@example.nl", "password": "wrong-wrong"}, nil)
	}
	if last.Code != 429 || last.Header().Get("Retry-After") == "" {
		t.Fatalf("%d retry-after=%q %s", last.Code, last.Header().Get("Retry-After"), last.Body)
	}
}

// M1 acceptance, through the whole HTTP stack.
func TestM1Acceptance(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)

	// A POST with a valid session cookie from an app subdomain origin is rejected.
	r := httptest.NewRequest("POST", "/api/v1/tokens", strings.NewReader(`{"name":"x","scope":"read"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", "https://x.bitploy.nl")
	r.Header.Set("X-CSRF-Token", e.csrf)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 403 || decode[errResp](t, w).Error.Code != "csrf_origin" {
		t.Fatalf("foreign origin: %d %s", w.Code, w.Body)
	}
	// Same with the origin of the configured apps domain.
	r = httptest.NewRequest("POST", "/api/v1/tokens", strings.NewReader(`{"name":"x","scope":"read"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", "https://x.bitploy.test")
	r.Header.Set("X-CSRF-Token", e.csrf)
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("app subdomain origin: %d %s", w.Code, w.Body)
	}

	// The right origin + token creates one.
	w = e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "laptop", "scope": "deploy"})
	if w.Code != 201 {
		t.Fatalf("create token: %d %s", w.Code, w.Body)
	}
	tc := decode[app.TokenCreated](t, w)
	if !strings.HasPrefix(tc.Value, "bp_") {
		t.Fatalf("token: %+v", tc)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tc.Value}
	if w := e.req("GET", "/api/v1/me", nil, bearer); w.Code != 200 {
		t.Fatalf("me with token: %d %s", w.Code, w.Body)
	}
	// A token can never create or list tokens.
	if w := e.req("GET", "/api/v1/tokens", nil, bearer); w.Code != 403 || decode[errResp](t, w).Error.Code != "session_required" {
		t.Fatalf("token listing tokens: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/v1/tokens", map[string]any{"name": "x", "scope": "read"}, bearer); w.Code != 403 {
		t.Fatalf("token creating token: %d %s", w.Code, w.Body)
	}
	// Revoke, and the very next request gets 401.
	if w := e.asSession("DELETE", "/api/v1/tokens/"+tc.ID, nil); w.Code != 204 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	w = e.req("GET", "/api/v1/me", nil, bearer)
	if w.Code != 401 || decode[errResp](t, w).Error.Code != "token_invalid" {
		t.Fatalf("revoked token: %d %s", w.Code, w.Body)
	}
}

func TestErrorShapeAndValidation(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	tests := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		code   string
	}{
		{"unknown api path", "GET", "/api/v1/nope", nil, 404, "not_found"},
		{"unknown api root", "GET", "/api/other", nil, 404, "not_found"},
		{"missing required field", "POST", "/api/v1/tokens", map[string]any{"name": "x"}, 422, "invalid_request"},
		{"invalid json", "POST", "/api/v1/tokens", "{nope", 400, "invalid_request"},
		{"unknown field", "POST", "/api/v1/tokens", map[string]any{"name": "x", "scope": "read", "admin": true}, 422, "invalid_request"},
		{"bad scope", "POST", "/api/v1/tokens", map[string]any{"name": "x", "scope": "root"}, 400, "invalid_scope"},
		{"bad cursor", "GET", "/api/v1/tokens?cursor=zzz", nil, 400, "invalid_cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w *httptest.ResponseRecorder
			if s, ok := tt.body.(string); ok {
				r := httptest.NewRequest(tt.method, tt.path, strings.NewReader(s))
				r.Header.Set("Content-Type", "application/json")
				r.AddCookie(e.cookie)
				r.Header.Set("Origin", pub)
				r.Header.Set("X-CSRF-Token", e.csrf)
				w = httptest.NewRecorder()
				e.h.ServeHTTP(w, r)
			} else {
				w = e.asSession(tt.method, tt.path, tt.body)
			}
			if w.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.status, w.Body)
			}
			var raw map[string]map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || raw["error"] == nil {
				t.Fatalf("not in {error:{...}} shape: %s", w.Body)
			}
			er := decode[errResp](t, w)
			if er.Error.Code != tt.code || er.Error.Message == "" {
				t.Fatalf("code %q message %q, want %q: %s", er.Error.Code, er.Error.Message, tt.code, w.Body)
			}
		})
	}
	// No credentials at all.
	w := e.req("GET", "/api/v1/me", nil, nil)
	if er := decode[errResp](t, w); w.Code != 401 || er.Error.Code != "not_logged_in" || er.Error.Hint == "" {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}
}

func TestListsArePaged(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	for i := 0; i < 5; i++ {
		if w := e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}); w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
	}
	w := e.asSession("GET", "/api/v1/tokens?limit=2", nil)
	p1 := decode[app.Page[app.Token]](t, w)
	if len(p1.Items) != 2 || p1.NextCursor == "" {
		t.Fatalf("page 1: %s", w.Body)
	}
	p2 := decode[app.Page[app.Token]](t, e.asSession("GET", "/api/v1/tokens?limit=2&cursor="+p1.NextCursor, nil))
	p3 := decode[app.Page[app.Token]](t, e.asSession("GET", "/api/v1/tokens?limit=2&cursor="+p2.NextCursor, nil))
	if len(p2.Items) != 2 || len(p3.Items) != 1 || p3.NextCursor != "" {
		t.Fatalf("pages: %d %d %q", len(p2.Items), len(p3.Items), p3.NextCursor)
	}
	seen := map[string]bool{}
	for _, it := range append(append(p1.Items, p2.Items...), p3.Items...) {
		if seen[it.ID] {
			t.Fatal("item repeated across pages")
		}
		seen[it.ID] = true
	}
	// limit above 100 is capped, an empty list is [] not null.
	if w := e.asSession("GET", "/api/v1/tokens?limit=5000", nil); w.Code != 200 {
		t.Fatalf("limit 5000: %d %s", w.Code, w.Body)
	}
	e2 := newEnv(t)
	e2.login("x@example.nl", false)
	w = e2.asSession("GET", "/api/v1/sessions", nil)
	if !strings.Contains(w.Body.String(), `"items":[`) || strings.Contains(w.Body.String(), `"items":null`) {
		t.Fatalf("items: %s", w.Body)
	}
}

func TestIdempotencyKeyReplays(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "deploy"}))
	hdr := func(key string) map[string]string {
		h := map[string]string{"Authorization": "Bearer " + tc.Value}
		if key != "" {
			h["Idempotency-Key"] = key
		}
		return h
	}
	post := func(key, name string) *httptest.ResponseRecorder {
		return e.req("POST", "/api/v1/apps", map[string]any{"name": name}, hdr(key))
	}
	w1 := post("key-1", "first")
	w2 := post("key-1", "first")
	if w1.Code != 201 || w2.Code != 201 || w1.Body.String() != w2.Body.String() {
		t.Fatalf("replay differs:\n%d %s\n%d %s", w1.Code, w1.Body, w2.Code, w2.Body)
	}
	if w2.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("replay not marked")
	}
	list := decode[app.Page[app.App]](t, e.req("GET", "/api/v1/apps", nil, hdr("")))
	if len(list.Items) != 1 {
		t.Fatalf("replayed request created %d apps", len(list.Items))
	}
	// Same key, different request.
	if w := e.req("POST", "/api/v1/apps/first/redeploy", nil, hdr("key-1")); w.Code != 422 || decode[errResp](t, w).Error.Code != "idempotency_key_reused" {
		t.Fatalf("key reuse: %d %s", w.Code, w.Body)
	}
	// Without a key every POST creates.
	post("", "a")
	post("", "b")
	list = decode[app.Page[app.App]](t, e.req("GET", "/api/v1/apps", nil, hdr("")))
	if len(list.Items) != 3 {
		t.Fatalf("want 3 apps, got %d", len(list.Items))
	}
	// A failed request is not remembered: fix it and retry with the same key.
	if w := e.req("POST", "/api/v1/apps", map[string]any{"name": "fixed", "type": "cron"}, hdr("key-2")); w.Code == 201 {
		t.Fatal("unknown type should fail")
	}
	if w := post("key-2", "fixed"); w.Code != 201 {
		t.Fatalf("retry after failure: %d %s", w.Code, w.Body)
	}
}

func TestRedeployOfNeverDeployedAppExplainsItself(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	app := decode[app.App](t, e.asSession("POST", "/api/v1/apps", map[string]any{"name": "fresh"}))
	w := e.asSession("POST", "/api/v1/apps/"+app.Slug+"/redeploy", nil)
	er := decode[errResp](t, w)
	if w.Code != 409 || er.Error.Code != "app_not_deployed" || er.Error.Next != "deploy_files" ||
		!strings.Contains(er.Error.Message, "never been deployed") || er.Error.Hint == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A retry that arrives while the first request with the same key is still
// running must wait for it and replay its answer, not do the work again.
func TestIdempotencyKeyConcurrentRetries(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ok":true}`))
	})
	h := idempotency(e.svc, slow)
	const n = 6
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/api/v1/apps", strings.NewReader(`{}`))
			r.Header.Set("Idempotency-Key", "burst-1")
			r = r.WithContext(auth.WithActor(r.Context(), app.Actor{UserID: "usr_1", Scope: app.ScopeDeploy}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			codes[i] = w.Code
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("%d concurrent requests with one key ran the handler %d times, want 1", n, got)
	}
	for i, c := range codes {
		if c != 201 {
			t.Fatalf("request %d: status %d", i, c)
		}
	}
}

func TestKeyLocksReleaseTheirEntries(t *testing.T) {
	var k keyLocks
	unlock := k.lock("a")
	done := make(chan struct{})
	go func() { k.lock("a")(); close(done) }()
	select {
	case <-done:
		t.Fatal("second lock on the same key did not wait")
	case <-time.After(20 * time.Millisecond):
	}
	k.lock("b")() // other keys do not wait
	unlock()
	<-done
	if len(k.m) != 0 {
		t.Fatalf("%d lock entries leaked", len(k.m))
	}
}

// Responses that hold a credential (raw API token, invite URL, upload URL) or
// that belong to account routes must never be stored: the table is in backups.
func TestIdempotencyNeverStoresCredentialResponses(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)
	send := func(method, path string, body any, key string) *httptest.ResponseRecorder {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r := httptest.NewRequest(method, path, rd)
		r.RemoteAddr = "203.0.113.7:1000"
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(e.cookie)
		r.Header.Set("Origin", pub)
		r.Header.Set("X-CSRF-Token", e.csrf)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}
	w1 := send("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}, "k-token")
	w2 := send("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}, "k-token")
	raw := decode[app.TokenCreated](t, w1).Value
	if w1.Code != 201 || w2.Code != 201 || w2.Header().Get("Idempotent-Replayed") != "" || strings.Contains(w2.Body.String(), raw) {
		t.Fatalf("token creation was replayed or leaked:\n%s\n%s", w1.Body, w2.Body)
	}
	if w := send("POST", "/api/v1/admin/invites", map[string]any{}, "k-invite"); w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	if w := send("POST", "/api/v1/apps", map[string]any{"name": "site"}, "k-app"); w.Code != 201 {
		t.Fatalf("app: %d %s", w.Code, w.Body)
	}
	send("POST", "/api/v1/apps/site/uploads", map[string]any{}, "k-upload")
	send("POST", "/api/v1/sessions/revoke-others", nil, "k-sessions")

	rows, err := e.svc.Store.DB.Query(`SELECT key, path, body FROM idempotency_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stored := map[string]bool{}
	for rows.Next() {
		var key, path string
		var body []byte
		rows.Scan(&key, &path, &body)
		stored[key] = true
		if strings.Contains(string(body), raw) || strings.Contains(string(body), "bp_") || strings.Contains(string(body), "/invite/") {
			t.Fatalf("credential stored for %s: %s", path, body)
		}
	}
	if len(stored) != 1 || !stored["k-app"] {
		t.Fatalf("only resource-creating app routes may be stored, got %v", stored)
	}
}

func TestIdempotentPathAllowList(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/v1/apps": true, "/api/v1/apps/x/files": true, "/api/v1/apps/x/deployments": true,
		"/api/v1/apps/x/domains": true, "/api/v1/apps/x/rollback": true, "/api/v1/apps/x/redeploy": true,
		"/api/v1/apps/x/uploads": false, "/api/v1/apps/x/stop": false, "/api/v1/tokens": false,
		"/api/v1/admin/invites": false, "/api/v1/invites/open/accept": false, "/api/v1/auth/login": false,
		"/api/v1/me/password": false, "/api/v1/sessions/revoke-others": false, "/api/v1/apps/x/domains/a.b/check": false,
	} {
		if got := idempotentPath(p); got != want {
			t.Errorf("idempotentPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// What is stored passes through the same redaction as logs.
func TestIdempotencyStoresRedactedBody(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "deploy"}))
	h := map[string]string{"Authorization": "Bearer " + tc.Value, "Idempotency-Key": "k"}
	name := "bp_" + strings.Repeat("a", 30)
	if w := e.req("POST", "/api/v1/apps", map[string]any{"name": name}, h); w.Code != 201 || !strings.Contains(w.Body.String(), name) {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var body string
	if err := e.svc.Store.DB.QueryRow(`SELECT body FROM idempotency_keys WHERE key = 'k'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, name) {
		t.Fatalf("stored body is not redacted: %s", body)
	}
}

func TestRateLimitPerToken(t *testing.T) {
	// A frozen clock: no refill while the loop runs, however slow the machine (-race).
	frozen := time.Now()
	rateClock = func() time.Time { return frozen }
	t.Cleanup(func() { rateClock = time.Now })
	e := newEnv(t)
	e.login("me@example.nl", false)
	other := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "other", "scope": "read"}))
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "busy", "scope": "read"}))
	hdr := map[string]string{"Authorization": "Bearer " + tc.Value}
	ok := 0
	var limited *httptest.ResponseRecorder
	for i := 0; i < 130; i++ {
		w := e.req("GET", "/api/v1/openapi.json", nil, hdr)
		if w.Code == 200 {
			ok++
		} else {
			limited = w
			break
		}
	}
	if ok != 120 || limited == nil {
		t.Fatalf("allowed %d requests before limiting", ok)
	}
	if limited.Code != 429 || limited.Header().Get("Retry-After") == "" || decode[errResp](t, limited).Error.Code != "rate_limited" {
		t.Fatalf("limited response: %d %v %s", limited.Code, limited.Header(), limited.Body)
	}
	// Another token is unaffected, and so is the dashboard session.
	if w := e.req("GET", "/api/v1/openapi.json", nil, map[string]string{"Authorization": "Bearer " + other.Value}); w.Code != 200 {
		t.Fatalf("other token: %d", w.Code)
	}
	if w := e.asSession("GET", "/api/v1/session", nil); w.Code != 200 {
		t.Fatalf("session: %d", w.Code)
	}
}

func TestOpenAPISpecAndDocsPage(t *testing.T) {
	e := newEnv(t)
	w := e.req("GET", "/api/v1/openapi.json", nil, nil)
	if w.Code != 200 {
		t.Fatalf("openapi: %d", w.Code)
	}
	var spec struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.1") {
		t.Fatalf("openapi version %q", spec.OpenAPI)
	}
	want := map[string][]string{
		"/auth/login": {"post"}, "/auth/logout": {"post"}, "/session": {"get"}, "/invites/{code}/accept": {"post"},
		"/me": {"get", "patch"}, "/usage": {"get"}, "/tokens": {"get", "post"}, "/tokens/{id}": {"delete"},
		"/me/password": {"put"},
		"/templates":   {"get"}, "/templates/{stack}": {"get"}, "/dockerfile/validate": {"post"},
		"/apps": {"get", "post"}, "/apps/{app}": {"get", "patch", "delete"},
		"/apps/{app}/files": {"post"}, "/apps/{app}/uploads": {"post"}, "/apps/{app}/git-source": {"put", "patch", "delete"},
		"/apps/{app}/deployments": {"get", "post"}, "/deployments/{id}": {"get"}, "/deployments/{id}/log": {"get"},
		"/apps/{app}/logs": {"get"}, "/apps/{app}/env": {"get", "put"},
		"/apps/{app}/restart": {"post"}, "/apps/{app}/start": {"post"}, "/apps/{app}/stop": {"post"},
		"/apps/{app}/rollback": {"post"}, "/apps/{app}/redeploy": {"post"},
		"/apps/{app}/domains": {"get", "post"}, "/apps/{app}/domains/{host}": {"delete"}, "/apps/{app}/domains/{host}/check": {"post"},
		"/apps/{app}/metrics": {"get"}, "/apps/{app}/traffic": {"get"},
		"/sessions": {"get"}, "/sessions/{id}": {"delete"}, "/sessions/revoke-others": {"post"},
		"/admin/users": {"get"}, "/admin/invites": {"get", "post"}, "/admin/invites/{id}": {"delete"},
		"/admin/quotas/{user_id}": {"put"}, "/admin/server": {"get"}, "/admin/apps": {"get"},
		"/admin/apps/{app}/stop": {"post"}, "/admin/audit": {"get"},
		"/github": {"get"}, "/github/connect": {"post"}, "/github/link": {"post"}, "/github/installations/{id}": {"delete"},
		"/github/repositories": {"get"}, "/github/branches": {"get"},
		"/admin/github": {"get", "delete"}, "/admin/github/manifest": {"post"}, "/admin/github/manifest/complete": {"post"},
	}
	for path, methods := range want {
		item, ok := spec.Paths[path]
		if !ok {
			t.Errorf("spec has no %s", path)
			continue
		}
		for _, m := range methods {
			if _, ok := item[m]; !ok {
				t.Errorf("spec has no %s %s", strings.ToUpper(m), path)
			}
		}
	}

	w = e.req("GET", "/docs/api", nil, nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "/api/v1/apps/{app}/deployments") || !strings.Contains(body, "createApp") {
		t.Fatalf("docs page: %d (%d bytes)", w.Code, len(body))
	}
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if strings.Contains(strings.ToLower(body), bad) {
			t.Fatalf("docs page contains %q", bad)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/apps/some-app", "/api/v1/openapi.json", "/docs/api"} {
		w := e.req("GET", path, nil, nil)
		h := w.Header()
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: csp %q", path, csp)
		}
		if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Referrer-Policy") != "same-origin" || !strings.Contains(h.Get("Strict-Transport-Security"), "max-age=") {
			t.Errorf("%s: headers %v", path, h)
		}
	}
	e.svc.Cfg.Dev = true
	e.svc.Cfg.PublicURL = "http://localhost:5173"
	e.h = New(e.svc, nil, fstest.MapFS{"index.html": {Data: []byte("x")}})
	if got := e.req("GET", "/", nil, nil).Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS in dev: %q", got)
	}
}

func TestSPAFallback(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		path   string
		status int
		body   string
	}{
		{"/", 200, "<title>spa</title>"},
		{"/apps/quiet-otter-3f9a/logs", 200, "<title>spa</title>"},
		{"/invite/abc123", 200, "<title>spa</title>"},
		{"/assets/app-1.js", 200, "console.log(1)"},
		{"/assets/missing.js", 404, ""},
		{"/api/v1/nope", 404, `"code":"not_found"`},
		{"/healthz", 200, `{"ok":true}`},
	}
	for _, tt := range tests {
		w := e.req("GET", tt.path, nil, nil)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.body) {
			t.Errorf("%s: %d %q", tt.path, w.Code, w.Body)
		}
	}
	if cc := e.req("GET", "/assets/app-1.js", nil, nil).Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("assets cache-control %q", cc)
	}
}

func TestCatchAllAppPages(t *testing.T) {
	e := newEnv(t)
	u, _ := e.svc.CreateUser(context.Background(), "me@example.nl", "Me", "password-1234", false)
	for _, row := range []struct{ slug, status, desired string }{
		{"stopped-app", "stopped", "stopped"}, {"live-app", "running", "running"},
	} {
		_, err := e.svc.Store.DB.Exec(`INSERT INTO apps (id, user_id, slug, name, status, desired_state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 1, 1)`,
			"app_"+row.slug, u.ID, row.slug, row.slug, row.status, row.desired)
		if err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		host   string
		path   string
		status int
		text   string
	}{
		{"nothing-here.bitploy.test", "/", 404, "No app here"},
		{"nothing-here.bitploy.test:8000", "/some/path", 404, "No app here"},
		{"a.b.bitploy.test", "/", 404, "No app here"},
		{"stopped-app.bitploy.test", "/", 503, "This app is stopped"},
		{"STOPPED-APP.bitploy.test", "/x", 503, "This app is stopped"},
		{"live-app.bitploy.test", "/", 503, "not responding"},
		{"nothing-here.bitploy.test", "/api/v1/me", 404, "No app here"},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", tt.path, nil)
		r.Host = tt.host
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.text) {
			t.Errorf("%s%s: %d %q", tt.host, tt.path, w.Code, w.Body)
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: content type %q", tt.host, w.Header().Get("Content-Type"))
		}
	}
	// The dashboard host itself is not a catch-all.
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "bitploy.test"
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "<title>spa</title>") {
		t.Errorf("dashboard host: %q", w.Body)
	}
	// Other hosts (custom domains routed here by mistake) are served the SPA, not a stranger's page.
	// A slug with markup is escaped.
	r = httptest.NewRequest("GET", "/", nil)
	r.Host = "<script>.bitploy.test"
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "<script>") {
		t.Error("slug not escaped")
	}
}

func TestMCPMountedWithBodyLimitAndActor(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "mcp", "scope": "read"}))
	var gotChannel app.Channel
	var readErr error
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, _ := actorFrom(r)
		gotChannel = a.Channel
		_, readErr = io.Copy(io.Discard, r.Body)
		w.Write([]byte("ok"))
	})
	e.h = New(e.svc, mcp, fstest.MapFS{"index.html": {Data: []byte("x")}})
	hdr := map[string]string{"Authorization": "Bearer " + tc.Value}
	if w := e.req("POST", "/mcp", "{}", hdr); w.Code != 200 || gotChannel != app.ChannelMCP {
		t.Fatalf("mcp: %d channel=%q", w.Code, gotChannel)
	}
	big := strings.Repeat("x", 4<<20+10)
	e.req("POST", "/mcp", big, hdr)
	if readErr == nil {
		t.Fatal("4 MB body limit not enforced on /mcp")
	}
	// A revoked token is refused before the MCP handler runs.
	e.asSession("DELETE", "/api/v1/tokens/"+tc.ID, nil)
	if w := e.req("POST", "/mcp", "{}", hdr); w.Code != 401 {
		t.Fatalf("revoked on mcp: %d", w.Code)
	}
}

func TestUploadEndpoint(t *testing.T) {
	e := newEnv(t)
	// No auth is needed; the stubbed service refuses an unknown ID in the standard shape.
	r := httptest.NewRequest("POST", "/u/upl_unknown", strings.NewReader("PK\x03\x04zip"))
	r.RemoteAddr = "203.0.113.7:1"
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code == 401 || w.Code == 403 {
		t.Fatalf("upload demanded auth: %d %s", w.Code, w.Body)
	}
	if w.Code != 200 {
		var raw map[string]map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || raw["error"] == nil {
			t.Fatalf("upload error not in standard shape: %d %s", w.Code, w.Body)
		}
	}
	// Content-Length over 95 MB is refused up front.
	r = httptest.NewRequest("POST", "/u/upl_x", strings.NewReader("x"))
	r.ContentLength = 96 << 20
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 413 || decode[errResp](t, w).Error.Code != "upload_too_large" {
		t.Fatalf("oversize: %d %s", w.Code, w.Body)
	}
}

func TestRequestLogDoesNotLeakSecrets(t *testing.T) {
	e := newEnv(t)
	var buf bytes.Buffer
	e.svc.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	e.h = New(e.svc, nil, fstest.MapFS{"index.html": {Data: []byte("x")}})
	e.login("me@example.nl", false)
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "read"}))
	e.req("GET", "/api/v1/me?token="+tc.Value, nil, map[string]string{"Authorization": "Bearer " + tc.Value})
	r := httptest.NewRequest("POST", "/u/upl_supersecretuploadid", strings.NewReader("x"))
	e.h.ServeHTTP(httptest.NewRecorder(), r)
	out := buf.String()
	for _, secret := range []string{tc.Value, e.cookie.Value, e.csrf, "password-1234", "supersecretuploadid"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log contains a secret %q:\n%s", secret[:6], out)
		}
	}
	if !strings.Contains(out, `"method":"GET"`) || !strings.Contains(out, `"status":200`) || !strings.Contains(out, `"duration_ms"`) {
		t.Fatalf("request log missing fields:\n%s", out)
	}
}

func TestPaginateCursor(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	p, err := paginate(items, PageParams{Limit: 2})
	if err != nil || len(p.Items) != 2 || p.NextCursor == "" {
		t.Fatalf("%+v %v", p, err)
	}
	p, _ = paginate(items, PageParams{Limit: 10, Cursor: p.NextCursor})
	if len(p.Items) != 3 || p.Items[0] != 3 || p.NextCursor != "" {
		t.Fatalf("%+v", p)
	}
	for _, bad := range []string{"abc", "!!!", "eDoy"} {
		if _, err := paginate(items, PageParams{Cursor: bad}); err == nil {
			t.Errorf("cursor %q accepted", bad)
		}
	}
	if p, _ := paginate([]int(nil), PageParams{}); p.Items == nil {
		t.Error("nil list should become an empty slice")
	}
}

func actorFrom(r *http.Request) (app.Actor, bool) { return auth.ActorFrom(r.Context()) }

func TestProfileAndPasswordEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	// The email needs the current password; a name does not.
	if w := e.asSession("PATCH", "/api/v1/me", map[string]any{"email": "Ada@Example.nl"}); w.Code != 400 || decode[errResp](t, w).Error.Code != "current_password_required" {
		t.Fatalf("email without password: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("PATCH", "/api/v1/me", map[string]any{"name": "Nope", "email": "Ada@Example.nl", "current_password": "wrong-password"}); w.Code != 401 {
		t.Fatalf("email with wrong password: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/me", nil); decode[app.Me](t, w).User.Name == "Nope" {
		t.Fatal("name changed although the password was wrong")
	}
	if w := e.asSession("PATCH", "/api/v1/me", map[string]any{"name": "Grace"}); w.Code != 200 {
		t.Fatalf("name only: %d %s", w.Code, w.Body)
	}
	w := e.asSession("PATCH", "/api/v1/me", map[string]any{"name": "Ada", "email": "Ada@Example.nl", "current_password": "password-1234"})
	u := decode[app.User](t, w)
	if w.Code != 200 || u.Name != "Ada" || u.Email != "ada@example.nl" {
		t.Fatalf("patch me: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("PATCH", "/api/v1/me", map[string]any{}); w.Code != 400 {
		t.Fatalf("empty patch: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PUT", "/api/v1/me/password", map[string]any{"current_password": "password-1234", "new_password": "another-password-9"})
	if w.Code != 204 {
		t.Fatalf("password: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/me", nil)
	if me := decode[app.Me](t, w); w.Code != 200 || me.Scope != app.ScopeSession || me.PublicURL != pub {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
}

func TestProfileBadNameKeepsTheEmail(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	w := e.asSession("PATCH", "/api/v1/me", map[string]any{"name": strings.Repeat("a", 81), "email": "new@example.nl", "current_password": "password-1234"})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "invalid_name" {
		t.Fatalf("bad name: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/me", nil); decode[app.Me](t, w).User.Email != "me@example.nl" {
		t.Fatalf("email changed although the name was refused: %s", w.Body)
	}
}

func TestGitSourceBranchIsOptional(t *testing.T) {
	e := newEnv(t)
	w := e.req("GET", "/api/v1/openapi.json", nil, nil)
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	s, ok := spec.Components.Schemas["GitSource"]
	if !ok {
		t.Fatal("no GitSource schema")
	}
	for _, r := range s.Required {
		if r == "branch" {
			t.Errorf("branch is required: %v", s.Required)
		}
	}
}
