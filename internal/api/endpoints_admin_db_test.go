package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/dbview"
)

// The admin viewer of bitploy's own database. The helper runs in-process here
// (the test binary is not bitploy); the process itself is tested in app.
func TestAdminDatabaseEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)
	var calls int
	e.svc.UseAdminDBHelper(func(ctx context.Context, file string, req []byte) ([]byte, error) {
		calls++
		if want, _ := filepath.Abs(e.svc.Cfg.DBPath()); file != want {
			t.Errorf("the helper was asked for %s, want %s", file, want)
		}
		// The test store is t.db; the service points at bitploy.db.
		return json.Marshal(dbview.HandleControl(ctx, req, filepath.Join(e.svc.Cfg.DataDir, "t.db")))
	})
	for _, q := range []string{
		`INSERT INTO apps (id, user_id, slug, name, created_at, updated_at, protect_hash) SELECT 'app_1', id, 'photos', 'Photos', 1, 1, 'SECRET-PROTECT' FROM users LIMIT 1`,
		`INSERT INTO env_vars (app_id, name, value_enc, updated_at) VALUES ('app_1', 'KEY', 'SECRET-ENV', 1)`,
		`INSERT INTO uploads (id, app_id, user_id, expires_at, created_at) VALUES ('upl_SECRET-ID', 'app_1', 'u', 9, 1)`,
	} {
		if _, err := e.svc.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// The schema: the shape of the app viewer, with redacted columns (Path is the file name: t.db in this test).
	w := e.asSession("GET", "/api/v1/admin/db", nil)
	if w.Code != 200 {
		t.Fatalf("schema: %d %s", w.Code, w.Body)
	}
	info := decode[app.DBInfo](t, w)
	if info.Path != "t.db" || len(info.Tables) < 10 {
		t.Errorf("info = %+v", info)
	}
	redacted := map[string]bool{}
	for _, tb := range info.Tables {
		if tb.Editable {
			t.Errorf("%s is editable", tb.Name)
		}
		for _, c := range tb.Columns {
			if c.Redacted {
				redacted[tb.Name+"."+c.Name] = true
			}
		}
	}
	for _, c := range []string{"users.password_hash", "sessions.id_hash", "sessions.csrf_token", "api_tokens.hash", "env_vars.value_enc", "uploads.id", "github_app.private_key_enc", "apps.protect_hash"} {
		if !redacted[c] {
			t.Errorf("%s is not redacted", c)
		}
	}
	if redacted["users.email"] || redacted["api_tokens.prefix"] {
		t.Errorf("too much is redacted: %v", redacted)
	}
	if !strings.Contains(w.Body.String(), `"redacted":true`) || !strings.Contains(w.Body.String(), `"redacted":false`) {
		t.Errorf("columns say nothing about redacted: %.300s", w.Body)
	}

	// Rows: the same shape, secrets NULL, no keys.
	w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "users", "order_by": "email"})
	if w.Code != 200 {
		t.Fatalf("rows: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "argon") || strings.Contains(w.Body.String(), "$") {
		t.Errorf("rows show a password hash: %s", w.Body)
	}
	rows := decode[app.DBRows](t, w)
	if rows.Editable || len(rows.Rows) != 1 || rows.Rows[0].Key != nil || rows.Total == nil || *rows.Total != 1 {
		t.Errorf("rows = %+v", rows)
	}
	for _, body := range []map[string]any{
		{"table": "apps"}, {"table": "env_vars"}, {"table": "uploads"}, {"table": "sessions"}, {"table": "api_tokens"},
	} {
		w = e.asSession("POST", "/api/v1/admin/db/rows", body)
		if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") {
			t.Errorf("rows %v: %d %s", body, w.Code, w.Body)
		}
	}
	// Search skips redacted columns; a filter or sort on one is refused.
	if w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "apps", "search": "SECRET"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("search for a secret: %d %s", w.Code, w.Body)
	}
	if w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "apps", "search": "photos"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Errorf("search: %d %s", w.Code, w.Body)
	}
	if w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "apps", "filters": []map[string]any{{"column": "protect_hash", "op": "like", "value": "S%"}}}); w.Code != 400 || decode[errResp](t, w).Error.Code != "column_redacted" {
		t.Errorf("filter on a secret: %d %s", w.Code, w.Body)
	}
	if w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "apps", "order_by": "protect_hash"}); w.Code != 400 || decode[errResp](t, w).Error.Code != "column_redacted" {
		t.Errorf("sort by a secret: %d %s", w.Code, w.Body)
	}
	if w = e.asSession("POST", "/api/v1/admin/db/rows", map[string]any{"table": "nope"}); w.Code != 404 || decode[errResp](t, w).Error.Code != "table_not_found" {
		t.Errorf("missing table: %d %s", w.Code, w.Body)
	}
	if calls == 0 {
		t.Error("the helper did not run")
	}

	// SQL, read-only; browsing wrote no audit row, the console does.
	audit := func() []string {
		rs, err := e.svc.Store.DB.Query(`SELECT details FROM audit_log WHERE action = 'admin.db.query' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		var out []string
		for rs.Next() {
			var d string
			rs.Scan(&d)
			out = append(out, d)
		}
		return out
	}
	if n := len(audit()); n != 0 {
		t.Errorf("browsing was audited: %d", n)
	}
	w = e.asSession("POST", "/api/v1/admin/db/sql", map[string]any{"sql": "SELECT slug, protect_hash, hex(protect_hash) FROM apps; SELECT 2"})
	if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") && !strings.Contains(w.Body.String(), `"sql":"SELECT slug, protect_hash`) {
		t.Fatalf("sql: %d %s", w.Code, w.Body)
	}
	res := decode[app.DBSQLResult](t, w)
	if len(res.Results) != 2 || res.Results[0].Rows[0][0] != "photos" || res.Results[0].Rows[0][1] != nil {
		t.Errorf("sql = %s", w.Body)
	}
	if a := audit(); len(a) != 1 || !strings.Contains(a[0], `"statements":["select","select"]`) || !strings.Contains(a[0], `"outcome":"ok"`) || strings.Contains(a[0], "protect_hash") || strings.Contains(a[0], "photos") {
		t.Errorf("audit = %v", a)
	}

	// Never a write.
	before := calls
	w = e.asSession("POST", "/api/v1/admin/db/sql", map[string]any{"sql": "SELECT 1", "write": true})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "db_read_only" {
		t.Errorf("write: %d %s", w.Code, w.Body)
	}
	if calls != before {
		t.Error("the helper ran for a request with write")
	}
	for _, q := range []string{"DELETE FROM users", "UPDATE users SET role = 'user'", "INSERT INTO instance (id, created_at) VALUES ('x', 1)", "DROP TABLE users", "CREATE TABLE t (a)", "PRAGMA writable_schema = 1", "ATTACH 'x' AS y", "SELECT * FROM sqlite_dbpage"} {
		w = e.asSession("POST", "/api/v1/admin/db/sql", map[string]any{"sql": q})
		if w.Code != 400 {
			t.Errorf("%s: %d %s", q, w.Code, w.Body)
		}
	}
	var users int
	_ = e.svc.Store.DB.QueryRow(`SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&users)
	if users != 1 {
		t.Error("the admin was changed")
	}
	if a := audit(); len(a) != 9 || !strings.Contains(a[1], `"outcome":"error"`) || strings.Contains(strings.Join(a, ""), "DELETE FROM") {
		t.Errorf("audit after refusals = %d entries: %v", len(a), a)
	}

	// Not an admin: no, whatever the method of logging in.
	u := newEnv(t)
	u.login("user@example.nl", false)
	u.svc.UseAdminDBHelper(func(ctx context.Context, file string, req []byte) ([]byte, error) {
		t.Error("the helper ran for a user who is not an admin")
		return nil, nil
	})
	for path, body := range map[string]any{"/api/v1/admin/db": nil, "/api/v1/admin/db/rows": map[string]any{"table": "users"}, "/api/v1/admin/db/sql": map[string]any{"sql": "SELECT 1"}} {
		method := "POST"
		if body == nil {
			method = "GET"
		}
		if w := u.asSession(method, path, body); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
			t.Errorf("user %s: %d %s", path, w.Code, w.Body)
		}
		if w := u.req(method, path, body, nil); w.Code != 401 {
			t.Errorf("anonymous %s: %d %s", path, w.Code, w.Body)
		}
	}
	// An API token of an admin is not a dashboard session either.
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "adm", "scope": "deploy"}))
	for path, body := range map[string]any{"/api/v1/admin/db": nil, "/api/v1/admin/db/rows": map[string]any{"table": "users"}, "/api/v1/admin/db/sql": map[string]any{"sql": "SELECT 1"}} {
		method := "POST"
		if body == nil {
			method = "GET"
		}
		if w := e.req(method, path, body, map[string]string{"Authorization": "Bearer " + tok.Value}); w.Code != 401 && w.Code != 403 {
			t.Errorf("admin token %s: %d %s", path, w.Code, w.Body)
		}
	}
	spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String()
	for _, op := range []string{"adminGetDatabase", "adminListDatabaseRows", "adminRunDatabaseSQL"} {
		if !strings.Contains(spec, op) {
			t.Errorf("openapi has no %s", op)
		}
	}
}
