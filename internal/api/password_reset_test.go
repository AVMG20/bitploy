package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestPasswordResetEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.com", true)
	bob, err := e.svc.CreateUser(context.Background(), "bob@example.com", "Bob", "password-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	w := e.req("POST", "/api/v1/auth/login", map[string]string{"email": "bob@example.com", "password": "password-1234"}, nil)
	if w.Code != 200 {
		t.Fatalf("bob login: %d %s", w.Code, w.Body)
	}
	bobInfo := decode[app.SessionInfo](t, w)
	var bobCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			bobCookie = c
		}
	}

	// Not for non-admins, and never with a token.
	if w := e.sessionReq(bobCookie, bobInfo.CSRFToken, "POST", "/api/v1/admin/users/"+bob.ID+"/password-reset", nil); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
		t.Fatalf("bob makes a link: %d %s", w.Code, w.Body)
	}
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "deploy"}))
	if w := e.req("POST", "/api/v1/admin/users/"+bob.ID+"/password-reset", nil, map[string]string{"Authorization": "Bearer " + tok.Value}); w.Code != 403 {
		t.Fatalf("token makes a link: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/admin/users/usr_nope/password-reset", nil); w.Code != 404 {
		t.Fatalf("unknown user: %d %s", w.Code, w.Body)
	}

	w = e.asSession("POST", "/api/v1/admin/users/"+bob.ID+"/password-reset", nil)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	r := decode[app.PasswordReset](t, w)
	code, ok := strings.CutPrefix(r.URL, pub+"/reset/")
	if !ok || r.User.Email != "bob@example.com" {
		t.Fatalf("reset = %+v", r)
	}
	if idempotentPath("/api/v1/admin/users/" + bob.ID + "/password-reset") {
		t.Error("reset links are replayable")
	}
	if logPath("/api/v1/password-resets/"+code+"/use") != "/api/v1/password-resets/[code]/use" || strings.Contains(logPath("/reset/"+code), code) {
		t.Error("the code lands in the request log")
	}

	// The link shows whose account it is, without a login.
	w = e.req("GET", "/api/v1/password-resets/"+code, nil, nil)
	if info := decode[app.PasswordResetInfo](t, w); w.Code != 200 || info.Email != "bob@example.com" || info.Name != "Bob" {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/password-resets/nope", nil, nil); w.Code != 404 || decode[errResp](t, w).Error.Code != "reset_link_invalid" {
		t.Fatalf("unknown code: %d %s", w.Code, w.Body)
	}

	w = e.req("POST", "/api/v1/password-resets/"+code+"/use", map[string]any{"password": "brand-new-pass-1"}, map[string]string{"Origin": pub})
	if w.Code != 200 {
		t.Fatalf("use: %d %s", w.Code, w.Body)
	}
	if info := decode[app.SessionInfo](t, w); info.User.ID != bob.ID || info.CSRFToken == "" {
		t.Fatalf("session = %s", w.Body)
	}
	var fresh *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			fresh = c
		}
	}
	if fresh == nil || fresh.Value == "" {
		t.Fatal("no session cookie after the reset")
	}
	if w := e.sessionReq(bobCookie, bobInfo.CSRFToken, "GET", "/api/v1/session", nil); w.Code != 401 {
		t.Errorf("bob's old session: %d", w.Code)
	}
	if w := e.req("POST", "/api/v1/password-resets/"+code+"/use", map[string]any{"password": "another-pass-12"}, map[string]string{"Origin": pub}); w.Code != 410 || decode[errResp](t, w).Error.Code != "reset_link_invalid" {
		t.Fatalf("second use: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/v1/auth/login", map[string]string{"email": "bob@example.com", "password": "brand-new-pass-1"}, nil); w.Code != 200 {
		t.Fatalf("login with the new password: %d %s", w.Code, w.Body)
	}
}
