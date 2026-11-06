package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func (e *env) sessionRaw(method, path string, body []byte, withCSRF bool) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.RemoteAddr = "203.0.113.7:1000"
	r.AddCookie(e.cookie)
	r.Header.Set("Origin", pub)
	if withCSRF {
		r.Header.Set("X-CSRF-Token", e.csrf)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestBackupEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Photos", "slug": "photos", "volume": true}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	base := "/api/v1/apps/photos/backups"

	// Not logged in at all.
	if w := e.req("GET", base, nil, nil); w.Code != 401 {
		t.Errorf("anonymous list: %d", w.Code)
	}
	if w := e.req("GET", base+"/bak_x/download", nil, nil); w.Code != 401 {
		t.Errorf("anonymous download: %d", w.Code)
	}

	l := decode[app.BackupList](t, e.asSession("GET", base, nil))
	if len(l.Items) != 0 || l.Settings.Schedule != "daily" || l.Settings.KeepMode != "days" || l.Settings.Keep != 14 || l.Settings.TimeUTC == "" { // on by default
		t.Errorf("empty list = %+v", l)
	}

	// Upload: the cookie needs the CSRF token, like every other change.
	archive := tarGz(t, map[string][]byte{"notes/a.txt": []byte("hello")})
	if w := e.sessionRaw("POST", base+"/upload", archive, false); w.Code != 403 {
		t.Errorf("upload without CSRF token: %d", w.Code)
	}
	w := e.sessionRaw("POST", base+"/upload", archive, true)
	if w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	b := decode[app.Backup](t, w)
	if b.Kind != "uploaded" || b.Status != "ready" {
		t.Errorf("uploaded = %+v", b)
	}
	if w := e.sessionRaw("POST", base+"/upload", []byte("not an archive"), true); w.Code != 422 || decode[errResp](t, w).Error.Code != "backup_invalid" {
		t.Errorf("bad upload: %d %s", w.Code, w.Body)
	}

	// Download works as a plain link: GET with the cookie, no CSRF header.
	r := httptest.NewRequest("GET", base+"/"+b.ID+"/download", nil)
	r.RemoteAddr = "203.0.113.7:1000"
	r.AddCookie(e.cookie)
	dl := httptest.NewRecorder()
	e.h.ServeHTTP(dl, r)
	if dl.Code != 200 || !strings.HasPrefix(dl.Header().Get("Content-Disposition"), "attachment; filename=photos-") ||
		!strings.Contains(dl.Header().Get("Content-Disposition"), "uploaded.tar.gz") || dl.Header().Get("Content-Type") != "application/gzip" {
		t.Fatalf("download: %d %v", dl.Code, dl.Header())
	}
	gz, err := gzip.NewReader(dl.Body)
	if err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(gz).Next()
	if err != nil || h.Name != "notes/a.txt" {
		t.Errorf("downloaded archive: %v %v", h, err)
	}
	// Ranges, so a download can resume.
	r = httptest.NewRequest("GET", base+"/"+b.ID+"/download", nil)
	r.AddCookie(e.cookie)
	r.Header.Set("Range", "bytes=0-9")
	part := httptest.NewRecorder()
	e.h.ServeHTTP(part, r)
	if part.Code != 206 || part.Body.Len() != 10 {
		t.Errorf("range: %d, %d bytes", part.Code, part.Body.Len())
	}

	// The same with a bearer token (curl).
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "cli", "scope": "deploy"}))
	bearer := map[string]string{"Authorization": "Bearer " + tok.Value}
	if w := e.req("GET", base+"/"+b.ID+"/download", nil, bearer); w.Code != 200 || w.Body.Len() == 0 {
		t.Errorf("bearer download: %d", w.Code)
	}
	if w := e.req("POST", base+"/upload", string(archive), bearer); w.Code != 201 {
		t.Errorf("bearer upload: %d %s", w.Code, w.Body)
	}
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	readOnly := map[string]string{"Authorization": "Bearer " + ro.Value}
	if w := e.req("POST", base+"/upload", string(archive), readOnly); w.Code != 403 || decode[errResp](t, w).Error.Code != "token_read_only" {
		t.Errorf("read-only upload: %d %s", w.Code, w.Body)
	}

	// The files are the user's data: a read-only token may list, not download.
	if w := e.req("GET", base+"/"+b.ID+"/download", nil, readOnly); w.Code != 403 || decode[errResp](t, w).Error.Code != "token_read_only" {
		t.Errorf("read-only download: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", base, nil, readOnly); w.Code != 200 {
		t.Errorf("read-only list: %d", w.Code)
	}
	// A finished restore can be dismissed (there is none here, which is fine).
	if w := e.asSession("DELETE", "/api/v1/apps/photos/backup-restore", nil); w.Code != 204 {
		t.Errorf("dismiss: %d %s", w.Code, w.Body)
	}
	if l := decode[app.BackupList](t, e.asSession("GET", base, nil)); l.UploadMaxBytes <= 0 {
		t.Errorf("upload_max_bytes = %d", l.UploadMaxBytes)
	}

	// An archive bigger than the 8 MB the JSON API accepts still goes through.
	big := make([]byte, 9<<20)
	_, _ = rand.Read(big)
	if w := e.sessionRaw("POST", base+"/upload", tarGz(t, map[string][]byte{"big.bin": big}), true); w.Code != 201 {
		t.Errorf("9 MB upload: %d %s", w.Code, w.Body)
	}

	// Somebody else cannot see any of it.
	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.asSession("GET", base, nil); w.Code != 404 {
		t.Errorf("other user lists: %d", w.Code)
	}

	// Settings and delete.
	w = e.asSession("PUT", "/api/v1/apps/photos/backup-settings", map[string]any{"schedule": "weekly", "keep_mode": "days", "keep": 14})
	if st := decode[app.BackupSettings](t, w); w.Code != 200 || st.Schedule != "weekly" || st.KeepMode != "days" || st.Keep != 14 || st.NextAt == nil {
		t.Errorf("settings: %d %+v", w.Code, st)
	}
	if w := e.asSession("PUT", "/api/v1/apps/photos/backup-settings", map[string]any{"schedule": "often"}); w.Code != 400 {
		t.Errorf("bad schedule: %d", w.Code)
	}
	if w := e.asSession("DELETE", base+"/"+b.ID, nil); w.Code != 204 {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("DELETE", base+"/"+b.ID, nil); w.Code != 404 {
		t.Errorf("delete again: %d", w.Code)
	}
	// Making a backup needs Docker, which this test does not have.
	if w := e.asSession("POST", base, nil); w.Code != 503 || decode[errResp](t, w).Error.Code != "docker_unavailable" {
		t.Errorf("create without docker: %d %s", w.Code, w.Body)
	}
}
