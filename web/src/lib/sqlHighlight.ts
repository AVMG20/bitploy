// A small SQL tokenizer for syntax colours (SQLite flavour). It returns tokens, not
// HTML: the view renders each one as a text node in a span, so nothing from the
// SQL can ever become markup. The tokens always add up to the input, character
// for character, which is what lets the console lay them under its textarea.
// Pure on purpose (no Vue), so Node can run its test: tests/sqlHighlight.test.ts.

export type SqlTokenType = 'keyword' | 'string' | 'identifier' | 'number' | 'comment' | 'operator' | 'punct' | 'text'

export interface SqlToken {
  type: SqlTokenType
  text: string
}

const KEYWORDS = new Set(
  (
    'abort action add after all alter always analyze and as asc attach autoincrement before begin between by cascade case cast check collate column commit conflict constraint create cross ' +
    'current current_date current_time current_timestamp database default deferrable deferred delete desc detach distinct do drop each else end escape except exclusive exists explain fail filter first ' +
    'following for foreign from full generated glob group groups having if ignore immediate in index indexed initially inner insert instead intersect into is isnull join key last left like limit match ' +
    'materialized natural no not nothing notnull null nulls of offset on or order others outer over partition plan pragma preceding primary query raise range recursive references regexp reindex release ' +
    'rename replace restrict returning right rollback row rows savepoint select set table temp temporary then ties to transaction trigger unbounded union unique update using vacuum values view virtual ' +
    'when where window with without true false'
  ).split(' '),
)

const WHITESPACE = /\s+/y
const WORD = /[A-Za-z_][A-Za-z0-9_$]*/y
const NUMBER = /(?:0[xX][0-9a-fA-F]+|\d+\.?\d*(?:[eE][+-]?\d+)?|\.\d+(?:[eE][+-]?\d+)?)/y
const OPERATOR = /[<>=!|&+\-*/%~]+/y

/** The end of a quoted run that starts at `from` (the opening quote): a doubled quote is an escaped one. An unterminated run goes to the end. */
function endOfQuoted(sql: string, from: number, quote: string): number {
  let i = from + 1
  while (i < sql.length) {
    if (sql[i] === quote) {
      if (sql[i + 1] === quote) i += 2
      else return i + 1
    } else i++
  }
  return sql.length
}

function match(re: RegExp, sql: string, at: number): string | null {
  re.lastIndex = at
  const m = re.exec(sql)
  return m ? m[0] : null
}

/** `merge: false` keeps every word and space apart (for code that reads the SQL); the default joins neighbouring plain text. */
export function tokenizeSql(sql: string, merge = true): SqlToken[] {
  const out: SqlToken[] = []
  const push = (type: SqlTokenType, text: string) => {
    const last = out[out.length - 1]
    // Neighbouring plain text (names, spaces) is one token.
    if (merge && last && type === 'text' && last.type === 'text') last.text += text
    else out.push({ type, text })
  }
  let i = 0
  while (i < sql.length) {
    const c = sql[i]!
    const next = sql[i + 1]
    let end: number
    let m: string | null
    if ((m = match(WHITESPACE, sql, i))) {
      push('text', m)
      i += m.length
    } else if (c === '-' && next === '-') {
      end = sql.indexOf('\n', i)
      end = end === -1 ? sql.length : end
      push('comment', sql.slice(i, end))
      i = end
    } else if (c === '/' && next === '*') {
      end = sql.indexOf('*/', i + 2)
      end = end === -1 ? sql.length : end + 2
      push('comment', sql.slice(i, end))
      i = end
    } else if (c === "'") {
      end = endOfQuoted(sql, i, "'")
      push('string', sql.slice(i, end))
      i = end
    } else if (c === '"' || c === '`') {
      end = endOfQuoted(sql, i, c)
      push('identifier', sql.slice(i, end))
      i = end
    } else if (c === '[') {
      end = sql.indexOf(']', i + 1)
      end = end === -1 ? sql.length : end + 1
      push('identifier', sql.slice(i, end))
      i = end
    } else if ((m = match(WORD, sql, i))) {
      push(KEYWORDS.has(m.toLowerCase()) ? 'keyword' : 'text', m)
      i += m.length
    } else if ((m = match(NUMBER, sql, i))) {
      push('number', m)
      i += m.length
    } else if ((m = match(OPERATOR, sql, i))) {
      push('operator', m)
      i += m.length
    } else if ('(),;.'.includes(c)) {
      push('punct', c)
      i++
    } else {
      push('text', c)
      i++
    }
  }
  return out
}
