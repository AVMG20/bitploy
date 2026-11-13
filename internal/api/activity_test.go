package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// GET /activity: each user sees the activity on their own and shared apps,
// with other people by name only and never their IP address or email.
func TestActivityEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("alice@example.com", false) // from 203.0.113.7

	if w := e.req("GET", "/api/v1/activity", nil, nil); w.Code != 401 {
		t.Fatalf("logged out: %d %s", w.Code, w.Body)
	}

	w := e.asSession("POST", "/api/v1/projects", map[string]any{"name": "Shop"})
	if w.Code != 201 {
		t.Fatalf("create project: %d %s", w.Code, w.Body)
	}
	p := decode[app.Project](t, w)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Shop", "slug": "shop", "project": p.ID}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{"email": "bob@example.com"})
	code := strings.TrimPrefix(decode[app.ProjectInvite](t, w).URL, pub+"/join/")
	w = e.req("POST", "/api/v1/project-invites/"+code+"/signup", map[string]any{"name": "Bob", "password": "password-1234"}, map[string]string{"Origin": pub})
	if w.Code != 201 {
		t.Fatalf("signup: %d %s", w.Code, w.Body)
	}
	signup := decode[app.ProjectSignup](t, w)
	var bob *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			bob = c
		}
	}
	// bob changes alice's app from 203.0.113.8.
	if w := e.sessionReq(bob, signup.CSRFToken, "PUT", "/api/v1/apps/shop/env", map[string]any{"set": map[string]string{"A": "1"}}); w.Code != 200 {
		t.Fatalf("bob sets env: %d %s", w.Code, w.Body)
	}

	w = e.asSession("GET", "/api/v1/activity", nil)
	if w.Code != 200 {
		t.Fatalf("alice: %d %s", w.Code, w.Body)
	}
	alice := decode[app.Page[app.ActivityEntry]](t, w).Items
	var sawBob, sawOwn bool
	for _, a := range alice {
		if a.Action == "env.set" && a.ActorName == "Bob" && a.Actor == "person" && a.IP == "" && a.App != nil && a.App.Slug == "shop" {
			sawBob = true
		}
		if a.Action == "app.create" && a.Actor == "you" && a.IP == "203.0.113.7" {
			sawOwn = true
		}
	}
	if !sawBob || !sawOwn || strings.Contains(w.Body.String(), "203.0.113.8") || strings.Contains(w.Body.String(), "token_id") {
		t.Errorf("alice's activity: %s", w.Body)
	}

	w = e.sessionReq(bob, signup.CSRFToken, "GET", "/api/v1/activity", nil)
	if w.Code != 200 {
		t.Fatalf("bob: %d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if strings.Contains(body, "alice@example.com") || strings.Contains(body, "auth.login") {
		t.Errorf("bob sees alice's email or logins: %s", body)
	}
	var sawAlice bool
	for _, a := range decode[app.Page[app.ActivityEntry]](t, w).Items {
		if a.Actor != "you" && a.IP != "" {
			t.Errorf("bob sees the IP of %s: %+v", a.ActorName, a)
		}
		if a.Action == "app.create" && a.Actor == "person" && a.ActorName == "Tester" && a.App != nil && a.App.Slug == "shop" {
			sawAlice = true
		}
	}
	if !sawAlice {
		t.Errorf("bob does not see alice making the shared app: %s", body)
	}

	// A read-only token reads it; the app filter and paging work.
	w = e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"})
	bearer := map[string]string{"Authorization": "Bearer " + decode[app.TokenCreated](t, w).Value}
	w = e.req("GET", "/api/v1/activity?app=shop&limit=1", nil, bearer)
	pg := decode[app.Page[app.ActivityEntry]](t, w)
	if w.Code != 200 || len(pg.Items) != 1 || pg.NextCursor == "" || pg.Items[0].App == nil || pg.Items[0].App.Slug != "shop" {
		t.Fatalf("token, one app: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/activity?app=shop&limit=1&cursor="+pg.NextCursor, nil, bearer); w.Code != 200 || decode[app.Page[app.ActivityEntry]](t, w).Items[0].ID >= pg.Items[0].ID {
		t.Errorf("next page: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/activity?cursor=bad", nil, bearer); w.Code != 400 {
		t.Errorf("bad cursor: %d %s", w.Code, w.Body)
	}
}
