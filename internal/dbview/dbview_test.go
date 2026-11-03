package dbview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newDB makes a database with a few kinds of tables and returns its path.
func newDB(t *testing.T, extra ...string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL, age INT DEFAULT 5, created DATETIME, note TEXT, data BLOB)`,
		`CREATE UNIQUE INDEX users_email ON users(email)`,
		`CREATE TABLE kv (k TEXT PRIMARY KEY, v) WITHOUT ROWID`,
		`CREATE TABLE ck (a INTEGER, b TEXT, c, PRIMARY KEY (a, b)) WITHOUT ROWID`,
		`CREATE TABLE shadow (rowid TEXT, _rowid_ TEXT, oid TEXT, x)`,
		`CREATE TABLE partial (rowid TEXT, x)`,
		`CREATE TABLE plain (a, b)`,
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, pid INTEGER REFERENCES parent(id))`,
		`CREATE VIEW adults AS SELECT id, email, created FROM users WHERE age >= 18`,
		`INSERT INTO users (id, email, age, created, note) VALUES (1, 'a@x.nl', 30, '2024-03-05 10:11:12', 'first')`,
		`INSERT INTO users (id, email, age, created, note) VALUES (2, 'b@x.nl', 12, '2024-03-06', 'second')`,
		`INSERT INTO users (id, email, age, created, note, data) VALUES (3, 'c@y.nl', 41, '2024-03-07T01:02:03Z', NULL, x'00ff10')`,
		`INSERT INTO kv VALUES ('one', 1), ('two', 2.5), ('three', 'x')`,
		`INSERT INTO ck VALUES (1, 'a', 'x'), (1, 'b', 'y')`,
		`INSERT INTO partial VALUES ('r', 'x')`,
		`INSERT INTO parent VALUES (1)`,
		`INSERT INTO child VALUES (1, 1)`,
	}
	for _, s := range append(stmts, extra...) {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return file
}

func do[T any](t *testing.T, file string, req Request) T {
	t.Helper()
	resp := Run(context.Background(), file, req)
	if resp.Error != nil {
		t.Fatalf("%s: %s: %s", req.Op, resp.Error.Code, resp.Error.Message)
	}
	var out T
	if err := Decode(resp.Result, &out); err != nil {
		t.Fatalf("%s: decoding %s: %v", req.Op, resp.Result, err)
	}
	return out
}

func fail(t *testing.T, file string, req Request, code string) *Error {
	t.Helper()
	resp := Run(context.Background(), file, req)
	if resp.Error == nil {
		t.Fatalf("%s %s: expected %s, got %s", req.Op, req.Table, code, resp.Result)
	}
	if resp.Error.Code != code {
		t.Fatalf("%s %s: expected %s, got %s: %s", req.Op, req.Table, code, resp.Error.Code, resp.Error.Message)
	}
	if resp.Error.Message == "" {
		t.Errorf("%s: no message", code)
	}
	return resp.Error
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func vals(kv ...string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = raw(kv[i+1])
	}
	return m
}

func TestNotSQLite(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"text.db": "hello, this is no database", "empty.db": "", "short.db": "SQLite"} {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		fail(t, f, Request{Op: OpInfo}, "not_sqlite")
	}
	fail(t, filepath.Join(dir, "missing.db"), Request{Op: OpInfo}, "db_not_found")
}

func TestInfo(t *testing.T) {
	file := newDB(t)
	info := do[Info](t, file, Request{Op: OpInfo, Path: "app.db"})
	if info.Path != "app.db" || info.Size <= 0 || info.PageSize <= 0 || info.SQLiteVersion == "" || info.JournalMode == "" {
		t.Errorf("info = %+v", info)
	}
	byName := map[string]Table{}
	var names []string
	for _, tb := range info.Tables {
		byName[tb.Name] = tb
		names = append(names, tb.Name)
	}
	if got := strings.Join(names, ","); got != "adults,child,ck,kv,parent,partial,plain,shadow,users" {
		t.Errorf("tables = %s", got)
	}
	u := byName["users"]
	if u.Type != "table" || !u.Editable || u.WithoutRowid || u.Rows == nil || *u.Rows != 3 || !strings.HasPrefix(u.SQL, "CREATE TABLE users") {
		t.Errorf("users = %+v", u)
	}
	if len(u.Columns) != 6 || u.Columns[0].Name != "id" || u.Columns[0].PK != 1 || u.Columns[0].Type != "INTEGER" ||
		!u.Columns[1].NotNull || u.Columns[2].Default == nil || *u.Columns[2].Default != "5" || u.Columns[3].Default != nil {
		t.Errorf("columns = %+v", u.Columns)
	}
	if len(u.Indexes) != 1 || u.Indexes[0].Name != "users_email" || !u.Indexes[0].Unique || strings.Join(u.Indexes[0].Columns, ",") != "email" ||
		u.Indexes[0].SQL == nil || !strings.HasPrefix(*u.Indexes[0].SQL, "CREATE UNIQUE INDEX") {
		t.Errorf("indexes = %+v", u.Indexes)
	}
	if kv := byName["kv"]; !kv.WithoutRowid || !kv.Editable || len(kv.Indexes) != 1 || kv.Indexes[0].SQL != nil || kv.Indexes[0].Unique != true {
		t.Errorf("kv = %+v", kv)
	}
	if v := byName["adults"]; v.Type != "view" || v.Editable || v.Rows == nil || *v.Rows != 2 || len(v.Indexes) != 0 {
		t.Errorf("view = %+v", v)
	}
	if byName["shadow"].Editable || !byName["partial"].Editable || !byName["plain"].Editable {
		t.Errorf("editable: shadow %v partial %v plain %v", byName["shadow"].Editable, byName["partial"].Editable, byName["plain"].Editable)
	}
	for _, tb := range info.Tables {
		if strings.HasPrefix(tb.Name, "sqlite_") {
			t.Errorf("internal table listed: %s", tb.Name)
		}
		if tb.Columns == nil || tb.Indexes == nil {
			t.Errorf("%s: nil columns or indexes (must be [])", tb.Name)
		}
	}
}

func TestRowsPagingSortFilter(t *testing.T) {
	file := newDB(t)
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users"})
	if r.Limit != 100 || r.Offset != 0 || r.Total == nil || *r.Total != 3 || !r.Editable || len(r.Rows) != 3 ||
		strings.Join(r.Columns, ",") != "id,email,age,created,note,data" || strings.Join(r.KeyColumns, ",") != "rowid" {
		t.Fatalf("rows = %+v", r)
	}
	first := r.Rows[0]
	if k := first.Key["rowid"]; fmt.Sprint(k) != "1" {
		t.Errorf("key = %v", first.Key)
	}
	// A DATETIME column comes back exactly as it was stored.
	if first.Values[3] != "2024-03-05 10:11:12" || r.Rows[1].Values[3] != "2024-03-06" || r.Rows[2].Values[3] != "2024-03-07T01:02:03Z" {
		t.Errorf("dates = %v %v %v", first.Values[3], r.Rows[1].Values[3], r.Rows[2].Values[3])
	}
	if first.Values[4] != "first" || r.Rows[2].Values[4] != nil {
		t.Errorf("note = %v / %v", first.Values[4], r.Rows[2].Values[4])
	}
	if b, ok := r.Rows[2].Values[5].(map[string]any); !ok || b["blob"] != "AP8Q" || fmt.Sprint(b["size"]) != "3" || b["truncated"] != false {
		t.Errorf("blob = %#v", r.Rows[2].Values[5])
	}

	// Paging.
	p := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Limit: 2, Offset: 1})
	if len(p.Rows) != 2 || fmt.Sprint(p.Rows[0].Key["rowid"]) != "2" || p.Offset != 1 || p.Limit != 2 || *p.Total != 3 {
		t.Errorf("page = %+v", p)
	}
	// Sorting, descending, by a name in other letters.
	d := do[RowsResult](t, file, Request{Op: OpRows, Table: "USERS", OrderBy: "EMAIL", Desc: true})
	if fmt.Sprint(d.Rows[0].Key["rowid"]) != "3" || fmt.Sprint(d.Rows[2].Key["rowid"]) != "1" {
		t.Errorf("desc = %+v", d.Rows)
	}
	// Filters are ANDed.
	f := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{
		{Column: "email", Op: "like", Value: raw(`"%@x.nl"`)}, {Column: "age", Op: ">=", Value: raw(`18`)},
	}})
	if len(f.Rows) != 1 || *f.Total != 1 || f.Rows[0].Values[1] != "a@x.nl" {
		t.Errorf("filtered = %+v", f)
	}
	n := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "note", Op: "is null"}}})
	if len(n.Rows) != 1 || fmt.Sprint(n.Rows[0].Key["rowid"]) != "3" {
		t.Errorf("is null = %+v", n.Rows)
	}
	n = do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "note", Op: "= ", Value: raw(`null`)}}})
	if len(n.Rows) != 1 {
		t.Errorf("= null should match NULL: %+v", n.Rows)
	}
	n = do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "note", Op: "is not null"}}})
	if len(n.Rows) != 2 {
		t.Errorf("is not null = %+v", n.Rows)
	}
	// Values are bound: SQL in a value is only a value.
	n = do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "email", Op: "=", Value: raw(`"x' OR '1'='1"`)}}})
	if len(n.Rows) != 0 {
		t.Errorf("injection matched %d rows", len(n.Rows))
	}
	// Empty result: rows is [] and not null.
	b, _ := json.Marshal(n)
	if !strings.Contains(string(b), `"rows":[]`) {
		t.Errorf("empty page = %s", b)
	}
}

func TestRowsErrors(t *testing.T) {
	file := newDB(t)
	fail(t, file, Request{Op: OpRows, Table: "nope"}, "table_not_found")
	fail(t, file, Request{Op: OpRows, Table: "sqlite_schema"}, "table_not_found")
	fail(t, file, Request{Op: OpRows, Table: `users"; DROP TABLE users; --`}, "table_not_found")
	fail(t, file, Request{Op: OpRows, Table: "users", OrderBy: "nope"}, "column_not_found")
	fail(t, file, Request{Op: OpRows, Table: "users", OrderBy: `email"; DROP TABLE users; --`}, "column_not_found")
	fail(t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "nope", Op: "="}}}, "column_not_found")
	fail(t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "email", Op: "; drop"}}}, "filter_invalid")
	fail(t, file, Request{Op: OpRows, Table: "users", Limit: 1001}, "limit_invalid")
	fail(t, file, Request{Op: OpRows, Table: "users", Limit: -1}, "limit_invalid")
	fail(t, file, Request{Op: OpRows, Table: "users", Offset: -1}, "offset_invalid")
	// The table is still there.
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users"}); len(r.Rows) != 3 {
		t.Error("users changed")
	}
}

func TestRowKeys(t *testing.T) {
	file := newDB(t)
	// WITHOUT ROWID: the key is the primary key.
	kv := do[RowsResult](t, file, Request{Op: OpRows, Table: "kv"})
	if !kv.Editable || strings.Join(kv.KeyColumns, ",") != "k" || len(kv.Rows) != 3 || kv.Rows[0].Key["k"] != "one" {
		t.Errorf("kv = %+v", kv)
	}
	ck := do[RowsResult](t, file, Request{Op: OpRows, Table: "ck"})
	if strings.Join(ck.KeyColumns, ",") != "a,b" || fmt.Sprint(ck.Rows[1].Key["a"]) != "1" || ck.Rows[1].Key["b"] != "b" || len(ck.Rows[1].Values) != 3 {
		t.Errorf("ck = %+v", ck)
	}
	// A view and a table whose columns hide every rowid name have no key.
	for _, name := range []string{"adults", "shadow"} {
		r := do[RowsResult](t, file, Request{Op: OpRows, Table: name})
		if r.Editable || len(r.KeyColumns) != 0 || r.KeyColumns == nil {
			t.Errorf("%s: editable %v keys %v", name, r.Editable, r.KeyColumns)
		}
		for _, row := range r.Rows {
			if row.Key != nil {
				t.Errorf("%s: key %v, want null", name, row.Key)
			}
		}
	}
	b, _ := json.Marshal(do[RowsResult](t, file, Request{Op: OpRows, Table: "adults"}).Rows[0])
	if !strings.Contains(string(b), `"key":null`) {
		t.Errorf("view row = %s", b)
	}
	// A column called rowid hides that name only: the table still has a key.
	p := do[RowsResult](t, file, Request{Op: OpRows, Table: "partial"})
	if !p.Editable || fmt.Sprint(p.Rows[0].Key["rowid"]) != "1" || p.Rows[0].Values[0] != "r" || len(p.Rows[0].Values) != 2 {
		t.Errorf("partial = %+v", p)
	}
	// And changes use the right one.
	u := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "partial", Key: vals("rowid", "1"), Values: vals("x", `"changed"`)})
	if u.Row.Values[1] != "changed" || u.Row.Values[0] != "r" {
		t.Errorf("update partial = %+v", u)
	}
	fail(t, file, Request{Op: OpInsert, Table: "adults", Values: vals("email", `"x"`)}, "not_editable")
	fail(t, file, Request{Op: OpInsert, Table: "shadow", Values: vals("x", `"x"`)}, "not_editable")
	fail(t, file, Request{Op: OpDelete, Table: "adults", Keys: []map[string]json.RawMessage{vals("rowid", "1")}}, "not_editable")
}

func TestValueEncoding(t *testing.T) {
	file := newDB(t)
	big := strings.Repeat("é", 40000) // 80000 bytes
	if _, err := insertRaw(file, `INSERT INTO users (id, email, note, data) VALUES (10, 'big@x.nl', ?, ?)`, big, make([]byte, 5000)); err != nil {
		t.Fatal(err)
	}
	if _, err := insertRaw(file, `INSERT INTO kv VALUES ('i1', 9007199254740993), ('i2', -9007199254740993), ('i3', 9007199254740992), ('i4', 1.5), ('i5', 9e999), ('i6', -9e999), ('i7', 0.1)`); err != nil {
		t.Fatal(err)
	}
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "id", Op: "=", Value: raw("10")}}})
	note, ok := r.Rows[0].Values[4].(map[string]any)
	if !ok || note["truncated"] != true || fmt.Sprint(note["size"]) != "80000" {
		t.Fatalf("note = %v", r.Rows[0].Values[4])
	}
	txt := note["text"].(string)
	if len(txt) > MaxTextBytes || len(txt) < MaxTextBytes-3 || strings.ContainsRune(txt, '�') || !strings.HasPrefix(big, txt) {
		t.Errorf("cut text: %d bytes", len(txt))
	}
	blob := r.Rows[0].Values[5].(map[string]any)
	if blob["truncated"] != true || fmt.Sprint(blob["size"]) != "5000" || len(blob["blob"].(string)) != 4096/3*4+4 {
		t.Errorf("blob = size %v truncated %v len %d", blob["size"], blob["truncated"], len(blob["blob"].(string)))
	}

	kv := do[RowsResult](t, file, Request{Op: OpRows, Table: "kv", Filters: []Filter{{Column: "k", Op: "like", Value: raw(`"i%"`)}}, OrderBy: "k"})
	want := []any{
		map[string]any{"int": "9007199254740993"}, map[string]any{"int": "-9007199254740993"}, json.Number("9007199254740992"),
		json.Number("1.5"), map[string]any{"real": "Inf"}, map[string]any{"real": "-Inf"}, json.Number("0.1"),
	}
	for i, w := range want {
		if fmt.Sprint(kv.Rows[i].Values[1]) != fmt.Sprint(w) {
			t.Errorf("%s = %#v, want %#v", kv.Rows[i].Key["k"], kv.Rows[i].Values[1], w)
		}
	}
}

func insertRaw(file, q string, args ...any) (sql.Result, error) {
	db, err := sql.Open("sqlite", file)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(q, args...)
}

func TestInsertUpdateDelete(t *testing.T) {
	file := newDB(t)

	// Insert into a rowid table: the key and the row come back, defaults apply.
	ins := do[InsertResult](t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `"d@x.nl"`, "note", `"hi"`, "data", `{"blob":"AAEC"}`)})
	if fmt.Sprint(ins.Key["rowid"]) != "4" || ins.Row.Values[1] != "d@x.nl" || fmt.Sprint(ins.Row.Values[2]) != "5" {
		t.Errorf("insert = %+v", ins)
	}
	if b := ins.Row.Values[5].(map[string]any); b["blob"] != "AAEC" || fmt.Sprint(b["size"]) != "3" {
		t.Errorf("blob = %v", b)
	}
	// An explicit id and a big integer.
	ins = do[InsertResult](t, file, Request{Op: OpInsert, Table: "users", Values: vals("id", `{"int":"9007199254740993"}`, "email", `"big@x.nl"`)})
	if k, ok := ins.Key["rowid"].(map[string]any); !ok || k["int"] != "9007199254740993" {
		t.Errorf("big key = %v", ins.Key)
	}
	// Nothing but defaults.
	pl := do[InsertResult](t, file, Request{Op: OpInsert, Table: "plain"})
	if fmt.Sprint(pl.Key["rowid"]) != "1" || pl.Row.Values[0] != nil {
		t.Errorf("defaults = %+v", pl)
	}
	// A constraint says so in plain words.
	e := fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `"a@x.nl"`)}, "sql_error")
	if !strings.Contains(e.Message, "UNIQUE constraint failed: users.email") || strings.Contains(e.Message, "(2067)") {
		t.Errorf("message = %q", e.Message)
	}
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("note", `"no email"`)}, "sql_error")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("nope", `1`)}, "column_not_found")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `{"text":"cut","size":99,"truncated":true}`)}, "value_truncated")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `{"blob":"AA","size":9,"truncated":true}`)}, "value_truncated")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `[1]`)}, "value_invalid")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `{"int":"x"}`)}, "value_invalid")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `{"foo":1}`)}, "value_invalid")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `{"blob":"!!"}`)}, "value_invalid")
	fail(t, file, Request{Op: OpInsert, Table: "users", Values: vals("email", `99999999999999999999`)}, "value_invalid")

	// WITHOUT ROWID: the key is the primary key.
	k := do[InsertResult](t, file, Request{Op: OpInsert, Table: "ck", Values: vals("a", "2", "b", `"z"`, "c", `1.5`)})
	if fmt.Sprint(k.Key["a"]) != "2" || k.Key["b"] != "z" {
		t.Errorf("ck key = %v", k.Key)
	}

	// Update: change two columns, the row comes back changed.
	up := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "1"), Values: vals("age", `31`, "note", `null`)})
	if fmt.Sprint(up.Row.Values[2]) != "31" || up.Row.Values[4] != nil || up.Row.Values[1] != "a@x.nl" {
		t.Errorf("update = %+v", up)
	}
	// A text for a number column: SQLite's own affinity rules.
	up = do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "1"), Values: vals("age", `"32"`)})
	if fmt.Sprint(up.Row.Values[2]) != "32" {
		t.Errorf("affinity = %v", up.Row.Values[2])
	}
	// Changing the INTEGER PRIMARY KEY moves the row: the new key comes back.
	up = do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "2"), Values: vals("id", `20`)})
	if fmt.Sprint(up.Row.Key["rowid"]) != "20" || fmt.Sprint(up.Row.Values[0]) != "20" {
		t.Errorf("moved = %+v", up)
	}
	// WITHOUT ROWID with a composite key, changing a key column.
	ku := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "ck", Key: vals("a", "1", "b", `"a"`), Values: vals("b", `"q"`, "c", `"new"`)})
	if ku.Row.Key["b"] != "q" || ku.Row.Values[2] != "new" {
		t.Errorf("ck update = %+v", ku)
	}
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "999"), Values: vals("age", `1`)}, "row_not_found")
	fail(t, file, Request{Op: OpUpdate, Table: "ck", Key: vals("a", "9", "b", `"a"`), Values: vals("c", `1`)}, "row_not_found")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "1")}, "value_invalid")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("id", "1"), Values: vals("age", `1`)}, "key_invalid")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", `"x"`), Values: vals("age", `1`)}, "key_invalid")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", `{"text":"1","truncated":true}`), Values: vals("age", `1`)}, "value_truncated")
	fail(t, file, Request{Op: OpUpdate, Table: "ck", Key: vals("a", "1"), Values: vals("c", `1`)}, "key_invalid")
	fail(t, file, Request{Op: OpUpdate, Table: "ck", Key: vals("a", "1", "b", `null`), Values: vals("c", `1`)}, "key_invalid")
	// A statement that fails changes nothing.
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "3"), Values: vals("age", `1`, "email", `"a@x.nl"`)}, "sql_error")
	u3 := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "id", Op: "=", Value: raw("3")}}})
	if fmt.Sprint(u3.Rows[0].Values[2]) != "41" {
		t.Errorf("a failed update changed the row: %v", u3.Rows[0].Values)
	}

	// Delete: several rows in one go; keys that match nothing do not count.
	del := do[DeleteResult](t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{vals("rowid", "1"), vals("rowid", "3"), vals("rowid", "777")}})
	if del.Deleted != 2 {
		t.Errorf("deleted = %d", del.Deleted)
	}
	del = do[DeleteResult](t, file, Request{Op: OpDelete, Table: "kv", Keys: []map[string]json.RawMessage{vals("k", `"one"`)}})
	if del.Deleted != 1 {
		t.Errorf("kv deleted = %d", del.Deleted)
	}
	fail(t, file, Request{Op: OpDelete, Table: "users"}, "key_invalid")
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: make([]map[string]json.RawMessage, MaxKeys+1)}, "key_invalid")
	// Foreign keys are enforced, and one bad key undoes the whole delete.
	fail(t, file, Request{Op: OpDelete, Table: "parent", Keys: []map[string]json.RawMessage{vals("rowid", "1")}}, "sql_error")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "parent"}); len(r.Rows) != 1 {
		t.Error("the parent row went despite the foreign key")
	}
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{vals("rowid", "2"), vals("nope", "1")}}, "key_invalid")
	fail(t, file, Request{Op: OpDelete, Table: "child", Keys: []map[string]json.RawMessage{vals("rowid", "1"), vals("rowid", `{"x":1}`)}}, "value_invalid")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "child"}); len(r.Rows) != 1 {
		t.Error("child row went")
	}
}

func TestGeneratedColumn(t *testing.T) {
	file := newDB(t, `CREATE TABLE g (a INTEGER, b INTEGER GENERATED ALWAYS AS (a * 2) STORED, c INTEGER AS (a + 1))`, `INSERT INTO g (a) VALUES (2)`)
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "g"})
	if strings.Join(r.Columns, ",") != "a,b,c" || fmt.Sprint(r.Rows[0].Values[1]) != "4" || fmt.Sprint(r.Rows[0].Values[2]) != "3" {
		t.Errorf("generated = %+v", r)
	}
	info := do[Info](t, file, Request{Op: OpInfo})
	for _, tb := range info.Tables {
		if tb.Name == "g" && (tb.Columns[0].Generated || !tb.Columns[1].Generated || !tb.Columns[2].Generated || tb.Columns[1].Hidden) {
			t.Errorf("g columns = %+v", tb.Columns)
		}
	}
	fail(t, file, Request{Op: OpInsert, Table: "g", Values: vals("b", "1")}, "column_read_only")
	up := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "g", Key: vals("rowid", "1"), Values: vals("a", "5")})
	if fmt.Sprint(up.Row.Values[1]) != "10" {
		t.Errorf("recomputed = %v", up.Row.Values)
	}
}

func TestConsoleRead(t *testing.T) {
	file := newDB(t)
	r := do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT id, email FROM users ORDER BY id; SELECT count(*) AS n FROM users -- done\n;;  -- nothing\n"})
	if len(r.Results) != 2 {
		t.Fatalf("results = %+v", r)
	}
	a := r.Results[0]
	if a.SQL != "SELECT id, email FROM users ORDER BY id" || strings.Join(a.Columns, ",") != "id,email" || len(a.Rows) != 3 || a.Truncated ||
		a.RowsAffected != nil || a.LastInsertRowid != nil || fmt.Sprint(a.Rows[1][0]) != "2" || a.Rows[1][1] != "b@x.nl" {
		t.Errorf("first = %+v", a)
	}
	if r.Results[1].Columns[0] != "n" || fmt.Sprint(r.Results[1].Rows[0][0]) != "3" {
		t.Errorf("second = %+v", r.Results[1])
	}

	// Dates come back as stored, also with a name used twice and a CTE on top.
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT created, created, id FROM users ORDER BY id"})
	row := r.Results[0].Rows
	if strings.Join(r.Results[0].Columns, ",") != "created,created,id" || row[0][0] != "2024-03-05 10:11:12" || row[1][1] != "2024-03-06" || row[2][0] != "2024-03-07T01:02:03Z" || fmt.Sprint(row[0][2]) != "1" {
		t.Errorf("dates = %v", row)
	}
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "WITH x AS (SELECT * FROM users) SELECT created FROM x ORDER BY id LIMIT 1"})
	if r.Results[0].Rows[0][0] != "2024-03-05 10:11:12" {
		t.Errorf("cte date = %v", r.Results[0].Rows)
	}

	// Values: a big integer, a real, text, blob, null in one row.
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT 9007199254740993, 1.5, 'x', x'0102', NULL, 9e999, 3"})
	got, _ := json.Marshal(r.Results[0].Rows[0])
	if string(got) != `[{"int":"9007199254740993"},1.5,"x",{"blob":"AQI=","size":2,"truncated":false},null,{"real":"Inf"},3]` {
		t.Errorf("row = %s", got)
	}

	// 1000 rows at most, and it says so.
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 1500) SELECT i FROM n"})
	if x := r.Results[0]; len(x.Rows) != 1000 || !x.Truncated {
		t.Errorf("rows = %d truncated %v", len(x.Rows), x.Truncated)
	}
	// A statement without rows has columns [] and rows [].
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "PRAGMA foreign_keys = ON"})
	b, _ := json.Marshal(r.Results[0])
	if !strings.Contains(string(b), `"columns":[]`) || !strings.Contains(string(b), `"rows":[]`) {
		t.Errorf("empty = %s", b)
	}
	// PRAGMAs that read are fine.
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "PRAGMA table_info(users)"})
	if len(r.Results[0].Rows) != 6 {
		t.Errorf("table_info = %v", r.Results[0].Rows)
	}
	r = do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT name, sql FROM sqlite_schema WHERE name = 'ck'"})
	if r.Results[0].Rows[0][0] != "ck" {
		t.Errorf("schema = %v", r.Results[0].Rows)
	}
	fail(t, file, Request{Op: OpSQL, SQL: "  -- only a comment\n ; "}, "sql_empty")
	fail(t, file, Request{Op: OpSQL, SQL: strings.Repeat("x", MaxSQLBytes+1)}, "sql_too_long")
}

func TestConsoleReadOnly(t *testing.T) {
	file := newDB(t)
	for _, q := range []string{
		"INSERT INTO plain VALUES (1, 2)", "UPDATE users SET age = 1", "DELETE FROM users", "DROP TABLE users", "CREATE TABLE z (a)",
		"ALTER TABLE users ADD COLUMN z", "REPLACE INTO kv VALUES ('one', 9)", "VACUUM", "WITH x AS (SELECT 1) INSERT INTO plain SELECT 1, 2 FROM x",
		"CREATE TEMP TABLE z (a)", "ANALYZE", "REINDEX",
	} {
		e := fail(t, file, Request{Op: OpSQL, SQL: q}, "db_read_only")
		if e.Message != "Turn on writes to change data." {
			t.Errorf("%s: message = %q", q, e.Message)
		}
	}
	// A reader can not switch the guard off, however it is spelled.
	for _, q := range []string{"PRAGMA query_only = 0", "pragma QUERY_ONLY=off", "PRAGMA main.query_only(false)", "SELECT 1; PRAGMA query_only = 0"} {
		fail(t, file, Request{Op: OpSQL, SQL: q}, "statement_not_allowed")
	}
	fail(t, file, Request{Op: OpSQL, SQL: "PRAGMA journal_mode = delete"}, "statement_not_allowed")
	// Even a transaction can not write.
	fail(t, file, Request{Op: OpSQL, SQL: "BEGIN; INSERT INTO plain VALUES (1,2); COMMIT"}, "db_read_only")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 0 {
		t.Error("a read-only request wrote")
	}
	// The error of the third statement says which one and what came before.
	e := fail(t, file, Request{Op: OpSQL, SQL: "SELECT 1; SELECT 2; SELECT * FROM nope; SELECT 4"}, "sql_error")
	if e.Message != "no such table: nope" {
		t.Errorf("message = %q", e.Message)
	}
	if fmt.Sprint(e.Details["statement"]) != "3" {
		t.Errorf("details = %v", e.Details)
	}
	if prior, ok := e.Details["results"].([]StatementResult); !ok || len(prior) != 2 {
		t.Errorf("results = %#v", e.Details["results"])
	}
}

func TestConsoleRefusesFileAccess(t *testing.T) {
	file := newDB(t)
	other := filepath.Join(t.TempDir(), "other.db")
	for _, write := range []bool{false, true} {
		for _, q := range []string{
			"ATTACH DATABASE '" + other + "' AS o", "attach '" + other + "' as o", "ATTACH ':memory:' AS m", "DETACH DATABASE o",
			"SELECT load_extension('/lib/x.so')", "VACUUM INTO '" + other + "'", "vacuum into '" + other + "'",
			"PRAGMA writable_schema = 1", "PRAGMA locking_mode = EXCLUSIVE", "PRAGMA temp_store_directory = '/tmp'", "PRAGMA schema_version = 5",
		} {
			resp := Run(context.Background(), file, Request{Op: OpSQL, SQL: q, Write: write})
			if resp.Error == nil || (resp.Error.Code != "statement_not_allowed" && !(strings.Contains(q, "load_extension") && resp.Error.Code == "sql_error")) {
				t.Errorf("write=%v %q: %+v %s", write, q, resp.Error, resp.Result)
			}
		}
	}
	if _, err := os.Stat(other); err == nil {
		t.Error("a file was written outside the database")
	}
	// The refusal stops the text before the first statement runs.
	fail(t, file, Request{Op: OpSQL, Write: true, SQL: "INSERT INTO plain VALUES (1,1); VACUUM INTO '" + other + "'"}, "statement_not_allowed")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 0 {
		t.Error("the first statement ran")
	}
}

func TestConsoleWrite(t *testing.T) {
	file := newDB(t)
	r := do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: `
		INSERT INTO plain VALUES (1, 'a'), (2, 'b');
		UPDATE plain SET b = 'z' WHERE a > 5;
		DELETE FROM plain WHERE a = 1;
		CREATE TABLE made (x);
		INSERT INTO made VALUES (1) RETURNING x;
		SELECT * FROM plain`})
	if len(r.Results) != 6 {
		t.Fatalf("results = %d", len(r.Results))
	}
	ins, upd, del, cr, ret, sel := r.Results[0], r.Results[1], r.Results[2], r.Results[3], r.Results[4], r.Results[5]
	if ins.RowsAffected == nil || *ins.RowsAffected != 2 || ins.LastInsertRowid == nil || *ins.LastInsertRowid != 2 {
		t.Errorf("insert = %+v", ins)
	}
	if upd.RowsAffected == nil || *upd.RowsAffected != 0 || upd.LastInsertRowid != nil {
		t.Errorf("update = %+v", upd)
	}
	if del.RowsAffected == nil || *del.RowsAffected != 1 {
		t.Errorf("delete = %+v", del)
	}
	if cr.RowsAffected != nil || len(cr.Columns) != 0 {
		t.Errorf("create = %+v", cr)
	}
	if ret.RowsAffected == nil || *ret.RowsAffected != 1 || len(ret.Rows) != 1 || fmt.Sprint(ret.Rows[0][0]) != "1" {
		t.Errorf("returning = %+v", ret)
	}
	if len(sel.Rows) != 1 || sel.RowsAffected != nil {
		t.Errorf("select = %+v", sel)
	}
	// Autocommit: a failing statement keeps what the ones before it did.
	fail(t, file, Request{Op: OpSQL, Write: true, SQL: "INSERT INTO plain VALUES (3, 'c'); INSERT INTO users (email) VALUES ('a@x.nl')"}, "sql_error")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 2 {
		t.Errorf("plain has %d rows, want 2", len(r.Rows))
	}
	// An explicit transaction is rolled back as a whole.
	fail(t, file, Request{Op: OpSQL, Write: true, SQL: "BEGIN; INSERT INTO plain VALUES (4, 'd'); INSERT INTO users (email) VALUES ('a@x.nl'); COMMIT"}, "sql_error")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 2 {
		t.Errorf("plain has %d rows after a failed transaction, want 2", len(r.Rows))
	}
	r = do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: "BEGIN; INSERT INTO plain VALUES (4, 'd'); COMMIT"})
	if len(r.Results) != 3 {
		t.Errorf("transaction results = %d", len(r.Results))
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 3 {
		t.Errorf("plain has %d rows, want 3", len(r.Rows))
	}
	// A plain VACUUM and a trigger with semicolons in its body.
	do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: `VACUUM;
		CREATE TRIGGER t1 AFTER INSERT ON plain BEGIN
			INSERT INTO made VALUES (CASE WHEN new.a > 1 THEN 1 ELSE 2 END);
			UPDATE made SET x = x + 1;
		END;
		INSERT INTO plain VALUES (9, 'trigger')`})
	m := do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT x FROM made ORDER BY x"})
	if len(m.Results[0].Rows) != 2 {
		t.Errorf("trigger did not run: %v", m.Results[0].Rows)
	}
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{" ;; -- c\n /* x */ ", nil},
		{"SELECT 1", []string{"SELECT 1"}},
		{"SELECT ';' ; SELECT \"a;b\", 'it''s;'; SELECT [x;y]", []string{"SELECT ';'", `SELECT "a;b", 'it''s;'`, "SELECT [x;y]"}},
		{"SELECT 1 /* ; */ ; -- ; \n SELECT 2 -- tail", []string{"SELECT 1", "SELECT 2"}},
		{"CREATE TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; SELECT CASE WHEN 1 THEN 2 END; END; SELECT 3",
			[]string{"CREATE TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; SELECT CASE WHEN 1 THEN 2 END; END", "SELECT 3"}},
		{"CREATE TEMP TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; END;SELECT 2", []string{"CREATE TEMP TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; END", "SELECT 2"}},
		{"DROP TRIGGER x; SELECT 2", []string{"DROP TRIGGER x", "SELECT 2"}},
		{"BEGIN; SELECT 1; END", []string{"BEGIN", "SELECT 1", "END"}},
		{"SELECT 'unterminated; SELECT 2", []string{"SELECT 'unterminated; SELECT 2"}},
	}
	for _, c := range cases {
		var got []string
		for _, s := range splitStatements(c.in) {
			got = append(got, s.sql)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("split %q = %q, want %q", c.in, got, c.want)
		}
	}
	kinds := map[string]string{
		"select 1": "SELECT", "  insert into t values (1)": "INSERT", "REPLACE INTO t VALUES (1)": "INSERT",
		"WITH x AS (SELECT 1) SELECT * FROM x": "SELECT", "WITH x(a) AS (SELECT 1) DELETE FROM t WHERE a IN (SELECT a FROM x)": "DELETE",
		"/* c */ UPDATE t SET a = 1": "UPDATE", "PRAGMA x": "PRAGMA", "EXPLAIN SELECT 1": "EXPLAIN",
	}
	for q, want := range kinds {
		if s := splitStatements(q); len(s) != 1 || s[0].kind != want {
			t.Errorf("kind of %q = %v, want %s", q, s, want)
		}
	}
	if got := strings.Join(Kinds("SELECT 1; update t set a=1; DELETE FROM t"), ","); got != "select,update,delete" {
		t.Errorf("Kinds = %s", got)
	}
}

func TestDecodeValue(t *testing.T) {
	ok := map[string]any{
		`null`: nil, ``: nil, `1`: int64(1), `-7`: int64(-7), `1.5`: 1.5, `1e3`: 1000.0, `3.0`: 3.0, `"x"`: "x", `true`: int64(1), `false`: int64(0),
		`{"int":"9007199254740993"}`: int64(9007199254740993), `{"real":"Inf"}`: nil, `{"real":"2.5"}`: 2.5, `{"text":"hi"}`: "hi",
		`{"blob":"AQI="}`: []byte{1, 2}, `{"blob":"AQI"}`: []byte{1, 2}, `{"blob":""}`: []byte{},
	}
	for in, want := range ok {
		got, e := decodeValue("v", raw(in))
		if e != nil {
			t.Errorf("%s: %v", in, e)
			continue
		}
		if in == `{"real":"Inf"}` {
			if f, _ := got.(float64); f < 1e308 {
				t.Errorf("Inf = %v", got)
			}
			continue
		}
		if fmt.Sprintf("%T %v", got, got) != fmt.Sprintf("%T %v", want, want) {
			t.Errorf("%s = %T %v, want %T %v", in, got, got, want, want)
		}
	}
	for _, in := range []string{`{}`, `[]`, `{"int":1}`, `{"int":"1","text":"1"}`, `{"real":"NaN"}`, `{"real":"x"}`, `1e999`, `99999999999999999999`, `abc`, `"x`, `{"blob":"%%"}`} {
		if _, e := decodeValue("v", raw(in)); e == nil || e.Code != "value_invalid" {
			t.Errorf("%s: %v", in, e)
		}
	}
	for _, in := range []string{`{"text":"x","truncated":true}`, `{"blob":"AA==","size":100,"truncated":true}`} {
		if _, e := decodeValue("v", raw(in)); e == nil || e.Code != "value_truncated" {
			t.Errorf("%s: %v", in, e)
		}
	}
	// What is sent back is what was read, also a cut value that is not truncated.
	if v, e := decodeValue("v", raw(`{"blob":"AQI=","size":2,"truncated":false}`)); e != nil || string(v.([]byte)) != "\x01\x02" {
		t.Errorf("round trip blob: %v %v", v, e)
	}
}

func TestEncodeValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, `null`}, {int64(5), `5`}, {int64(1 << 53), `9007199254740992`}, {int64(1<<53 + 1), `{"int":"9007199254740993"}`},
		{int64(-1<<53 - 1), `{"int":"-9007199254740993"}`}, {1.5, `1.5`}, {"é", `"é"`}, {true, `1`},
		{[]byte{}, `{"blob":"","size":0,"truncated":false}`},
	}
	for _, c := range cases {
		v, _ := encodeValue(c.in)
		b, _ := encode(v)
		if string(b) != c.want {
			t.Errorf("encode %v = %s, want %s", c.in, b, c.want)
		}
	}
}

func TestWALDatabase(t *testing.T) {
	file := newDB(t, `PRAGMA journal_mode = wal`)
	// The app: a connection that stays open and holds a write lock for a moment.
	app, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := app.Exec(`INSERT INTO plain VALUES (1, 'app')`); err != nil {
		t.Fatal(err)
	}
	info := do[Info](t, file, Request{Op: OpInfo, Path: "app.db"})
	if info.JournalMode != "wal" {
		t.Errorf("journal mode = %s", info.JournalMode)
	}
	// Reads see what the app wrote (it is still in the -wal file), writes go through.
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 1 {
		t.Errorf("rows = %d", len(r.Rows))
	}
	do[InsertResult](t, file, Request{Op: OpInsert, Table: "plain", Values: vals("a", "2")})
	var n int
	if err := app.QueryRow(`SELECT count(*) FROM plain`).Scan(&n); err != nil || n != 2 {
		t.Errorf("the app sees %d rows (%v)", n, err)
	}
	// A reader leaves the folder as it was: no -journal left behind.
	if _, err := os.Stat(file + "-journal"); err == nil {
		t.Error("journal file left behind")
	}
}

func TestBusy(t *testing.T) {
	file := newDB(t)
	old := BusyTimeout
	BusyTimeout = 200 * time.Millisecond
	defer func() { BusyTimeout = old }()
	app, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.SetMaxOpenConns(1)
	if _, err := app.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e := fail(t, file, Request{Op: OpInsert, Table: "plain", Values: vals("a", "1")}, "db_busy")
	if time.Since(start) < 150*time.Millisecond || !strings.Contains(e.Message, "in use") {
		t.Errorf("busy after %v: %q", time.Since(start), e.Message)
	}
	fail(t, file, Request{Op: OpSQL, Write: true, SQL: "DELETE FROM plain"}, "db_busy")
	if _, err := app.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	do[InsertResult](t, file, Request{Op: OpInsert, Table: "plain", Values: vals("a", "1")})
}

func TestTimeout(t *testing.T) {
	file := newDB(t)
	old := QueryTimeout
	QueryTimeout = 300 * time.Millisecond
	defer func() { QueryTimeout = old }()
	start := time.Now()
	e := fail(t, file, Request{Op: OpSQL, SQL: "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT count(*) FROM n"}, "db_timeout")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to stop", d)
	}
	if !strings.Contains(e.Message, "too long") {
		t.Errorf("message = %q", e.Message)
	}
}

func TestCountTimeoutIsNotAnError(t *testing.T) {
	file := newDB(t)
	old := CountTimeout
	CountTimeout = time.Nanosecond
	defer func() { CountTimeout = old }()
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users"})
	if len(r.Rows) != 3 || r.Total != nil {
		t.Errorf("rows %d total %v", len(r.Rows), r.Total)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"total":null`) {
		t.Errorf("json = %s", b)
	}
	info := do[Info](t, file, Request{Op: OpInfo})
	for _, tb := range info.Tables {
		if tb.Rows != nil {
			t.Errorf("%s: rows = %d, want null", tb.Name, *tb.Rows)
		}
	}
}

func TestResultTooLarge(t *testing.T) {
	file := newDB(t)
	big := strings.Repeat("x", 60000)
	for i := 0; i < 200; i++ {
		if _, err := insertRaw(file, `INSERT INTO plain VALUES (?, ?)`, i, big); err != nil {
			t.Fatal(err)
		}
	}
	// 200 rows of 60 KB: a page of them does not fit in one response.
	fail(t, file, Request{Op: OpRows, Table: "plain", Limit: 200}, "result_too_large")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain", Limit: 20}); len(r.Rows) != 20 {
		t.Errorf("rows = %d", len(r.Rows))
	}
	// The console cuts the rows instead.
	r := do[SQLResult](t, file, Request{Op: OpSQL, SQL: "SELECT b FROM plain"})
	if x := r.Results[0]; !x.Truncated || len(x.Rows) == 0 || len(x.Rows) >= 200 {
		t.Errorf("console rows = %d truncated %v", len(x.Rows), x.Truncated)
	}
}

func TestHandle(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := newDB(t)
	data, _ := os.ReadFile(src)
	if err := os.WriteFile(filepath.Join(root, "db", "main.sqlite"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("db", filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("db/main.sqlite", filepath.Join(root, "link.db")); err != nil {
		t.Fatal(err)
	}
	h := func(path string) *Error {
		b, _ := json.Marshal(Request{Op: OpInfo, Path: path})
		return Handle(context.Background(), b, HelperOptions{Root: root}).Error
	}
	b, _ := json.Marshal(Request{Op: OpInfo, Path: "/db/main.sqlite"})
	if resp := Handle(context.Background(), b, HelperOptions{Root: root}); resp.Error != nil || resp.Result == nil {
		t.Errorf("good path: %+v", resp.Error)
	}
	for path, code := range map[string]string{
		"nope.db": "db_not_found", "db/nope.db": "db_not_found", "notes.txt/x": "db_not_found", "notes.txt": "not_sqlite", "db": "not_a_file",
		"linkdir/main.sqlite": "file_symlink", "link.db": "file_symlink", "": "path_invalid",
	} {
		if e := h(path); e == nil || e.Code != code {
			t.Errorf("%q: %+v, want %s", path, e, code)
		}
	}
	// ".." is cleaned away like a leading slash: it can not leave the volume.
	if e := h("../../etc/passwd"); e == nil || e.Code != "db_not_found" {
		t.Errorf("..: %+v", e)
	}
	if resp := Handle(context.Background(), []byte("{nope"), HelperOptions{Root: root}); resp.Error == nil || resp.Error.Code != "request_invalid" {
		t.Errorf("bad json: %+v", resp)
	}
	// The user to become is the owner of the file.
	var gotUID uint32 = 12345
	st, _ := os.Stat(filepath.Join(root, "db", "main.sqlite"))
	uid, _ := ownerOf(st)
	Handle(context.Background(), b, HelperOptions{Root: root, Drop: func(u, g uint32) error { gotUID = u; return nil }})
	if gotUID != uid {
		t.Errorf("dropped to %d, want %d", gotUID, uid)
	}
	if resp := Handle(context.Background(), b, HelperOptions{Root: root, Drop: func(u, g uint32) error { return fmt.Errorf("no") }}); resp.Error == nil || resp.Error.Code != "db_failed" {
		t.Errorf("drop error: %+v", resp)
	}
}

func TestHelperMain(t *testing.T) {
	root := t.TempDir()
	_ = root
	var out strings.Builder
	req := filepath.Join(t.TempDir(), "req.json")
	if err := os.WriteFile(req, []byte(`{"op":"info","path":"missing.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// DataRoot is /data, which does not exist here: a clean answer, not a crash.
	if code := HelperMain([]string{req}, strings.NewReader(""), &out); code != 0 {
		t.Errorf("exit %d", code)
	}
	var resp Response
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil || resp.Error == nil || resp.Error.Code != "db_not_found" {
		t.Errorf("out = %q", out.String())
	}
	out.Reset()
	if code := HelperMain([]string{"-"}, strings.NewReader(`{"op":"info","path":"x"}`), &out); code != 0 || !strings.Contains(out.String(), "db_not_found") {
		t.Errorf("stdin: %d %q", code, out.String())
	}
	if code := HelperMain(nil, nil, &out); code != 2 {
		t.Errorf("no args: %d", code)
	}
	if code := HelperMain([]string{filepath.Join(t.TempDir(), "none")}, nil, &out); code != 2 {
		t.Errorf("missing request file: %d", code)
	}
}

func TestPathWithSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "my db #1 ?x%41.sqlite")
	db, err := sql.Open("sqlite", "file:"+dsnPath.Replace(file))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (a); INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "t"}); len(r.Rows) != 1 {
		t.Errorf("rows = %+v", r)
	}
}

func TestCell(t *testing.T) {
	long := strings.Repeat("é", 100000) // 200 KB
	file := newDB(t, `INSERT INTO kv VALUES ('long', '`+long+`')`, `INSERT INTO users (id, email, data) VALUES (9, 'n@x.nl', zeroblob(10000))`, `INSERT INTO kv VALUES ('big', 9007199254740993)`)
	type cellOut struct {
		Value any `json:"value"`
	}
	cellOf := func(table, col string, key map[string]json.RawMessage) any {
		return do[cellOut](t, file, Request{Op: OpCell, Table: table, Column: col, Key: key}).Value
	}
	row := func(k string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"k": json.RawMessage(`"` + k + `"`)}
	}
	id := func(n string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"rowid": json.RawMessage(n)}
	}

	// The grid cuts a text at 64 KiB, the cell gives it all (a WITHOUT ROWID key too).
	if v := cellOf("kv", "v", row("long")); v != long {
		t.Errorf("long text: %d bytes", len(fmt.Sprint(v)))
	}
	if v := cellOf("kv", "v", row("one")); v.(json.Number).String() != "1" {
		t.Errorf("integer cell: %v", v)
	}
	if v := cellOf("kv", "v", row("two")); v.(json.Number).String() != "2.5" {
		t.Errorf("real cell: %v", v)
	}
	if v := cellOf("kv", "v", row("big")); fmt.Sprint(v) != "map[int:9007199254740993]" {
		t.Errorf("big integer cell: %v", v)
	}
	// A date column comes back as stored.
	if v := cellOf("users", "created", id("3")); v != "2024-03-07T01:02:03Z" {
		t.Errorf("date cell: %v", v)
	}
	if v := cellOf("users", "note", id("3")); v != nil {
		t.Errorf("null cell: %v", v)
	}
	if v := cellOf("users", "DATA", id("3")); fmt.Sprint(v) != "map[blob:AP8Q size:3 truncated:false]" {
		t.Errorf("blob cell: %v", v)
	}
	// A blob is whole up to 4 MiB, where the grid shows 4 KiB.
	blob := cellOf("users", "data", id("9")).(map[string]any)
	if blob["truncated"] != false || len(blob["blob"].(string)) != 13336 {
		t.Errorf("10 KB blob: truncated=%v len=%d", blob["truncated"], len(blob["blob"].(string)))
	}

	fail(t, file, Request{Op: OpCell, Table: "users", Column: "email", Key: id("99")}, "row_not_found")
	fail(t, file, Request{Op: OpCell, Table: "users", Column: "nope", Key: id("1")}, "column_not_found")
	fail(t, file, Request{Op: OpCell, Table: "nope", Column: "x", Key: id("1")}, "table_not_found")
	fail(t, file, Request{Op: OpCell, Table: "users", Column: "email", Key: map[string]json.RawMessage{"id": json.RawMessage("1")}}, "key_invalid")
	fail(t, file, Request{Op: OpCell, Table: "adults", Column: "email", Key: id("1")}, "not_editable")
	fail(t, file, Request{Op: OpCell, Table: "kv", Column: "v", Key: map[string]json.RawMessage{"k": json.RawMessage("null")}}, "key_invalid")
}

// Text and blobs of 4 MiB come back whole, bigger ones in the cut-off form.
func TestCellLimits(t *testing.T) {
	file := newDB(t)
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, c := range []struct {
		n int
		v string
	}{{MaxCellBytes, "a"}, {MaxCellBytes + 1, "a"}} {
		if _, err := db.Exec(`INSERT INTO users (email, note, data) VALUES (?, ?, zeroblob(?))`, fmt.Sprint("x", c.n), strings.Repeat(c.v, c.n), c.n); err != nil {
			t.Fatal(err)
		}
	}
	type cellOut struct {
		Value any `json:"value"`
	}
	get := func(email, col string) any {
		var id int64
		if err := db.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return do[cellOut](t, file, Request{Op: OpCell, Table: "users", Column: col, Key: map[string]json.RawMessage{"rowid": json.RawMessage(fmt.Sprint(id))}}).Value
	}
	if s, ok := get(fmt.Sprint("x", MaxCellBytes), "note").(string); !ok || len(s) != MaxCellBytes {
		t.Errorf("4 MiB text is not whole: %T", s)
	}
	cut := get(fmt.Sprint("x", MaxCellBytes+1), "note").(map[string]any)
	if cut["truncated"] != true || len(cut["text"].(string)) != MaxTextBytes {
		t.Errorf("over 4 MiB text: %v", cut["truncated"])
	}
	b := get(fmt.Sprint("x", MaxCellBytes), "data").(map[string]any)
	if b["truncated"] != false || fmt.Sprint(b["size"]) != fmt.Sprint(MaxCellBytes) {
		t.Errorf("4 MiB blob: truncated=%v size=%v", b["truncated"], b["size"])
	}
	b = get(fmt.Sprint("x", MaxCellBytes+1), "data").(map[string]any)
	if b["truncated"] != true || len(b["blob"].(string)) != 5464 {
		t.Errorf("over 4 MiB blob: truncated=%v len=%d", b["truncated"], len(b["blob"].(string)))
	}
}

// A text of 4 MiB can be saved by update, and read back.
func TestUpdateLongText(t *testing.T) {
	file := newDB(t)
	long := strings.Repeat("ab\n", MaxCellBytes/3)
	raw, _ := json.Marshal(long)
	do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: map[string]json.RawMessage{"rowid": json.RawMessage("1")}, Values: map[string]json.RawMessage{"note": raw}})
	var got struct {
		Value any `json:"value"`
	}
	got = do[struct {
		Value any `json:"value"`
	}](t, file, Request{Op: OpCell, Table: "users", Column: "note", Key: map[string]json.RawMessage{"rowid": json.RawMessage("1")}})
	if got.Value != long {
		t.Error("the saved text is not what was sent")
	}
}
