package dbview

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// budget keeps one response within MaxResponse. Rows are cheap to ask for and
// a page of 1000 rows with 64 KiB texts is not.
type budget struct{ left int }

func newBudget() *budget { return &budget{left: MaxResponse - (512 << 10)} }

func (b *budget) take(n int) bool {
	b.left -= n
	return b.left >= 0
}

// isDateColumn: the driver reads a TEXT value of such a column as a time and
// would hand it over in another format than it was stored in. Wrapping the
// column in a function hides its declared type, so the text comes through as is.
func isDateColumn(c Column) bool {
	switch strings.ToUpper(c.Type) {
	case "DATE", "DATETIME", "TIMESTAMP":
		return true
	}
	return false
}

// selectList is what to select of a table to show its rows: the key first (a
// rowid table), then every visible column.
func (m *tableMeta) selectList() (list string, cols []string, withRowid bool) {
	var exprs []string
	if m.editable() && !m.WithoutRowid {
		exprs = append(exprs, m.alias)
		withRowid = true
	}
	for _, c := range m.visible() {
		cols = append(cols, c.Name)
		if isDateColumn(c) {
			exprs = append(exprs, "ifnull("+quote(c.Name)+", NULL) AS "+quote(c.Name))
		} else {
			exprs = append(exprs, quote(c.Name))
		}
	}
	return strings.Join(exprs, ", "), cols, withRowid
}

// rowFrom reads one row of a select made with selectList.
func (m *tableMeta) rowFrom(vals []any, cols []string, withRowid bool, b *budget) (Row, bool) {
	row := Row{Values: make([]any, 0, len(cols))}
	size := 0
	rest := vals
	var rowid any
	if withRowid {
		rowid, rest = vals[0], vals[1:]
	}
	enc := make([]any, len(rest))
	for i, v := range rest {
		var n int
		enc[i], n = encodeValue(v)
		size += n
	}
	row.Values = enc
	switch {
	case withRowid:
		k, n := encodeValue(rowid)
		size += n + 16
		row.Key = map[string]any{"rowid": k}
	case m.editable() && m.WithoutRowid:
		row.Key = map[string]any{}
		for _, p := range m.pk {
			for i, c := range cols {
				if c == p {
					row.Key[p] = enc[i]
				}
			}
		}
	}
	return row, b.take(size)
}

// where builds the WHERE of a rows request. Columns are looked up in the
// schema, values are bound.
func (m *tableMeta) where(filters []Filter) (string, []any, *Error) {
	var conds []string
	var args []any
	for i, f := range filters {
		c, ok := m.column(f.Column)
		if !ok {
			return "", nil, columnNotFound(m, f.Column)
		}
		if c.Redacted {
			return "", nil, columnRedacted(m, c.Name)
		}
		col := quote(c.Name)
		switch op := strings.ToLower(strings.Join(strings.Fields(f.Op), " ")); op {
		case "is null", "is not null":
			conds = append(conds, col+" "+strings.ToUpper(op))
		case "=", "!=", "<", "<=", ">", ">=", "like", "not like":
			v, e := decodeValue(fmt.Sprintf("The value of filter %d", i+1), f.Value)
			if e != nil {
				return "", nil, e
			}
			switch {
			case v == nil && op == "=":
				conds = append(conds, col+" IS NULL")
			case v == nil && op == "!=":
				conds = append(conds, col+" IS NOT NULL")
			default:
				conds = append(conds, col+" "+strings.ToUpper(op)+" ?")
				args = append(args, v)
			}
		default:
			return "", nil, errf("filter_invalid", fmt.Sprintf("%q is not a filter operator.", f.Op),
				"Use one of =, !=, <, <=, >, >=, like, not like, is null, is not null.")
		}
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

// numericTerm: a search term that is a number (12, -3, 1.5).
var numericTerm = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)$`)

// likeEscape makes text safe to use in LIKE ... ESCAPE '\'.
var likeEscape = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// affinityOf is the affinity SQLite gives a declared type (its rules, in order).
func affinityOf(decl string) string {
	t := strings.ToUpper(decl)
	switch {
	case strings.Contains(t, "INT"):
		return "INTEGER"
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return "TEXT"
	case t == "" || strings.Contains(t, "BLOB"):
		return "BLOB"
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return "REAL"
	}
	return "NUMERIC"
}

// searchCondition matches a term against all visible columns, ORed. Text-like
// columns (TEXT, no declared type, dates) contain the term. Number columns
// only take part when the term is a number, and then start with it, so 12
// finds 12, 120 and 1234. A blob never matches. LIKE is case-insensitive for
// ASCII. Everything is compared as stored (CAST ... AS TEXT is SQL, the driver
// does not touch it), and the term is a bound parameter with %, _ and \ escaped.
// No column that can match gives "0": no rows.
func (m *tableMeta) searchCondition(term string) (string, []any) {
	numeric := numericTerm.MatchString(term)
	contains := "%" + likeEscape.Replace(term) + "%"
	prefix := likeEscape.Replace(strings.TrimPrefix(term, "+")) + "%"
	var conds []string
	var args []any
	add := func(col, pattern string) {
		conds = append(conds, "(typeof("+col+") <> 'blob' AND CAST("+col+" AS TEXT) LIKE ? ESCAPE '\\')")
		args = append(args, pattern)
	}
	if numeric && m.hasKey() && !m.WithoutRowid {
		add(m.alias, prefix)
	}
	for _, c := range m.visible() {
		if c.Redacted {
			continue // a secret never matches: it would tell what it holds
		}
		switch aff := affinityOf(c.Type); {
		case aff == "TEXT" || aff == "BLOB" || isDateColumn(c):
			add(quote(c.Name), contains)
		case numeric:
			add(quote(c.Name), prefix)
		}
	}
	if len(conds) == 0 {
		return "0", nil
	}
	return "(" + strings.Join(conds, " OR ") + ")", args
}

func columnRedacted(m *tableMeta, name string) *Error {
	return errf("column_redacted", fmt.Sprintf("Column %s of %s is a secret: it cannot be used to filter or sort.", quoteMsg(name), quoteMsg(m.Name)),
		"Secrets read as NULL here. Filter or sort on another column.")
}

func columnNotFound(m *tableMeta, name string) *Error {
	return errf("column_not_found", fmt.Sprintf("Table %s has no column %s.", quoteMsg(m.Name), quoteMsg(name)),
		"Reload the database: the column may have been renamed or dropped.")
}

// orderBy is the ORDER BY of a page. Without a sort the rows come in key order,
// and a sort ends in the key so that the pages do not overlap.
func (m *tableMeta) orderBy(col string, desc bool) (string, *Error) {
	var parts []string
	if col != "" {
		c, ok := m.column(col)
		if !ok {
			return "", columnNotFound(m, col)
		}
		if c.Redacted {
			return "", columnRedacted(m, c.Name)
		}
		p := quote(c.Name)
		if desc {
			p += " DESC"
		}
		parts = append(parts, p)
	}
	switch {
	case m.hasKey() && !m.WithoutRowid:
		parts = append(parts, m.alias)
	case m.hasKey():
		for _, p := range m.pk {
			parts = append(parts, quote(p))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " ORDER BY " + strings.Join(parts, ", "), nil
}

func (s *session) rows(req Request) (any, *Error) {
	m, e := s.loadTable(req.Table)
	if e != nil {
		return nil, e
	}
	limit := req.Limit
	switch {
	case limit == 0:
		limit = DefaultRows
	case limit < 0 || limit > MaxRows:
		return nil, errf("limit_invalid", fmt.Sprintf("The limit must be between 1 and %d.", MaxRows), "")
	}
	if req.Offset < 0 {
		return nil, errf("offset_invalid", "The offset cannot be negative.", "")
	}
	where, args, e := m.where(req.Filters)
	if e != nil {
		return nil, e
	}
	if term := strings.TrimSpace(req.Search); term != "" {
		if utf8.RuneCountInString(term) > MaxSearch {
			return nil, errf("search_invalid", fmt.Sprintf("The search text may be at most %d characters.", MaxSearch), "Search for a shorter text.")
		}
		cond, sargs := m.searchCondition(term)
		if where == "" {
			where = " WHERE " + cond
		} else {
			where += " AND " + cond
		}
		args = append(args, sargs...)
	}
	order, e := m.orderBy(req.OrderBy, req.Desc)
	if e != nil {
		return nil, e
	}
	list, cols, withRowid := m.selectList()
	if list == "" {
		return nil, errf("table_empty", "This table has no columns to show.", "")
	}
	q := "SELECT " + list + " FROM " + quote(m.Name) + where + order + " LIMIT ? OFFSET ?"
	rs, err := s.conn.QueryContext(s.ctx, q, append(append([]any{}, args...), limit, req.Offset)...)
	if err != nil {
		return nil, s.fail(err)
	}
	res := RowsResult{Columns: cols, KeyColumns: m.keyColumns(), Editable: m.editable(), Rows: []Row{}, Offset: req.Offset, Limit: limit}
	if res.Columns == nil {
		res.Columns = []string{}
	}
	b := newBudget()
	n := len(cols)
	if withRowid {
		n++
	}
	for rs.Next() {
		vals, err := scanRow(rs, n)
		if err != nil {
			rs.Close()
			return nil, s.fail(err)
		}
		row, ok := m.rowFrom(vals, cols, withRowid, b)
		if !ok {
			rs.Close()
			return nil, errf("result_too_large", "These rows are too big to send in one page.", "Ask for fewer rows per page.")
		}
		res.Rows = append(res.Rows, row)
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return nil, s.fail(err)
	}

	// The count is nice to have: a timeout on it is not an error.
	cctx, cancel := context.WithTimeout(s.ctx, CountTimeout)
	defer cancel()
	var total int64
	if err := s.conn.QueryRowContext(cctx, "SELECT count(*) FROM "+quote(m.Name)+where, args...).Scan(&total); err == nil {
		res.Total = &total
	} else if s.ctx.Err() != nil {
		return nil, s.fail(err)
	}
	return res, nil
}

// ---- changes ----

// editableTable loads a table that rows can be changed in.
func (s *session) editableTable(name string) (*tableMeta, *Error) {
	m, e := s.loadTable(name)
	if e != nil {
		return nil, e
	}
	if !m.editable() {
		what := "has no way to tell its rows apart"
		switch {
		case m.View:
			what = "is a view"
		case m.Virtual:
			what = "is a virtual table"
		}
		return nil, errf("not_editable", fmt.Sprintf("Rows of %s cannot be changed here: it %s.", quoteMsg(m.Name), what),
			"Use the SQL console to change it.")
	}
	return m, nil
}

// writable checks the columns of an insert or update and decodes the values,
// in a fixed order (the order of the table).
func (m *tableMeta) writable(values map[string]json.RawMessage) (cols []string, args []any, e *Error) {
	seen := map[string]bool{}
	for k := range values {
		c, ok := m.column(k)
		if !ok {
			return nil, nil, columnNotFound(m, k)
		}
		if c.Hidden || c.Generated {
			return nil, nil, errf("column_read_only", fmt.Sprintf("Column %s is computed by SQLite and cannot be set.", quoteMsg(c.Name)),
				"Leave it out.")
		}
		if seen[c.Name] {
			return nil, nil, errf("value_invalid", fmt.Sprintf("Column %s is given twice (names are not case sensitive).", quoteMsg(c.Name)), "")
		}
		seen[c.Name] = true
	}
	for _, c := range m.Cols {
		raw, ok := lookup(values, c.Name)
		if !ok {
			continue
		}
		v, e := decodeValue("The value for "+quoteMsg(c.Name), raw)
		if e != nil {
			return nil, nil, e
		}
		cols = append(cols, quote(c.Name))
		args = append(args, v)
	}
	return cols, args, nil
}

// lookup finds a key of a request map in any case.
func lookup(m map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if v, ok := m[name]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// keyCondition turns the key a client sent into a WHERE.
func (m *tableMeta) keyCondition(key map[string]json.RawMessage) (string, []any, *Error) {
	bad := func(why string) (string, []any, *Error) {
		return "", nil, errf("key_invalid", "The key of the row is not right: "+why+".",
			"Use the key of the row as it was listed: {\"rowid\": 5}, or the primary key columns of a WITHOUT ROWID table.")
	}
	if !m.WithoutRowid {
		if len(key) != 1 {
			return bad("it must be {\"rowid\": <number>}")
		}
		raw, ok := key["rowid"]
		if !ok {
			return bad("it must be {\"rowid\": <number>}")
		}
		v, e := decodeValue("The rowid", raw)
		if e != nil {
			return "", nil, e
		}
		if _, ok := v.(int64); !ok {
			return bad("the rowid must be an integer")
		}
		return m.alias + " = ?", []any{v}, nil
	}
	if len(key) != len(m.pk) {
		return bad("it must have a value for each of " + strings.Join(m.pk, ", "))
	}
	var conds []string
	var args []any
	for _, p := range m.pk {
		raw, ok := lookup(key, p)
		if !ok {
			return bad("the value of " + p + " is missing")
		}
		v, e := decodeValue("The key value of "+quoteMsg(p), raw)
		if e != nil {
			return "", nil, e
		}
		if v == nil {
			return bad(p + " cannot be null")
		}
		conds = append(conds, quote(p)+" = ?")
		args = append(args, v)
	}
	return strings.Join(conds, " AND "), args, nil
}

// matchCondition turns the values a row must still have into " AND col IS ?"
// parts (IS, so that NULL matches NULL). The values are compared as they are
// stored: the client sends back what it was shown.
func (m *tableMeta) matchCondition(match map[string]json.RawMessage) (string, []any, *Error) {
	names := make([]string, 0, len(match))
	for k := range match {
		names = append(names, k)
	}
	sort.Strings(names)
	var cond strings.Builder
	var args []any
	for _, k := range names {
		c, ok := m.column(k)
		if !ok {
			return "", nil, columnNotFound(m, k)
		}
		v, e := decodeValue("The value to match for "+quoteMsg(c.Name), match[k])
		if e != nil {
			return "", nil, e
		}
		cond.WriteString(" AND " + quote(c.Name) + " IS ?")
		args = append(args, v)
	}
	return cond.String(), args, nil
}

func rowChanged() *Error {
	return errf("row_changed", "This row changed since you loaded it.", "Reload the rows and try again.")
}

// keyExists tells a row that is gone from one that was changed.
func (s *session) keyExists(q queryer, m *tableMeta, cond string, args []any) (bool, *Error) {
	rs, err := q.QueryContext(s.ctx, "SELECT 1 FROM "+quote(m.Name)+" WHERE "+cond, args...)
	if err != nil {
		return false, s.fail(err)
	}
	defer rs.Close()
	found := rs.Next()
	if err := rs.Err(); err != nil {
		return false, s.fail(err)
	}
	return found, nil
}

// returning is what an INSERT or UPDATE gives back to find the row again.
func (m *tableMeta) returning() string {
	if m.WithoutRowid {
		qs := make([]string, len(m.pk))
		for i, p := range m.pk {
			qs[i] = quote(p)
			// A key declared as a date must come back as the stored text, or the
			// row can not be found again by it (see isDateColumn).
			if c, ok := m.column(p); ok && isDateColumn(c) {
				qs[i] = "ifnull(" + quote(p) + ", NULL)"
			}
		}
		return strings.Join(qs, ", ")
	}
	return m.alias
}

// readReturned reads the key values from the RETURNING of a change.
func (s *session) readReturned(rows *sql.Rows, m *tableMeta) ([]any, *Error) {
	defer rows.Close()
	var key []any
	n := 1
	if m.WithoutRowid {
		n = len(m.pk)
	}
	for rows.Next() {
		v, err := scanRow(rows, n)
		if err != nil {
			return nil, s.fail(err)
		}
		if key == nil {
			key = v
		}
	}
	if err := rows.Err(); err != nil {
		return nil, s.fail(err)
	}
	return key, nil
}

// fetchRow reads one row back by its key, to show what the change did.
func (s *session) fetchRow(q queryer, m *tableMeta, key []any) (Row, *Error) {
	list, cols, withRowid := m.selectList()
	var conds []string
	if m.WithoutRowid {
		for _, p := range m.pk {
			conds = append(conds, quote(p)+" = ?")
		}
	} else {
		conds = []string{m.alias + " = ?"}
	}
	rs, err := q.QueryContext(s.ctx, "SELECT "+list+" FROM "+quote(m.Name)+" WHERE "+strings.Join(conds, " AND "), key...)
	if err != nil {
		return Row{}, s.fail(err)
	}
	defer rs.Close()
	n := len(cols)
	if withRowid {
		n++
	}
	if !rs.Next() {
		if err := rs.Err(); err != nil {
			return Row{}, s.fail(err)
		}
		return Row{}, errf("row_not_found", "The row is gone.", "")
	}
	vals, err := scanRow(rs, n)
	if err != nil {
		return Row{}, s.fail(err)
	}
	// One row never goes over the response limit unchecked: texts and blobs are cut.
	row, _ := m.rowFrom(vals, cols, withRowid, newBudget())
	return row, nil
}

func (s *session) insert(req Request) (any, *Error) {
	m, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	cols, args, e := m.writable(req.Values)
	if e != nil {
		return nil, e
	}
	q := "INSERT INTO " + quote(m.Name) + " DEFAULT VALUES RETURNING " + m.returning()
	if len(cols) > 0 {
		q = "INSERT INTO " + quote(m.Name) + " (" + strings.Join(cols, ", ") + ") VALUES (" +
			strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ") RETURNING " + m.returning()
	}
	tx, err := s.conn.BeginTx(s.ctx, nil)
	if err != nil {
		return nil, s.fail(err)
	}
	defer tx.Rollback()
	rs, err := tx.QueryContext(s.ctx, q, args...)
	if err != nil {
		return nil, s.fail(err)
	}
	key, e := s.readReturned(rs, m)
	if e != nil {
		return nil, e
	}
	if key == nil {
		return nil, errf("sql_error", "The row was not added.", "")
	}
	row, e := s.fetchRow(tx, m, key)
	if e != nil {
		return nil, e
	}
	if err := tx.Commit(); err != nil {
		return nil, s.fail(err)
	}
	return InsertResult{Key: row.Key, Row: row}, nil
}

func (s *session) update(req Request) (any, *Error) {
	m, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	keyCond, kargs, e := m.keyCondition(req.Key)
	if e != nil {
		return nil, e
	}
	matchCond, margs, e := m.matchCondition(req.Match)
	if e != nil {
		return nil, e
	}
	cond := keyCond + matchCond
	kargs = append(kargs, margs...)
	cols, args, e := m.writable(req.Values)
	if e != nil {
		return nil, e
	}
	if len(cols) == 0 {
		return nil, errf("value_invalid", "There is nothing to change: no values were sent.", "Send the columns to change in values.")
	}
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = c + " = ?"
	}
	q := "UPDATE " + quote(m.Name) + " SET " + strings.Join(sets, ", ") + " WHERE " + cond + " RETURNING " + m.returning()
	tx, err := s.conn.BeginTx(s.ctx, nil)
	if err != nil {
		return nil, s.fail(err)
	}
	defer tx.Rollback()
	rs, err := tx.QueryContext(s.ctx, q, append(args, kargs...)...)
	if err != nil {
		return nil, s.fail(err)
	}
	key, e := s.readReturned(rs, m)
	if e != nil {
		return nil, e
	}
	if key == nil {
		if matchCond != "" {
			// Not found by key and values: gone, or changed?
			if found, e := s.keyExists(tx, m, keyCond, kargs[:len(kargs)-len(margs)]); e != nil {
				return nil, e
			} else if found {
				return nil, rowChanged()
			}
		}
		return nil, errf("row_not_found", "This row does not exist (any more).", "Reload the table: someone may have deleted or changed the row.")
	}
	row, e := s.fetchRow(tx, m, key)
	if e != nil {
		return nil, e
	}
	if err := tx.Commit(); err != nil {
		return nil, s.fail(err)
	}
	return UpdateResult{Row: row}, nil
}

// updateMany sets the same values on many rows, in one transaction: all or
// none. A row whose key is gone is skipped; one that changed (its match fails)
// stops everything with row_changed and details.index, the place in keys.
func (s *session) updateMany(req Request) (any, *Error) {
	if len(req.Keys) == 0 || len(req.Keys) > MaxKeys {
		return nil, errf("key_invalid", fmt.Sprintf("Send between 1 and %d keys to change.", MaxKeys), "")
	}
	if len(req.Matches) != 0 && len(req.Matches) != len(req.Keys) {
		return nil, errf("key_invalid", "There must be one set of values to match for each key.", "Send match with as many entries as keys, in the same order, or leave it out.")
	}
	m, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	cols, vargs, e := m.writable(req.Values)
	if e != nil {
		return nil, e
	}
	if len(cols) == 0 {
		return nil, errf("value_invalid", "There is nothing to change: no values were sent.", "Send the columns to change in values.")
	}
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = c + " = ?"
	}
	type upd struct {
		q, keyCond string
		args       []any
		keyArgs    int
		matched    bool
	}
	ups := make([]upd, 0, len(req.Keys))
	for i, k := range req.Keys {
		cond, kargs, e := m.keyCondition(k)
		if e != nil {
			return nil, e
		}
		u := upd{keyCond: cond, keyArgs: len(kargs)}
		args := append(append([]any{}, vargs...), kargs...)
		if len(req.Matches) > 0 {
			mc, margs, e := m.matchCondition(req.Matches[i])
			if e != nil {
				return nil, e
			}
			u.matched = mc != ""
			cond += mc
			args = append(args, margs...)
		}
		u.q, u.args = "UPDATE "+quote(m.Name)+" SET "+strings.Join(sets, ", ")+" WHERE "+cond, args
		ups = append(ups, u)
	}
	tx, err := s.conn.BeginTx(s.ctx, nil)
	if err != nil {
		return nil, s.fail(err)
	}
	defer tx.Rollback()
	var n int64
	for i, u := range ups {
		r, err := tx.ExecContext(s.ctx, u.q, u.args...)
		if err != nil {
			return nil, s.fail(err)
		}
		c, _ := r.RowsAffected()
		if c == 0 && u.matched {
			found, e := s.keyExists(tx, m, u.keyCond, u.args[len(vargs):len(vargs)+u.keyArgs])
			if e != nil {
				return nil, e
			}
			if found {
				e := rowChanged()
				e.Details = map[string]any{"index": i}
				return nil, e
			}
		}
		n += c
	}
	if err := tx.Commit(); err != nil {
		return nil, s.fail(err)
	}
	return UpdateManyResult{Updated: n}, nil
}

func (s *session) delete(req Request) (any, *Error) {
	if len(req.Keys) == 0 || len(req.Keys) > MaxKeys {
		return nil, errf("key_invalid", fmt.Sprintf("Send between 1 and %d keys to delete.", MaxKeys), "")
	}
	m, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	if len(req.Matches) != 0 && len(req.Matches) != len(req.Keys) {
		return nil, errf("key_invalid", "There must be one set of values to match for each key.", "Send match with as many entries as keys, in the same order, or leave it out.")
	}
	type del struct {
		q, keyCond string
		args       []any
		keyArgs    int // how many of args belong to the key
		matched    bool
	}
	dels := make([]del, 0, len(req.Keys))
	for i, k := range req.Keys {
		cond, args, e := m.keyCondition(k)
		if e != nil {
			return nil, e
		}
		d := del{keyCond: cond, keyArgs: len(args)}
		if len(req.Matches) > 0 {
			mc, margs, e := m.matchCondition(req.Matches[i])
			if e != nil {
				return nil, e
			}
			d.matched = mc != ""
			cond += mc
			args = append(args, margs...)
		}
		d.q, d.args = "DELETE FROM "+quote(m.Name)+" WHERE "+cond, args
		dels = append(dels, d)
	}
	tx, err := s.conn.BeginTx(s.ctx, nil)
	if err != nil {
		return nil, s.fail(err)
	}
	defer tx.Rollback()
	var n int64
	for _, d := range dels {
		r, err := tx.ExecContext(s.ctx, d.q, d.args...)
		if err != nil {
			return nil, s.fail(err)
		}
		c, _ := r.RowsAffected()
		if c == 0 && d.matched {
			// All or nothing: a row that changed stops the whole delete (the
			// transaction is rolled back). A row that is gone is skipped.
			if found, e := s.keyExists(tx, m, d.keyCond, d.args[:d.keyArgs]); e != nil {
				return nil, e
			} else if found {
				return nil, rowChanged()
			}
		}
		n += c
	}
	if err := tx.Commit(); err != nil {
		return nil, s.fail(err)
	}
	return DeleteResult{Deleted: n}, nil
}
