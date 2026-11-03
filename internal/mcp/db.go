package mcp

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

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

// queryDBError says in which statement a console request failed. Statements
// before it ran already; their results stay out of the error (they can be big).
func queryDBError(err error) error {
	ae := apperr.As(err)
	if ae.Code != "sql_error" || ae.Details == nil || ae.Details["statement"] == nil {
		return err
	}
	n := ae.Details["statement"]
	out := apperr.BadRequest("sql_error", fmt.Sprintf("Statement %v failed: %s", n, ae.Message),
		"Fix that statement and run it again. Statements before it already ran (each one is committed on its own, unless you opened a transaction with BEGIN).")
	out.HTTPStatus = ae.HTTPStatus
	out.Details = map[string]any{"statement": n}
	return out
}
