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
		// PostgreSQL nests block comments: the quote is inside the comment.
		{"SELECT 1 /* /* */ ' */; COMMIT; DELETE FROM users; SELECT ''", false, []string{"SELECT", "COMMIT", "DELETE", "SELECT"}},
		{"SELECT 1 /* a /* b /* c */ */ ; */; DROP TABLE t", false, []string{"SELECT", "DROP"}},
		{"/* /* */ SELECT 1", false, nil},
		// MySQL does not nest them.
		{"SELECT 1 /* /* */; DROP TABLE t", true, []string{"SELECT", "DROP"}},
		// MySQL: -- starts a comment only before whitespace.
		{"SELECT 5 --1; DROP TABLE t", true, []string{"SELECT", "DROP"}},
		{"SELECT 5 -- 1; DROP TABLE t", true, []string{"SELECT"}},
		{"SELECT 5 --\tx; DROP TABLE t", true, []string{"SELECT"}},
		{"SELECT 5 --1; DROP TABLE t", false, []string{"SELECT"}},
		// MySQL runs the text of /*! */ comments.
		{"/*! DROP TABLE t */", true, []string{"DROP"}},
		{"/*!50000 DROP TABLE t */; SELECT 1", true, []string{"DROP", "SELECT"}},
		{"/*M!100000 DELETE FROM t */", true, []string{"DELETE"}},
		{"SELECT 1 /*! , ';' */; SELECT 2", true, []string{"SELECT", "SELECT"}},
		{"SELECT 1 /*+ MAX_EXECUTION_TIME(5) */ FROM t", true, []string{"SELECT"}},
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
	for _, sql := range []string{
		"SELECT * FROM t INTO OUTFILE '/tmp/x'",
		"SELECT 1 --1 INTO OUTFILE '/tmp/x'",
		"SELECT 1 /*! INTO OUTFILE '/tmp/x' */",
		"SELECT 1 /*!50000 INTO DUMPFILE '/tmp/x' */",
		"SELECT 1 /*M! INTO OUTFILE '/tmp/x' */",
		"SELECT 1 INTO @v",
		"TABLE t INTO OUTFILE '/tmp/x'",
	} {
		if st := splitServer(sql, true); len(st) != 1 || !st[0].writesInto() {
			t.Errorf("%q: INTO not seen: %+v", sql, st)
		}
	}
	for _, sql := range []string{"SELECT 'INTO OUTFILE' FROM t", "SELECT `into` FROM t", "SELECT 1 -- INTO OUTFILE '/tmp/x'", "SELECT 1 /* INTO OUTFILE */"} {
		if st := splitServer(sql, true); len(st) != 1 || st[0].writesInto() {
			t.Errorf("%q counted as INTO: %+v", sql, st)
		}
	}
	if !leftOpen(splitServer("BEGIN; INSERT INTO t VALUES (1)", true)) || leftOpen(splitServer("BEGIN; INSERT INTO t VALUES (1); COMMIT", true)) {
		t.Error("leftOpen")
	}
}

func TestSplitterSQLMode(t *testing.T) {
	cases := []struct {
		in, want string
		changed  bool
	}{
		{"STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION", "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION", false},
		{"", "", false},
		{"ANSI_QUOTES,STRICT_ALL_TABLES", "STRICT_ALL_TABLES", true},
		{"REAL_AS_FLOAT,PIPES_AS_CONCAT,ANSI_QUOTES,IGNORE_SPACE,ONLY_FULL_GROUP_BY,ANSI", "REAL_AS_FLOAT,PIPES_AS_CONCAT,IGNORE_SPACE,ONLY_FULL_GROUP_BY", true},
		{"NO_BACKSLASH_ESCAPES", "", true},
		{"ORACLE,PIPES_AS_CONCAT", "PIPES_AS_CONCAT", true},
		{"X'; DROP", "", true},
	}
	for _, tc := range cases {
		got, changed := splitterSQLMode(tc.in)
		if got != tc.want || changed != tc.changed {
			t.Errorf("%q: got %q %v, want %q %v", tc.in, got, changed, tc.want, tc.changed)
		}
	}
}
