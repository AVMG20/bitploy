package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// The file browser needs Docker, which this test does not have: it covers
// what happens before Docker is asked (login, scopes, paths, /data off).
func TestFileEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Photos", "slug": "photos", "volume": true}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Plain", "slug": "plain"}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	base := "/api/v1/apps/photos/files"

	// Not logged in at all.
	for _, c := range [][2]string{{"GET", base}, {"GET", base + "/download?path=a"}, {"POST", base + "/upload?name=a"}, {"DELETE", base + "?path=a"}, {"POST", base + "/folder"}} {
		var body any
		if c[1] == base+"/folder" {
			body = map[string]any{"name": "x"}
		}
		if w := e.req(c[0], c[1], body, nil); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", c[0], c[1], w.Code)
		}
	}

	// Paths are checked before anything else.
	for _, p := range []string{"..", "a/../../b", "a%00b"} {
		if w := e.asSession("GET", base+"?path="+p, nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "path_invalid" {
			t.Errorf("list %q: %d %s", p, w.Code, w.Body)
		}
		if w := e.asSession("GET", base+"/download?path="+p, nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "path_invalid" {
			t.Errorf("download %q: %d %s", p, w.Code, w.Body)
		}
		if w := e.asSession("DELETE", base+"?path="+p, nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "path_invalid" {
			t.Errorf("delete %q: %d %s", p, w.Code, w.Body)
		}
	}
	if w := e.asSession("DELETE", base+"?path=/", nil); w.Code != 400 || decode[errResp](t, w).Error.Code != "path_invalid" {
		t.Errorf("delete /data: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", base+"/folder", map[string]any{"name": "a/b"}); w.Code != 400 || decode[errResp](t, w).Error.Code != "name_invalid" {
		t.Errorf("folder name: %d %s", w.Code, w.Body)
	}
	if w := e.sessionRaw("POST", base+"/upload?name=..", []byte("x"), true); w.Code != 400 || decode[errResp](t, w).Error.Code != "name_invalid" {
		t.Errorf("upload name: %d %s", w.Code, w.Body)
	}
	if w := e.sessionRaw("POST", base+"/upload?name=a&overwrite=maybe", []byte("x"), true); w.Code != 400 || decode[errResp](t, w).Error.Code != "overwrite_invalid" {
		t.Errorf("overwrite flag: %d %s", w.Code, w.Body)
	}

	// The upload with a cookie needs the CSRF token, like every change.
	if w := e.sessionRaw("POST", base+"/upload?name=a.txt", []byte("x"), false); w.Code != 403 {
		t.Errorf("upload without CSRF token: %d", w.Code)
	}

	// /data off: nothing to browse.
	plain := "/api/v1/apps/plain/files"
	if w := e.asSession("GET", plain, nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "volume_off" {
		t.Errorf("list, /data off: %d %s", w.Code, w.Body)
	}
	if w := e.sessionRaw("POST", plain+"/upload?name=a.txt", []byte("x"), true); w.Code != 409 || decode[errResp](t, w).Error.Code != "volume_off" {
		t.Errorf("upload, /data off: %d %s", w.Code, w.Body)
	}

	// No Docker here: the real work answers that it cannot be done now.
	if w := e.asSession("GET", base, nil); w.Code != 503 || decode[errResp](t, w).Error.Code != "docker_unavailable" {
		t.Errorf("list without docker: %d %s", w.Code, w.Body)
	}

	// Scopes: a read-only token may list, but not download or change anything.
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	readOnly := map[string]string{"Authorization": "Bearer " + ro.Value}
	if w := e.req("GET", base, nil, readOnly); w.Code != 503 {
		t.Errorf("read-only list reaches the store: %d %s", w.Code, w.Body)
	}
	for _, c := range [][2]string{{"GET", base + "/download?path=a"}, {"POST", base + "/upload?name=a"}, {"DELETE", base + "?path=a"}, {"POST", base + "/folder"}} {
		var body any
		if c[0] == "POST" && c[1] == base+"/folder" {
			body = map[string]any{"name": "x"}
		}
		if c[1] == base+"/upload?name=a" {
			body = "x"
		}
		if w := e.req(c[0], c[1], body, readOnly); w.Code != 403 || decode[errResp](t, w).Error.Code != "token_read_only" {
			t.Errorf("read-only %s %s: %d %s", c[0], c[1], w.Code, w.Body)
		}
	}

	// Somebody else cannot see any of it.
	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.asSession("GET", base, nil); w.Code != 404 {
		t.Errorf("other user lists: %d", w.Code)
	}
}

func TestGetTemplateForFiles(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	w := e.asSession("GET", "/api/v1/templates/node?file=package.json&file=pnpm-lock.yaml", nil)
	if w.Code != 200 || !strings.Contains(decode[app.Template](t, w).Dockerfile, "pnpm install --frozen-lockfile") {
		t.Fatalf("node for pnpm: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/templates/node", nil)
	if w.Code != 200 || !strings.Contains(decode[app.Template](t, w).Dockerfile, "npm ci") {
		t.Fatalf("node: %d %s", w.Code, w.Body)
	}
}
