package dbview

import (
	"fmt"
	"strings"
	"testing"
)

func searchDB(t *testing.T) string {
	return newDB(t,
		`CREATE TABLE s (id INTEGER PRIMARY KEY, name TEXT, qty INT, price REAL, tag, born DATETIME, pic BLOB, big INTEGER)`,
		`INSERT INTO s VALUES (12, 'Alice', 5, 1.5, 'x12', '2024-03-05 10:11:12', x'41424344', 9007199254740993)`,
		`INSERT INTO s VALUES (120, '100% sure', 7, 12.25, 'a_b', '2021-01-01', NULL, 1)`,
		`INSERT INTO s VALUES (1234, 'under_score', 3, -3.5, NULL, NULL, 'ABCD', 2)`,
		`INSERT INTO s VALUES (7, 'back\slash', 12, 0.5, 'plain', '1999-12-31', NULL, 3)`,
		`CREATE TABLE nonum (a TEXT, b BLOB)`,
		`INSERT INTO nonum VALUES ('hello', x'68656c6c6f')`,
		`CREATE VIEW sv AS SELECT id, name, qty FROM s`,
	)
}

func ids(t *testing.T, file string, req Request) string {
	t.Helper()
	req.Op, req.OrderBy = OpRows, "id"
	if req.Table == "" {
		req.Table = "s"
	}
	r := do[RowsResult](t, file, req)
	var out []string
	for _, row := range r.Rows {
		out = append(out, fmt.Sprint(row.Values[0]))
	}
	if r.Total == nil || int(*r.Total) != len(r.Rows) {
		t.Errorf("total = %v for %d rows", r.Total, len(r.Rows))
	}
	return strings.Join(out, ",")
}

func TestSearch(t *testing.T) {
	file := searchDB(t)
	s := func(term string) string { return ids(t, file, Request{Search: term}) }

	// A number: number columns start with it, text columns contain it.
	// 12: id 12 and 120 (start), qty 12 (id 7), name 'Alice'? no; tag 'x12' contains (id 12); price 12.25 (id 120); born '...10:11:12' contains (id 12).
	if got := s("12"); got != "7,12,120,1234" {
		t.Errorf(`"12" = %s`, got)
	}
	// A number is not found by its end (1234 has no 4 at the start), but a date contains it.
	if got := s("4"); got != "12" {
		t.Errorf(`"4" = %s`, got)
	}
	if got := s("-3"); got != "7,1234" { // price -3.5 starts with -3; the date 1999-12-31 contains it
		t.Errorf(`"-3" = %s`, got)
	}
	if got := s("1.5"); got != "12" {
		t.Errorf(`"1.5" = %s`, got)
	}
	if got := s("+12"); got != "7,12,120,1234" {
		t.Errorf(`"+12" = %s`, got)
	}
	// Text: contains, any case; numbers are skipped for a word.
	if got := s("ALIC"); got != "12" {
		t.Errorf(`"ALIC" = %s`, got)
	}
	if got := s("ce"); got != "12" {
		t.Errorf(`"ce" = %s`, got)
	}
	// % _ and \ mean themselves.
	for term, want := range map[string]string{"%": "120", "100%": "120", "_": "120,1234", "a_b": "120", "r_s": "1234", `\`: "7", `k\s`: "7", "a%b": "", "u_der": ""} {
		if got := s(term); got != want {
			t.Errorf("%q = %q, want %q", term, got, want)
		}
	}
	// Dates are searched as stored; a blob is never searched, in a BLOB column or in another one.
	if got := s("2024-03-05"); got != "12" {
		t.Errorf("date = %s", got)
	}
	if got := s("10:11"); got != "12" {
		t.Errorf("time = %s", got)
	}
	if got := s("ABCD"); got != "1234" { // the text 'ABCD' in the blob column of id 1234 is TEXT, id 12's blob x'41424344' is a blob
		t.Errorf("ABCD = %s", got)
	}
	if got := s("hello"); got != "" {
		t.Errorf("a blob matched in s: %s", got)
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "nonum", Search: "hello"}); len(r.Rows) != 1 {
		t.Errorf("nonum rows = %d (the text in a matches, the blob in b does not add one)", len(r.Rows))
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "nonum", Search: "68656c"}); len(r.Rows) != 0 {
		t.Error("the hex of a blob matched")
	}
	// A big integer is searched exactly.
	if got := s("90071992547409"); got != "12" {
		t.Errorf("big = %s", got)
	}
	if got := s("9007199254740993"); got != "12" {
		t.Errorf("big full = %s", got)
	}
	// Blank means no search; a term with spaces is trimmed.
	if got := s("   "); got != "7,12,120,1234" {
		t.Errorf("blank = %s", got)
	}
	if got := s("  alice  "); got != "12" {
		t.Errorf("trimmed = %s", got)
	}
	// A term that matches nothing is an empty page, not an error.
	if got := s("zzz"); got != "" {
		t.Errorf("zzz = %s", got)
	}
}

func TestSearchWithFiltersAndPaging(t *testing.T) {
	file := searchDB(t)
	got := ids(t, file, Request{Search: "12", Filters: []Filter{{Column: "qty", Op: ">", Value: raw("6")}}})
	if got != "7,120" {
		t.Errorf("search and filter = %s", got)
	}
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "s", Search: "12", OrderBy: "id", Limit: 2, Offset: 1})
	if len(r.Rows) != 2 || *r.Total != 4 {
		t.Errorf("page = %d rows, total %v", len(r.Rows), r.Total)
	}
}

func TestSearchView(t *testing.T) {
	file := searchDB(t)
	if got := ids(t, file, Request{Table: "sv", Search: "12"}); got != "7,12,120,1234" {
		t.Errorf("view number = %s", got)
	}
	if got := ids(t, file, Request{Table: "sv", Search: "alice"}); got != "12" {
		t.Errorf("view text = %s", got)
	}
	// A view has no rowid: nothing breaks, and the column of a view that has no declared type is text.
	file2 := newDB(t, `CREATE VIEW v2 AS SELECT upper(email) AS e, age + 1 AS n FROM users`)
	r := do[RowsResult](t, file2, Request{Op: OpRows, Table: "v2", Search: "B@X"})
	if len(r.Rows) != 1 {
		t.Errorf("expression column rows = %d", len(r.Rows))
	}
}

func TestSearchNoColumnCanMatch(t *testing.T) {
	file := newDB(t, `CREATE TABLE only_num (a INTEGER, b REAL)`, `INSERT INTO only_num VALUES (1, 2.5)`)
	r := do[RowsResult](t, file, Request{Op: OpRows, Table: "only_num", Search: "abc"})
	if len(r.Rows) != 0 || r.Total == nil || *r.Total != 0 {
		t.Errorf("rows = %d total %v", len(r.Rows), r.Total)
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "only_num", Search: "2.5"}); len(r.Rows) != 1 {
		t.Errorf("number rows = %d", len(r.Rows))
	}
	// The rowid is searched as a number too.
	file = newDB(t, `CREATE TABLE r (x TEXT)`, `INSERT INTO r VALUES ('a'), ('b')`)
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "r", Search: "2"}); len(r.Rows) != 1 {
		t.Errorf("rowid search rows = %d", len(r.Rows))
	}
}

func TestSearchTooLongAndInjection(t *testing.T) {
	file := searchDB(t)
	fail(t, file, Request{Op: OpRows, Table: "s", Search: strings.Repeat("a", MaxSearch+1)}, "search_invalid")
	do[RowsResult](t, file, Request{Op: OpRows, Table: "s", Search: strings.Repeat("é", MaxSearch)})
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "s", Search: "'; DROP TABLE s; --"}); len(r.Rows) != 0 {
		t.Error("injection matched")
	}
	if r := do[RowsResult](t, file, Request{Op: OpRows, Table: "s"}); len(r.Rows) != 4 {
		t.Error("the table changed")
	}
}
