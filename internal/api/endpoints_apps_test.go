package api

import (
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

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
