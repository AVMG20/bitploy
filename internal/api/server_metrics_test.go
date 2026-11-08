package api

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestAdminServerMetricsEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)
	now := time.Now().Unix()
	for i := int64(0); i < 3; i++ {
		if _, err := e.svc.Store.DB.Exec(`INSERT INTO host_samples (ts, cpu, mem_used_bytes, mem_total_bytes) VALUES (?, 40, ?, ?)`,
			now-i*15, 2<<30, 8<<30); err != nil {
			t.Fatal(err)
		}
	}

	w := e.asSession("GET", "/api/v1/admin/server/metrics?window=1h", nil)
	if w.Code != 200 {
		t.Fatalf("1h: %d %s", w.Code, w.Body)
	}
	m := decode[app.ServerMetrics](t, w)
	if m.Window != "1h" || len(m.Points) != 3 || m.Points[0].CPU != 40 || m.Points[0].MemoryMB != 2048 || m.Points[0].MemoryTotalMB != 8192 {
		t.Errorf("1h = %+v", m)
	}
	for _, f := range []string{`"ts":`, `"cpu":`, `"memory_mb":`, `"memory_total_mb":`, `"mem_total_mb":`} {
		if !strings.Contains(w.Body.String(), f) {
			t.Errorf("no %s in %s", f, w.Body)
		}
	}
	if w := e.asSession("GET", "/api/v1/admin/server/metrics", nil); w.Code != 200 || decode[app.ServerMetrics](t, w).Window != "24h" {
		t.Errorf("default window: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/admin/server/metrics?window=30d", nil); w.Code != 422 && w.Code != 400 {
		t.Errorf("unknown window: %d %s", w.Code, w.Body)
	}

	// An admin's API token is not a dashboard session.
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "adm", "scope": "read"}))
	if w := e.req("GET", "/api/v1/admin/server/metrics", nil, map[string]string{"Authorization": "Bearer " + tok.Value}); w.Code != 401 && w.Code != 403 {
		t.Errorf("admin token: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/admin/server/metrics", nil, nil); w.Code != 401 {
		t.Errorf("anonymous: %d %s", w.Code, w.Body)
	}

	// Not an admin.
	u := newEnv(t)
	u.login("user@example.nl", false)
	if w := u.asSession("GET", "/api/v1/admin/server/metrics", nil); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
		t.Errorf("user: %d %s", w.Code, w.Body)
	}

	if spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String(); !strings.Contains(spec, "adminServerMetrics") {
		t.Error("openapi has no adminServerMetrics")
	}
}
