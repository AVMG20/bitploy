package dbview

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// The SQL console.

// isDateType: the driver turns a TEXT value of such a declared type into a time (see isDateColumn).
func isDateType(t string) bool {
	switch strings.ToUpper(t) {
	case "DATE", "DATETIME", "TIMESTAMP":
		return true
	}
	return false
}

func (s *session) console(req Request) (any, *Error) {
	if len(req.SQL) > MaxSQLBytes {
		return nil, errf("sql_too_long", fmt.Sprintf("The SQL is longer than %d KB.", MaxSQLBytes>>10),
			"Split it into several requests.")
	}
	stmts := splitStatements(req.SQL)
	if len(stmts) == 0 {
		return nil, errf("sql_empty", "There is no SQL to run.", "Send at least one statement.")
	}
	// Refuse before anything runs: a VACUUM INTO writes a new file, and SQLite
	// does not ask the authorizer about what VACUUM does inside.
	for i, st := range stmts {
		if st.kind == "VACUUM" && st.hasWord("INTO") {
			return nil, withStatement(errf("statement_not_allowed", "VACUUM INTO is not allowed: it writes a new file.",
				"Use a plain VACUUM, or download the database."), i+1, nil)
		}
	}
	start := time.Now()
	b := newBudget()
	res := SQLResult{Results: []StatementResult{}}
	for i, st := range stmts {
		r, e := s.runStatement(st, b)
		if e != nil {
			return nil, withStatement(e, i+1, res.Results)
		}
		res.Results = append(res.Results, r)
	}
	if req.Write {
		if e := s.endOpenTransaction(); e != nil {
			e.Details = map[string]any{"results": res.Results}
			return nil, e
		}
	}
	res.TookMS = since(start)
	return res, nil
}

// endOpenTransaction fails a write request that started a transaction and did
// not end it: closing the connection would roll it back, and the answer would
// say it worked. BEGIN is refused inside a transaction, which tells.
func (s *session) endOpenTransaction() *Error {
	_, err := s.conn.ExecContext(s.ctx, "BEGIN")
	if err == nil {
		_, _ = s.conn.ExecContext(s.ctx, "ROLLBACK") // there was none: undo ours
		return nil
	}
	if !strings.Contains(err.Error(), "within a transaction") {
		return s.fail(err)
	}
	_, _ = s.conn.ExecContext(s.ctx, "ROLLBACK")
	return errf("transaction_open", "The SQL started a transaction but did not COMMIT, so nothing in it was saved.",
		"End it with COMMIT;. Statements before the BEGIN were saved.")
}

func withStatement(e *Error, n int, done []StatementResult) *Error {
	if done == nil {
		done = []StatementResult{}
	}
	e.Details = map[string]any{"statement": n, "results": done}
	return e
}

func (s *session) runStatement(st statement, b *budget) (StatementResult, *Error) {
	start := time.Now()
	r := StatementResult{SQL: st.sql, Columns: []string{}, Rows: [][]any{}}
	// A plain VACUUM attaches a scratch database of its own (an empty file name,
	// so nothing outside this database): that is the one ATTACH let through.
	s.vacuum = st.kind == "VACUUM"
	defer func() { s.vacuum = false }()
	query := st.sql
	// A column declared as a date would come back as a time in some other format
	// than it is stored in. Look at the columns of a SELECT first, without
	// running it (LIMIT 0), and hide the declared type of those columns.
	if st.kind == "SELECT" || st.kind == "VALUES" {
		if names, cts, ok := s.probeColumns(st.sql); ok {
			for _, ct := range cts {
				if isDateType(ct.DatabaseTypeName()) {
					query = hideDateTypes(st.sql, names, cts)
					break
				}
			}
		}
	}
	rs, err := s.conn.QueryContext(s.ctx, query)
	if err != nil {
		return r, s.fail(err)
	}
	defer func() { rs.Close() }()
	cols, err := rs.Columns()
	if err != nil {
		return r, s.fail(err)
	}
	r.Columns = cols
	if r.Columns == nil {
		r.Columns = []string{}
	}
	dml := st.kind == "INSERT" || st.kind == "UPDATE" || st.kind == "DELETE"
	for rs.Next() {
		if len(r.Rows) >= MaxRows || r.Truncated {
			r.Truncated = true
			if dml {
				continue // the change has to run to its end
			}
			break
		}
		vals, err := scanRow(rs, len(cols))
		if err != nil {
			return r, s.fail(err)
		}
		row, size := make([]any, len(vals)), 0
		for i, v := range vals {
			var n int
			row[i], n = encodeValue(v)
			size += n
		}
		if !b.take(size) {
			r.Truncated = true
			continue
		}
		r.Rows = append(r.Rows, row)
	}
	if err := rs.Err(); err != nil {
		return r, s.fail(err)
	}
	rs.Close()
	if dml {
		var n int64
		if err := s.conn.QueryRowContext(s.ctx, "SELECT changes()").Scan(&n); err == nil {
			r.RowsAffected = &n
		}
		if st.kind == "INSERT" {
			var id int64
			if err := s.conn.QueryRowContext(s.ctx, "SELECT last_insert_rowid()").Scan(&id); err == nil {
				r.LastInsertRowid = &id
			}
		}
	}
	r.TookMS = since(start)
	return r, nil
}

// hideDateTypes wraps a SELECT so that the columns declared as a date are
// passed through ifnull(): same value, no declared type for the driver to act
// on. Columns are renamed by position first, as a result may repeat a name.
func hideDateTypes(query string, names []string, cts []*sql.ColumnType) string {
	var cte, out []string
	for i, n := range names {
		c := fmt.Sprintf("c%d", i)
		cte = append(cte, quote(c))
		if isDateType(cts[i].DatabaseTypeName()) {
			out = append(out, "ifnull("+quote(c)+", NULL) AS "+quote(n))
		} else {
			out = append(out, quote(c)+" AS "+quote(n))
		}
	}
	return "WITH \"bp_q\"(" + strings.Join(cte, ", ") + ") AS MATERIALIZED (\n" + query + "\n)\nSELECT " + strings.Join(out, ", ") + " FROM \"bp_q\""
}

// probeColumns gives the names and declared types of the columns a SELECT
// returns, without running it. A name a subquery made unique (id, id:1) is
// turned back into what the statement itself calls it.
func (s *session) probeColumns(query string) ([]string, []*sql.ColumnType, bool) {
	rs, err := s.conn.QueryContext(s.ctx, "SELECT * FROM (\n"+query+"\n) LIMIT 0")
	if err != nil {
		return nil, nil, false
	}
	defer rs.Close()
	names, err1 := rs.Columns()
	cts, err2 := rs.ColumnTypes()
	if err1 != nil || err2 != nil || len(names) != len(cts) {
		return nil, nil, false
	}
	seen := map[string]bool{}
	for i, n := range names {
		if j := strings.LastIndexByte(n, ':'); j > 0 && seen[n[:j]] && isDigits(n[j+1:]) {
			names[i] = n[:j]
		}
		seen[names[i]] = true
	}
	return names, cts, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
