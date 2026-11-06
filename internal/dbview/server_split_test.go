package dbview

import "testing"

func TestSplitServer(t *testing.T) {
	cases := []struct {
		sql   string
		mysql bool
		want  []string // kinds
	}{
		{"SELECT 1; SELECT 2", false, []string{"SELECT", "SELECT"}},
		{"-- only a comment\n/* and another */", false, nil},
		{"SELECT 'a;b'; INSERT INTO t VALUES ('it''s;')", false, []string{"SELECT", "INSERT"}},
		{"CREATE FUNCTION f() RETURNS int AS $$ BEGIN; RETURN 1; END $$ LANGUAGE plpgsql; SELECT f()", false, []string{"CREATE", "SELECT"}},
		{"CREATE FUNCTION f() RETURNS int AS $body$ SELECT 1; $body$ LANGUAGE sql", false, []string{"CREATE"}},
		{"SELECT E'a\\';b'; SELECT 2", false, []string{"SELECT", "SELECT"}},
		{"SELECT $1; SELECT 2", false, []string{"SELECT", "SELECT"}},
		{"# note\nSELECT 'a\\';b'; SHOW TABLES", true, []string{"SELECT", "SHOW"}},
		{"SELECT `a;b` FROM t; DESCRIBE t", true, []string{"SELECT", "DESCRIBE"}},
		{"(SELECT 1) UNION (SELECT 2)", false, []string{"SELECT"}},
	}
	for _, tc := range cases {
		got := splitServer(tc.sql, tc.mysql)
		if len(got) != len(tc.want) {
			t.Errorf("%q: %d statements %+v, want %v", tc.sql, len(got), got, tc.want)
			continue
		}
		for i, st := range got {
			if st.kind != tc.want[i] {
				t.Errorf("%q: statement %d is %s, want %s", tc.sql, i+1, st.kind, tc.want[i])
			}
		}
	}
	if st := splitServer("SELECT * FROM t INTO OUTFILE '/tmp/x'", true); !st[0].writesFile() {
		t.Error("INTO OUTFILE not seen")
	}
	if st := splitServer("SELECT 'INTO OUTFILE' FROM t", true); st[0].writesFile() {
		t.Error("a string counted as INTO OUTFILE")
	}
	if !leftOpen(splitServer("BEGIN; INSERT INTO t VALUES (1)", true)) || leftOpen(splitServer("BEGIN; INSERT INTO t VALUES (1); COMMIT", true)) {
		t.Error("leftOpen")
	}
}
