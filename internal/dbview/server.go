package dbview

// The viewer for a database server: PostgreSQL, MariaDB or MySQL running as a
// database app of bitploy. The helper container shares the network of the
// database's container, so the server is at 127.0.0.1; bitploy hands it the
// login in the request (Request.Server). The operations and the answers are
// those of the SQLite viewer, so the dashboard shows both with the same UI:
// a table's key is its primary key, values go through the shapes of value.go.
//
// Values are read as the server's own text (PostgreSQL's simple protocol,
// MySQL's text protocol) and sent back as text, which the server turns into
// the column's type: a timestamp, a numeric or a uuid survives a round trip
// unchanged. Integers and floats become JSON numbers, binary data a blob.
//
// Reading runs in a READ ONLY transaction that is rolled back, and the console
// takes only statements that read (SELECT, WITH, SHOW, EXPLAIN, ...) unless
// write is on.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Engines of ServerConn.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
	EngineMariaDB  = "mariadb"
)

// ServerConn is how the helper logs in to a database server.
type ServerConn struct {
	Engine   string `json:"engine"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
}

// colKind is how a value of a column is turned into JSON.
type colKind int

const (
	kindText colKind = iota
	kindInt
	kindFloat
	kindBool
	kindBytes
)

// srvTable is one table or view of a server database.
type srvTable struct {
	Table
	schema, rel string
	pk          []string
	kinds       map[string]colKind // by column name
	textCmp     map[string]bool    // compared as text: the type has no equality (json, xml, ...)
}

func (t *srvTable) column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	var found *Column
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			if found != nil {
				return Column{}, false
			}
			found = &t.Columns[i]
		}
	}
	if found == nil {
		return Column{}, false
	}
	return *found, true
}

// backend is what differs between PostgreSQL and MySQL.
type backend interface {
	quoteIdent(name string) string
	// qualified is the table's name in SQL.
	qualified(t *srvTable) string
	// ph is the placeholder of argument n (1-based).
	ph(n int) string
	// arg adapts a decoded value for binding.
	arg(v any) any
	// query runs a statement and calls row for each row with its values already
	// read (nil, int64, float64, string or []byte). It returns the columns and
	// the rows the statement changed (-1: not known).
	query(q string, args []any, row func(cols []string, vals []any) error) (cols []string, affected int64, err error)
	// exec runs a statement that returns no rows.
	exec(q string, args []any) (affected int64, lastID *int64, err error)
	// readOnly starts the read-only transaction of a request that reads.
	readOnly() error
	tables() ([]*srvTable, error)
	// count is the rows of a table within a time limit; nil when it took too long.
	count(t *srvTable, where string, args []any, limit time.Duration) (*int64, error)
	info(i *Info) error
	// textExpr is the column as text, for LIKE and search.
	textExpr(t *srvTable, col string) string
	likeOp() string
	// rowsKind: the leading keyword gives rows, for the console.
	readKeywords() map[string]bool
	// openTransaction reports whether the console left a transaction open.
	openTransaction() bool
	classify(err error, write bool) *Error
	close()
}

type srvSession struct {
	ctx   context.Context
	b     backend
	write bool
	conn  ServerConn
}

// runServer is Run for a database server.
func runServer(ctx context.Context, req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = Response{Error: errf("db_failed", fmt.Sprintf("The database helper failed: %v.", r), "Try again. If it keeps happening, tell the bitploy admin.")}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	result, e := serverOp(ctx, req)
	if e != nil {
		return Response{Error: e}
	}
	raw, err := encode(result)
	if err != nil {
		return Response{Error: errf("db_failed", "The result could not be turned into JSON: "+err.Error(), "")}
	}
	if len(raw) > MaxResponse {
		if isWrite(req) {
			return Response{Error: errf("result_too_large", "The changes were saved, but the answer is too big to send.",
				"Do not repeat the change. Look at the rows with a smaller query.")}
		}
		return Response{Error: errf("result_too_large", "The result is too big to send.", "Ask for fewer rows or fewer columns.")}
	}
	return Response{Result: raw}
}

func serverOp(ctx context.Context, req Request) (any, *Error) {
	c := *req.Server
	if c.Port <= 0 || c.Port > 65535 {
		return nil, errf("request_invalid", "The port of the database server is not right.", "")
	}
	write := isWrite(req)
	var b backend
	var e *Error
	switch c.Engine {
	case EnginePostgres:
		b, e = openPostgres(ctx, c, write)
	case EngineMySQL, EngineMariaDB:
		b, e = openMySQL(ctx, c, write)
	default:
		return nil, errf("request_invalid", fmt.Sprintf("%q is not a database engine the viewer knows.", c.Engine), "")
	}
	if e != nil {
		return nil, e
	}
	defer b.close()
	s := &srvSession{ctx: ctx, b: b, write: write, conn: c}
	if !write {
		if err := b.readOnly(); err != nil {
			return nil, b.classify(err, false)
		}
	}
	switch req.Op {
	case OpInfo:
		return s.info()
	case OpRows:
		return s.rows(req)
	case OpCell:
		return s.cell(req)
	case OpInsert:
		return s.insert(req)
	case OpUpdate:
		return s.update(req)
	case OpUpdateMany:
		return s.updateMany(req)
	case OpDelete:
		return s.deleteRows(req)
	case OpSQL:
		return s.console(req)
	}
	return nil, errf("op_invalid", fmt.Sprintf("%q is not a known operation.", req.Op), "")
}

func (s *srvSession) fail(err error) *Error { return s.b.classify(err, s.write) }

// ---- schema ----

func (s *srvSession) info() (any, *Error) {
	ts, err := s.b.tables()
	if err != nil {
		return nil, s.fail(err)
	}
	out := Info{Path: s.conn.Database, Database: s.conn.Database, Engine: s.conn.Engine, Tables: []Table{}}
	if err := s.b.info(&out); err != nil {
		return nil, s.fail(err)
	}
	deadline := time.Now().Add(CountTimeout)
	for _, t := range ts {
		if left := time.Until(deadline); left > 0 {
			if n, err := s.b.count(t, "", nil, left); err == nil {
				t.Rows = n
			} else if s.ctx.Err() != nil {
				return nil, s.fail(err)
			}
		}
		out.Tables = append(out.Tables, t.Table)
	}
	return out, nil
}

func (s *srvSession) loadTable(name string) (*srvTable, *Error) {
	ts, err := s.b.tables()
	if err != nil {
		return nil, s.fail(err)
	}
	for _, t := range ts {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, tableNotFound(name)
}

func (s *srvSession) editableTable(name string) (*srvTable, *Error) {
	t, e := s.loadTable(name)
	if e != nil {
		return nil, e
	}
	if !t.Editable {
		what := "has no primary key that tells its rows apart"
		if t.Type == "view" {
			what = "is a view"
		}
		return nil, errf("not_editable", fmt.Sprintf("Rows of %s cannot be changed here: it %s.", quoteMsg(t.Name), what),
			"Use the SQL console to change it.")
	}
	return t, nil
}

func srvColumnNotFound(t *srvTable, name string) *Error {
	return errf("column_not_found", fmt.Sprintf("Table %s has no column %s.", quoteMsg(t.Name), quoteMsg(name)),
		"Reload the database: the column may have been renamed or dropped.")
}

// ---- reading rows ----

// args collects bound values and hands out their placeholders.
type args struct {
	b    backend
	vals []any
}

func (a *args) add(v any) string {
	a.vals = append(a.vals, a.b.arg(v))
	return a.b.ph(len(a.vals))
}

func (s *srvSession) selectList(t *srvTable) (string, []string) {
	var exprs, cols []string
	for _, c := range t.Columns {
		exprs = append(exprs, s.b.quoteIdent(c.Name))
		cols = append(cols, c.Name)
	}
	return strings.Join(exprs, ", "), cols
}

// rowOf turns the values of a row read with selectList into a Row.
func rowOf(t *srvTable, cols []string, vals []any, b *budget) (Row, bool) {
	row := Row{Values: make([]any, len(vals))}
	size := 0
	for i, v := range vals {
		var n int
		row.Values[i], n = encodeValue(v)
		size += n
	}
	if t.Editable {
		row.Key = map[string]any{}
		for _, p := range t.pk {
			for i, c := range cols {
				if c == p {
					row.Key[p] = row.Values[i]
				}
			}
		}
	}
	return row, b.take(size)
}

func (s *srvSession) where(t *srvTable, filters []Filter, search string, a *args) (string, *Error) {
	var conds []string
	for i, f := range filters {
		c, ok := t.column(f.Column)
		if !ok {
			return "", srvColumnNotFound(t, f.Column)
		}
		col := s.b.quoteIdent(c.Name)
		switch op := strings.ToLower(strings.Join(strings.Fields(f.Op), " ")); op {
		case "is null", "is not null":
			conds = append(conds, col+" "+strings.ToUpper(op))
		case "=", "!=", "<", "<=", ">", ">=", "like", "not like":
			v, e := decodeValue(fmt.Sprintf("The value of filter %d", i+1), f.Value)
			if e != nil {
				return "", e
			}
			switch {
			case v == nil && op == "=":
				conds = append(conds, col+" IS NULL")
			case v == nil && op == "!=":
				conds = append(conds, col+" IS NOT NULL")
			case op == "like":
				conds = append(conds, s.b.textExpr(t, c.Name)+" "+s.b.likeOp()+" "+a.add(v))
			case op == "not like":
				conds = append(conds, s.b.textExpr(t, c.Name)+" NOT "+s.b.likeOp()+" "+a.add(v))
			case op == "!=":
				conds = append(conds, col+" <> "+a.add(v))
			default:
				conds = append(conds, col+" "+op+" "+a.add(v))
			}
		default:
			return "", errf("filter_invalid", fmt.Sprintf("%q is not a filter operator.", f.Op),
				"Use one of =, !=, <, <=, >, >=, like, not like, is null, is not null.")
		}
	}
	if term := strings.TrimSpace(search); term != "" {
		if len([]rune(term)) > MaxSearch {
			return "", errf("search_invalid", fmt.Sprintf("The search text may be at most %d characters.", MaxSearch), "Search for a shorter text.")
		}
		var ors []string
		for _, c := range t.Columns {
			if t.kinds[c.Name] == kindBytes {
				continue
			}
			ors = append(ors, s.b.textExpr(t, c.Name)+" "+s.b.likeOp()+" "+a.add("%"+likeEscape.Replace(term)+"%")+s.escapeClause())
		}
		if len(ors) == 0 {
			conds = append(conds, "1 = 0")
		} else {
			conds = append(conds, "("+strings.Join(ors, " OR ")+")")
		}
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

// escapeClause names the backslash as the escape of LIKE; in a MySQL string a
// backslash is an escape itself, so it is written twice there.
func (s *srvSession) escapeClause() string {
	if s.conn.Engine == EnginePostgres {
		return ` ESCAPE '\'`
	}
	return ` ESCAPE '\\'`
}

func (s *srvSession) orderBy(t *srvTable, col string, desc bool) (string, *Error) {
	var parts []string
	if col != "" {
		c, ok := t.column(col)
		if !ok {
			return "", srvColumnNotFound(t, col)
		}
		p := s.b.quoteIdent(c.Name)
		if desc {
			p += " DESC"
		}
		parts = append(parts, p)
	}
	for _, p := range t.pk {
		parts = append(parts, s.b.quoteIdent(p))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " ORDER BY " + strings.Join(parts, ", "), nil
}

func (s *srvSession) rows(req Request) (any, *Error) {
	t, e := s.loadTable(req.Table)
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
	a := &args{b: s.b}
	where, e := s.where(t, req.Filters, req.Search, a)
	if e != nil {
		return nil, e
	}
	order, e := s.orderBy(t, req.OrderBy, req.Desc)
	if e != nil {
		return nil, e
	}
	list, cols := s.selectList(t)
	if list == "" {
		return nil, errf("table_empty", "This table has no columns to show.", "")
	}
	res := RowsResult{Columns: cols, KeyColumns: keyColumns(t), Editable: t.Editable, Rows: []Row{}, Offset: req.Offset, Limit: limit}
	b := newBudget()
	q := fmt.Sprintf("SELECT %s FROM %s%s%s LIMIT %d OFFSET %d", list, s.b.qualified(t), where, order, limit, req.Offset)
	tooBig := false
	_, _, err := s.b.query(q, a.vals, func(_ []string, vals []any) error {
		row, ok := rowOf(t, cols, vals, b)
		if !ok {
			tooBig = true
			return errStop
		}
		res.Rows = append(res.Rows, row)
		return nil
	})
	if tooBig {
		return nil, errf("result_too_large", "These rows are too big to send in one page.", "Ask for fewer rows per page.")
	}
	if err != nil {
		return nil, s.fail(err)
	}
	if n, err := s.b.count(t, where, a.vals, CountTimeout); err == nil {
		res.Total = n
	} else if s.ctx.Err() != nil {
		return nil, s.fail(err)
	}
	return res, nil
}

func keyColumns(t *srvTable) []string {
	if !t.Editable {
		return []string{}
	}
	return append([]string{}, t.pk...)
}

// keyCondition turns the key a client sent into a WHERE.
func (s *srvSession) keyCondition(t *srvTable, key map[string]json.RawMessage, a *args) (string, *Error) {
	bad := func(why string) (string, *Error) {
		return "", errf("key_invalid", "The key of the row is not right: "+why+".",
			"Use the key of the row as it was listed: the values of its primary key columns.")
	}
	if len(key) != len(t.pk) {
		return bad("it must have a value for each of " + strings.Join(t.pk, ", "))
	}
	var conds []string
	for _, p := range t.pk {
		raw, ok := lookup(key, p)
		if !ok {
			return bad("the value of " + p + " is missing")
		}
		v, e := decodeValue("The key value of "+quoteMsg(p), raw)
		if e != nil {
			return "", e
		}
		if v == nil {
			return bad(p + " cannot be null")
		}
		conds = append(conds, s.cmp(t, p, a.add(v), false))
	}
	return strings.Join(conds, " AND "), nil
}

// cmp compares a column with a placeholder; nullSafe matches NULL with NULL.
func (s *srvSession) cmp(t *srvTable, col, ph string, nullSafe bool) string {
	expr := s.b.quoteIdent(col)
	if t.textCmp[col] {
		expr = s.b.textExpr(t, col)
	}
	switch {
	case !nullSafe:
		return expr + " = " + ph
	case s.conn.Engine == EnginePostgres:
		return expr + " IS NOT DISTINCT FROM " + ph
	}
	return expr + " <=> " + ph
}

// matchCondition: the values a row must still have.
func (s *srvSession) matchCondition(t *srvTable, match map[string]json.RawMessage, a *args) (string, *Error) {
	var b strings.Builder
	for _, c := range t.Columns {
		raw, ok := lookup(match, c.Name)
		if !ok {
			continue
		}
		v, e := decodeValue("The value to match for "+quoteMsg(c.Name), raw)
		if e != nil {
			return "", e
		}
		// A FLOAT of MySQL is single precision: the text it was shown as is not
		// the double it is compared with. Such a column is not checked.
		if t.kinds[c.Name] == kindFloat && s.conn.Engine != EnginePostgres {
			continue
		}
		b.WriteString(" AND " + s.cmp(t, c.Name, a.add(v), true))
	}
	for k := range match {
		if _, ok := t.column(k); !ok {
			return "", srvColumnNotFound(t, k)
		}
	}
	return b.String(), nil
}

// fetchRow reads one row by its key.
func (s *srvSession) fetchRow(t *srvTable, key map[string]json.RawMessage) (Row, bool, *Error) {
	a := &args{b: s.b}
	cond, e := s.keyCondition(t, key, a)
	if e != nil {
		return Row{}, false, e
	}
	list, cols := s.selectList(t)
	var row Row
	found := false
	_, _, err := s.b.query("SELECT "+list+" FROM "+s.b.qualified(t)+" WHERE "+cond+" LIMIT 1", a.vals, func(_ []string, vals []any) error {
		row, _ = rowOf(t, cols, vals, newBudget())
		found = true
		return nil
	})
	if err != nil {
		return Row{}, false, s.fail(err)
	}
	return row, found, nil
}

func (s *srvSession) cell(req Request) (any, *Error) {
	t, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	c, ok := t.column(req.Column)
	if !ok {
		return nil, srvColumnNotFound(t, req.Column)
	}
	a := &args{b: s.b}
	cond, e := s.keyCondition(t, req.Key, a)
	if e != nil {
		return nil, e
	}
	var val any
	found := false
	_, _, err := s.b.query("SELECT "+s.b.quoteIdent(c.Name)+" FROM "+s.b.qualified(t)+" WHERE "+cond+" LIMIT 1", a.vals, func(_ []string, vals []any) error {
		val, found = vals[0], true
		return nil
	})
	if err != nil {
		return nil, s.fail(err)
	}
	if !found {
		return nil, errf("row_not_found", "This row does not exist.", "Reload the table: someone may have deleted or changed the row.")
	}
	return CellResult{Value: encodeCell(val)}, nil
}

// ---- changes ----

// writable checks the columns of an insert or update and decodes the values.
func (s *srvSession) writable(t *srvTable, values map[string]json.RawMessage) (cols []string, vals []any, e *Error) {
	seen := map[string]bool{}
	for k := range values {
		c, ok := t.column(k)
		if !ok {
			return nil, nil, srvColumnNotFound(t, k)
		}
		if c.Generated {
			return nil, nil, errf("column_read_only", fmt.Sprintf("Column %s is computed by the database and cannot be set.", quoteMsg(c.Name)), "Leave it out.")
		}
		if seen[c.Name] {
			return nil, nil, errf("value_invalid", fmt.Sprintf("Column %s is given twice.", quoteMsg(c.Name)), "")
		}
		seen[c.Name] = true
	}
	for _, c := range t.Columns {
		raw, ok := lookup(values, c.Name)
		if !ok {
			continue
		}
		v, e := decodeValue("The value for "+quoteMsg(c.Name), raw)
		if e != nil {
			return nil, nil, e
		}
		cols = append(cols, c.Name)
		vals = append(vals, v)
	}
	return cols, vals, nil
}

// keyJSON is a key as the client sends it, from values read or given.
func keyJSON(m map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		raw, _ := json.Marshal(v)
		out[k] = raw
	}
	return out
}

func (s *srvSession) insert(req Request) (any, *Error) {
	t, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	cols, vals, e := s.writable(t, req.Values)
	if e != nil {
		return nil, e
	}
	a := &args{b: s.b}
	var q string
	if len(cols) == 0 {
		q = "INSERT INTO " + s.b.qualified(t) + " DEFAULT VALUES"
		if s.conn.Engine != EnginePostgres {
			q = "INSERT INTO " + s.b.qualified(t) + " () VALUES ()"
		}
	} else {
		var names, phs []string
		for i, c := range cols {
			names = append(names, s.b.quoteIdent(c))
			phs = append(phs, a.add(vals[i]))
		}
		q = "INSERT INTO " + s.b.qualified(t) + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
	}
	key := map[string]any{}
	if s.conn.Engine == EnginePostgres {
		var pks []string
		for _, p := range t.pk {
			pks = append(pks, s.b.quoteIdent(p))
		}
		_, _, err := s.b.query(q+" RETURNING "+strings.Join(pks, ", "), a.vals, func(_ []string, got []any) error {
			for i, p := range t.pk {
				key[p], _ = encodeValue(got[i])
			}
			return nil
		})
		if err != nil {
			return nil, s.fail(err)
		}
	} else {
		_, lastID, err := s.b.exec(q, a.vals)
		if err != nil {
			return nil, s.fail(err)
		}
		// The key is what was given, else the AUTO_INCREMENT value.
		for _, p := range t.pk {
			for i, c := range cols {
				if c == p {
					key[p], _ = encodeValue(vals[i])
				}
			}
			if _, ok := key[p]; !ok && len(t.pk) == 1 && lastID != nil && *lastID != 0 {
				key[p] = *lastID
			}
		}
	}
	if len(key) != len(t.pk) {
		return nil, errf("sql_error", "The row was added, but bitploy cannot tell which one it is to show it.", "Reload the table to see it.")
	}
	row, found, e := s.fetchRow(t, keyJSON(key))
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, errf("sql_error", "The row was added, but it could not be read back.", "Reload the table to see it.")
	}
	return InsertResult{Key: key, Row: row}, nil
}

// changeRows runs one UPDATE or DELETE per key in one transaction. A key
// whose row is gone is skipped; a row whose values changed undoes all.
func (s *srvSession) changeRows(t *srvTable, keys []map[string]json.RawMessage, matches []map[string]json.RawMessage, stmt func(a *args) (string, *Error)) (int64, *Error) {
	if len(keys) == 0 || len(keys) > MaxKeys {
		return 0, errf("key_invalid", fmt.Sprintf("Send between 1 and %d keys.", MaxKeys), "")
	}
	if matches != nil && len(matches) != len(keys) {
		return 0, errf("key_invalid", "There must be one set of values to match for each key.", "Send match with as many entries as keys, in the same order, or leave it out.")
	}
	if _, _, err := s.b.exec("BEGIN", nil); err != nil {
		return 0, s.fail(err)
	}
	done := false
	defer func() {
		if !done {
			_, _, _ = s.b.exec("ROLLBACK", nil)
		}
	}()
	var total int64
	for i, k := range keys {
		a := &args{b: s.b}
		head, e := stmt(a)
		if e != nil {
			return 0, e
		}
		cond, e := s.keyCondition(t, k, a)
		if e != nil {
			return 0, e
		}
		var match string
		if matches != nil {
			if match, e = s.matchCondition(t, matches[i], a); e != nil {
				return 0, e
			}
		}
		n, _, err := s.b.exec(head+" WHERE "+cond+match, a.vals)
		if err != nil {
			return 0, s.fail(err)
		}
		if n == 0 && match != "" {
			ka := &args{b: s.b}
			kc, _ := s.keyCondition(t, k, ka)
			exists := false
			if _, _, err := s.b.query("SELECT 1 FROM "+s.b.qualified(t)+" WHERE "+kc, ka.vals, func([]string, []any) error { exists = true; return nil }); err != nil {
				return 0, s.fail(err)
			}
			if exists {
				e := rowChanged()
				e.Details = map[string]any{"index": i}
				return 0, e
			}
		}
		total += n
	}
	if _, _, err := s.b.exec("COMMIT", nil); err != nil {
		return 0, s.fail(err)
	}
	done = true
	return total, nil
}

func (s *srvSession) setClause(t *srvTable, values map[string]json.RawMessage) (func(a *args) (string, *Error), []string, []any, *Error) {
	if len(values) == 0 {
		return nil, nil, nil, errf("value_invalid", "There is nothing to change: no values were sent.", "Send the columns to change in values.")
	}
	cols, vals, e := s.writable(t, values)
	if e != nil {
		return nil, nil, nil, e
	}
	return func(a *args) (string, *Error) {
		var sets []string
		for i, c := range cols {
			sets = append(sets, s.b.quoteIdent(c)+" = "+a.add(vals[i]))
		}
		return "UPDATE " + s.b.qualified(t) + " SET " + strings.Join(sets, ", "), nil
	}, cols, vals, nil
}

func (s *srvSession) update(req Request) (any, *Error) {
	t, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	stmt, cols, vals, e := s.setClause(t, req.Values)
	if e != nil {
		return nil, e
	}
	var matches []map[string]json.RawMessage
	if req.Match != nil {
		matches = []map[string]json.RawMessage{req.Match}
	}
	n, e := s.changeRows(t, []map[string]json.RawMessage{req.Key}, matches, stmt)
	if e != nil {
		return nil, e
	}
	if n == 0 {
		return nil, errf("row_not_found", "This row does not exist.", "Reload the table: someone may have deleted or changed the row.")
	}
	// A change of a key column moves the row: read it back by its new key.
	key := map[string]json.RawMessage{}
	for k, v := range req.Key {
		key[k] = v
	}
	for i, c := range cols {
		for _, p := range t.pk {
			if p == c {
				for k := range key {
					if strings.EqualFold(k, p) {
						delete(key, k)
					}
				}
				enc, _ := encodeValue(vals[i])
				raw, _ := json.Marshal(enc)
				key[p] = raw
			}
		}
	}
	row, found, e := s.fetchRow(t, key)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, errf("row_not_found", "The row was changed, but it could not be read back.", "Reload the table.")
	}
	return UpdateResult{Row: row}, nil
}

func (s *srvSession) updateMany(req Request) (any, *Error) {
	t, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	stmt, _, _, e := s.setClause(t, req.Values)
	if e != nil {
		return nil, e
	}
	n, e := s.changeRows(t, req.Keys, req.Matches, stmt)
	if e != nil {
		return nil, e
	}
	return UpdateManyResult{Updated: n}, nil
}

func (s *srvSession) deleteRows(req Request) (any, *Error) {
	t, e := s.editableTable(req.Table)
	if e != nil {
		return nil, e
	}
	n, e := s.changeRows(t, req.Keys, req.Matches, func(*args) (string, *Error) {
		return "DELETE FROM " + s.b.qualified(t), nil
	})
	if e != nil {
		return nil, e
	}
	return DeleteResult{Deleted: n}, nil
}

// ---- the console ----

func (s *srvSession) console(req Request) (any, *Error) {
	if len(req.SQL) > MaxSQLBytes {
		return nil, errf("sql_too_long", fmt.Sprintf("The SQL is longer than %d KB.", MaxSQLBytes>>10), "Split it into several requests.")
	}
	stmts := splitServer(req.SQL, s.conn.Engine != EnginePostgres)
	if len(stmts) == 0 {
		return nil, errf("sql_empty", "There is no SQL to run.", "Send at least one statement.")
	}
	reads := s.b.readKeywords()
	if !req.Write {
		for i, st := range stmts {
			if !reads[st.kind] || (s.conn.Engine != EnginePostgres && st.writesFile()) {
				return nil, withStatement(errf("db_read_only", "Turn on writes to change data.",
					"This query was run read-only, which takes only statements that read (SELECT, WITH, SHOW, EXPLAIN). Run it with write on to run anything else."), i+1, nil)
			}
		}
	}
	start := time.Now()
	b := newBudget()
	res := SQLResult{Results: []StatementResult{}}
	for i, st := range stmts {
		r, e := s.runStatement(st, reads[st.kind], b)
		if e != nil {
			return nil, withStatement(e, i+1, res.Results)
		}
		res.Results = append(res.Results, r)
	}
	if req.Write && (s.b.openTransaction() || (s.conn.Engine != EnginePostgres && leftOpen(stmts))) {
		_, _, _ = s.b.exec("ROLLBACK", nil)
		return nil, &Error{Code: "transaction_open", Message: "The SQL started a transaction but did not COMMIT, so nothing in it was saved.",
			Hint: "End it with COMMIT;. Statements before the BEGIN were saved.", Details: map[string]any{"results": res.Results}}
	}
	res.TookMS = since(start)
	return res, nil
}

func (s *srvSession) runStatement(st srvStatement, rows bool, b *budget) (StatementResult, *Error) {
	start := time.Now()
	r := StatementResult{SQL: st.sql, Columns: []string{}, Rows: [][]any{}}
	if !rows && s.conn.Engine != EnginePostgres {
		// MySQL tells the rows changed and the new id only for a statement run with exec.
		n, id, err := s.b.exec(st.sql, nil)
		if err != nil {
			return r, s.fail(err)
		}
		if changesRows[st.kind] {
			r.RowsAffected = &n
		}
		if (st.kind == "INSERT" || st.kind == "REPLACE") && id != nil && *id != 0 {
			r.LastInsertRowid = id
		}
		r.TookMS = since(start)
		return r, nil
	}
	cols, affected, err := s.b.query(st.sql, nil, func(_ []string, vals []any) error {
		if len(r.Rows) >= MaxRows || r.Truncated {
			r.Truncated = true
			return nil
		}
		row, size := make([]any, len(vals)), 0
		for i, v := range vals {
			var n int
			row[i], n = encodeValue(v)
			size += n
		}
		if !b.take(size) {
			r.Truncated = true
			return nil
		}
		r.Rows = append(r.Rows, row)
		return nil
	})
	if err != nil {
		return r, s.fail(err)
	}
	if cols != nil {
		r.Columns = cols
	}
	if affected >= 0 && changesRows[st.kind] {
		r.RowsAffected = &affected
	}
	r.TookMS = since(start)
	return r, nil
}

// leftOpen follows BEGIN and COMMIT through the statements of a MySQL console
// text, whose server does not always tell (a transaction that has not touched
// a table yet is not listed). A statement that changes the schema commits.
func leftOpen(stmts []srvStatement) bool {
	open := false
	for _, st := range stmts {
		switch st.kind {
		case "BEGIN", "START":
			open = true
		case "COMMIT", "ROLLBACK", "END", "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME":
			open = false
		}
	}
	return open
}

// changesRows are the statements whose count of changed rows is shown.
var changesRows = map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "MERGE": true}

// errStop ends a query early without an error.
var errStop = fmt.Errorf("stop")
