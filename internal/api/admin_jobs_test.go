package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestAdminJobsEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)

	w := e.asSession("GET", "/api/v1/admin/jobs", nil)
	if w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	list := decode[app.JobList](t, w)
	if len(list.Jobs) == 0 {
		t.Fatal("no jobs")
	}
	// A job that has not run says so with null, not a missing field.
	for _, f := range []string{`"jobs":`, `"since":null`, `"last_run":null`, `"next_run_at":null`, `"runs":0`, `"schedule":"Every minute"`} {
		if !strings.Contains(w.Body.String(), f) {
			t.Errorf("no %s in %s", f, w.Body)
		}
	}

	// An admin's API token is not a dashboard session.
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "adm", "scope": "read"}))
	if w := e.req("GET", "/api/v1/admin/jobs", nil, map[string]string{"Authorization": "Bearer " + tok.Value}); w.Code != 401 && w.Code != 403 {
		t.Errorf("admin token: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/admin/jobs", nil, nil); w.Code != 401 {
		t.Errorf("anonymous: %d %s", w.Code, w.Body)
	}

	// Not an admin.
	u := newEnv(t)
	u.login("user@example.nl", false)
	if w := u.asSession("GET", "/api/v1/admin/jobs", nil); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
		t.Errorf("user: %d %s", w.Code, w.Body)
	}

	if spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String(); !strings.Contains(spec, "adminJobs") {
		t.Error("openapi has no adminJobs")
	}
}
