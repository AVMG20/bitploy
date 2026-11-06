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

	// Read-only by default.
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "DELETE FROM users"})
	if m := assertErrorShape(t, "write without write=true", res); m != nil && m["code"] != "db_read_only" {
		t.Errorf("error = %v", m)
	}
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "write": true, "sql": "DELETE FROM users WHERE id = 2; UPDATE users SET email = 'z@x.nl'"})
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
		if m != nil && code == "db_not_found" && m["next_tool"] != "query_db" {
			t.Errorf("db_not_found next_tool = %v", m["next_tool"])
		}
	}
	assertErrorShape(t, "missing arguments", call(t, cs, "query_db", map[string]any{"app": "notes-app"}))

	// A read-only token queries, and cannot write.
	ro := e.connect(t, e.readToken)
	if res := call(t, ro, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT count(*) AS n FROM users"}); res.IsError {
		t.Errorf("read token: %s", text(res))
	}
	res = call(t, ro, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "DELETE FROM users", "write": true})
	if m := assertErrorShape(t, "read token with write", res); m != nil && m["code"] != "token_read_only" {
		t.Errorf("error = %v", m)
	}
	res = call(t, cs, "query_db", map[string]any{"app": "notes-app", "path": "app.db", "sql": "SELECT count(*) FROM users"})
	if !strings.Contains(text(res), "\n1\n") {
		t.Errorf("rows left = %s", text(res))
	}

	// The tool is documented for a model, and is not marked read-only (it can write).
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != "query_db" {
			continue
		}
		if tl.Annotations == nil || tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
			t.Errorf("annotations = %+v", tl.Annotations)
		}
		for _, s := range []string{"sqlite_schema", "create_backup", "write=true", "200 rows"} {
			if !strings.Contains(tl.Description, s) {
				t.Errorf("description lacks %q", s)
			}
		}
	}
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
