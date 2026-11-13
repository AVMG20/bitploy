package api

import (
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestAdminBuildEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)
	var uid string
	if err := e.svc.Store.DB.QueryRow(`SELECT id FROM users WHERE email = 'admin@example.nl'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := e.svc.Store.DB.Exec(`INSERT INTO build_history (deployment_id, app_id, app_slug, app_name, owner_id, person_id, channel, source_kind,
		queued_at, started_at, built_at, finished_at, result, peak_cpu, peak_mem_mb)
		VALUES ('dep_1', 'app_1', 'shop', 'Shop', ?, ?, 'dashboard', 'upload', ?, ?, ?, ?, 'live', 71.5, 3000)`,
		uid, uid, now-600, now-590, now-500, now-480); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Store.DB.Exec(`INSERT INTO audit_log (ts, user_id, channel, action) VALUES (?, ?, 'api', 'app.create'), (?, 'usr_other', 'api', 'app.delete')`,
		now, uid, now); err != nil {
		t.Fatal(err)
	}

	w := e.asSession("GET", "/api/v1/admin/builds/stats?window=7d", nil)
	if w.Code != 200 {
		t.Fatalf("stats: %d %s", w.Code, w.Body)
	}
	st := decode[app.BuildStats](t, w)
	if st.Window != "7d" || len(st.People) != 1 || st.People[0].UserID != uid || st.People[0].Name != "Tester" || st.People[0].TotalBuildSeconds != 90 ||
		st.People[0].LongestApp != "shop" || st.People[0].PeakCPU == nil || *st.People[0].PeakCPU != 71.5 {
		t.Errorf("stats = %+v", st)
	}
	if w := e.asSession("GET", "/api/v1/admin/builds/stats?window=1y", nil); w.Code != 422 && w.Code != 400 {
		t.Errorf("unknown window: %d", w.Code)
	}

	w = e.asSession("GET", "/api/v1/admin/builds?user="+uid, nil)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	pg := decode[app.Page[app.BuildRecord]](t, w)
	if len(pg.Items) != 1 || pg.Items[0].DeploymentID != "dep_1" || pg.Items[0].WaitSeconds != 10 || pg.Items[0].BuildSeconds != 90 || pg.Items[0].Result != "live" {
		t.Errorf("list = %+v", pg)
	}
	if w := e.asSession("GET", "/api/v1/admin/builds", nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "user_required" {
		t.Errorf("no user: %d %s", w.Code, w.Body)
	}

	w = e.asSession("GET", "/api/v1/admin/server/metrics?window=1h", nil)
	if m := decode[app.ServerMetrics](t, w); w.Code != 200 || len(m.Builds) != 1 || m.Builds[0].Person != "Tester" || m.Builds[0].App != "shop" {
		t.Errorf("metrics builds: %d %s", w.Code, w.Body)
	}

	w = e.asSession("GET", "/api/v1/admin/audit?user="+uid, nil)
	if pa := decode[app.Page[app.AuditEntry]](t, w); w.Code != 200 || len(pa.Items) == 0 {
		t.Errorf("audit by user: %d %s", w.Code, w.Body)
	} else {
		for _, it := range pa.Items {
			if it.UserID == nil || *it.UserID != uid {
				t.Errorf("audit entry of someone else: %+v", it)
			}
		}
	}

	// Not an admin.
	u := newEnv(t)
	u.login("user@example.nl", false)
	for _, path := range []string{"/api/v1/admin/builds/stats", "/api/v1/admin/builds?user=" + uid, "/api/v1/admin/audit?user=" + uid} {
		if w := u.asSession("GET", path, nil); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
			t.Errorf("user %s: %d %s", path, w.Code, w.Body)
		}
		if w := u.req("GET", path, nil, nil); w.Code != 401 {
			t.Errorf("anonymous %s: %d", path, w.Code)
		}
	}

	spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String()
	for _, op := range []string{"adminBuildStats", "adminListBuilds"} {
		if !strings.Contains(spec, op) {
			t.Errorf("openapi has no %s", op)
		}
	}
}
