package dbview

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// pgBackend talks to PostgreSQL with the simple protocol: values come as the
// server's text and arguments go as quoted literals, which the server reads
// as the type of the column they are compared with or stored in.
type pgBackend struct {
	ctx   context.Context
	conn  *pgx.Conn
	cache []*srvTable
}

func openPostgres(ctx context.Context, c ServerConn, write bool) (backend, *Error) {
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, errf("db_failed", "The login could not be prepared: "+err.Error(), "")
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = "127.0.0.1", uint16(c.Port), c.User, c.Password, c.Database
	cfg.ConnectTimeout = 5 * time.Second
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	cfg.RuntimeParams = map[string]string{"application_name": "bitploy database viewer", "bytea_output": "hex", "DateStyle": "ISO, YMD"}
	if write {
		cfg.RuntimeParams["lock_timeout"] = strconv.FormatInt(BusyTimeout.Milliseconds(), 10)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		b := &pgBackend{ctx: ctx}
		return nil, b.classify(err, write)
	}
	return &pgBackend{ctx: ctx, conn: conn}, nil
}

func (b *pgBackend) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = b.conn.Close(ctx)
}

func (b *pgBackend) quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (b *pgBackend) qualified(t *srvTable) string {
	return b.quoteIdent(t.schema) + "." + b.quoteIdent(t.rel)
}

func (b *pgBackend) ph(n int) string { return "$" + strconv.Itoa(n) }

// arg sends numbers as text too: '5' is read as the column's type, 5 would be
// an integer that a text column cannot be compared with.
func (b *pgBackend) arg(v any) any {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return v
}

func (b *pgBackend) textExpr(_ *srvTable, col string) string { return b.quoteIdent(col) + "::text" }
func (b *pgBackend) likeOp() string                          { return "ILIKE" }

func (b *pgBackend) readKeywords() map[string]bool {
	return map[string]bool{"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "TABLE": true, "VALUES": true}
}

// PostgreSQL type OIDs the viewer reads as something other than text.
const (
	oidBool   = 16
	oidBytea  = 17
	oidInt8   = 20
	oidInt2   = 21
	oidInt4   = 23
	oidOID    = 26
	oidFloat4 = 700
	oidFloat8 = 701
)

func pgKind(oid uint32) colKind {
	switch oid {
	case oidInt2, oidInt4, oidInt8, oidOID:
		return kindInt
	case oidFloat4, oidFloat8:
		return kindFloat
	case oidBool:
		return kindBool
	case oidBytea:
		return kindBytes
	}
	return kindText
}

// readValue turns the server's text of a value into what encodeValue takes.
func readValue(k colKind, raw []byte, isNull bool) any {
	if isNull {
		return nil
	}
	s := string(raw)
	switch k {
	case kindInt:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case kindFloat:
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	case kindBool:
		switch s {
		case "t", "1":
			return "true"
		case "f", "0":
			return "false"
		}
	case kindBytes:
		if strings.HasPrefix(s, `\x`) {
			if d, err := hex.DecodeString(s[2:]); err == nil {
				return d
			}
		}
		return append([]byte{}, raw...)
	}
	return s
}

func (b *pgBackend) query(q string, args []any, row func(cols []string, vals []any) error) ([]string, int64, error) {
	rs, err := b.conn.Query(b.ctx, q, args...)
	if err != nil {
		return nil, -1, err
	}
	defer rs.Close()
	fds := rs.FieldDescriptions()
	cols := make([]string, len(fds))
	kinds := make([]colKind, len(fds))
	for i, f := range fds {
		cols[i], kinds[i] = f.Name, pgKind(f.DataTypeOID)
	}
	for rs.Next() {
		raw := rs.RawValues()
		vals := make([]any, len(raw))
		for i, r := range raw {
			vals[i] = readValue(kinds[i], r, r == nil)
		}
		if err := row(cols, vals); err != nil {
			if errors.Is(err, errStop) {
				return cols, -1, nil
			}
			return cols, -1, err
		}
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return cols, -1, err
	}
	tag := rs.CommandTag()
	n := int64(-1)
	if tag.Insert() || tag.Update() || tag.Delete() || strings.HasPrefix(tag.String(), "MERGE") {
		n = tag.RowsAffected()
	}
	return cols, n, nil
}

func (b *pgBackend) exec(q string, args []any) (int64, *int64, error) {
	tag, err := b.conn.Exec(b.ctx, q, args...)
	if err != nil {
		return 0, nil, err
	}
	return tag.RowsAffected(), nil, nil
}

func (b *pgBackend) readOnly() error {
	if _, err := b.conn.Exec(b.ctx, "BEGIN READ ONLY"); err != nil {
		return err
	}
	_, err := b.conn.Exec(b.ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", BusyTimeout.Milliseconds()))
	return err
}

func (b *pgBackend) openTransaction() bool { return b.conn.PgConn().TxStatus() != 'I' }

// strs runs a query of the schema and gives every value as text ("" for NULL).
func (b *pgBackend) strs(q string, args ...any) ([][]string, error) {
	var out [][]string
	_, _, err := b.query(q, args, func(_ []string, vals []any) error {
		row := make([]string, len(vals))
		for i, v := range vals {
			if v != nil {
				row[i] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
		return nil
	})
	return out, err
}

const pgUserRelations = `n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'`

// textOnly are types without an equality operator: a row is matched on their text.
var textOnly = map[string]bool{"json": true, "xml": true, "point": true, "line": true, "lseg": true, "box": true, "path": true, "polygon": true, "circle": true}

func (b *pgBackend) tables() ([]*srvTable, error) {
	if b.cache != nil {
		return b.cache, nil
	}
	rels, err := b.strs(`SELECT c.oid::text, n.nspname, c.relname, c.relkind::text,
  CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid, true) END
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f') AND NOT c.relispartition AND ` + pgUserRelations + `
ORDER BY n.nspname <> 'public', n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	byOID := map[string]*srvTable{}
	out := []*srvTable{}
	for _, r := range rels {
		t := &srvTable{schema: r[1], rel: r[2], kinds: map[string]colKind{}, textCmp: map[string]bool{}}
		t.Name = r[2]
		if r[1] != "public" {
			t.Name = r[1] + "." + r[2]
		}
		t.Type = "table"
		if r[3] == "v" || r[3] == "m" {
			t.Type = "view"
			kw := "VIEW"
			if r[3] == "m" {
				kw = "MATERIALIZED VIEW"
			}
			t.SQL = "CREATE " + kw + " " + b.quoteIdent(r[1]) + "." + b.quoteIdent(r[2]) + " AS\n" + strings.TrimSpace(r[4])
		}
		t.Columns, t.Indexes = []Column{}, []Index{}
		byOID[r[0]] = t
		out = append(out, t)
	}
	cols, err := b.strs(`SELECT a.attrelid::text, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull::text,
  pg_get_expr(d.adbin, d.adrelid), a.attgenerated::text, a.attidentity::text, a.atttypid::text, t.typname
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attnum > 0 AND NOT a.attisdropped AND c.relkind IN ('r', 'p', 'v', 'm', 'f') AND ` + pgUserRelations + `
ORDER BY a.attrelid, a.attnum`)
	if err != nil {
		return nil, err
	}
	for _, r := range cols {
		t := byOID[r[0]]
		if t == nil {
			continue
		}
		c := Column{Name: r[1], Type: r[2], NotNull: r[3] == "true", Generated: r[5] != ""}
		switch {
		case r[4] != "" && !c.Generated:
			def := r[4]
			c.Default = &def
		case r[6] == "a" || r[6] == "d":
			def := "generated by default as identity"
			if r[6] == "a" {
				def = "generated always as identity"
			}
			c.Default = &def
		}
		oid, _ := strconv.ParseUint(r[7], 10, 32)
		t.kinds[c.Name] = pgKind(uint32(oid))
		t.textCmp[c.Name] = textOnly[strings.TrimPrefix(r[8], "_")]
		t.Columns = append(t.Columns, c)
	}
	idx, err := b.strs(`SELECT i.indrelid::text, ic.relname, i.indisunique::text, i.indisprimary::text, pg_get_indexdef(i.indexrelid),
  (SELECT string_agg(pg_get_indexdef(i.indexrelid, k, true), E'\x1f' ORDER BY k) FROM generate_series(1, i.indnkeyatts) k)
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_class c ON c.oid = i.indrelid JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE ` + pgUserRelations + `
ORDER BY i.indrelid, NOT i.indisprimary, ic.relname`)
	if err != nil {
		return nil, err
	}
	for _, r := range idx {
		t := byOID[r[0]]
		if t == nil {
			continue
		}
		ix := Index{Name: r[1], Unique: r[2] == "true", Columns: strings.Split(r[5], "\x1f")}
		if r[3] == "true" {
			t.pk = ix.Columns
		} else {
			def := r[4]
			ix.SQL = &def
		}
		t.Indexes = append(t.Indexes, ix)
	}
	for _, t := range out {
		// A primary key over an expression cannot be a key here; every column must be a real one.
		ok := t.Type == "table" && len(t.pk) > 0
		for i, p := range t.pk {
			found := false
			for ci, c := range t.Columns {
				if c.Name == p {
					found = true
					t.Columns[ci].PK = i + 1
				}
			}
			ok = ok && found
		}
		if !ok {
			t.pk = nil
		}
		t.Editable = ok
	}
	b.cache = out
	return out, nil
}

func (b *pgBackend) info(i *Info) error {
	r, err := b.strs(`SELECT current_setting('server_version'), pg_database_size(current_database())::text`)
	if err != nil {
		return err
	}
	if len(r) == 1 {
		i.ServerVersion = r[0][0]
		i.Size, _ = strconv.ParseInt(r[0][1], 10, 64)
	}
	dbs, err := b.strs(`SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname`)
	if err != nil {
		return err
	}
	for _, d := range dbs {
		i.Databases = append(i.Databases, d[0])
	}
	return nil
}

// count runs in a savepoint with its own statement timeout, so a count that
// takes too long is cancelled without ending the transaction.
func (b *pgBackend) count(t *srvTable, where string, args []any, limit time.Duration) (*int64, error) {
	if _, err := b.conn.Exec(b.ctx, "SAVEPOINT bp_count"); err != nil {
		return nil, err
	}
	ms := max(limit.Milliseconds(), 1)
	if _, err := b.conn.Exec(b.ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", ms)); err != nil {
		return nil, err
	}
	var n *int64
	_, _, err := b.query("SELECT count(*) FROM "+b.qualified(t)+where, args, func(_ []string, vals []any) error {
		if v, ok := vals[0].(int64); ok {
			n = &v
		}
		return nil
	})
	if err != nil {
		if _, rerr := b.conn.Exec(b.ctx, "ROLLBACK TO SAVEPOINT bp_count"); rerr != nil {
			return nil, rerr
		}
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "57014" && b.ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	if _, err := b.conn.Exec(b.ctx, "RELEASE SAVEPOINT bp_count"); err != nil {
		return nil, err
	}
	_, err = b.conn.Exec(b.ctx, "SET LOCAL statement_timeout TO DEFAULT")
	return n, err
}

func (b *pgBackend) classify(err error, write bool) *Error {
	if errors.Is(err, context.DeadlineExceeded) || (b.ctx != nil && b.ctx.Err() != nil) {
		return errf("db_timeout", "The query took too long and was stopped.", "Narrow it down with a filter or a LIMIT, or look at fewer rows.")
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		msg := pe.Message
		if pe.Detail != "" {
			msg += ". " + pe.Detail
		}
		switch pe.Code {
		case "57014":
			return errf("db_timeout", "The query took too long and was stopped.", "Narrow it down with a filter or a LIMIT, or look at fewer rows.")
		case "25006":
			if write {
				return errf("db_read_only", "The database is read-only: "+msg, "")
			}
			return errf("db_read_only", "Turn on writes to change data.", "This query was run read-only. Run it with write on if you mean to change the database.")
		case "55P03", "40P01", "40001":
			return errf("db_busy", "The rows are in use by the app and stayed locked for 5 seconds.", "Try again in a moment.")
		case "28P01", "28000":
			return errf("db_login_failed", "The database did not accept bitploy's login: "+msg+".",
				"The password in the app's variables may have been changed after the database was made. Set it back to the one the database was made with.")
		case "3D000":
			return errf("db_not_found", "There is no such database on this server.", "Pick one of the databases in the list.")
		}
		return errf("sql_error", msg, "")
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		return errf("db_unreachable", "The database server does not answer.", "Check that the database app is running and healthy, then try again.")
	}
	return errf("sql_error", err.Error(), "")
}
