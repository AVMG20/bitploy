// What the viewer reads out of a CREATE TABLE statement: the foreign keys, which
// the schema endpoint does not list. Best effort, for a label ("-> users.id"):
// it understands column constraints (`user_id INTEGER REFERENCES users(id)`) and
// table constraints (`FOREIGN KEY (a, b) REFERENCES t (x, y)`).
// Pure on purpose (only the tokenizer), so Node can run its test: tests/sqlSchema.test.ts.

import { tokenizeSql } from './sqlHighlight.ts'

export interface ForeignKeyRef {
  table: string
  /** Empty when the statement names no column (it then points at the primary key). */
  column: string
}

function unquote(s: string): string {
  const q = s[0]
  if ((q === '"' || q === '`') && s.length > 1) return s.slice(1, s[s.length - 1] === q ? -1 : undefined).replaceAll(q + q, q)
  if (q === '[') return s.slice(1, s.endsWith(']') ? -1 : undefined)
  return s
}

/** Foreign keys by column name: `{ user_id: { table: 'users', column: 'id' } }`. */
export function foreignKeys(createSql: string | null): Record<string, ForeignKeyRef> {
  const out: Record<string, ForeignKeyRef> = {}
  if (!createSql) return out
  const toks = tokenizeSql(createSql, false)
    .filter((t) => t.type !== 'comment' && t.text.trim() !== '')
    .map((t) => t.text)
  const open = toks.indexOf('(')
  if (open < 0) return out

  // The parts of the body, split at the commas that are not inside a bracket.
  const parts: string[][] = [[]]
  let depth = 0
  for (let i = open + 1; i < toks.length; i++) {
    const t = toks[i]!
    if (t === '(') depth++
    if (t === ')') {
      if (depth === 0) break
      depth--
    }
    if (t === ',' && depth === 0) parts.push([])
    else parts[parts.length - 1]!.push(t)
  }

  /** The names in "(a, b)" starting at index i; and the index after it. */
  const names = (p: string[], i: number): [string[], number] => {
    if (p[i] !== '(') return [[], i]
    const list: string[] = []
    let j = i + 1
    for (; j < p.length && p[j] !== ')'; j++) if (p[j] !== ',') list.push(unquote(p[j]!))
    return [list, j + 1]
  }
  const word = (p: string[], i: number) => (p[i] ?? '').toLowerCase()

  for (const p of parts) {
    let i = 0
    if (word(p, 0) === 'constraint') i = 2
    if (word(p, i) === 'foreign' && word(p, i + 1) === 'key') {
      const [cols, next] = names(p, i + 2)
      if (word(p, next) !== 'references' || !p[next + 1]) continue
      const table = unquote(p[next + 1]!)
      const [refs] = names(p, next + 2)
      cols.forEach((c, k) => (out[c] = { table, column: refs[k] ?? '' }))
      continue
    }
    // A column definition: its name, then anything, then maybe REFERENCES table (column).
    const at = p.findIndex((t, k) => k > 0 && t.toLowerCase() === 'references')
    if (at > 0 && p[at + 1] && !['primary', 'unique', 'check', 'key'].includes(word(p, 0))) {
      const [refs] = names(p, at + 2)
      out[unquote(p[0]!)] = { table: unquote(p[at + 1]!), column: refs[0] ?? '' }
    }
  }
  return out
}
