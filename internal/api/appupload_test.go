package api

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bearerRaw(e *env, method, path string, body []byte, token string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.RemoteAddr = "203.0.113.7:1000"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestCreateAppWithBuildMode(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "my-site", "build_mode": "auto"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if a := decode[app.App](t, w); a.BuildMode != "auto" || a.URL != "https://my-site.bitploy.test" {
		t.Errorf("app = %+v", a)
	}
	// Without build_mode the strict rule stays.
	w = e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Plain", "slug": "plain-app"})
	if a := decode[app.App](t, w); a.BuildMode != "dockerfile" {
		t.Errorf("default mode = %q", a.BuildMode)
	}
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Bad", "slug": "bad-mode", "build_mode": "magic"}); w.Code != 422 && w.Code != 400 {
		t.Errorf("unknown mode: %d %s", w.Code, w.Body)
	}
	// The dashboard builds the address preview from this.
	me := decode[app.Me](t, e.asSession("GET", "/api/v1/me", nil))
	if me.AppURLTemplate != "https://{slug}.bitploy.test" {
		t.Errorf("app_url_template = %q", me.AppURLTemplate)
	}
}

func TestAppUploadEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "my-site", "build_mode": "auto"}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	path := "/api/v1/apps/my-site/upload"
	site := zipOf(t, map[string]string{"index.html": "<h1>hi</h1>"})

	// Not logged in.
	if w := e.req("POST", path, string(site), nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	// A cookie needs the CSRF token, like every other change.
	if w := e.sessionRaw("POST", path, site, false); w.Code != 403 {
		t.Errorf("no CSRF token: %d %s", w.Code, w.Body)
	}
	// A foreign Origin is refused even with the token.
	r := httptest.NewRequest("POST", path, bytes.NewReader(site))
	r.RemoteAddr = "203.0.113.7:1000"
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-CSRF-Token", e.csrf)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("foreign origin: %d %s", w.Code, w.Body)
	}

	// Another account sees no such app.
	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.sessionRaw("POST", path, site, true); w.Code != 404 {
		t.Errorf("foreign app: %d %s", w.Code, w.Body)
	}

	// A token that may only read cannot upload.
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	if w := bearerRaw(e, "POST", path, site, ro.Value); w.Code != 403 {
		t.Errorf("read token: %d %s", w.Code, w.Body)
	}

	// Not a zip.
	w = e.sessionRaw("POST", path, []byte("hello"), true)
	if w.Code != 422 || decode[errResp](t, w).Error.Code != "source_zip_invalid" {
		t.Errorf("not a zip: %d %s", w.Code, w.Body)
	}

	// Too large, announced up front.
	r = httptest.NewRequest("POST", path, strings.NewReader("x"))
	r.RemoteAddr = "203.0.113.7:1000"
	r.ContentLength = 96 << 20
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", pub)
	r.Header.Set("X-CSRF-Token", e.csrf)
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 413 || decode[errResp](t, w).Error.Code != "upload_too_large" {
		t.Errorf("too large: %d %s", w.Code, w.Body)
	}

	// The real thing.
	w = e.sessionRaw("POST", path, site, true)
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	d := decode[app.Deployment](t, w)
	if d.State != "queued" || d.SourceKind != "upload" || d.AppSlug != "my-site" || d.Seq != 1 {
		t.Errorf("deployment = %+v", d)
	}
	// It shows up in the deployment list the dashboard watches.
	if w := e.asSession("GET", "/api/v1/deployments/"+d.ID, nil); w.Code != 200 {
		t.Errorf("get deployment: %d %s", w.Code, w.Body)
	}

	// A deploy token works without CSRF.
	dt := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ci", "scope": "deploy"}))
	if w := bearerRaw(e, "POST", path, site, dt.Value); w.Code != 202 {
		t.Errorf("deploy token: %d %s", w.Code, w.Body)
	}
}

func TestAppUploadQuotasAndGitLinked(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	for _, slug := range []string{"one-app", "two-app"} {
		if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": slug, "slug": slug, "build_mode": "auto"}); w.Code != 201 {
			t.Fatalf("create %s: %d %s", slug, w.Code, w.Body)
		}
	}
	site := zipOf(t, map[string]string{"index.html": "<h1>hi</h1>"})

	// App-count quota applies to creating the app the upload goes to.
	if _, err := e.svc.Store.DB.Exec(`UPDATE users SET quota_apps = 2`); err != nil {
		t.Fatal(err)
	}
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Three", "slug": "three-app", "build_mode": "auto"})
	if w.Code != 403 || decode[errResp](t, w).Error.Code != "apps_quota_exceeded" {
		t.Errorf("app quota: %d %s", w.Code, w.Body)
	}

	// Deploys per hour.
	if _, err := e.svc.Store.DB.Exec(`UPDATE users SET quota_deploys_per_hour = 1`); err != nil {
		t.Fatal(err)
	}
	if w := e.sessionRaw("POST", "/api/v1/apps/one-app/upload", site, true); w.Code != 202 {
		t.Fatalf("first upload: %d %s", w.Code, w.Body)
	}
	w = e.sessionRaw("POST", "/api/v1/apps/one-app/upload", site, true)
	if w.Code != 429 || decode[errResp](t, w).Error.Code != "deploy_rate_limited" {
		t.Errorf("deploy rate: %d %s", w.Code, w.Body)
	}
	w = e.sessionRaw("POST", "/api/v1/apps/two-app/upload", site, true)
	if w.Code != 429 {
		t.Errorf("the limit is per account: %d %s", w.Code, w.Body)
	}

	// An app linked to git takes no uploads.
	if _, err := e.svc.Store.DB.Exec(`UPDATE users SET quota_deploys_per_hour = 20`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Store.DB.Exec(`UPDATE apps SET git_repo_url = 'https://github.com/o/r', git_branch = 'main' WHERE slug = 'two-app'`); err != nil {
		t.Fatal(err)
	}
	w = e.sessionRaw("POST", "/api/v1/apps/two-app/upload", site, true)
	if w.Code != 409 || decode[errResp](t, w).Error.Code != "git_source_linked" {
		t.Errorf("git-linked: %d %s", w.Code, w.Body)
	}
}
