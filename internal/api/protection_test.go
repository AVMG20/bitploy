package api

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/proxy"
)

func TestPasswordProtectionEndpoint(t *testing.T) {
	e := newEnv(t)
	var logs bytes.Buffer
	e.svc.Log = slog.New(slog.NewJSONHandler(io.MultiWriter(&logs), nil))
	e.svc.Routes = &proxy.Writer{Dir: t.TempDir()}
	e.login("me@example.nl", false)
	created := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Shop", "slug": "shop-pw"})
	if created.Code != 201 {
		t.Fatalf("create app: %d %s", created.Code, created.Body)
	}
	if decode[app.App](t, created).Protected {
		t.Fatal("a new app is protected")
	}
	const path = "/api/v1/apps/shop-pw/password-protection"
	body := map[string]any{"enabled": true, "username": "team", "password": "blue-river-42"}

	// Session needs the CSRF token like other settings.
	if w := e.req("PUT", path, body, map[string]string{"Cookie": e.cookie.Name + "=" + e.cookie.Value, "Origin": pub}); w.Code != 403 {
		t.Fatalf("PUT without CSRF token = %d", w.Code)
	}
	w := e.asSession("PUT", path, body)
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	got := decode[app.App](t, w)
	if !got.Protected || got.ProtectedUsername != "team" {
		t.Fatalf("app: %+v", got)
	}
	for _, secret := range []string{"blue-river-42", "$2a$"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("response contains %q: %s", secret, w.Body)
		}
	}
	// GET reports it too.
	if g := decode[app.App](t, e.asSession("GET", "/api/v1/apps/shop-pw", nil)); !g.Protected || g.ProtectedUsername != "team" {
		t.Fatalf("GET app: %+v", g)
	}
	if strings.Contains(logs.String(), "blue-river-42") {
		t.Fatal("the request log holds the password")
	}

	// Validation errors use the shared error shape.
	for _, c := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"enabled": true, "username": "a:b", "password": "long-enough-1"}, "username_invalid"},
		{map[string]any{"enabled": true, "password": "short"}, "password_too_short"},
	} {
		w := e.asSession("PUT", path, c.body)
		if w.Code != 400 || decode[errResp](t, w).Error.Code != c.code {
			t.Errorf("%v: %d %s", c.body, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "\"short\"") {
			t.Errorf("error repeats the password: %s", w.Body)
		}
	}
	if w := e.asSession("PUT", path, map[string]any{"username": "x"}); w.Code != 422 && w.Code != 400 {
		t.Errorf("missing enabled = %d", w.Code)
	}

	// API tokens: deploy scope may, read scope may not.
	mk := func(scope string) string {
		return decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": scope, "scope": scope})).Value
	}
	deploy, read := mk("deploy"), mk("read")
	if w := e.req("PUT", path, map[string]any{"enabled": false}, map[string]string{"Authorization": "Bearer " + read}); w.Code != 403 {
		t.Errorf("read token = %d %s", w.Code, w.Body)
	}
	if g := decode[app.App](t, e.asSession("GET", "/api/v1/apps/shop-pw", nil)); !g.Protected {
		t.Fatal("a refused request changed the app")
	}
	w = e.req("PUT", path, map[string]any{"enabled": false}, map[string]string{"Authorization": "Bearer " + deploy})
	if w.Code != 200 || decode[app.App](t, w).Protected {
		t.Fatalf("deploy token disable: %d %s", w.Code, w.Body)
	}
	// No credentials at all.
	if w := e.req("PUT", path, map[string]any{"enabled": false}, nil); w.Code != 401 {
		t.Errorf("anonymous = %d", w.Code)
	}
}
