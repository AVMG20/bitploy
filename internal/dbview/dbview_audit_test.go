package dbview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMatchUpdate(t *testing.T) {
	file := newDB(t)
	key := vals("rowid", "1")
	// The values the row has: the update goes through.
	up := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: key, Values: vals("note", `"edited"`),
		Match: vals("email", `"a@x.nl"`, "note", `"first"`, "created", `"2024-03-05 10:11:12"`, "data", `null`)})
	if up.Row.Values[4] != "edited" {
		t.Errorf("update = %+v", up)
	}
	// The row changed since it was loaded: row_changed, nothing is written.
	e := fail(t, file, Request{Op: OpUpdate, Table: "users", Key: key, Values: vals("age", `99`), Match: vals("note", `"first"`)}, "row_changed")
	if e.Message != "This row changed since you loaded it." || e.Hint != "Reload the rows and try again." {
		t.Errorf("row_changed = %+v", e)
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "id", Op: "=", Value: raw("1")}}}); fmt.Sprint(r.Rows[0].Values[2]) != "30" {
		t.Errorf("a stale update wrote: %v", r.Rows[0].Values)
	}
	// NULL matches NULL, and not a value.
	do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "3"), Values: vals("age", `1`), Match: vals("note", `null`)})
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "1"), Values: vals("age", `1`), Match: vals("note", `null`)}, "row_changed")
	// The key is gone: row_not_found, with or without values.
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "77"), Values: vals("age", `1`), Match: vals("note", `"x"`)}, "row_not_found")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: key, Values: vals("age", `1`), Match: vals("nope", `1`)}, "column_not_found")
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: key, Values: vals("age", `1`), Match: vals("note", `{"text":"x","truncated":true}`)}, "value_truncated")
	// A rowid reused by another row is caught by the values.
	if _, err := insertRaw(file, `DELETE FROM users WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := insertRaw(file, `INSERT INTO users (id, email, age) VALUES (2, 'new@x.nl', 50)`); err != nil {
		t.Fatal(err)
	}
	fail(t, file, Request{Op: OpUpdate, Table: "users", Key: vals("rowid", "2"), Values: vals("age", `1`), Match: vals("email", `"b@x.nl"`)}, "row_changed")
	// WITHOUT ROWID.
	do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "kv", Key: vals("k", `"one"`), Values: vals("v", `5`), Match: vals("v", `1`)})
	fail(t, file, Request{Op: OpUpdate, Table: "kv", Key: vals("k", `"one"`), Values: vals("v", `6`), Match: vals("v", `1`)}, "row_changed")
}

func TestMatchDelete(t *testing.T) {
	file := newDB(t)
	k := func(n string) map[string]json.RawMessage { return vals("rowid", n) }
	// One stale row stops the whole delete.
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("1"), k("2")},
		Matches: []map[string]json.RawMessage{vals("email", `"a@x.nl"`), vals("email", `"changed@x.nl"`)}}, "row_changed")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users"}); len(r.Rows) != 3 {
		t.Errorf("rows = %d, the delete was not all or nothing", len(r.Rows))
	}
	// A key that is gone is skipped; the rest goes.
	d := do[DeleteResult](t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("1"), k("99")},
		Matches: []map[string]json.RawMessage{vals("email", `"a@x.nl"`), vals("email", `"x"`)}})
	if d.Deleted != 1 {
		t.Errorf("deleted = %d", d.Deleted)
	}
	// An empty set matches anything; the lengths must agree.
	d = do[DeleteResult](t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("2")}, Matches: []map[string]json.RawMessage{{}}})
	if d.Deleted != 1 {
		t.Errorf("deleted = %d", d.Deleted)
	}
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("3")}, Matches: []map[string]json.RawMessage{{}, {}}}, "key_invalid")
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("3")}, Matches: []map[string]json.RawMessage{vals("nope", `1`)}}, "column_not_found")
	// Date columns are compared as the stored text.
	fail(t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("3")}, Matches: []map[string]json.RawMessage{vals("created", `"2024-03-07 01:02:03"`)}}, "row_changed")
	d = do[DeleteResult](t, file, Request{Op: OpDelete, Table: "users", Keys: []map[string]json.RawMessage{k("3")}, Matches: []map[string]json.RawMessage{vals("created", `"2024-03-07T01:02:03Z"`)}})
	if d.Deleted != 1 {
		t.Errorf("deleted = %d", d.Deleted)
	}
}

func TestDateKeyWithoutRowid(t *testing.T) {
	file := newDB(t, `CREATE TABLE dk (d DATETIME PRIMARY KEY, v) WITHOUT ROWID`, `INSERT INTO dk VALUES ('2024-01-02 03:04:05', 'a')`)
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "dk"})
	if r.Rows[0].Key["d"] != "2024-01-02 03:04:05" {
		t.Fatalf("key = %v", r.Rows[0].Key)
	}
	ins := do[InsertResult](t, file, Request{Op: OpInsert, Table: "dk", Values: vals("d", `"2025-05-06T07:08:09Z"`, "v", `"b"`)})
	if ins.Key["d"] != "2025-05-06T07:08:09Z" || ins.Row.Values[1] != "b" {
		t.Errorf("insert = %+v", ins)
	}
	up := do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "dk", Key: vals("d", `"2024-01-02 03:04:05"`), Values: vals("v", `"c"`)})
	if up.Row.Values[1] != "c" {
		t.Errorf("update = %+v", up)
	}
	up = do[UpdateResult](t, file, Request{Op: OpUpdate, Table: "dk", Key: vals("d", `"2024-01-02 03:04:05"`), Values: vals("d", `"2024-02-02"`)})
	if up.Row.Key["d"] != "2024-02-02" {
		t.Errorf("moved = %+v", up)
	}
	d := do[DeleteResult](t, file, Request{Op: OpDelete, Table: "dk", Keys: []map[string]json.RawMessage{vals("d", `"2024-02-02"`)}})
	if d.Deleted != 1 {
		t.Errorf("deleted = %d", d.Deleted)
	}
}

func TestTransactionLeftOpen(t *testing.T) {
	file := newDB(t)
	e := fail(t, file, Request{Op: OpSQL, Write: true, SQL: "INSERT INTO plain VALUES (1, 'kept'); BEGIN; INSERT INTO plain VALUES (2, 'lost')"}, "transaction_open")
	if !strings.Contains(e.Message, "did not COMMIT") || !strings.Contains(e.Hint, "COMMIT") {
		t.Errorf("error = %+v", e)
	}
	if r, _ := e.Details["results"].([]StatementResult); len(r) != 3 {
		t.Errorf("details = %#v", e.Details)
	}
	// What came before the BEGIN is saved; what is inside is not.
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"})
	if len(r.Rows) != 1 || r.Rows[0].Values[1] != "kept" {
		t.Errorf("rows = %+v", r.Rows)
	}
	// A savepoint opens one too; a finished one is fine.
	fail(t, file, Request{Op: OpSQL, Write: true, SQL: "SAVEPOINT a; INSERT INTO plain VALUES (3, 'x')"}, "transaction_open")
	do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: "BEGIN; INSERT INTO plain VALUES (4, 'y'); END"})
	do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: "SAVEPOINT a; INSERT INTO plain VALUES (5, 'z'); RELEASE a"})
	do[SQLResult](t, file, Request{Op: OpSQL, Write: true, SQL: "BEGIN; INSERT INTO plain VALUES (6, 'w'); ROLLBACK"})
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "plain"}); len(r.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(r.Rows))
	}
}

func TestJSONSize(t *testing.T) {
	for in, want := range map[string]int{"": 0, "abc": 3, "a\"b": 4, "\x01\x02": 12, "é": 2, "\xff": 6, "\n": 6} {
		if got := jsonSize(in); got != want {
			t.Errorf("jsonSize(%q) = %d, want %d", in, got, want)
		}
	}
	// The real encoding never takes more than the estimate.
	for _, s := range []string{strings.Repeat("\x01", 1000), strings.Repeat("\"\\", 500), "é\xff <>&"} {
		b, _ := encode(s)
		if len(b)-2 > jsonSize(s) {
			t.Errorf("%q takes %d bytes, estimated %d", s, len(b)-2, jsonSize(s))
		}
	}
}

func TestEscapedTextCountsTowardsTheBudget(t *testing.T) {
	file := newDB(t)
	ctl := strings.Repeat("\x01", 60000) // 60 KB as text, 360 KB as JSON
	for i := 0; i < 40; i++ {
		if _, err := insertRaw(file, `INSERT INTO plain VALUES (?, ?)`, i, ctl); err != nil {
			t.Fatal(err)
		}
	}
	// 40 x 360 KB = 14 MB of JSON: the console cuts the rows before the answer is too big.
	resp := Run(context.Background(), file, Request{Op: OpSQL, SQL: "SELECT b FROM plain"})
	if resp.Error != nil {
		t.Fatalf("console: %+v", resp.Error)
	}
	if len(resp.Result) > MaxResponse {
		t.Errorf("answer is %d bytes", len(resp.Result))
	}
	var r SQLResult
	if err := Decode(resp.Result, &r); err != nil || !r.Results[0].Truncated || len(r.Results[0].Rows) >= 40 {
		t.Errorf("rows %d truncated %v err %v", len(r.Results[0].Rows), r.Results[0].Truncated, err)
	}
	// A page of rows is refused instead.
	fail(t, file, Request{Op: OpRows, Table: "plain", Limit: 40}, "result_too_large")
}

func TestWriteResultTooLargeSaysSaved(t *testing.T) {
	file := newDB(t, `CREATE TABLE tri (id INTEGER PRIMARY KEY, a, b, c)`)
	old := MaxResponse
	MaxResponse = 100 << 10
	defer func() { MaxResponse = old }()
	big := string(mustJSON(strings.Repeat("x", 60000)))
	// Three columns of 60 KB come back in the row: more than may be sent. The row is in.
	e := fail(t, file, Request{Op: OpInsert, Table: "tri", Values: vals("a", big, "b", big, "c", big)}, "result_too_large")
	if !strings.Contains(e.Message, "saved") || !strings.Contains(e.Hint, "Do not repeat") {
		t.Errorf("error = %+v", e)
	}
	var n int
	db := mustOpen(t, file)
	defer db.Close()
	if err := db.QueryRow(`SELECT count(*) FROM tri`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows = %d %v", n, err)
	}
	// A read says it plainly.
	e = fail(t, file, Request{Op: OpRows, Table: "tri"}, "result_too_large")
	if strings.Contains(e.Message, "saved") {
		t.Errorf("read error = %+v", e)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func mustOpen(t *testing.T, file string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestUpdateMany(t *testing.T) {
	file := newDB(t)
	ks := func(ns ...string) []map[string]json.RawMessage {
		var out []map[string]json.RawMessage
		for _, n := range ns {
			out = append(out, vals("rowid", n))
		}
		return out
	}
	u := do[UpdateManyResult](t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1", "2", "3"), Values: vals("age", `18`, "note", `"bulk"`)})
	if u.Updated != 3 {
		t.Errorf("updated = %d", u.Updated)
	}
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users"})
	for _, row := range r.Rows {
		if fmt.Sprint(row.Values[2]) != "18" || row.Values[4] != "bulk" {
			t.Errorf("row = %v", row.Values)
		}
	}
	// Gone keys are skipped.
	u = do[UpdateManyResult](t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1", "99"), Values: vals("age", `20`)})
	if u.Updated != 1 {
		t.Errorf("updated = %d", u.Updated)
	}
	// A row that changed stops everything, and says which.
	e := fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1", "2", "3"), Values: vals("age", `77`),
		Matches: []map[string]json.RawMessage{vals("age", `20`), vals("age", `18`), vals("age", `5`)}}, "row_changed")
	if fmt.Sprint(e.Details["index"]) != "2" {
		t.Errorf("details = %v", e.Details)
	}
	r = do[RowsResult](t, file, Request{Op: OpRows, Table: "users"})
	for _, row := range r.Rows {
		if fmt.Sprint(row.Values[2]) == "77" {
			t.Errorf("not all or nothing: %v", row.Values)
		}
	}
	// With matches that hold, it goes through; a gone key with a match is skipped.
	u = do[UpdateManyResult](t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1", "2", "99"), Values: vals("age", `77`),
		Matches: []map[string]json.RawMessage{vals("age", `20`), vals("age", `18`), vals("age", `1`)}})
	if u.Updated != 2 {
		t.Errorf("updated = %d", u.Updated)
	}
	// An error of SQLite undoes all: the second row breaks a unique index.
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1", "2"), Values: vals("email", `"same@x.nl"`)}, "sql_error")
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "email", Op: "=", Value: raw(`"same@x.nl"`)}}}); len(r.Rows) != 0 {
		t.Error("a failed bulk update wrote")
	}
	// WITHOUT ROWID.
	u = do[UpdateManyResult](t, file, Request{Op: OpUpdateMany, Table: "kv", Keys: []map[string]json.RawMessage{vals("k", `"one"`), vals("k", `"two"`)}, Values: vals("v", `0`),
		Matches: []map[string]json.RawMessage{vals("v", `1`), vals("v", `2.5`)}})
	if u.Updated != 2 {
		t.Errorf("kv updated = %d", u.Updated)
	}
	// The same rules as update.
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1"), Values: vals("nope", `1`)}, "column_not_found")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1")}, "value_invalid")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1"), Values: vals("age", `{"text":"x","truncated":true}`)}, "value_truncated")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Values: vals("age", `1`)}, "key_invalid")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: make([]map[string]json.RawMessage, MaxKeys+1), Values: vals("age", `1`)}, "key_invalid")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1"), Values: vals("age", `1`), Matches: []map[string]json.RawMessage{{}, {}}}, "key_invalid")
	fail(t, file, Request{Op: OpUpdateMany, Table: "users", Keys: ks("1"), Values: vals("age", `1`), Matches: []map[string]json.RawMessage{vals("nope", `1`)}}, "column_not_found")
	fail(t, file, Request{Op: OpUpdateMany, Table: "adults", Keys: ks("1"), Values: vals("age", `1`)}, "not_editable")
	g := newDB(t, `CREATE TABLE g (a INTEGER, b INTEGER AS (a * 2))`, `INSERT INTO g (a) VALUES (1)`)
	fail(t, g, Request{Op: OpUpdateMany, Table: "g", Keys: ks("1"), Values: vals("b", `1`)}, "column_read_only")
}
