package auth_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arno/bitploy/internal/app"
	"github.com/arno/bitploy/internal/auth"
	"github.com/arno/bitploy/internal/config"
	"github.com/arno/bitploy/internal/store"
)

const publicURL = "https://bitploy.test"

type fixture struct {
	svc    *app.Service
	cookie string
	csrf   string
	actor  app.Actor
	userID string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{PublicURL: publicURL, AppsDomain: "bitploy.test", AppsScheme: "https", DataDir: dir, Signup: "invite"}
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
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, "me@example.nl", "Me", "password-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	cookie, info, err := svc.Login(ctx, "me@example.nl", "password-1234", "203.0.113.7", "ua")
	if err != nil {
		t.Fatal(err)
	}
	actor, _, err := svc.AuthenticateSession(ctx, cookie, "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, cookie: cookie, csrf: info.CSRFToken, actor: actor, userID: u.ID}
}

// probe is the downstream handler: it reports which actor the middleware built.
func probe(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := auth.ActorFrom(r.Context())
		if !ok {
			w.Write([]byte("anonymous"))
			return
		}
		w.Write([]byte(string(a.Channel) + "/" + string(a.Scope)))
	})
}

func (f *fixture) do(t *testing.T, opt auth.Options, method string, hdr map[string]string, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	h := auth.Middleware(f.svc, opt)(probe(t))
	r := httptest.NewRequest(method, "/api/v1/x", nil)
	r.RemoteAddr = "203.0.113.7:4000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: auth.CookieName(f.svc.Cfg), Value: cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var apiOpts = auth.Options{Channel: app.ChannelAPI, AllowCookie: true, CheckOrigin: true}

func TestCSRFTable(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name     string
		method   string
		origin   string
		csrf     string
		wantCode int
		wantErr  string
	}{
		{"GET needs no CSRF", "GET", "", "", 200, ""},
		{"HEAD needs no CSRF", "HEAD", "", "", 200, ""},
		{"POST ok", "POST", publicURL, f.csrf, 200, ""},
		{"DELETE ok", "DELETE", publicURL, f.csrf, 200, ""},
		// M1 acceptance: valid session cookie, but the request comes from an app subdomain.
		{"POST from an app subdomain origin", "POST", "https://x.bitploy.test", f.csrf, 403, "csrf_origin"},
		{"POST from foreign origin", "POST", "https://evil.example", f.csrf, 403, "csrf_origin"},
		{"POST with origin null", "POST", "null", f.csrf, 403, "csrf_origin"},
		{"POST without origin", "POST", "", f.csrf, 403, "csrf_origin"},
		{"POST origin with trailing slash is not exact", "POST", publicURL + "/", f.csrf, 403, "csrf_origin"},
		{"POST http instead of https", "POST", "http://bitploy.test", f.csrf, 403, "csrf_origin"},
		{"POST right origin, no token", "POST", publicURL, "", 403, "csrf_token"},
		{"POST right origin, wrong token", "POST", publicURL, "nope", 403, "csrf_token"},
		{"PUT wrong origin", "PUT", "https://x.bitploy.test", f.csrf, 403, "csrf_origin"},
		{"PATCH no token", "PATCH", publicURL, "", 403, "csrf_token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := map[string]string{}
			if tt.origin != "" {
				hdr["Origin"] = tt.origin
			}
			if tt.csrf != "" {
				hdr["X-CSRF-Token"] = tt.csrf
			}
			w := f.do(t, apiOpts, tt.method, hdr, f.cookie)
			if w.Code != tt.wantCode {
				t.Fatalf("status %d, want %d: %s", w.Code, tt.wantCode, w.Body)
			}
			if tt.wantErr != "" && !strings.Contains(w.Body.String(), `"code":"`+tt.wantErr+`"`) {
				t.Fatalf("want error code %s, got %s", tt.wantErr, w.Body)
			}
			if tt.wantCode == 200 && tt.method != "HEAD" && w.Body.String() != "dashboard/session" {
				t.Fatalf("actor = %q", w.Body)
			}
		})
	}
}

func TestBearerSkipsCSRF(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tc, err := f.svc.CreateToken(ctx, f.actor, app.CreateTokenInput{Name: "t", Scope: app.ScopeDeploy})
	if err != nil {
		t.Fatal(err)
	}
	// No Origin, no CSRF token, even a hostile Origin: bearer requests are not cookie requests.
	for _, origin := range []string{"", "https://x.bitploy.test"} {
		hdr := map[string]string{"Authorization": "Bearer " + tc.Value}
		if origin != "" {
			hdr["Origin"] = origin
		}
		w := f.do(t, apiOpts, "POST", hdr, "")
		if w.Code != 200 || w.Body.String() != "api/deploy" {
			t.Fatalf("origin %q: %d %s", origin, w.Code, w.Body)
		}
	}
	// A bearer token wins over a cookie and brings no CSRF duty.
	w := f.do(t, apiOpts, "POST", map[string]string{"Authorization": "Bearer " + tc.Value}, f.cookie)
	if w.Code != 200 || w.Body.String() != "api/deploy" {
		t.Fatalf("bearer+cookie: %d %s", w.Code, w.Body)
	}
	// The MCP channel is stamped on the actor.
	w = f.do(t, auth.Options{Channel: app.ChannelMCP}, "POST", map[string]string{"Authorization": "Bearer " + tc.Value}, "")
	if w.Body.String() != "mcp/deploy" {
		t.Fatalf("mcp channel: %s", w.Body)
	}
}

// M1 acceptance: a revoked token gets 401 on its next request.
func TestRevokedTokenGets401OnNextRequest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tc, _ := f.svc.CreateToken(ctx, f.actor, app.CreateTokenInput{Name: "t", Scope: app.ScopeRead})
	hdr := map[string]string{"Authorization": "Bearer " + tc.Value}
	if w := f.do(t, apiOpts, "GET", hdr, ""); w.Code != 200 || w.Body.String() != "api/read" {
		t.Fatalf("before revoke: %d %s", w.Code, w.Body)
	}
	if err := f.svc.RevokeToken(ctx, f.actor, tc.ID); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, apiOpts, "GET", hdr, "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"code":"token_invalid"`) {
		t.Fatalf("after revoke: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("401 should carry WWW-Authenticate")
	}
}

func TestAuthenticationTable(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	good, _ := f.svc.CreateToken(ctx, f.actor, app.CreateTokenInput{Name: "good", Scope: app.ScopeDeploy})
	tests := []struct {
		name     string
		opt      auth.Options
		hdr      map[string]string
		cookie   string
		wantCode int
		wantBody string
	}{
		{"no credentials", apiOpts, nil, "", 200, "anonymous"},
		{"valid token", apiOpts, map[string]string{"Authorization": "Bearer " + good.Value}, "", 200, "api/deploy"},
		{"lowercase scheme", apiOpts, map[string]string{"Authorization": "bearer " + good.Value}, "", 200, "api/deploy"},
		{"unknown token", apiOpts, map[string]string{"Authorization": "Bearer bp_" + strings.Repeat("a", 43)}, "", 401, "token_invalid"},
		{"malformed token", apiOpts, map[string]string{"Authorization": "Bearer nonsense"}, "", 401, "token_invalid"},
		{"basic auth", apiOpts, map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}, "", 401, "token_invalid"},
		{"empty bearer", apiOpts, map[string]string{"Authorization": "Bearer "}, "", 401, "token_invalid"},
		{"valid cookie", apiOpts, nil, f.cookie, 200, "dashboard/session"},
		{"stale cookie is ignored", apiOpts, nil, "stale-cookie-value", 200, "anonymous"},
		{"cookie not accepted on mcp", auth.Options{Channel: app.ChannelMCP}, nil, f.cookie, 200, "anonymous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := f.do(t, tt.opt, "GET", tt.hdr, tt.cookie)
			if w.Code != tt.wantCode || !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Fatalf("got %d %q, want %d %q", w.Code, w.Body, tt.wantCode, tt.wantBody)
			}
		})
	}
	// A stale cookie is cleared.
	w := f.do(t, apiOpts, "GET", nil, "stale-cookie-value")
	if c := w.Header().Get("Set-Cookie"); !strings.Contains(c, "Max-Age=0") && !strings.Contains(c, "Max-Age=-1") && !strings.Contains(c, "01 Jan 1970") {
		t.Fatalf("stale cookie not cleared: %q", c)
	}
}

func TestUnauthenticatedUnsafeRequestWithForeignOrigin(t *testing.T) {
	f := newFixture(t)
	// Login CSRF: no session yet, but a hostile Origin is still refused.
	w := f.do(t, apiOpts, "POST", map[string]string{"Origin": "https://x.bitploy.test"}, "")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "csrf_origin") {
		t.Fatalf("foreign origin: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, apiOpts, "POST", map[string]string{"Origin": publicURL}, ""); w.Code != 200 {
		t.Fatalf("own origin: %d", w.Code)
	}
	if w := f.do(t, apiOpts, "POST", nil, ""); w.Code != 200 {
		t.Fatalf("no origin (curl): %d", w.Code)
	}
}

func TestSuspendedUserIsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	tc, _ := f.svc.CreateToken(ctx, f.actor, app.CreateTokenInput{Name: "t", Scope: app.ScopeRead})
	if _, err := f.svc.Store.DB.Exec(`UPDATE users SET status = 'suspended'`); err != nil {
		t.Fatal(err)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"token":  f.do(t, apiOpts, "GET", map[string]string{"Authorization": "Bearer " + tc.Value}, ""),
		"cookie": f.do(t, apiOpts, "GET", nil, f.cookie),
	} {
		if w.Code != 403 || !strings.Contains(w.Body.String(), "account_suspended") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestClientIP(t *testing.T) {
	cfg := &config.Config{}
	tests := []struct {
		name   string
		remote string
		hdr    map[string]string
		want   string
	}{
		{"public peer ignores X-Forwarded-For", "203.0.113.9:1234", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.9"},
		{"public peer ignores X-Real-IP", "203.0.113.9:1234", map[string]string{"X-Real-IP": "1.2.3.4"}, "203.0.113.9"},
		{"private peer trusts X-Forwarded-For", "172.18.0.2:5555", map[string]string{"X-Forwarded-For": "198.51.100.4"}, "198.51.100.4"},
		{"private peer takes the first hop", "10.0.0.5:5555", map[string]string{"X-Forwarded-For": "198.51.100.4, 172.18.0.1"}, "198.51.100.4"},
		{"X-Real-IP is ignored", "172.18.0.2:5555", map[string]string{"X-Real-IP": "198.51.100.8"}, "172.18.0.2"},
		{"spoofed first hop through Cloudflare is ignored", "172.18.0.2:5555", map[string]string{"X-Forwarded-For": "6.6.6.6, 198.51.100.4, 162.158.1.1"}, "198.51.100.4"},
		{"loopback peer trusted", "127.0.0.1:9", map[string]string{"X-Forwarded-For": "198.51.100.4"}, "198.51.100.4"},
		{"ipv6 loopback trusted", "[::1]:9", map[string]string{"X-Forwarded-For": "198.51.100.4"}, "198.51.100.4"},
		{"ipv6 ULA peer trusted", "[fd00::2]:9", map[string]string{"X-Forwarded-For": "2001:db8::1"}, "2001:db8::1"},
		{"garbage header falls back to the peer", "172.18.0.2:5555", map[string]string{"X-Forwarded-For": "not-an-ip"}, "172.18.0.2"},
		{"no headers", "172.18.0.2:5555", nil, "172.18.0.2"},
		{"ipv4-mapped public peer", "[::ffff:203.0.113.9]:1", map[string]string{"X-Forwarded-For": "1.2.3.4"}, "203.0.113.9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for k, v := range tt.hdr {
				r.Header.Set(k, v)
			}
			if got := auth.ClientIP(r, cfg); got != tt.want {
				t.Fatalf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	prod := &config.Config{}
	c := auth.NewSessionCookie(prod, "v")
	if c.Name != "__Host-bitploy_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("prod cookie: %+v", c)
	}
	dev := &config.Config{Dev: true}
	c = auth.NewSessionCookie(dev, "v")
	if c.Name != "bitploy_session" || c.Secure || !c.HttpOnly {
		t.Fatalf("dev cookie: %+v", c)
	}
	if auth.ExpiredSessionCookie(prod).MaxAge >= 0 {
		t.Fatal("expired cookie should have a negative MaxAge")
	}
}

func TestWriteErrorShape(t *testing.T) {
	w := httptest.NewRecorder()
	auth.WriteError(w, &testErr{})
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "secret-cause") {
		t.Fatal("internal cause leaked")
	}
}

type testErr struct{}

func (*testErr) Error() string { return "secret-cause" }
