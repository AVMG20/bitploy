package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/dbview"

	_ "modernc.org/sqlite"
)

// The viewer needs Docker for its helper container, which this test does not
// have: dbview.Handle runs in its place, on a temp folder, with real SQLite.
func TestDatabaseEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Photos", "slug": "photos", "volume": true}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Plain", "slug": "plain"}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body BLOB)`,
		`CREATE TABLE big (k INTEGER PRIMARY KEY, v)`,
		`INSERT INTO notes (title, body) VALUES ('one', x'0102'), ('two', NULL)`,
		`INSERT INTO big VALUES (1, 9007199254740993)`,
		`CREATE VIEW v AS SELECT * FROM notes`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls int
	e.svc.UseDBHelper(func(ctx context.Context, _ string, req []byte) ([]byte, error) {
		calls++
		return json.Marshal(dbview.Handle(ctx, req, dbview.HelperOptions{Root: root}))
	})
	base := "/api/v1/apps/photos/db"

	// Not logged in at all.
	anon := map[string]any{
		base + "?path=app.db": nil,
		base + "/rows":        map[string]any{"path": "app.db", "table": "notes"},
		base + "/insert":      map[string]any{"path": "app.db", "table": "notes"},
		base + "/update":      map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 1}, "values": map[string]any{"title": "x"}},
		base + "/delete":      map[string]any{"path": "app.db", "table": "notes", "keys": []any{map[string]any{"rowid": 1}}},
		base + "/sql":         map[string]any{"path": "app.db", "sql": "SELECT 1"},
	}
	for path, body := range anon {
		method := "POST"
		if body == nil {
			method = "GET"
		}
		if w := e.req(method, path, body, nil); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d %s", method, path, w.Code, w.Body)
		}
	}

	// The schema, in the shape of the contract.
	w := e.asSession("GET", base+"?path=app.db", nil)
	if w.Code != 200 {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"path", "size", "journal_mode", "page_size", "sqlite_version", "tables"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("info has no %q: %s", k, w.Body)
		}
	}
	info := decode[app.DBInfo](t, w)
	if len(info.Tables) != 3 || info.Tables[1].Name != "notes" || len(info.Tables[1].Columns) != 3 || !info.Tables[1].Editable || info.Tables[2].Type != "view" {
		t.Errorf("info = %+v", info)
	}
	tbl := raw["tables"].([]any)[1].(map[string]any)
	for _, k := range []string{"name", "type", "without_rowid", "editable", "rows", "sql", "columns", "indexes"} {
		if _, ok := tbl[k]; !ok {
			t.Errorf("table has no %q: %v", k, tbl)
		}
	}
	col := tbl["columns"].([]any)[0].(map[string]any)
	for _, k := range []string{"name", "type", "notnull", "default", "pk", "hidden"} {
		if _, ok := col[k]; !ok {
			t.Errorf("column has no %q: %v", k, col)
		}
	}

	// Rows with a filter and a sort. A big integer keeps its digits.
	w = e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes", "limit": 10, "order_by": "title", "desc": true,
		"filters": []map[string]any{{"column": "title", "op": "like", "value": "%o%"}}})
	if w.Code != 200 {
		t.Fatalf("rows: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"key_columns":["rowid"]`) || !strings.Contains(w.Body.String(), `{"key":{"rowid":1},"values":[1,"one",{"blob":"AQI=","size":2,"truncated":false}]}`) ||
		!strings.Contains(w.Body.String(), `"total":2`) || !strings.Contains(w.Body.String(), `"offset":0`) || !strings.Contains(w.Body.String(), `"limit":10`) {
		t.Errorf("rows body = %s", w.Body)
	}
	w = e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "big"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `{"int":"9007199254740993"}`) {
		t.Errorf("big int: %d %s", w.Code, w.Body)
	}
	// ... and a big integer sent as {"int": ...} is stored exactly.
	w = e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "big", "values": map[string]any{"k": 2, "v": map[string]any{"int": "9007199254740995"}}})
	if w.Code != 201 || !strings.Contains(w.Body.String(), `{"int":"9007199254740995"}`) {
		t.Errorf("insert big: %d %s", w.Code, w.Body)
	}
	// A bare number beyond 2^53 is not rounded behind our back either.
	w = e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "big", "values": map[string]any{"k": 3, "v": json.Number("9007199254740993")}})
	if w.Code != 201 || !strings.Contains(w.Body.String(), `{"int":"9007199254740993"}`) {
		t.Errorf("insert bare big number: %d %s", w.Code, w.Body)
	}

	// Insert, update, delete.
	w = e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "notes", "values": map[string]any{"title": "three", "body": map[string]any{"blob": "AwQ="}}})
	if w.Code != 201 {
		t.Fatalf("insert: %d %s", w.Code, w.Body)
	}
	ins := decode[app.DBInsertResult](t, w)
	if ins.Key["rowid"] != float64(3) || ins.Row.Values[1] != "three" {
		t.Errorf("insert = %+v", ins)
	}
	w = e.asSession("POST", base+"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 3}, "values": map[string]any{"title": "THREE"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"row":{"key":{"rowid":3},"values":[3,"THREE"`) {
		t.Errorf("update: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 99}, "values": map[string]any{"title": "x"}})
	if w.Code != 404 || decode[errResp](t, w).Error.Code != "row_not_found" {
		t.Errorf("update missing: %d %s", w.Code, w.Body)
	}
	// A stale row is not changed or deleted: 409 row_changed.
	w = e.asSession("POST", base+"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 3}, "values": map[string]any{"title": "x"}, "match": map[string]any{"title": "three"}})
	if w.Code != 409 || decode[errResp](t, w).Error.Code != "row_changed" {
		t.Errorf("stale update: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/delete", map[string]any{"path": "app.db", "table": "notes", "keys": []map[string]any{{"rowid": 3}}, "match": []map[string]any{{"title": "three"}}})
	if w.Code != 409 || decode[errResp](t, w).Error.Code != "row_changed" {
		t.Errorf("stale delete: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/delete", map[string]any{"path": "app.db", "table": "notes", "keys": []map[string]any{{"rowid": 3}, {"rowid": 2}}, "match": []map[string]any{{"title": "THREE"}, {}}})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"deleted":2}` {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}

	// Search over all columns, ANDed with the filters; total follows.
	w = e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes", "search": "  ONE "})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Errorf("search: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes", "search": "one", "filters": []map[string]any{{"column": "title", "op": "=", "value": "bulk"}}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("search and filter: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes", "search": strings.Repeat("a", 201)})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "search_invalid" {
		t.Errorf("long search: %d %s", w.Code, w.Body)
	}

	// Update many rows: all or none; a stale one is named by its place.
	var mk []map[string]any
	for _, title := range []string{"m1", "m2"} {
		w = e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "notes", "values": map[string]any{"title": title}})
		mk = append(mk, decode[app.DBInsertResult](t, w).Key)
	}
	w = e.asSession("POST", base+"/update-many", map[string]any{"path": "app.db", "table": "notes", "keys": mk, "values": map[string]any{"title": "bulk"}})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"updated":2}` {
		t.Errorf("update-many: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/update-many", map[string]any{"path": "app.db", "table": "notes", "keys": mk, "values": map[string]any{"title": "again"},
		"match": []map[string]any{{"title": "bulk"}, {"title": "stale"}}})
	er2 := decode[errResp](t, w)
	if w.Code != 409 || er2.Error.Code != "row_changed" || er2.Error.Details["index"] != float64(1) {
		t.Errorf("stale update-many: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/update-many", map[string]any{"path": "app.db", "table": "notes", "keys": []map[string]any{}, "values": map[string]any{"title": "x"}})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "key_invalid" {
		t.Errorf("no keys: %d %s", w.Code, w.Body)
	}

	// SQL: read-only by default.
	w = e.asSession("POST", base+"/sql", map[string]any{"path": "app.db", "sql": "SELECT id, title FROM notes; SELECT 2"})
	if w.Code != 200 {
		t.Fatalf("sql: %d %s", w.Code, w.Body)
	}
	res := decode[app.DBSQLResult](t, w)
	if len(res.Results) != 2 || len(res.Results[0].Rows) != 3 || res.Results[0].Columns[1] != "title" || res.Results[0].RowsAffected != nil {
		t.Errorf("sql = %s", w.Body)
	}
	for _, k := range []string{`"sql":`, `"columns":`, `"rows":`, `"truncated":false`, `"rows_affected":null`, `"last_insert_rowid":null`, `"took_ms":`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Errorf("sql result lacks %s: %s", k, w.Body)
		}
	}
	w = e.asSession("POST", base+"/sql", map[string]any{"path": "app.db", "sql": "DELETE FROM notes"})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "db_read_only" || !strings.Contains(w.Body.String(), "Turn on writes") {
		t.Errorf("read-only write: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/sql", map[string]any{"path": "app.db", "write": true, "sql": "INSERT INTO notes (title) VALUES ('w'); SELECT * FROM missing"})
	er := decode[errResp](t, w)
	if w.Code != 400 || er.Error.Code != "sql_error" || er.Error.Message != "no such table: missing" || er.Error.Details["statement"] != float64(2) || len(er.Error.Details["results"].([]any)) != 1 {
		t.Errorf("sql error: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/sql", map[string]any{"path": "app.db", "sql": "ATTACH ':memory:' AS m"})
	if w.Code != 400 || decode[errResp](t, w).Error.Code != "statement_not_allowed" {
		t.Errorf("attach: %d %s", w.Code, w.Body)
	}

	// Errors of the file.
	for _, c := range []struct {
		path, code string
		status     int
	}{{"x.txt", "not_sqlite", 422}, {"nope.db", "db_not_found", 404}, {"..", "path_invalid", 400}, {"a/../../b", "path_invalid", 400}} {
		if w := e.asSession("GET", base+"?path="+c.path, nil); w.Code != c.status || decode[errResp](t, w).Error.Code != c.code {
			t.Errorf("info %q: %d %s", c.path, w.Code, w.Body)
		}
	}
	if w := e.asSession("GET", base, nil); w.Code != 422 && w.Code != 400 {
		t.Errorf("info without path: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "nope"}); w.Code != 404 || decode[errResp](t, w).Error.Code != "table_not_found" {
		t.Errorf("missing table: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes", "filters": []map[string]any{{"column": "title", "op": "bogus"}}}); w.Code != 422 && w.Code != 400 {
		t.Errorf("bad op: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "v", "values": map[string]any{"title": "x"}}); w.Code != 400 || decode[errResp](t, w).Error.Code != "not_editable" {
		t.Errorf("insert into a view: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", base+"/insert", map[string]any{"path": "app.db", "table": "notes", "values": map[string]any{"title": map[string]any{"text": "cut", "size": 9, "truncated": true}}}); w.Code != 400 || decode[errResp](t, w).Error.Code != "value_truncated" {
		t.Errorf("truncated: %d %s", w.Code, w.Body)
	}

	// /data off.
	plain := "/api/v1/apps/plain/db"
	if w := e.asSession("GET", plain+"?path=app.db", nil); w.Code != 409 || decode[errResp](t, w).Error.Code != "volume_off" {
		t.Errorf("volume off: %d %s", w.Code, w.Body)
	}

	// Scopes: a read-only token reads and runs read-only SQL, and changes nothing.
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	readOnly := map[string]string{"Authorization": "Bearer " + ro.Value}
	if w := e.req("GET", base+"?path=app.db", nil, readOnly); w.Code != 200 {
		t.Errorf("read-only info: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes"}, readOnly); w.Code != 200 {
		t.Errorf("read-only rows: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", base+"/sql", map[string]any{"path": "app.db", "sql": "SELECT 1"}, readOnly); w.Code != 200 {
		t.Errorf("read-only sql: %d %s", w.Code, w.Body)
	}
	before := calls
	for _, c := range []struct {
		path string
		body map[string]any
	}{
		{"/sql", map[string]any{"path": "app.db", "sql": "DELETE FROM notes", "write": true}},
		{"/insert", map[string]any{"path": "app.db", "table": "notes", "values": map[string]any{"title": "x"}}},
		{"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 1}, "values": map[string]any{"title": "x"}}},
		{"/delete", map[string]any{"path": "app.db", "table": "notes", "keys": []map[string]any{{"rowid": 1}}}},
		{"/update-many", map[string]any{"path": "app.db", "table": "notes", "keys": []map[string]any{{"rowid": 1}}, "values": map[string]any{"title": "x"}}},
	} {
		if w := e.req("POST", base+c.path, c.body, readOnly); w.Code != 403 || decode[errResp](t, w).Error.Code != "token_read_only" {
			t.Errorf("read-only %s: %d %s", c.path, w.Code, w.Body)
		}
	}
	if calls != before {
		t.Error("the helper ran for a read-only token that writes")
	}

	// Somebody else cannot see any of it.
	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.asSession("GET", base+"?path=app.db", nil); w.Code != 404 {
		t.Errorf("other user: %d", w.Code)
	}

	// The OpenAPI document has all six operations.
	spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String()
	for _, op := range []string{"getDatabase", "listDatabaseRows", "insertDatabaseRow", "updateDatabaseRow", "updateDatabaseRows", "deleteDatabaseRows", "runDatabaseSQL"} {
		if !strings.Contains(spec, op) {
			t.Errorf("openapi has no %s", op)
		}
	}
}

// Without Docker (and without a Linux binary) the answer is a plain error.
func TestDatabaseEndpointsWithoutDocker(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Photos", "slug": "photos", "volume": true}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/apps/photos/db?path=app.db", nil); w.Code != 503 || decode[errResp](t, w).Error.Code != "docker_unavailable" {
		t.Errorf("info: %d %s", w.Code, w.Body)
	}
}

// One whole cell, and a long text written back through update.
func TestDatabaseCellEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Photos", "slug": "photos", "volume": true}); w.Code != 201 {
		t.Fatalf("create app: %d %s", w.Code, w.Body)
	}
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("0123456789", 11000) // 110 KB
	for _, q := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT, body BLOB)`,
		`CREATE VIEW v AS SELECT * FROM notes`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO notes (title, body) VALUES (?, x'0102'), ('short', NULL)`, long); err != nil {
		t.Fatal(err)
	}
	db.Close()
	e.svc.UseDBHelper(func(ctx context.Context, _ string, req []byte) ([]byte, error) {
		return json.Marshal(dbview.Handle(ctx, req, dbview.HelperOptions{Root: root}))
	})
	base := "/api/v1/apps/photos/db"
	cell := func(table string, key any, col string) map[string]any {
		return map[string]any{"path": "app.db", "table": table, "key": key, "column": col}
	}

	if w := e.req("POST", base+"/cell", cell("notes", map[string]any{"rowid": 1}, "title"), nil); w.Code != 401 {
		t.Errorf("anonymous: %d %s", w.Code, w.Body)
	}
	// The grid cuts the text; the cell does not.
	w := e.asSession("POST", base+"/rows", map[string]any{"path": "app.db", "table": "notes"})
	if !strings.Contains(w.Body.String(), `"truncated":true`) {
		t.Fatalf("rows should cut the text: %s", w.Body)
	}
	w = e.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 1}, "title"))
	var got struct {
		Value any `json:"value"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Value != long {
		t.Fatalf("cell: %d (%d bytes)", w.Code, w.Body.Len())
	}
	w = e.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 1}, "body"))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"value":{"blob":"AQI=","size":2,"truncated":false}}` {
		t.Errorf("blob cell: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 2}, "body"))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"value":null}` {
		t.Errorf("null cell: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{cell("notes", map[string]any{"rowid": 99}, "title"), 404, "row_not_found"},
		{cell("notes", map[string]any{"rowid": 1}, "nope"), 400, "column_not_found"},
		{cell("nope", map[string]any{"rowid": 1}, "title"), 404, "table_not_found"},
		{cell("notes", map[string]any{"x": 1}, "title"), 400, "key_invalid"},
		{cell("v", map[string]any{"rowid": 1}, "title"), 400, "not_editable"},
	} {
		if w := e.asSession("POST", base+"/cell", c.body); w.Code != c.status || decode[errResp](t, w).Error.Code != c.code {
			t.Errorf("cell %v: %d %s", c.body, w.Code, w.Body)
		}
	}

	// A read-only token may read a cell.
	ro := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "ro", "scope": "read"}))
	if w := e.req("POST", base+"/cell", cell("notes", map[string]any{"rowid": 2}, "title"), map[string]string{"Authorization": "Bearer " + ro.Value}); w.Code != 200 {
		t.Errorf("read-only cell: %d %s", w.Code, w.Body)
	}

	// A text of 4 MiB with many escapes (an 8 MiB body) goes in and comes out whole.
	big := strings.Repeat("line \"x\"\n", 4<<20/9)
	w = e.asSession("POST", base+"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 2}, "values": map[string]any{"title": big}})
	if w.Code != 200 {
		t.Fatalf("update with a long text: %d %.200s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 2}, "title"))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Value != big {
		t.Errorf("big cell: %d (%d bytes)", w.Code, w.Body.Len())
	}
	// Over 4 MiB it is the normal cut-off form.
	huge := strings.Repeat("a", dbview.MaxCellBytes+1)
	if w := e.asSession("POST", base+"/update", map[string]any{"path": "app.db", "table": "notes", "key": map[string]any{"rowid": 2}, "values": map[string]any{"title": huge}}); w.Code != 200 {
		t.Fatalf("update with a huger text: %d %.200s", w.Code, w.Body)
	}
	w = e.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 2}, "title"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"truncated":true`) || !strings.Contains(w.Body.String(), `"size":4194305`) || w.Body.Len() > 70<<10 {
		t.Errorf("huge cell: %d (%d bytes) %.120s", w.Code, w.Body.Len(), w.Body)
	}

	other := newEnv(t)
	other.login("other@example.nl", false)
	if w := other.asSession("POST", base+"/cell", cell("notes", map[string]any{"rowid": 1}, "title")); w.Code != 404 {
		t.Errorf("other user: %d", w.Code)
	}
	if spec := e.req("GET", "/api/v1/openapi.json", nil, nil).Body.String(); !strings.Contains(spec, "getDatabaseCell") {
		t.Error("openapi has no getDatabaseCell")
	}
}
