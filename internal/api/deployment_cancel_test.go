package api

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestCancelDeploymentEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site"})
	if w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	appID := decode[app.App](t, w).ID
	add := func(id string, seq int, state string) {
		t.Helper()
		if _, err := e.svc.Store.DB.Exec(`INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES (?, ?, ?, ?, 'files', 'api', ?)`,
			id, appID, seq, state, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	add("dep_q", 1, "queued")
	add("dep_swap", 2, "swapping")
	add("dep_run", 3, "running")

	if w := e.req("POST", "/api/v1/deployments/dep_q/cancel", nil, nil); w.Code != 401 {
		t.Errorf("anonymous: %d %s", w.Code, w.Body)
	}
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	if w := e.req("POST", "/api/v1/deployments/dep_q/cancel", nil, map[string]string{"Authorization": "Bearer " + ro.Value}); w.Code != 403 || decode[errResp](t, w).Error.Code != "token_read_only" {
		t.Errorf("read token: %d %s", w.Code, w.Body)
	}
	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.asSession("POST", "/api/v1/deployments/dep_q/cancel", nil); w.Code != 404 {
		t.Errorf("other user: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/deployments/dep_swap/cancel", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "too_late_to_cancel" {
		t.Errorf("swapping: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/deployments/dep_run/cancel", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "deployment_finished" {
		t.Errorf("running: %d %s", w.Code, w.Body)
	}

	// A deploy token may cancel; the answer is the Deployment.
	dt := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "deploy"}))
	w = e.req("POST", "/api/v1/deployments/dep_q/cancel", nil, map[string]string{"Authorization": "Bearer " + dt.Value})
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	d := decode[app.Deployment](t, w)
	if d.ID != "dep_q" || d.State != "cancelled" || d.AppSlug != "site" || d.Error == nil || d.Error.Code != "cancelled" || d.FinishedAt == nil {
		t.Errorf("deployment = %+v", d)
	}
	if !strings.Contains(w.Body.String(), `"state":"cancelled"`) {
		t.Errorf("body = %s", w.Body)
	}
	var channel, tokenID string
	if err := e.svc.Store.DB.QueryRow(`SELECT channel, COALESCE(token_id, '') FROM audit_log WHERE action = 'deployment.cancelled'`).Scan(&channel, &tokenID); err != nil || channel != "api" || tokenID == "" {
		t.Errorf("audit: %v %s %s", err, channel, tokenID)
	}
	if w := e.asSession("POST", "/api/v1/deployments/dep_q/cancel", nil); w.Code != 409 {
		t.Errorf("again: %d %s", w.Code, w.Body)
	}
	if spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String(); !strings.Contains(spec, "cancelDeployment") || !strings.Contains(spec, "/deployments/{id}/cancel") {
		t.Error("openapi has no cancelDeployment")
	}
}
