package mcp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
)

// What query_db hands a model, at most: rows over all statements, text in all,
// characters in one cell.
const (
	queryDBMaxRows  = 200
	queryDBMaxBytes = 50 << 10
	queryDBMaxCell  = 200
)

// dbCell is a value of a result as one line of text.
func dbCell(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		s = x
	case map[string]any:
		switch {
		case x["int"] != nil:
			return fmt.Sprint(x["int"])
		case x["real"] != nil:
			return fmt.Sprint(x["real"])
		case x["blob"] != nil:
			b, _ := base64.StdEncoding.DecodeString(fmt.Sprint(x["blob"]))
			if len(b) > 8 {
				b = b[:8]
			}
			more := ""
			if size, _ := strconv.Atoi(fmt.Sprint(x["size"])); size > len(b) {
				more = "..."
			}
			return fmt.Sprintf("<blob %v bytes: x'%s%s'>", x["size"], hex.EncodeToString(b), more)
		case x["text"] != nil:
			s = fmt.Sprint(x["text"])
			return oneLineCell(cutText(s, queryDBMaxCell)) + fmt.Sprintf("... (%v bytes)", x["size"])
		}
		s = fmt.Sprint(x)
	default:
		s = fmt.Sprint(x)
	}
	if len(s) > queryDBMaxCell {
		return oneLineCell(cutText(s, queryDBMaxCell)) + fmt.Sprintf("... (%d bytes)", len(s))
	}
	return oneLineCell(s)
}

// cutText keeps the first n bytes, not ending in the middle of a character.
func cutText(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// oneLineCell keeps a value on one line of a table.
func oneLineCell(s string) string {
	s = strings.NewReplacer("\r\n", `\n`, "\n", `\n`, "\r", `\n`, "\t", " ", "|", `\|`).Replace(s)
	return s
}

// formatSQLResults renders the results of the console as compact text: per
// statement a line with the SQL, then a table (or what a change did). Every
// line, headers included, is checked against the size limit before it is
// written; it stops at the row and size limits and says what is missing.
func formatSQLResults(rs []dbview.StatementResult) (text string, rowsShown, rowsTotal int, cut bool) {
	var b strings.Builder
	rowsLeft := queryDBMaxRows
	full := false
	// line writes one line when it fits. The last bit of room is kept for the note.
	line := func(l string) bool {
		if full || b.Len()+len(l)+1 > queryDBMaxBytes {
			full = true
			return false
		}
		b.WriteString(l)
		b.WriteByte('\n')
		return true
	}
	for i, r := range rs {
		sql := oneLineCell(strings.Join(strings.Fields(r.SQL), " "))
		if len(sql) > 120 {
			sql = cutText(sql, 120) + "..."
		}
		if !line(fmt.Sprintf("[%d] %s", i+1, sql)) {
			break
		}
		if len(r.Columns) == 0 {
			l := "OK"
			if r.RowsAffected != nil {
				l += fmt.Sprintf(", %d row(s) affected", *r.RowsAffected)
			}
			if r.LastInsertRowid != nil {
				l += fmt.Sprintf(", last insert rowid %d", *r.LastInsertRowid)
			}
			if !line(l) {
				break
			}
			continue
		}
		rowsTotal += len(r.Rows)
		if !line(cutText(strings.Join(r.Columns, " | "), 1000)) {
			break
		}
		shown := 0
		for _, row := range r.Rows {
			if rowsLeft == 0 {
				break
			}
			cells := make([]string, len(row))
			for j, v := range row {
				cells[j] = dbCell(v)
			}
			if !line(strings.Join(cells, " | ")) {
				break
			}
			shown++
			rowsLeft--
		}
		rowsShown += shown
		switch {
		case shown < len(r.Rows):
			cut = true
			line(fmt.Sprintf("... %d more row(s) not shown. Add WHERE or LIMIT, or select fewer columns.", len(r.Rows)-shown))
		case r.Truncated:
			cut = true
			line("... more rows exist (the first 1000 were read). Add WHERE or LIMIT.")
		case len(r.Rows) == 0:
			line("(no rows)")
		}
		if r.RowsAffected != nil {
			line(fmt.Sprintf("%d row(s) affected", *r.RowsAffected))
		}
		if full {
			break
		}
	}
	if full {
		cut = true
		b.WriteString("... output cut at 50 KB. Use WHERE or LIMIT, select fewer columns, or run the statements one by one.\n")
	}
	return strings.TrimRight(b.String(), "\n"), rowsShown, rowsTotal, cut
}

// serverDB reports whether app is a PostgreSQL, MySQL or MariaDB service,
// whose path is a database name instead of a file.
func serverDB(ctx context.Context, svc *app.Service, a app.Actor, ref string) bool {
	ap, err := svc.GetApp(ctx, a, ref)
	if err != nil || ap.Template == nil {
		return false
	}
	switch *ap.Template {
	case "postgres", "pgvector", "mysql", "mariadb":
		return true
	}
	return false
}

// linkedDatabases are the PostgreSQL, MySQL and MariaDB services of the
// app's project that its variables refer to (add_service connect_to).
func linkedDatabases(ctx context.Context, svc *app.Service, a app.Actor, ref string) []app.App {
	ap, err := svc.GetApp(ctx, a, ref)
	if err != nil || ap.Template != nil || len(ap.References) == 0 || ap.Project.ID == "" {
		return nil
	}
	p, err := svc.GetProject(ctx, a, ap.Project.ID)
	if err != nil {
		return nil
	}
	var out []app.App
	for _, other := range p.Apps {
		if slices.Contains(ap.References, other.Alias) && serverDB(ctx, svc, a, other.Slug) {
			out = append(out, other)
		}
	}
	return out
}

// listTablesSQL says how to list the tables of each kind of database.
const listTablesSQL = "SQLite: SELECT name FROM sqlite_schema WHERE type IN ('table','view'); PostgreSQL: SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema'); MySQL and MariaDB: SHOW TABLES."

// queryDBError turns an error of the database viewer into one for an AI: it
// says in which statement a request failed, and the hints name tools and
// SQL instead of the dashboard's lists. Statements before a failed one ran
// already; their results stay out of the error (they can be big).
func queryDBError(ctx context.Context, svc *app.Service, a app.Actor, in queryDBIn, write bool, err error) error {
	ae := apperr.As(err)
	if ae == nil {
		return err
	}
	redo := func(hint, next string) error {
		out := apperr.New(ae.HTTPStatus, ae.Code, ae.Message, hint)
		out.Details = ae.Details
		return out.WithNext(next)
	}
	// An app whose data is in a database service: the SQLite hints (pass a
	// path, turn on /data) would send the AI the wrong way.
	if (strings.TrimSpace(in.Path) == "" && ae.Code == "path_invalid") || ae.Code == "volume_off" {
		if dbs := linkedDatabases(ctx, svc, a, in.App); len(dbs) > 0 {
			names := make([]string, len(dbs))
			for i, d := range dbs {
				names[i] = d.Alias + " (app=" + d.Slug + ")"
			}
			tool := "query_db"
			if write {
				tool = "write_db"
			}
			out := apperr.New(ae.HTTPStatus, "db_in_service", in.App+" keeps its data in the database service "+strings.Join(names, ", ")+", not in a file of its own.",
				"Call "+tool+" with app="+dbs[0].Slug+" and leave path out (that is the database the service was made with).")
			return out.WithNext(tool)
		}
	}
	switch ae.Code {
	case "sql_error":
		if ae.Details == nil || ae.Details["statement"] == nil {
			return err
		}
		n := ae.Details["statement"]
		out := apperr.BadRequest("sql_error", fmt.Sprintf("Statement %v failed: %s", n, ae.Message),
			"Fix that statement and run it again. Statements before it already ran (each one is committed on its own, unless you opened a transaction with BEGIN).")
		out.HTTPStatus = ae.HTTPStatus
		out.Details = map[string]any{"statement": n}
		return out
	case "db_read_only":
		// The read-only console refused a change; the other db_read_only
		// errors (file permissions, bitploy's own database) stay as they are.
		if !write && strings.Contains(ae.Hint, "write on") {
			out := apperr.New(ae.HTTPStatus, ae.Code, "query_db is read-only, so nothing was changed.",
				"If the user asked for this change, run the same SQL with write_db (it needs a deploy token). Otherwise only read with SELECT.")
			out.Details = ae.Details
			return out.WithNext("write_db")
		}
	case "db_not_found":
		if serverDB(ctx, svc, a, in.App) {
			return redo("Leave path out to open the database the service was made with, or list its databases with query_db: SELECT datname FROM pg_database (PostgreSQL) or SHOW DATABASES (MySQL, MariaDB).", "query_db")
		}
		return redo("path is the SQLite file relative to /data (or to the volume's folder), like app.db. Find it where the app's code opens its database, or ask the user; do not try names one after another.", "get_app")
	case "not_sqlite":
		return redo("Point path at the SQLite file the app's code opens (often .db, .sqlite or .sqlite3; not the -wal, -shm or -journal file next to it). Ask the user when the code does not say.", "get_app")
	case "volume_off":
		return redo("Without /data the app has no SQLite file to look at. A database the code reaches over the network (a hosted one, set with set_env) is not reachable with query_db. Turn /data on (update_app volume=true) only when the app should keep a SQLite database there.", "get_app")
	case "path_invalid":
		if strings.TrimSpace(in.Path) == "" {
			return redo("Pass path: the SQLite file relative to /data, like app.db, from where the app's code opens its database.", "query_db")
		}
	case "table_not_found":
		return redo("List the tables with query_db first and use one of those names. "+listTablesSQL, "query_db")
	}
	return err
}
