package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// sessionReq sends a request with another session's cookie and CSRF token.
func (e *env) sessionReq(cookie *http.Cookie, csrf, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "203.0.113.8:1000"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(cookie)
	r.Header.Set("Origin", pub)
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestProjectInviteEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("alice@example.com", false)

	w := e.asSession("POST", "/api/v1/projects", map[string]any{"name": "Shop"})
	if w.Code != 201 {
		t.Fatalf("create project: %d %s", w.Code, w.Body)
	}
	p := decode[app.Project](t, w)
	if p.Role != "owner" || p.Shared || p.Owner.Email != "alice@example.com" {
		t.Errorf("project = %+v", p)
	}

	// Only a session invites: never a token, of any scope.
	w = e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "laptop", "scope": "deploy"})
	tok := decode[app.TokenCreated](t, w)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Value}
	if w := e.req("POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{}, bearer); w.Code != 403 || decode[errResp](t, w).Error.Code != "session_required" {
		t.Fatalf("token invites: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/projects/"+p.ID+"/members", nil, bearer); w.Code != 200 {
		t.Fatalf("token lists members: %d %s", w.Code, w.Body)
	}

	w = e.asSession("POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{"email": "bob@example.com"})
	if w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	inv := decode[app.ProjectInvite](t, w)
	if !strings.HasPrefix(inv.URL, pub+"/join/") || inv.Email == nil || *inv.Email != "bob@example.com" {
		t.Fatalf("invite = %+v", inv)
	}
	code := strings.TrimPrefix(inv.URL, pub+"/join/")
	// The link is a credential: no Idempotency-Key replay stores it.
	if idempotentPath("/api/v1/projects/" + p.ID + "/invites") {
		t.Error("project invites are replayable")
	}

	// Anyone with the link sees what it is for.
	w = e.req("GET", "/api/v1/project-invites/"+code, nil, nil)
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	if info := decode[app.ProjectInviteInfo](t, w); info.ProjectName != "Shop" || info.InvitedBy != "Tester" {
		t.Errorf("info = %+v", info)
	}
	if w := e.req("GET", "/api/v1/project-invites/nope", nil, nil); w.Code != 404 {
		t.Errorf("unknown code: %d", w.Code)
	}

	// Sign up through it (BITPLOY_SIGNUP=invite) and land in the project.
	w = e.req("POST", "/api/v1/project-invites/"+code+"/signup",
		map[string]any{"name": "Bob", "password": "password-1234"}, map[string]string{"Origin": pub})
	if w.Code != 201 {
		t.Fatalf("signup: %d %s", w.Code, w.Body)
	}
	out := decode[app.ProjectSignup](t, w)
	if out.Project.ID != p.ID || out.User.Email != "bob@example.com" || out.CSRFToken == "" {
		t.Fatalf("signup = %+v", out)
	}
	var bobCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			bobCookie = c
		}
	}
	if bobCookie == nil {
		t.Fatal("no session cookie")
	}
	w = e.sessionReq(bobCookie, out.CSRFToken, "GET", "/api/v1/projects", nil)
	page := decode[app.Page[app.Project]](t, w)
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].ID != p.ID || !page.Items[0].Shared || page.Items[0].Role != "member" {
		t.Fatalf("bob's projects: %d %s", w.Code, w.Body)
	}
	// Bob cannot manage; alice sees him; bob leaves.
	if w := e.sessionReq(bobCookie, out.CSRFToken, "POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{}); w.Code != 403 || decode[errResp](t, w).Error.Code != "project_owner_only" {
		t.Errorf("bob invites: %d %s", w.Code, w.Body)
	}
	if w := e.sessionReq(bobCookie, out.CSRFToken, "DELETE", "/api/v1/projects/"+p.ID+"?confirm=Shop", nil); w.Code != 403 {
		t.Errorf("bob deletes: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/projects/"+p.ID+"/members", nil)
	members := decode[app.Page[app.ProjectMember]](t, w)
	if len(members.Items) != 2 || members.Items[0].Role != "owner" || members.Items[1].Email != "bob@example.com" {
		t.Fatalf("members: %s", w.Body)
	}
	if w := e.sessionReq(bobCookie, out.CSRFToken, "DELETE", "/api/v1/projects/"+p.ID+"/members/me", nil); w.Code != 204 {
		t.Fatalf("leave: %d %s", w.Code, w.Body)
	}
	if w := e.sessionReq(bobCookie, out.CSRFToken, "GET", "/api/v1/projects/"+p.ID, nil); w.Code != 404 {
		t.Errorf("after leaving: %d %s", w.Code, w.Body)
	}

	// A logged-in user accepts with a click.
	w = e.asSession("POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{})
	code = strings.TrimPrefix(decode[app.ProjectInvite](t, w).URL, pub+"/join/")
	if w := e.sessionReq(bobCookie, out.CSRFToken, "POST", "/api/v1/project-invites/"+code+"/accept", nil); w.Code != 200 || decode[app.Project](t, w).ID != p.ID {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	pending := decode[app.Page[app.ProjectInvite]](t, e.asSession("GET", "/api/v1/projects/"+p.ID+"/invites", nil))
	if len(pending.Items) != 0 {
		t.Errorf("pending after use: %+v", pending.Items)
	}
}
