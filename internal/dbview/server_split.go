package dbview

import "strings"

// srvStatement is one statement of a console text for PostgreSQL or MySQL.
type srvStatement struct {
	sql   string
	kind  string   // the first keyword, upper-cased
	words []string // the bare words, upper-cased, in order
}

// writesInto: a read of MySQL with INTO writes somewhere (INTO OUTFILE or
// DUMPFILE writes a file on the server). Read-only mode refuses every INTO,
// so a form the splitter does not know cannot slip through.
func (st srvStatement) writesInto() bool {
	for _, w := range st.words {
		if w == "INTO" || w == "OUTFILE" || w == "DUMPFILE" {
			return true
		}
	}
	return false
}

// splitServer cuts a console text at the semicolons that end a statement. It
// knows comments, strings, quoted names and, for PostgreSQL, dollar quotes
// (the bodies of functions) and E'...' strings; for MySQL, # comments, backslash
// escapes and backticks. Texts that hold only whitespace and comments give no
// statement.
//
// Comments follow the server: PostgreSQL nests /* */ comments; MySQL starts a
// -- comment only when whitespace or a control character follows, and runs
// the text of /*! ... */ and /*M! ... */ comments, so their words count.
func splitServer(sql string, mysql bool) []srvStatement {
	var out []srvStatement
	start, end := -1, 0
	var words []string
	flush := func() {
		if start >= 0 {
			st := srvStatement{sql: strings.TrimSpace(sql[start:end]), words: words}
			if len(words) > 0 {
				st.kind = words[0]
			}
			out = append(out, st)
		}
		start, words = -1, nil
	}
	mark := func(from, to int) {
		if start < 0 {
			start = from
		}
		end = to
	}
	n := len(sql)
	inCode := false // inside a MySQL /*! ... */ comment, whose text runs
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < n && sql[i+1] == '-' && (!mysql || i+2 >= n || sql[i+2] <= ' '), mysql && c == '#':
			for i < n && sql[i] != '\n' {
				i++
			}
		case mysql && inCode && c == '*' && i+1 < n && sql[i+1] == '/':
			inCode = false
			i += 2
		case mysql && c == '/' && i+2 < n && sql[i+1] == '*' && (sql[i+2] == '!' || (i+3 < n && sql[i+2] == 'M' && sql[i+3] == '!')):
			// The version number after the ! is not part of the text.
			i += 3
			if sql[i-1] == 'M' {
				i++
			}
			for i < n && sql[i] >= '0' && sql[i] <= '9' {
				i++
			}
			inCode = true
		case c == '/' && i+1 < n && sql[i+1] == '*':
			i = commentEnd(sql, i, !mysql)
		case c == ';':
			flush()
			i++
		case c == '\'' || c == '"' || (mysql && c == '`'):
			// E'...' of PostgreSQL and every string of MySQL know backslash escapes.
			esc := mysql && c != '`'
			if !mysql && c == '\'' && i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i < 2 || !isWordByte(sql[i-2])) {
				esc = true
			}
			j := i + 1
			for j < n {
				if esc && sql[j] == '\\' {
					j += 2
					continue
				}
				if sql[j] == c {
					if j+1 < n && sql[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, n)
			mark(i, j)
			i = j
		case !mysql && c == '$' && (i == 0 || !isWordByte(sql[i-1])):
			if tag, ok := dollarTag(sql[i:]); ok {
				j := strings.Index(sql[i+len(tag):], tag)
				if j < 0 {
					j = n
				} else {
					j = i + len(tag) + j + len(tag)
				}
				mark(i, j)
				i = j
				continue
			}
			mark(i, i+1)
			i++
		case isWordByte(c):
			j := i
			for j < n && isWordByte(sql[j]) {
				j++
			}
			words = append(words, strings.ToUpper(sql[i:j]))
			mark(i, j)
			i = j
		default:
			mark(i, i+1)
			i++
		}
	}
	flush()
	return out
}

// commentEnd is the index after the /* */ comment that starts at i, or the
// end of the text when it is not closed. PostgreSQL nests these comments.
func commentEnd(sql string, i int, nested bool) int {
	n, depth := len(sql), 0
	for i < n {
		switch {
		case i+1 < n && sql[i] == '/' && sql[i+1] == '*':
			depth++
			i += 2
			if !nested && depth > 1 {
				depth = 1
			}
		case i+1 < n && sql[i] == '*' && sql[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return n
}

// dollarTag reads the $tag$ that opens a dollar-quoted string, if s starts with one.
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '$':
			return s[:j+1], true
		case c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (j > 1 && c >= '0' && c <= '9'):
		default:
			return "", false
		}
	}
	return "", false
}
