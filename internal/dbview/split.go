package dbview

import (
	"strings"
)

// The SQL console runs a text with several statements one by one. SQLite has
// its own splitter (sqlite3_complete) but the driver does not offer it, so this
// is a small tokenizer: it knows comments, strings, quoted names and the
// BEGIN ... END body of a trigger, which holds semicolons of its own.

type tokKind int

const (
	tokWord  tokKind = iota // a keyword or a bare name, upper-cased
	tokOther                // a string, a quoted name, a number, punctuation
)

type token struct {
	kind  tokKind
	text  string // upper-cased for a word
	depth int    // parentheses open before it
}

// statement is one statement of the console text.
type statement struct {
	sql    string // as the user wrote it, without comments around it
	kind   string // SELECT, INSERT, UPDATE, DELETE, CREATE, ...
	tokens []token
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// splitStatements cuts sql at the semicolons that end a statement. Texts that
// hold nothing but whitespace and comments give no statement.
func splitStatements(sql string) []statement {
	var (
		out       []statement
		toks      []token
		start     = -1 // where the current statement's first token begins
		end       int  // where its last token ends
		depth     int
		inTrigger bool // CREATE TRIGGER seen in this statement
		inBody    bool // ... and its BEGIN
		caseDepth int
		prevIsEnd bool
	)
	flush := func() {
		if start >= 0 && len(toks) > 0 {
			out = append(out, statement{sql: sql[start:end], kind: kindOf(toks), tokens: toks})
		}
		toks, start, depth, inTrigger, inBody, caseDepth, prevIsEnd = nil, -1, 0, false, false, 0, false
	}
	add := func(k tokKind, text string, from, to int) {
		if start < 0 {
			start = from
		}
		end = to
		toks = append(toks, token{kind: k, text: text, depth: depth})
	}
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				i = len(sql)
			} else {
				i += 2 + j + 2
			}
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(sql) {
				if sql[j] == c {
					if j+1 < len(sql) && sql[j+1] == c { // doubled quote
						j += 2
						continue
					}
					break
				}
				j++
			}
			j = min(j+1, len(sql))
			add(tokOther, sql[i:j], i, j)
			prevIsEnd = false
			i = j
		case c == '[':
			j := strings.IndexByte(sql[i:], ']')
			if j < 0 {
				j = len(sql) - i - 1
			}
			add(tokOther, sql[i:i+j+1], i, i+j+1)
			prevIsEnd = false
			i += j + 1
		case isWordByte(c):
			j := i
			for j < len(sql) && isWordByte(sql[j]) {
				j++
			}
			w := strings.ToUpper(sql[i:j])
			add(tokWord, w, i, j)
			if len(toks) <= 3 && w == "TRIGGER" && toks[0].text == "CREATE" {
				inTrigger = true
			}
			if inTrigger {
				switch w {
				case "BEGIN":
					inBody = true
				case "CASE":
					if inBody {
						caseDepth++
					}
				case "END":
					if inBody && caseDepth > 0 {
						caseDepth--
						w = ""
					}
				}
			}
			prevIsEnd = w == "END"
			i = j
		case c == ';':
			if inTrigger && !(inBody && prevIsEnd && caseDepth == 0) {
				add(tokOther, ";", i, i+1)
				prevIsEnd = false
				i++
				continue
			}
			flush()
			i++
		default:
			if c == '(' {
				add(tokOther, "(", i, i+1)
				depth++
			} else if c == ')' {
				if depth > 0 {
					depth--
				}
				add(tokOther, ")", i, i+1)
			} else {
				add(tokOther, string(c), i, i+1)
			}
			prevIsEnd = false
			i++
		}
	}
	flush()
	return out
}

// kindOf names the statement by its first keyword. A WITH is named after the
// statement it leads into (a SELECT, or an INSERT, UPDATE or DELETE).
func kindOf(toks []token) string {
	first := ""
	for _, t := range toks {
		if t.kind == tokWord {
			first = t.text
			break
		}
	}
	switch first {
	case "WITH":
		for _, t := range toks {
			if t.kind == tokWord && t.depth == 0 {
				switch t.text {
				case "SELECT", "VALUES", "INSERT", "UPDATE", "DELETE", "REPLACE":
					return norm(t.text)
				}
			}
		}
		return "SELECT"
	case "":
		return "OTHER"
	}
	return norm(first)
}

func norm(k string) string {
	if k == "REPLACE" {
		return "INSERT"
	}
	return k
}

func (s statement) hasWord(w string) bool {
	for _, t := range s.tokens {
		if t.kind == tokWord && t.text == w {
			return true
		}
	}
	return false
}

// Kinds lists the kind of each statement of a SQL text, for the audit log
// (which never holds the SQL itself).
func Kinds(sql string) []string {
	var out []string
	for _, s := range splitStatements(sql) {
		out = append(out, strings.ToLower(s.kind))
	}
	return out
}
