package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestPublicPortEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	p := decode[app.Project](t, e.asSession("POST", "/api/v1/projects", map[string]any{"name": "Shop"}))
	w := e.asSession("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"template": "postgres"})
	if w.Code != 202 {
		t.Fatalf("add postgres: %d %s", w.Code, w.Body)
	}
	pg := decode[app.App](t, w)
	if pg.PublicPort.Enabled || pg.PublicPort.Available || pg.PublicPort.Reason != "public_ports_off" {
		t.Fatalf("public_port of a new app on a server without a range: %+v", pg.PublicPort)
	}
	if !strings.Contains(w.Body.String(), `"public_port":{`) {
		t.Fatalf("the app JSON lacks public_port: %s", w.Body)
	}
	path := "/api/v1/apps/" + pg.Slug + "/public-port"

	w = e.asSession("PUT", path, map[string]any{"enabled": true})
	if w.Code != 422 || decode[errResp](t, w).Error.Code != "public_ports_off" || decode[errResp](t, w).Error.Hint == "" {
		t.Fatalf("off: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("PUT", path, map[string]any{"enabled": true, "close_after": "5m"}); w.Code != 422 && w.Code != 400 {
		t.Errorf("close_after outside the enum = %d", w.Code)
	}

	// With a range: the port is saved and shown (no Docker here, so the
	// forwarder does not run and the call says so).
	e.svc.Cfg.PublicPortsFrom, e.svc.Cfg.PublicPortsTo, e.svc.Cfg.PublicPortsPerUser = 30000, 30009, 3
	e.svc.Cfg.PublicPortsHost = "203.0.113.10"
	w = e.asSession("PUT", path, map[string]any{"enabled": true, "allow_from": []string{"198.51.100.0/24"}, "close_after": "24h"})
	if w.Code != 503 || decode[errResp](t, w).Error.Code != "docker_unavailable" {
		t.Fatalf("open without Docker: %d %s", w.Code, w.Body)
	}
	g := decode[app.App](t, e.asSession("GET", "/api/v1/apps/"+pg.Slug, nil))
	if !g.PublicPort.Enabled || g.PublicPort.Address != "203.0.113.10:30000" || g.PublicPort.Running ||
		!strings.HasPrefix(g.PublicPort.ConnectionURL, "postgresql://app:********@203.0.113.10:30000/") {
		t.Fatalf("GET app: %+v", g.PublicPort)
	}

	// Reveal: the session may, a read token may not.
	w = e.asSession("POST", path+"/reveal", nil)
	if w.Code != 200 {
		t.Fatalf("reveal: %d %s", w.Code, w.Body)
	}
	rev := decode[app.PublicConnection](t, w)
	if strings.Contains(rev.ConnectionURL, "********") || rev.Secrets["POSTGRES_PASSWORD"] == "" {
		t.Fatalf("reveal = %+v", rev)
	}
	read := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"})).Value
	if w := e.req("POST", path+"/reveal", nil, map[string]string{"Authorization": "Bearer " + read}); w.Code != 403 {
		t.Errorf("reveal with a read token = %d %s", w.Code, w.Body)
	}
	if w := e.req("PUT", path, map[string]any{"enabled": false}, map[string]string{"Authorization": "Bearer " + read}); w.Code != 403 {
		t.Errorf("close with a read token = %d", w.Code)
	}

	// Admin endpoints: not for users.
	if w := e.asSession("GET", "/api/v1/admin/public-ports", nil); w.Code != 403 {
		t.Errorf("admin list as a user = %d", w.Code)
	}

	// Close: the reveal is refused afterwards.
	if w := e.asSession("PUT", path, map[string]any{"enabled": false}); w.Code != 200 || decode[app.App](t, w).PublicPort.Enabled {
		t.Fatalf("close: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", path+"/reveal", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "public_port_closed" {
		t.Errorf("reveal after close: %d %s", w.Code, w.Body)
	}

	// The admin sees the range.
	e.login("root@example.nl", true)
	w = e.asSession("GET", "/api/v1/admin/public-ports", nil)
	ov := decode[app.PublicPortsOverview](t, w)
	if w.Code != 200 || !ov.Enabled || ov.From != 30000 || ov.To != 30009 || ov.PerUser != 3 || ov.Open == nil {
		t.Fatalf("admin overview: %d %s", w.Code, w.Body)
	}
}
