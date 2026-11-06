package dbview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// myBackend talks to MariaDB and MySQL over the text protocol: arguments are
// put into the statement by the driver (interpolateParams), so values always
// come back as the server's text.
type myBackend struct {
	ctx     context.Context
	db      *sql.DB
	conn    *sql.Conn
	mariadb bool
	cache   []*srvTable
}

// The driver prints connection trouble to stderr; the error comes back anyway.
func init() { _ = mysql.SetLogger(log.New(io.Discard, "", 0)) }

func openMySQL(ctx context.Context, c ServerConn, write bool) (backend, *Error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.Net, cfg.Addr, cfg.DBName = c.User, c.Password, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port)), c.Database
	cfg.InterpolateParams = true
	cfg.ClientFoundRows = true // an UPDATE that sets the same values still counts the row
	cfg.Timeout = 5 * time.Second
	cfg.AllowNativePasswords = true
	conn, err := mysql.NewConnector(cfg)
	b := &myBackend{ctx: ctx, mariadb: c.Engine == EngineMariaDB}
	if err != nil {
		return nil, b.classify(err, write)
	}
	b.db = sql.OpenDB(conn)
	b.db.SetMaxOpenConns(1)
	if b.conn, err = b.db.Conn(ctx); err != nil {
		_ = b.db.Close()
		return nil, b.classify(err, write)
	}
	if _, err := b.conn.ExecContext(ctx, fmt.Sprintf("SET SESSION innodb_lock_wait_timeout = %d", int(BusyTimeout.Seconds()))); err != nil {
		b.close()
		return nil, b.classify(err, write)
	}
	return b, nil
}

func (b *myBackend) close() {
	if b.conn != nil {
		_ = b.conn.Close()
	}
	if b.db != nil {
		_ = b.db.Close()
	}
}

func (b *myBackend) quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (b *myBackend) qualified(t *srvTable) string { return b.quoteIdent(t.rel) }
func (b *myBackend) ph(int) string                { return "?" }
func (b *myBackend) arg(v any) any                { return v }
func (b *myBackend) likeOp() string               { return "LIKE" }

func (b *myBackend) textExpr(_ *srvTable, col string) string {
	return "CAST(" + b.quoteIdent(col) + " AS CHAR)"
}

func (b *myBackend) readKeywords() map[string]bool {
	return map[string]bool{"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESCRIBE": true, "DESC": true, "TABLE": true, "VALUES": true}
}

// myKind is how a value of a column type (the driver's name, or DATA_TYPE of
// information_schema) is read.
func myKind(typ string) colKind {
	typ = strings.TrimPrefix(strings.ToUpper(typ), "UNSIGNED ")
	switch typ {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT", "YEAR":
		return kindInt
	case "FLOAT", "DOUBLE", "REAL":
		return kindFloat
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT", "GEOMETRY",
		"POINT", "LINESTRING", "POLYGON", "MULTIPOINT", "MULTILINESTRING", "MULTIPOLYGON", "GEOMETRYCOLLECTION":
		return kindBytes
	}
	return kindText
}

func (b *myBackend) query(q string, args []any, row func(cols []string, vals []any) error) ([]string, int64, error) {
	rs, err := b.conn.QueryContext(b.ctx, q, args...)
	if err != nil {
		return nil, -1, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, -1, err
	}
	cts, err := rs.ColumnTypes()
	if err != nil {
		return nil, -1, err
	}
	kinds := make([]colKind, len(cts))
	for i, ct := range cts {
		kinds[i] = myKind(ct.DatabaseTypeName())
	}
	raw := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rs.Next() {
		if err := rs.Scan(ptrs...); err != nil {
			return cols, -1, err
		}
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
	return cols, -1, rs.Err()
}

func (b *myBackend) exec(q string, args []any) (int64, *int64, error) {
	r, err := b.conn.ExecContext(b.ctx, q, args...)
	if err != nil {
		return 0, nil, err
	}
	n, _ := r.RowsAffected()
	var last *int64
	if id, err := r.LastInsertId(); err == nil {
		last = &id
	}
	return n, last, nil
}

func (b *myBackend) readOnly() error {
	_, err := b.conn.ExecContext(b.ctx, "START TRANSACTION READ ONLY")
	return err
}

// openTransaction: MariaDB says so in @@in_transaction; MySQL lists the
// transaction of the connection in innodb_trx once it has done something.
func (b *myBackend) openTransaction() bool {
	var v sql.NullString
	q := "SELECT COUNT(*) FROM information_schema.INNODB_TRX WHERE trx_mysql_thread_id = CONNECTION_ID()"
	if b.mariadb {
		q = "SELECT @@in_transaction"
	}
	if err := b.conn.QueryRowContext(b.ctx, q).Scan(&v); err != nil {
		return false
	}
	return v.String != "0" && v.String != ""
}

func (b *myBackend) strs(q string, args ...any) ([][]string, error) {
	var out [][]string
	_, _, err := b.query(q, args, func(_ []string, vals []any) error {
		row := make([]string, len(vals))
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
			case []byte:
				row[i] = string(x)
			default:
				row[i] = fmt.Sprint(x)
			}
		}
		out = append(out, row)
		return nil
	})
	return out, err
}

func (b *myBackend) tables() ([]*srvTable, error) {
	if b.cache != nil {
		return b.cache, nil
	}
	rels, err := b.strs(`SELECT TABLE_NAME, TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	byName := map[string]*srvTable{}
	out := []*srvTable{}
	for _, r := range rels {
		t := &srvTable{rel: r[0], kinds: map[string]colKind{}, textCmp: map[string]bool{}}
		t.Name, t.Type = r[0], "table"
		if strings.Contains(r[1], "VIEW") {
			t.Type = "view"
		}
		t.Columns, t.Indexes = []Column{}, []Index{}
		byName[r[0]] = t
		out = append(out, t)
	}
	cols, err := b.strs(`SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA, DATA_TYPE
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	// COLUMN_DEFAULT is NULL (MySQL) or the word NULL (MariaDB) when there is none.
	defs, err := b.strs(`SELECT TABLE_NAME, COLUMN_NAME, COLUMN_DEFAULT IS NULL FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE()`)
	if err != nil {
		return nil, err
	}
	noDefault := map[string]bool{}
	for _, d := range defs {
		noDefault[d[0]+"\x00"+d[1]] = d[2] == "1"
	}
	for _, r := range cols {
		t := byName[r[0]]
		if t == nil {
			continue
		}
		extra := strings.ToUpper(r[5])
		c := Column{Name: r[1], Type: r[2], NotNull: r[3] == "NO", Generated: strings.Contains(extra, "GENERATED") && !strings.Contains(extra, "DEFAULT_GENERATED")}
		switch {
		case strings.Contains(extra, "AUTO_INCREMENT"):
			def := "auto_increment"
			c.Default = &def
		case !noDefault[r[0]+"\x00"+r[1]] && !(b.mariadb && r[4] == "NULL"):
			def := r[4]
			c.Default = &def
		}
		t.kinds[c.Name] = myKind(r[6])
		t.textCmp[c.Name] = t.kinds[c.Name] == kindText
		t.Columns = append(t.Columns, c)
	}
	idx, err := b.strs(`SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, INDEX_NAME <> 'PRIMARY', INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		return nil, err
	}
	for _, r := range idx {
		t := byName[r[0]]
		if t == nil {
			continue
		}
		n := len(t.Indexes)
		if n == 0 || t.Indexes[n-1].Name != r[1] {
			t.Indexes = append(t.Indexes, Index{Name: r[1], Unique: r[2] == "0", Columns: []string{}})
			n++
		}
		t.Indexes[n-1].Columns = append(t.Indexes[n-1].Columns, r[3])
		if r[1] == "PRIMARY" {
			t.pk = append(t.pk, r[3])
		}
	}
	for _, t := range out {
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

func (b *myBackend) info(i *Info) error {
	r, err := b.strs(`SELECT VERSION(), (SELECT COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE())`)
	if err != nil {
		return err
	}
	if len(r) == 1 {
		i.ServerVersion = r[0][0]
		i.Size, _ = strconv.ParseInt(r[0][1], 10, 64)
	}
	dbs, err := b.strs(`SELECT SCHEMA_NAME FROM information_schema.SCHEMATA
WHERE SCHEMA_NAME NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys') ORDER BY SCHEMA_NAME`)
	if err != nil {
		return err
	}
	for _, d := range dbs {
		i.Databases = append(i.Databases, d[0])
	}
	return nil
}

func (b *myBackend) count(t *srvTable, where string, args []any, limit time.Duration) (*int64, error) {
	q := "SELECT COUNT(*) FROM " + b.qualified(t) + where
	if b.mariadb {
		q = fmt.Sprintf("SET STATEMENT max_statement_time = %.3f FOR ", limit.Seconds()) + q
	} else {
		q = fmt.Sprintf("SELECT /*+ MAX_EXECUTION_TIME(%d) */ COUNT(*) FROM ", max(limit.Milliseconds(), 1)) + b.qualified(t) + where
	}
	var n *int64
	_, _, err := b.query(q, args, func(_ []string, vals []any) error {
		if v, ok := vals[0].(int64); ok {
			n = &v
		}
		return nil
	})
	if err != nil {
		var me *mysql.MySQLError
		if errors.As(err, &me) && (me.Number == 3024 || me.Number == 1969) && b.ctx.Err() == nil {
			return nil, nil
		}
		return nil, err
	}
	return n, nil
}

func (b *myBackend) classify(err error, write bool) *Error {
	if errors.Is(err, context.DeadlineExceeded) || (b.ctx != nil && b.ctx.Err() != nil) {
		return errf("db_timeout", "The query took too long and was stopped.", "Narrow it down with a filter or a LIMIT, or look at fewer rows.")
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 3024, 1969, 1317:
			return errf("db_timeout", "The query took too long and was stopped.", "Narrow it down with a filter or a LIMIT, or look at fewer rows.")
		case 1792:
			if write {
				return errf("db_read_only", "The database is read-only: "+me.Message, "")
			}
			return errf("db_read_only", "Turn on writes to change data.", "This query was run read-only. Run it with write on if you mean to change the database.")
		case 1205, 1213:
			return errf("db_busy", "The rows are in use by the app and stayed locked for 5 seconds.", "Try again in a moment.")
		case 1045:
			return errf("db_login_failed", "The database did not accept bitploy's login: "+me.Message+".",
				"The root password in the app's variables may have been changed after the database was made. Set it back to the one the database was made with.")
		case 1049:
			return errf("db_not_found", "There is no such database on this server.", "Pick one of the databases in the list.")
		}
		return errf("sql_error", me.Message, "")
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, mysql.ErrInvalidConn) {
		return errf("db_unreachable", "The database server does not answer.", "Check that the database app is running and healthy, then try again.")
	}
	return errf("sql_error", err.Error(), "")
}
