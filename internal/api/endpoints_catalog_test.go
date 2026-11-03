package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestCatalogEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)

	// Any signed-in caller, a read token included, may read the catalog.
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "r", "scope": "read"}))
	read := map[string]string{"Authorization": "Bearer " + tc.Value}
	w := e.req("GET", "/api/v1/catalog", nil, read)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	page := decode[app.Page[app.CatalogApp]](t, w)
	if len(page.Items) < 6 || page.NextCursor != "" {
		t.Fatalf("list: %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	if strings.Contains(w.Body.String(), `"generate":`) {
		t.Error("generator details must not be part of the catalog JSON")
	}
	if w := e.req("GET", "/api/v1/catalog", nil, nil); w.Code != 401 {
		t.Errorf("anonymous list: %d", w.Code)
	}
	w = e.req("GET", "/api/v1/catalog/memos", nil, read)
	one := decode[app.CatalogApp](t, w)
	if w.Code != 200 || one.ID != "memos" || one.Dockerfile == "" || one.Env == nil || one.FirstSteps == nil {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	w = e.req("GET", "/api/v1/catalog/nope", nil, read)
	er := decode[errResp](t, w)
	if w.Code != 404 || er.Error.Code != "catalog_unknown" || !strings.Contains(er.Error.Hint, "memos") {
		t.Fatalf("unknown: %d %s", w.Code, w.Body)
	}

	// Deploy: a read token is refused, a session works.
	if w := e.req("POST", "/api/v1/catalog/memos/deploy", map[string]any{}, read); w.Code != 403 {
		t.Errorf("read token deploy: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", "/api/v1/catalog/memos/deploy", map[string]any{"name": "Notes"})
	if w.Code != 202 {
		t.Fatalf("deploy: %d %s", w.Code, w.Body)
	}
	res := decode[app.CatalogDeployResult](t, w)
	if res.App.Slug != "memos" || res.App.Name != "Notes" || res.App.CatalogID != "memos" || res.Deployment.State != "queued" || len(res.FirstSteps) == 0 {
		t.Fatalf("deploy result: %+v", res)
	}
	if strings.Contains(strings.Join(res.FirstSteps, " "), "{url}") {
		t.Errorf("placeholder left: %v", res.FirstSteps)
	}

	// The app JSON carries the catalog fields.
	w = e.asSession("GET", "/api/v1/apps/memos", nil)
	if !strings.Contains(w.Body.String(), `"catalog_id":"memos"`) || !strings.Contains(w.Body.String(), `"catalog_update":null`) {
		t.Errorf("app JSON: %s", w.Body)
	}
	if w := e.asSession("POST", "/api/v1/apps/memos/catalog-update", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "catalog_up_to_date" {
		t.Errorf("up to date: %d %s", w.Code, w.Body)
	}
	plain := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Plain", "slug": "plain"})
	if plain.Code != 201 {
		t.Fatal(plain.Body)
	}
	if w := e.asSession("POST", "/api/v1/apps/plain/catalog-update", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "not_catalog_app" {
		t.Errorf("not catalog: %d %s", w.Code, w.Body)
	}
}

func TestDockerfileEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site"}); w.Code != 201 {
		t.Fatal(w.Body)
	}
	w := e.asSession("GET", "/api/v1/apps/site/dockerfile", nil)
	v := decode[app.DockerfileView](t, w)
	if w.Code != 200 || v.Editable || v.Reason == "" || v.Path != "Dockerfile" {
		t.Fatalf("before deploy: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"editable":false`) || !strings.Contains(w.Body.String(), `"reason":"`) {
		t.Errorf("field names: %s", w.Body)
	}
	w = e.asSession("PUT", "/api/v1/apps/site/dockerfile", map[string]any{"content": "FROM alpine:3.22\n"})
	if w.Code != 409 || decode[errResp](t, w).Error.Code != "source_missing" {
		t.Fatalf("source_missing: %d %s", w.Code, w.Body)
	}
	files := map[string]any{"files": []map[string]string{{"path": "Dockerfile", "content": "FROM alpine:3.22\nUSER 1000\nEXPOSE 80\n"}}}
	if w := e.asSession("POST", "/api/v1/apps/site/files", files); w.Code != 202 {
		t.Fatalf("files: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/apps/site/dockerfile", nil)
	v = decode[app.DockerfileView](t, w)
	if !v.Editable || v.Reason != "" || !strings.Contains(v.Content, "EXPOSE 80") {
		t.Fatalf("get: %s", w.Body)
	}
	w = e.asSession("PUT", "/api/v1/apps/site/dockerfile", map[string]any{"content": "FROM ghcr.io/foo/bar:1.2\n"})
	if w.Code != 202 || decode[app.Deployment](t, w).SourceKind != "files" {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		content string
		status  int
		code    string
	}{
		{"", 400, "dockerfile_empty"},
		{"RUN x\n", 422, "no_from"},
		{"FROM alpine:3.22\n#" + strings.Repeat("x", 70<<10), 413, "dockerfile_too_large"},
	} {
		w := e.asSession("PUT", "/api/v1/apps/site/dockerfile", map[string]any{"content": c.content})
		if w.Code != c.status || decode[errResp](t, w).Error.Code != c.code {
			t.Errorf("%s: %d %s", c.code, w.Code, w.Body)
		}
	}
	// No session or token: refused; a read token cannot change it.
	if w := e.req("PUT", "/api/v1/apps/site/dockerfile", map[string]any{"content": "FROM alpine:3.22\n"}, nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	tc := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "r", "scope": "read"}))
	read := map[string]string{"Authorization": "Bearer " + tc.Value}
	if w := e.req("PUT", "/api/v1/apps/site/dockerfile", map[string]any{"content": "FROM alpine:3.22\n"}, read); w.Code != 403 {
		t.Errorf("read token: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/v1/apps/site/dockerfile", nil, read); w.Code != 200 {
		t.Errorf("read token get: %d %s", w.Code, w.Body)
	}
}
