package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestAdminAppDomainsEndpoints(t *testing.T) {
	e := newEnv(t)
	u, err := e.svc.CreateUser(context.Background(), "friend@example.nl", "Friend", "password-1234", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Store.DB.Exec(`INSERT INTO apps (id, user_id, slug, name, status, desired_state, created_at, updated_at) VALUES ('app_1', ?, 'stopped-app', 'x', 'stopped', 'stopped', 1, 1)`, u.ID); err != nil {
		t.Fatal(err)
	}
	e.login("admin@example.nl", true)

	// Not example.com: httptest sends that Host, and bitploy answers an extra
	// domain itself with the catch-all page.
	w := e.asSession("POST", "/api/v1/admin/app-domains", map[string]any{"domain": "Example.org"})
	if w.Code != 201 {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	d := decode[app.AppDomain](t, w)
	if d.Domain != "example.org" || d.Certificate != "none" {
		t.Fatalf("added %+v", d)
	}
	w = e.asSession("GET", "/api/v1/admin/app-domains", nil)
	list := decode[app.AppDomainList](t, w)
	if w.Code != 200 || len(list.Items) != 2 || list.Items[0].ID != "default" || list.Items[0].AppCount != 1 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PUT", "/api/v1/admin/users/"+u.ID+"/app-domains", map[string]any{"domain_ids": []string{d.ID}})
	if au := decode[app.AdminUser](t, w); w.Code != 200 || len(au.AppDomainIDs) != 1 {
		t.Fatalf("grant: %d %s", w.Code, w.Body)
	}
	w = e.asSession("PUT", "/api/v1/admin/app-domains/"+d.ID+"/certificate", map[string]any{"certificate": "x", "private_key": "y"})
	if er := decode[errResp](t, w); w.Code != 400 || er.Error.Code != "cert_invalid" {
		t.Fatalf("bad certificate: %d %s", w.Code, w.Body)
	}

	// The catch-all knows the new domain, and an app is only found on its own domain.
	for host, text := range map[string]string{
		"nothing.example.org":      "No app here",
		"stopped-app.example.org":  "No app here",
		"stopped-app.bitploy.test": "This app is stopped",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		rw := httptest.NewRecorder()
		e.h.ServeHTTP(rw, r)
		if !strings.Contains(rw.Body.String(), text) {
			t.Errorf("%s: %d %q", host, rw.Code, rw.Body)
		}
	}

	// The domain itself: nothing until an app holds it, then that app's page.
	root := func() string {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = "example.org"
		rw := httptest.NewRecorder()
		e.h.ServeHTTP(rw, r)
		return rw.Body.String()
	}
	if body := root(); !strings.Contains(body, "No app here") {
		t.Errorf("example.org without a root app: %q", body)
	}
	if _, err := e.svc.Store.DB.Exec(`UPDATE apps SET base_domain = 'example.org', on_root = 1 WHERE id = 'app_1'`); err != nil {
		t.Fatal(err)
	}
	if body := root(); !strings.Contains(body, "This app is stopped") {
		t.Errorf("example.org held by a stopped app: %q", body)
	}
	if _, err := e.svc.Store.DB.Exec(`DELETE FROM apps WHERE id = 'app_1'`); err != nil {
		t.Fatal(err)
	}

	if w := e.asSession("DELETE", "/api/v1/admin/app-domains/"+d.ID, nil); w.Code != 204 {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
}
