package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// Apps no longer move between projects: PATCH with project answers a clear
// 422 instead of a generic unknown-field error, and the field is not in the
// API docs.
func TestPatchProjectRefused(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site-one"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PATCH", "/api/v1/apps/site-one", map[string]any{"project": "prj_other", "name": "New"})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"code":"project_move_unsupported"`) {
		t.Fatalf("patch project: %d %s", w.Code, w.Body)
	}
	if got := decode[app.App](t, e.asSession("GET", "/api/v1/apps/site-one", nil)); got.Name != "Site" {
		t.Errorf("a refused patch changed the name: %q", got.Name)
	}
	spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String()
	i := strings.Index(spec, `"UpdateAppInput":`)
	if i < 0 {
		t.Fatal("UpdateAppInput not in the spec")
	}
	end := strings.Index(spec[i:], `"type":"object"`)
	if end < 0 || strings.Contains(spec[i:i+end], `"project"`) || !strings.Contains(spec[i:i+end], `"alias"`) {
		t.Errorf("UpdateAppInput in the spec: %s", spec[i:i+min(end, 4000)])
	}
}

// A PATCH with volume=false must stay off: it is the "Keep files in /data"
// switch of the dashboard, and a page refresh reads the app again.
func TestPatchVolumeOffPersists(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site-one", "volume": true})
	if w.Code != 201 || !decode[app.App](t, w).Volume {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PATCH", "/api/v1/apps/site-one", map[string]any{"volume": false})
	if w.Code != 200 || decode[app.App](t, w).Volume {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/apps/site-one", nil)
	if w.Code != 200 || decode[app.App](t, w).Volume {
		t.Fatalf("get after patch: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PATCH", "/api/v1/apps/site-one", map[string]any{"volume": true})
	if w.Code != 200 || !decode[app.App](t, w).Volume {
		t.Fatalf("patch on: %d %s", w.Code, w.Body)
	}
	// delete_data is accepted next to volume=false.
	w = e.asSession("PATCH", "/api/v1/apps/site-one", map[string]any{"volume": false, "delete_data": true})
	if w.Code != 200 || decode[app.App](t, w).Volume {
		t.Fatalf("patch with delete_data: %d %s", w.Code, w.Body)
	}
}
