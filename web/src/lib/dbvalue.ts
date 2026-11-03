// The SQLite viewer's value encoding and small helpers. Pure on purpose (no Vue,
// no imports besides types), so Node can run its test: see tests/dbvalue.test.ts.
//
// A value travels as JSON: NULL is null, an INTEGER within +-2^53 and a REAL are
// numbers, TEXT is a string. What a number cannot carry is wrapped: {int: "..."}
// for big integers, {real: "NaN"} for NaN and infinity, {text, size, truncated}
// for text cut at 64 KiB and {blob: "<base64>", size, truncated} for a BLOB. The
// server only ever sends numbers it can show exactly, so JSON.parse loses nothing.

import type { DbBlob, DbColumn, DbInt, DbReal, DbTable, DbText, DbValue } from './types'

/** Is this file name one the viewer offers to open (a SQLite file, not its -wal, -shm or -journal side file)? */
export function isDbFileName(name: string): boolean {
  const n = name.toLowerCase()
  if (/-(wal|shm|journal)$/.test(n)) return false
  return /\.(db|sqlite|sqlite3|db3)$/.test(n)
}

const isObj = (v: DbValue): v is DbInt | DbReal | DbText | DbBlob => typeof v === 'object' && v !== null

export function isBlob(v: DbValue): v is DbBlob {
  return isObj(v) && 'blob' in v
}
export function isBigInt(v: DbValue): v is DbInt {
  return isObj(v) && 'int' in v
}
export function isRealWrapper(v: DbValue): v is DbReal {
  return isObj(v) && 'real' in v
}
export function isLongText(v: DbValue): v is DbText {
  return isObj(v) && 'text' in v
}

/** A value that was cut by the server. It cannot be sent back as a new value. */
export function isTruncated(v: DbValue): boolean {
  if (isLongText(v)) return true
  if (isBlob(v)) return v.truncated === true
  return false
}

export type ValueKind = 'null' | 'int' | 'real' | 'text' | 'blob'

export function kindOf(v: DbValue): ValueKind {
  if (v === null) return 'null'
  if (typeof v === 'number') return Number.isInteger(v) ? 'int' : 'real'
  if (typeof v === 'string' || isLongText(v)) return 'text'
  if (isBigInt(v)) return 'int'
  if (isRealWrapper(v)) return 'real'
  return 'blob'
}

// ---------------------------------------------------------------- base64

export function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function bytesToBase64(bytes: Uint8Array): string {
  let bin = ''
  const step = 0x8000
  for (let i = 0; i < bytes.length; i += step) bin += String.fromCharCode(...bytes.subarray(i, i + step))
  return btoa(bin)
}

/** The size in bytes of a BLOB value: the server's figure, or what the base64 holds. */
export function blobSize(v: DbBlob): number {
  if (typeof v.size === 'number') return v.size
  const pad = v.blob.endsWith('==') ? 2 : v.blob.endsWith('=') ? 1 : 0
  return Math.floor((v.blob.length * 3) / 4) - pad
}

/** A classic hex dump: offset, 16 bytes in hex, then the printable characters. */
export function hexDump(b64: string, maxBytes = 4096): string {
  const bytes = base64ToBytes(b64).subarray(0, maxBytes)
  const lines: string[] = []
  for (let i = 0; i < bytes.length; i += 16) {
    const row = Array.from(bytes.subarray(i, i + 16))
    const hex = row.map((b) => b.toString(16).padStart(2, '0')).join(' ')
    const ascii = row.map((b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : '.')).join('')
    lines.push(`${i.toString(16).padStart(8, '0')}  ${hex.padEnd(47, ' ')}  ${ascii}`)
  }
  return lines.join('\n')
}

// ---------------------------------------------------------------- showing values

export interface CellView {
  kind: ValueKind
  /** What the cell shows (cut to `max` characters). */
  text: string
  /** The full value is worth a look in the viewer (long, multi-line, a BLOB, or cut by the server). */
  expandable: boolean
}

/** A readable size: "812 B", "4.0 KB". Kept here so the helpers stay free of imports. */
export function sizeLabel(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export function cellView(v: DbValue, max = 200): CellView {
  const kind = kindOf(v)
  if (v === null) return { kind, text: 'NULL', expandable: false }
  if (typeof v === 'number') return { kind, text: String(v), expandable: false }
  if (typeof v === 'string') {
    const oneLine = v.length > max ? v.slice(0, max) : v
    return { kind, text: oneLine.replace(/\s+/g, ' ') + (v.length > max ? '...' : ''), expandable: v.length > 48 || v.includes('\n') }
  }
  if (isBigInt(v)) return { kind, text: v.int, expandable: false }
  if (isRealWrapper(v)) return { kind, text: v.real, expandable: false }
  if (isLongText(v)) return { kind, text: v.text.slice(0, max).replace(/\s+/g, ' ') + `... (${sizeLabel(v.size)})`, expandable: true }
  return { kind, text: `BLOB ${sizeLabel(blobSize(v))}`, expandable: true }
}

export interface FullView {
  kind: ValueKind
  text: string
  /** The size in bytes of a TEXT or BLOB value, when known. */
  size: number | null
  /** The server cut the value: `text` is only its beginning. */
  truncated: boolean
  /** `text` is a hex dump of a BLOB. */
  hex: boolean
}

export function fullView(v: DbValue): FullView {
  const kind = kindOf(v)
  if (v === null) return { kind, text: 'NULL', size: null, truncated: false, hex: false }
  if (typeof v === 'number') return { kind, text: String(v), size: null, truncated: false, hex: false }
  if (typeof v === 'string') return { kind, text: v, size: new TextEncoder().encode(v).length, truncated: false, hex: false }
  if (isBigInt(v)) return { kind, text: v.int, size: null, truncated: false, hex: false }
  if (isRealWrapper(v)) return { kind, text: v.real, size: null, truncated: false, hex: false }
  if (isLongText(v)) return { kind, text: v.text, size: v.size, truncated: true, hex: false }
  return { kind, text: hexDump(v.blob), size: blobSize(v), truncated: v.truncated === true, hex: true }
}

/** The text a copy action puts on the clipboard: the full value where the client has it. */
export function copyableText(v: DbValue): string {
  if (isBlob(v)) return v.blob
  return fullView(v).text
}

// ---------------------------------------------------------------- which database

/** Common names for the one database of an app, best first. `<slug>.db` comes last. */
const COMMON_DB_NAMES = ['app.db', 'data.db', 'database.db', 'db.sqlite', 'database.sqlite', 'sqlite.db', 'db.sqlite3', 'database.sqlite3']

/** The database to open from the Database tab: among the SQLite files of a folder, a common name wins, else the largest. Null when there is none. */
export function pickDatabase(items: { name: string; type: string; size: number }[], slug: string): string | null {
  const dbs = items.filter((f) => f.type === 'file' && isDbFileName(f.name))
  if (!dbs.length) return null
  for (const want of [...COMMON_DB_NAMES, `${slug}.db`]) {
    const hit = dbs.find((f) => f.name.toLowerCase() === want.toLowerCase())
    if (hit) return hit.name
  }
  return dbs.reduce((a, b) => (b.size > a.size ? b : a)).name
}

// ---------------------------------------------------------------- column types

export type Affinity = 'integer' | 'real' | 'numeric' | 'text' | 'blob'

/** SQLite's rules (https://sqlite.org/datatype3.html#determination_of_column_affinity). "blob" also means "no type": anything goes. */
export function affinityOf(declared: string): Affinity {
  const t = declared.toUpperCase()
  if (t.includes('INT')) return 'integer'
  if (/CHAR|CLOB|TEXT/.test(t)) return 'text'
  if (t === '' || t.includes('BLOB')) return 'blob'
  if (/REAL|FLOA|DOUB/.test(t)) return 'real'
  return 'numeric'
}

/** An INTEGER PRIMARY KEY of a rowid table is the rowid: left out of an insert, SQLite picks the next number. */
export function isRowidAlias(table: DbTable, col: DbColumn): boolean {
  if (table.without_rowid || col.pk !== 1 || col.type.toUpperCase() !== 'INTEGER') return false
  return table.columns.filter((c) => c.pk > 0).length === 1
}

// ---------------------------------------------------------------- typing values

const MAX_SAFE = BigInt(Number.MAX_SAFE_INTEGER)
const INT64_MAX = 9223372036854775807n
const INT64_MIN = -9223372036854775808n

/** An integer literal as a JSON value: a number when it is exact, {int} when not. Null beyond 64 bits. */
export function intValue(text: string): DbValue | null {
  let n: bigint
  try {
    n = BigInt(text.trim())
  } catch {
    return null
  }
  if (n > INT64_MAX || n < INT64_MIN) return null
  if (n <= MAX_SAFE && n >= -MAX_SAFE) return Number(n)
  return { int: n.toString() }
}

const INT_RE = /^[+-]?\d+$/
const NUM_RE = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/

export type Parsed = { ok: true; value: DbValue } | { ok: false; error: string }

/** What the user typed for a column of this affinity, as a value to send. Text stays text; a number in a number column becomes one. */
export function parseInput(aff: Affinity, raw: string): Parsed {
  if (aff === 'text' || aff === 'blob') return { ok: true, value: raw }
  const t = raw.trim()
  if (t === '') return aff === 'numeric' ? { ok: true, value: raw } : { ok: false, error: 'Enter a number, or use NULL.' }
  if (INT_RE.test(t)) {
    const v = intValue(t)
    if (v === null) return { ok: false, error: 'This number is too big for SQLite.' }
    return { ok: true, value: v }
  }
  if (NUM_RE.test(t)) {
    const n = Number(t)
    return Number.isFinite(n) ? { ok: true, value: n } : { ok: false, error: 'This number is too big.' }
  }
  if (aff === 'real') {
    const w = t.toLowerCase()
    if (w === 'nan') return { ok: true, value: { real: 'NaN' } }
    if (/^\+?inf(inity)?$/.test(w)) return { ok: true, value: { real: 'Inf' } }
    if (/^-inf(inity)?$/.test(w)) return { ok: true, value: { real: '-Inf' } }
  }
  if (aff === 'numeric') return { ok: true, value: raw }
  return { ok: false, error: aff === 'integer' ? 'Enter a whole number, or use NULL.' : 'Enter a number, or use NULL.' }
}

/** A cell that was NULL in a column of no fixed type: a clean number (no leading zeros or plus) is a number, anything else text. */
export function guessKind(text: string): InputKind {
  return /^-?(0|[1-9]\d*)(\.\d+)?$/.test(text.trim()) ? 'number' : 'text'
}

/** What a column without a fixed type holds: a typed value is kept as a number or as text, whatever is typed. */
export type InputKind = 'text' | 'number'

/** The kind to start an edit with: a number stays a number. */
export function kindFromValue(v: DbValue): InputKind {
  return typeof v === 'number' || isBigInt(v) || isRealWrapper(v) ? 'number' : 'text'
}

/** The affinity to parse with: in a column of no fixed type, the person's choice (text or number) decides. */
export function effectiveAffinity(aff: Affinity, kind: InputKind): Affinity {
  return aff === 'blob' && kind === 'number' ? 'real' : aff
}

/** The values to send as `match` (or part of it): the row as it was loaded, without what the server cannot compare (cut values, hidden and generated columns). */
export function matchValues(columns: DbColumn[], values: Record<string, DbValue>): Record<string, DbValue> {
  const out: Record<string, DbValue> = {}
  for (const c of columns) {
    if (c.hidden || c.generated || !(c.name in values)) continue
    const v = values[c.name] ?? null
    if (!isTruncated(v)) out[c.name] = v
  }
  return out
}

/** The text a form field starts with when it edits this value. */
export function inputText(v: DbValue): string {
  if (v === null || isBlob(v)) return ''
  if (typeof v === 'number') return String(v)
  if (typeof v === 'string') return v
  if (isBigInt(v)) return v.int
  if (isRealWrapper(v)) return v.real
  return v.text
}

/** A filter value: a number where the text is one (the column's affinity does the rest), else the text. */
export function filterValue(raw: string): DbValue {
  const t = raw.trim()
  if (INT_RE.test(t)) return intValue(t) ?? raw
  if (NUM_RE.test(t) && Number.isFinite(Number(t))) return Number(t)
  return raw
}

/** Do two values show the same thing? Used to send only the fields an edit changed. */
export function sameValue(a: DbValue, b: DbValue): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** "1 row", "12 rows"; with a search or filters on, "1 row matches", "12 rows match". */
export function rowsLabel(total: number, filtered: boolean): string {
  return `${total.toLocaleString('en-US')} ${total === 1 ? 'row' : 'rows'}${filtered ? (total === 1 ? ' matches' : ' match') : ''}`
}

/** An identity for a row key, to tell selected rows apart. */
export function keyId(key: Record<string, DbValue>): string {
  return JSON.stringify(Object.keys(key).sort().map((k) => [k, key[k]]))
}

// ---------------------------------------------------------------- SQL history

export const HISTORY_MAX = 20
export const HISTORY_KEY_PREFIX = 'bitploy:db-sql-history:'

/** Forget the remembered queries (they can hold data): on logout, or when the session is gone. */
export function clearSqlHistory(): void {
  try {
    for (const k of Object.keys(sessionStorage)) if (k.startsWith(HISTORY_KEY_PREFIX)) sessionStorage.removeItem(k)
  } catch {
    // storage blocked: nothing was kept
  }
}

/** The new query first, an earlier copy of it dropped, at most HISTORY_MAX kept. */
export function pushHistory(list: string[], sql: string, max = HISTORY_MAX): string[] {
  const s = sql.trim()
  if (!s) return list
  return [s, ...list.filter((q) => q !== s)].slice(0, max)
}

/** Does the SQL contain a statement that probably changes data or schema? A hint for the UI, the server decides. */
export function looksLikeWrite(sql: string): boolean {
  const stripped = sql.replace(/--[^\n]*/g, ' ').replace(/\/\*[\s\S]*?\*\//g, ' ')
  return /\b(insert|update|delete|replace|create|drop|alter|vacuum|reindex|pragma|attach|detach)\b/i.test(stripped)
}
