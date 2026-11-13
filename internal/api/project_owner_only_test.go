package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// In a shared project the member deploys and changes apps, but deleting apps
// and backups and restoring backups is up to the owner.
func TestProjectMemberCannotDelete(t *testing.T) {
	e := newEnv(t)
	e.login("alice@example.com", false)
	p := decode[app.Project](t, e.asSession("POST", "/api/v1/projects", map[string]any{"name": "Shop"}))
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "shop", "project": p.ID})
	if w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	ap := decode[app.App](t, w)
	inv := decode[app.ProjectInvite](t, e.asSession("POST", "/api/v1/projects/"+p.ID+"/invites", map[string]any{}))
	w = e.req("POST", "/api/v1/project-invites/"+strings.TrimPrefix(inv.URL, pub+"/join/")+"/signup",
		map[string]any{"name": "Bob", "email": "bob@example.com", "password": "password-1234"}, map[string]string{"Origin": pub})
	if w.Code != 201 {
		t.Fatalf("signup: %d %s", w.Code, w.Body)
	}
	out := decode[app.ProjectSignup](t, w)
	var bob *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.HasSuffix(c.Name, "bitploy_session") {
			bob = c
		}
	}

	// Bob sees the app as shared (the dashboard hides the delete buttons), and may change it.
	w = e.sessionReq(bob, out.CSRFToken, "GET", "/api/v1/apps/"+ap.Slug, nil)
	if got := decode[app.App](t, w); w.Code != 200 || !got.Shared {
		t.Fatalf("bob's view: %d %s", w.Code, w.Body)
	}
	if w := e.sessionReq(bob, out.CSRFToken, "PATCH", "/api/v1/apps/"+ap.Slug, map[string]any{"name": "Shop front"}); w.Code != 200 {
		t.Fatalf("bob renames: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path string }{
		{"DELETE", "/api/v1/apps/" + ap.Slug + "?confirm_slug=" + ap.Slug},
		{"DELETE", "/api/v1/apps/" + ap.Slug + "/backups/bak_x"},
		{"POST", "/api/v1/apps/" + ap.Slug + "/backups/bak_x/restore"},
	} {
		w := e.sessionReq(bob, out.CSRFToken, c.method, c.path, nil)
		if r := decode[errResp](t, w); w.Code != 403 || r.Error.Code != "owner_only" || !strings.Contains(r.Error.Message, "(Tester)") {
			t.Errorf("bob %s %s: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	// The owner gets past the check (this test server has no proxy to delete
	// an app from, so a wrong confirmation shows it; internal/app deletes).
	if w := e.asSession("DELETE", "/api/v1/apps/"+ap.Slug+"?confirm_slug=nope", nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "confirm_mismatch" {
		t.Fatalf("alice deletes: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("DELETE", "/api/v1/apps/"+ap.Slug+"/backups/bak_x", nil); w.Code != 404 {
		t.Fatalf("alice deletes a backup: %d %s", w.Code, w.Body)
	}
}
