package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	_ "modernc.org/sqlite"
)

func TestQueryDB(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Notes", "slug": "notes-app", "volume": true}); res.IsError {
		t.Fatal(text(res))
	}
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, bio TEXT, avatar BLOB)`,
		`INSERT INTO users (email, bio, avatar) VALUES ('a@x.nl', 'line one` + "\n" + `line two | with pipe', x'0102030405060708090a')`,
		`INSERT INTO users (email) VALUES ('b@x.nl')`,
		`CREATE TABLE many (n INTEGER)`,
		`WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 500) INSERT INTO many SELECT i FROM c`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.WriteFile(filepath.Join(root, "x.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.UseDBHelper(func(ctx context.Context, _ string, req []byte) ([]byte, error) {
		return json.Marshal(dbview.Handle(ctx, req, dbview.HelperOptions{Root: root}))
	})

	res := call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name"})
	if res.IsError {
		t.Fatal(text(res))
	}
	out := text(res)
	if !strings.Contains(out, "[1] SELECT name FROM sqlite_schema") || !strings.Contains(out, "name\nmany\nusers") {
		t.Errorf("text = %s", out)
	}
	m := structured(t, res)
	if m["rows_shown"].(float64) != 2 || m["truncated"] != false || m["statements"].(float64) != 1 || len(m["next_steps"].([]any)) == 0 {
		t.Errorf("data = %v", m)
	}
	// The table is in the structured data too (a client may show only that),
	// and in the text content exactly once.
	if tbl, _ := m["table"].(string); !strings.Contains(tbl, "name\nmany\nusers") {
		t.Errorf("structured table = %q", m["table"])
	}
	if n := strings.Count(out, "[1] SELECT name FROM sqlite_schema"); n != 1 {
		t.Errorf("the table is %d times in the text:\n%s", n, out)
	}

	// One row: the row itself comes back, not only the counts.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT id, email FROM users WHERE id = 1"})
	m = structured(t, res)
	if tbl, _ := m["table"].(string); m["rows_returned"].(float64) != 1 || m["rows_shown"].(float64) != 1 || !strings.Contains(tbl, "id | email\n1 | a@x.nl") || !strings.Contains(text(res), "1 | a@x.nl") {
		t.Errorf("one row: %v\n%s", m, text(res))
	}

	// Values on one line, blobs and NULL spelled out.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT id, bio, avatar FROM users ORDER BY id"})
	out = text(res)
	if !strings.Contains(out, `1 | line one\nline two \| with pipe | <blob 10 bytes: x'0102030405060708...'>`) || !strings.Contains(out, "2 | NULL | NULL") {
		t.Errorf("text = %s", out)
	}

	// At most 200 rows: the rest is announced.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT n FROM many"})
	m = structured(t, res)
	if m["rows_shown"].(float64) != 200 || m["rows_returned"].(float64) != 500 || m["truncated"] != true || !strings.Contains(text(res), "300 more row(s) not shown") {
		t.Errorf("cap: %v\n%.300s", m, text(res))
	}
	if len(text(res)) > 60<<10 {
		t.Errorf("text is %d bytes", len(text(res)))
	}

	// query_db is always read-only and points at write_db.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "DELETE FROM users"})
	if m := assertErrorShape(t, "change with query_db", res); m != nil && (m["code"] != "db_read_only" || m["next_tool"] != "write_db" || !strings.Contains(m["hint"].(string), "write_db")) {
		t.Errorf("error = %v", m)
	}
	if m := assertErrorShape(t, "write=true on query_db", call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "write": true, "sql": "DELETE FROM users"})); m != nil && m["code"] != "invalid_arguments" {
		t.Errorf("write=true on query_db: %v", m)
	}
	res = call(t, cs, "write_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "DELETE FROM users WHERE id = 2; UPDATE users SET email = 'z@x.nl'"})
	if res.IsError || !strings.Contains(text(res), "OK, 1 row(s) affected") {
		t.Fatalf("write: %s", text(res))
	}
	if m := structured(t, res); m["rows_affected"].(float64) != 2 {
		t.Errorf("data = %v", m)
	}

	// Errors: which statement, and the usual shape.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT 1; SELECT * FROM nope"})
	m = assertErrorShape(t, "sql error", res)
	if m["code"] != "sql_error" || !strings.Contains(m["message"].(string), "Statement 2 failed: no such table: nope") {
		t.Errorf("error = %v", m)
	}
	if d, _ := m["details"].(map[string]any); d["results"] != nil {
		t.Errorf("results of earlier statements in the error: %v", d)
	}
	for sql, code := range map[string]string{"ATTACH ':memory:' AS m": "statement_not_allowed"} {
		if m := assertErrorShape(t, sql, call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": sql})); m != nil && m["code"] != code {
			t.Errorf("%s: %v", sql, m)
		}
	}
	for path, code := range map[string]string{"x.txt": "not_sqlite", "nope.db": "db_not_found", "../x": "path_invalid"} {
		m := assertErrorShape(t, path, call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": path, "sql": "SELECT 1"}))
		if m != nil && (m["code"] != code) {
			t.Errorf("%s: %v", path, m)
		}
		// No guessing loop: find the file in the code or list /data.
		if m != nil && code == "db_not_found" && (m["next_tool"] != "list_files" || !strings.Contains(m["hint"].(string), "list_files") || !strings.Contains(m["hint"].(string), "do not try names")) {
			t.Errorf("db_not_found = %v", m)
		}
	}
	assertErrorShape(t, "missing arguments", call(t, cs, "query_db", map[string]any{"app": "notes-app"}))

	// A read-only token queries, and cannot write.
	ro := e.connect(t, e.readToken)
	if res := call(t, ro, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT count(*) AS n FROM users"}); res.IsError {
		t.Errorf("read token: %s", text(res))
	}
	res = call(t, ro, "write_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "DELETE FROM users"})
	if m := assertErrorShape(t, "read token with write_db", res); m != nil && m["code"] != "token_read_only" {
		t.Errorf("error = %v", m)
	}
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT count(*) FROM users"})
	if !strings.Contains(text(res), "\n1\n") {
		t.Errorf("rows left = %s", text(res))
	}

	// query_db is read-only and documented for every kind of database;
	// write_db is the destructive one.
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"query_db": {"sqlite_schema", "PostgreSQL", "MySQL", "information_schema", "SHOW TABLES", "write_db", "200 rows"},
		"write_db": {"create_backup", "query_db", "the user asked"},
	}
	for _, tl := range tools.Tools {
		words, ok := want[tl.Name]
		if !ok {
			continue
		}
		delete(want, tl.Name)
		a := tl.Annotations
		if tl.Name == "query_db" && (a == nil || !a.ReadOnlyHint || strings.Contains(tl.Description, "SQLite-only")) {
			t.Errorf("query_db annotations = %+v", a)
		}
		if tl.Name == "write_db" && (a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint) {
			t.Errorf("write_db annotations = %+v", a)
		}
		for _, s := range words {
			if !strings.Contains(tl.Description, s) {
				t.Errorf("%s description lacks %q", tl.Name, s)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("tools missing: %v", want)
	}
	if b, _ := json.Marshal(toolByName(tools.Tools, "query_db").InputSchema); strings.Contains(string(b), `"write"`) {
		t.Errorf("query_db still takes write: %s", b)
	}
}

func toolByName(ts []*mcp.Tool, name string) *mcp.Tool {
	for _, t := range ts {
		if t.Name == name {
			return t
		}
	}
	return &mcp.Tool{}
}

func TestFormatSQLResultsStaysWithinTheLimit(t *testing.T) {
	var rs []dbview.StatementResult
	// Many statements with long SQL and wide column headers: nothing but headers.
	for i := 0; i < 400; i++ {
		cols := make([]string, 30)
		for j := range cols {
			cols[j] = strings.Repeat("c", 40)
		}
		rs = append(rs, dbview.StatementResult{SQL: strings.Repeat("SELECT 1 ", 40), Columns: cols, Rows: [][]any{}})
	}
	text, _, _, cut := formatSQLResults(rs)
	if len(text) > queryDBMaxBytes+300 || !cut || !strings.Contains(text, "output cut at 50 KB") {
		t.Errorf("text is %d bytes, cut %v", len(text), cut)
	}
	// Normal output is not cut and has no note.
	text, shown, _, cut := formatSQLResults([]dbview.StatementResult{{SQL: "SELECT 1", Columns: []string{"a"}, Rows: [][]any{{json.Number("1")}}}})
	if cut || shown != 1 || strings.Contains(text, "cut at") || text != "[1] SELECT 1\na\n1" {
		t.Errorf("text = %q", text)
	}
}

// "Show me the rows" on an app whose data is in a PostgreSQL service: the
// answer points at the service, not at a SQLite path or turning on /data.
func TestQueryDBOnAppWithDatabaseService(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "create_app", map[string]any{"name": "Shop", "slug": "pg-shop"})
	pid := structured(t, res)["app"].(map[string]any)["project"].(map[string]any)["id"].(string)
	res = call(t, cs, "add_service", map[string]any{"project": pid, "template": "postgres", "connect_to": []string{"pg-shop"}})
	if res.IsError {
		t.Fatal(text(res))
	}
	svcSlug := structured(t, res)["app"].(map[string]any)["slug"].(string)
	for _, args := range []map[string]any{{"app": "pg-shop", "sql": "SELECT 1"}, {"app": "pg-shop", "path": "app.db", "sql": "SELECT 1"}} {
		m := assertErrorShape(t, "query_db", call(t, cs, "query_db", args))
		if m != nil && (m["code"] != "db_in_service" || m["next_tool"] != "query_db" || !strings.Contains(m["hint"].(string), "app="+svcSlug)) {
			t.Errorf("%v: %v", args, m)
		}
	}
	// Without a service, an app without /data is told what that means.
	call(t, cs, "create_app", map[string]any{"name": "Plain", "slug": "plain-app"})
	m := assertErrorShape(t, "plain", call(t, cs, "query_db", map[string]any{"app": "plain-app", "path": "app.db", "sql": "SELECT 1"}))
	if m != nil && (m["code"] != "volume_off" || !strings.Contains(m["hint"].(string), "no SQLite file")) {
		t.Errorf("plain: %v", m)
	}
}
