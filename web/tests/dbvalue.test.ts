import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  affinityOf,
  base64ToBytes,
  blobSize,
  bytesToBase64,
  cellView,
  clearSqlHistory,
  effectiveAffinity,
  filterValue,
  fullView,
  hexDump,
  inputText,
  intValue,
  isDbFileName,
  isTruncated,
  keyId,
  kindFromValue,
  looksLikeWrite,
  matchValues,
  parseInput,
  pickDatabase,
  pushHistory,
  rowsLabel,
  sameValue,
} from '../src/lib/dbvalue.ts'

test('detects SQLite files by name, not their side files', () => {
  for (const n of ['app.db', 'APP.DB', 'a.sqlite', 'a.sqlite3', 'a.db3', 'my.data.Sqlite']) assert.equal(isDbFileName(n), true, n)
  for (const n of ['app.db-wal', 'app.db-shm', 'app.sqlite-journal', 'app.sqlite3-wal', 'notes.txt', 'db', '.db.bak', 'dump.sql']) assert.equal(isDbFileName(n), false, n)
})

test('big integers keep every digit', () => {
  assert.equal(intValue('42'), 42)
  assert.equal(intValue('-9007199254740991'), -9007199254740991)
  assert.deepEqual(intValue('9007199254740993'), { int: '9007199254740993' })
  assert.deepEqual(intValue('-9223372036854775808'), { int: '-9223372036854775808' })
  assert.deepEqual(intValue('+9223372036854775807'), { int: '9223372036854775807' })
  assert.equal(intValue('9223372036854775808'), null)
  assert.equal(intValue('abc'), null)
})

test('parses typed input by column affinity', () => {
  assert.equal(affinityOf('INTEGER'), 'integer')
  assert.equal(affinityOf('bigint'), 'integer')
  assert.equal(affinityOf('VARCHAR(20)'), 'text')
  assert.equal(affinityOf(''), 'blob')
  assert.equal(affinityOf('BLOB'), 'blob')
  assert.equal(affinityOf('DOUBLE PRECISION'), 'real')
  assert.equal(affinityOf('DATETIME'), 'numeric')

  assert.deepEqual(parseInput('integer', ' 7 '), { ok: true, value: 7 })
  assert.deepEqual(parseInput('integer', '9007199254740993'), { ok: true, value: { int: '9007199254740993' } })
  assert.equal(parseInput('integer', '').ok, false)
  assert.equal(parseInput('integer', 'abc').ok, false)
  assert.equal(parseInput('integer', '99999999999999999999').ok, false)
  assert.deepEqual(parseInput('real', '1.5'), { ok: true, value: 1.5 })
  assert.deepEqual(parseInput('real', '-inf'), { ok: true, value: { real: '-Inf' } })
  assert.deepEqual(parseInput('real', 'NaN'), { ok: true, value: { real: 'NaN' } })
  assert.deepEqual(parseInput('text', '12'), { ok: true, value: '12' })
  assert.deepEqual(parseInput('text', ''), { ok: true, value: '' })
  assert.deepEqual(parseInput('numeric', '2024-01-01'), { ok: true, value: '2024-01-01' })
  assert.deepEqual(parseInput('numeric', '3'), { ok: true, value: 3 })
})

test('filter values are numbers where they look like one', () => {
  assert.equal(filterValue('5'), 5)
  assert.equal(filterValue('2.5'), 2.5)
  assert.equal(filterValue('%@x.nl'), '%@x.nl')
  assert.deepEqual(filterValue('9007199254740993'), { int: '9007199254740993' })
})

test('shows values for a cell and in full', () => {
  assert.deepEqual(cellView(null), { kind: 'null', text: 'NULL', expandable: false })
  assert.equal(cellView(3).text, '3')
  assert.equal(cellView({ int: '9007199254740993' }).text, '9007199254740993')
  assert.equal(cellView('short').expandable, false)
  assert.equal(cellView('a\nb').expandable, true)
  assert.equal(cellView('x'.repeat(300)).text.length, 203)
  const long = cellView({ text: 'abc', size: 100000, truncated: true })
  assert.equal(long.expandable, true)
  assert.match(long.text, /97\.7 KB/)
  const blob = cellView({ blob: 'AAEC', size: 3, truncated: false })
  assert.equal(blob.text, 'BLOB 3 B')
  assert.equal(blob.kind, 'blob')

  const full = fullView({ blob: bytesToBase64(new Uint8Array([0x68, 0x69, 0, 255])), size: 4, truncated: false })
  assert.equal(full.hex, true)
  assert.match(full.text, /^00000000 {2}68 69 00 ff/)
  assert.match(full.text, /hi\.\.$/)
  assert.equal(fullView('héllo').size, 6)
})

test('knows which values were cut', () => {
  assert.equal(isTruncated({ text: 'x', size: 70000, truncated: true }), true)
  assert.equal(isTruncated({ blob: 'AA==', size: 9000, truncated: true }), true)
  assert.equal(isTruncated({ blob: 'AA==', size: 1, truncated: false }), false)
  assert.equal(isTruncated('text'), false)
  assert.equal(isTruncated(null), false)
})

test('base64 round trip and blob size', () => {
  const bytes = new Uint8Array(70000).map((_, i) => i % 256)
  const b64 = bytesToBase64(bytes)
  assert.deepEqual(base64ToBytes(b64), bytes)
  assert.equal(blobSize({ blob: b64 }), 70000)
  assert.equal(blobSize({ blob: 'AAA=', size: 99 }), 99)
  assert.equal(hexDump('').length, 0)
})

test('edit helpers', () => {
  assert.equal(inputText(null), '')
  assert.equal(inputText({ int: '9007199254740993' }), '9007199254740993')
  assert.equal(inputText(1.5), '1.5')
  assert.equal(inputText({ blob: 'AA==' }), '')
  assert.equal(sameValue({ int: '1' }, { int: '1' }), true)
  assert.equal(sameValue(1, '1'), false)
  assert.equal(keyId({ b: 1, a: 'x' }), keyId({ a: 'x', b: 1 }))
  assert.notEqual(keyId({ rowid: 1 }), keyId({ rowid: 2 }))
})

test('history keeps the newest 20, without copies', () => {
  let h: string[] = []
  for (let i = 0; i < 25; i++) h = pushHistory(h, `select ${i}`)
  assert.equal(h.length, 20)
  assert.equal(h[0], 'select 24')
  h = pushHistory(h, '  select 10  ')
  assert.equal(h[0], 'select 10')
  assert.equal(h.filter((q) => q === 'select 10').length, 1)
  assert.deepEqual(pushHistory(['a'], '   '), ['a'])
})

test('spots statements that look like writes', () => {
  assert.equal(looksLikeWrite('SELECT * FROM t'), false)
  assert.equal(looksLikeWrite('-- delete everything\nselect 1'), false)
  assert.equal(looksLikeWrite('/* update */ select 1'), false)
  assert.equal(looksLikeWrite('delete from t'), true)
  assert.equal(looksLikeWrite('select 1; DROP TABLE t'), true)
})

test('a column of no type keeps a number a number, and text text', () => {
  // 42 -> 43 in a column without a declared type must not become the text '43'
  assert.equal(affinityOf(''), 'blob')
  assert.equal(kindFromValue(42), 'number')
  assert.equal(kindFromValue(1.5), 'number')
  assert.equal(kindFromValue({ int: '9007199254740993' }), 'number')
  assert.equal(kindFromValue({ real: 'NaN' }), 'number')
  assert.equal(kindFromValue('42'), 'text')
  assert.equal(kindFromValue(null), 'text')
  const asNumber = effectiveAffinity('blob', kindFromValue(42))
  assert.deepEqual(parseInput(asNumber, '43'), { ok: true, value: 43 })
  assert.deepEqual(parseInput(asNumber, '9007199254740993'), { ok: true, value: { int: '9007199254740993' } })
  assert.deepEqual(parseInput(asNumber, '2.5'), { ok: true, value: 2.5 })
  assert.equal(parseInput(asNumber, 'abc').ok, false)
  // text stays text, even when it looks like a number ("007")
  assert.deepEqual(parseInput(effectiveAffinity('blob', kindFromValue('007')), '007'), { ok: true, value: '007' })
  // typed columns are not affected by the choice
  assert.equal(effectiveAffinity('text', 'number'), 'text')
  assert.equal(effectiveAffinity('integer', 'text'), 'integer')
})

test('match values leave out cut, hidden and generated columns', () => {
  const col = (name: string, extra: object = {}) => ({ name, type: '', notnull: false, default: null, pk: 0, hidden: false, generated: false, ...extra })
  const columns = [col('id'), col('bio'), col('photo'), col('full', { generated: true }), col('rank', { hidden: true })]
  const values = {
    id: { int: '9007199254740993' },
    bio: { text: 'cut', size: 70000, truncated: true as const },
    photo: { blob: 'AA==', size: 1, truncated: false },
    full: 'x',
    rank: 1,
    other: 'not a column',
  }
  assert.deepEqual(matchValues(columns, values), { id: { int: '9007199254740993' }, photo: { blob: 'AA==', size: 1, truncated: false } })
  assert.deepEqual(matchValues(columns, {}), {})
})

test('clearing the SQL history without a browser does not throw', () => {
  assert.doesNotThrow(() => clearSqlHistory())
})

test('row count wording', () => {
  assert.equal(rowsLabel(0, false), '0 rows')
  assert.equal(rowsLabel(1, false), '1 row')
  assert.equal(rowsLabel(1, true), '1 row matches')
  assert.equal(rowsLabel(2, true), '2 rows match')
  assert.equal(rowsLabel(1234, true), '1,234 rows match')
  assert.equal(rowsLabel(0, true), '0 rows match')
})

test('picks the main database at the top of /data', () => {
  const f = (name: string, size = 1, type = 'file') => ({ name, size, type })
  assert.equal(pickDatabase([], 'blog'), null)
  assert.equal(pickDatabase([f('notes.txt', 99), f('app.db-wal', 99), f('dir.db', 5, 'dir')], 'blog'), null)
  // a common name beats a bigger file; the list's order is the preference
  assert.equal(pickDatabase([f('big.sqlite', 9000), f('data.db', 10), f('app.db', 5)], 'blog'), 'app.db')
  assert.equal(pickDatabase([f('big.sqlite', 9000), f('Database.DB', 10)], 'blog'), 'Database.DB')
  assert.equal(pickDatabase([f('big.sqlite', 9000), f('blog.db', 10)], 'blog'), 'blog.db')
  // otherwise the largest one
  assert.equal(pickDatabase([f('a.sqlite', 10), f('b.db3', 500), f('c.db', 20)], 'blog'), 'b.db3')
})
