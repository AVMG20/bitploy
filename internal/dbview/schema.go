package dbview

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
)

// tableMeta is what is known about a table or view before it is read or changed.
type tableMeta struct {
	Name         string
	View         bool
	Virtual      bool
	WithoutRowid bool
	SQL          string
	Cols         []Column
	alias        string   // _rowid_, rowid or oid: the one no column shadows; "" if there is none
	pk           []string // primary key columns in key order (without rowid)
	readOnly     bool     // the session may not change anything: no row is editable
}

// hasKey: the rows have a key (a rowid or a primary key) that tells them apart.
func (m *tableMeta) hasKey() bool {
	if m.View || m.Virtual {
		return false
	}
	if m.WithoutRowid {
		return len(m.pk) > 0
	}
	return m.alias != ""
}

// editable: rows have a key, so they can be changed one by one.
func (m *tableMeta) editable() bool { return !m.readOnly && m.hasKey() }

// keyColumns are the names in a row's key.
func (m *tableMeta) keyColumns() []string {
	switch {
	case !m.editable():
		return []string{}
	case m.WithoutRowid:
		return m.pk
	}
	return []string{"rowid"}
}

// column finds a column by name (SQLite does not care about case).
func (m *tableMeta) column(name string) (Column, bool) {
	for _, c := range m.Cols {
		if c.Name == name {
			return c, true
		}
	}
	for _, c := range m.Cols {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Column{}, false
}

// visible are the columns a SELECT * gives: all but the hidden ones of a
// virtual table.
func (m *tableMeta) visible() []Column {
	out := make([]Column, 0, len(m.Cols))
	for _, c := range m.Cols {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out
}

// quote makes a name safe to put in SQL. Names are only ever taken from the
// schema, never from the request, but quoting is cheap.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// loadTable reads the schema of one table or view. The name comes from the
// request and is looked up in the schema first.
func (s *session) loadTable(name string) (*tableMeta, *Error) {
	if name == "" || strings.HasPrefix(strings.ToLower(name), "sqlite_") {
		return nil, tableNotFound(name)
	}
	var typ, actual string
	var wr int64
	err := s.conn.QueryRowContext(s.ctx,
		`SELECT name, type, wr FROM pragma_table_list WHERE schema = 'main' AND name = ? COLLATE NOCASE
		 ORDER BY name = ? DESC LIMIT 1`, name, name).Scan(&actual, &typ, &wr)
	if err == sql.ErrNoRows {
		return nil, tableNotFound(name)
	}
	if err != nil {
		return nil, s.fail(err)
	}
	m := &tableMeta{Name: actual, View: typ == "view", Virtual: typ == "virtual", WithoutRowid: wr == 1, readOnly: s.pol != nil && s.pol.ReadOnly}
	var ddl sql.NullString
	if err := s.conn.QueryRowContext(s.ctx, `SELECT sql FROM sqlite_schema WHERE name = ? AND type IN ('table','view')`, actual).Scan(&ddl); err != nil && err != sql.ErrNoRows {
		return nil, s.fail(err)
	}
	m.SQL = ddl.String
	if err := s.loadColumns(m); err != nil {
		return nil, s.fail(err)
	}
	return m, nil
}

func tableNotFound(name string) *Error {
	return errf("table_not_found", "There is no table or view "+quoteMsg(name)+" in this database.", "Reload the database: the table may have been dropped or renamed.")
}

func quoteMsg(s string) string {
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return `"` + s + `"`
}

func (s *session) loadColumns(m *tableMeta) error {
	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?)`, m.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pkCol struct {
		pos  int
		name string
	}
	var pks []pkCol
	for rows.Next() {
		var c Column
		var notnull, pk, hidden int64
		var dflt sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &notnull, &dflt, &pk, &hidden); err != nil {
			return err
		}
		c.NotNull, c.PK = notnull != 0, int(pk)
		c.Hidden, c.Generated = hidden == 1, hidden >= 2
		c.Redacted = s.pol.Redacted(m.Name, c.Name)
		if dflt.Valid {
			d := dflt.String
			c.Default = &d
		}
		m.Cols = append(m.Cols, c)
		if pk > 0 {
			pks = append(pks, pkCol{int(pk), c.Name})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Slice(pks, func(i, j int) bool { return pks[i].pos < pks[j].pos })
	for _, p := range pks {
		m.pk = append(m.pk, p.name)
	}
	if !m.View && !m.Virtual && !m.WithoutRowid {
		// A column called rowid hides that name, not the others.
		for _, a := range []string{"_rowid_", "rowid", "oid"} {
			if _, shadowed := m.column(a); !shadowed {
				m.alias = a
				break
			}
		}
	}
	return nil
}

// info lists the tables and views of the database.
func (s *session) info(path, file string) (any, *Error) {
	out := Info{Path: path, Tables: []Table{}}
	if st, err := os.Stat(file); err == nil {
		out.Size = st.Size()
	}
	if err := s.conn.QueryRowContext(s.ctx, "SELECT sqlite_version()").Scan(&out.SQLiteVersion); err != nil {
		return nil, s.fail(err)
	}
	if err := s.conn.QueryRowContext(s.ctx, "PRAGMA journal_mode").Scan(&out.JournalMode); err != nil {
		return nil, s.fail(err)
	}
	if err := s.conn.QueryRowContext(s.ctx, "PRAGMA page_size").Scan(&out.PageSize); err != nil {
		return nil, s.fail(err)
	}

	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT name FROM pragma_table_list WHERE schema = 'main' AND type IN ('table','view','shadow','virtual')
		 AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name COLLATE NOCASE, name`)
	if err != nil {
		return nil, s.fail(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, s.fail(err)
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, s.fail(err)
	}

	// Counting every table of a big database can take long: all the counts
	// together get a couple of seconds, a table not counted in time says null.
	countCtx, cancel := context.WithTimeout(s.ctx, CountTimeout)
	defer cancel()
	for _, n := range names {
		m, e := s.loadTable(n)
		if e != nil {
			if e.Code == "table_not_found" {
				continue
			}
			// A virtual table whose module is not here: listed, but empty.
			out.Tables = append(out.Tables, Table{Name: n, Type: "table", SQL: "", Columns: []Column{}, Indexes: []Index{}})
			continue
		}
		t := Table{
			Name: m.Name, Type: "table", WithoutRowid: m.WithoutRowid, Editable: m.editable(),
			SQL: m.SQL, Columns: m.Cols, Indexes: []Index{},
		}
		if m.View {
			t.Type = "view"
		}
		if t.Columns == nil {
			t.Columns = []Column{}
		}
		if !m.View {
			if t.Indexes, err = s.indexes(m.Name); err != nil {
				return nil, s.fail(err)
			}
		}
		if countCtx.Err() == nil {
			var c int64
			if err := s.conn.QueryRowContext(countCtx, "SELECT count(*) FROM "+quote(m.Name)).Scan(&c); err == nil {
				t.Rows = &c
			}
		}
		out.Tables = append(out.Tables, t)
	}
	return out, nil
}

func (s *session) indexes(table string) ([]Index, error) {
	rows, err := s.conn.QueryContext(s.ctx,
		`SELECT l.name, l."unique", i.sql FROM pragma_index_list(?) l
		 LEFT JOIN sqlite_schema i ON i.type = 'index' AND i.name = l.name ORDER BY l.seq`, table)
	if err != nil {
		return nil, err
	}
	out := []Index{}
	for rows.Next() {
		var ix Index
		var u int64
		var ddl sql.NullString
		if err := rows.Scan(&ix.Name, &u, &ddl); err != nil {
			rows.Close()
			return nil, err
		}
		ix.Unique = u != 0
		if ddl.Valid {
			d := ddl.String
			ix.SQL = &d
		}
		out = append(out, ix)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		cols, err := s.conn.QueryContext(s.ctx, `SELECT name FROM pragma_index_xinfo(?) WHERE "key" = 1 ORDER BY seqno`, out[i].Name)
		if err != nil {
			return nil, err
		}
		out[i].Columns = []string{}
		for cols.Next() {
			var n sql.NullString
			if err := cols.Scan(&n); err != nil {
				cols.Close()
				return nil, err
			}
			if n.Valid {
				out[i].Columns = append(out[i].Columns, n.String)
			} else {
				out[i].Columns = append(out[i].Columns, "(expression)")
			}
		}
		cols.Close()
		if err := cols.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
