import assert from 'node:assert/strict'
import { test } from 'node:test'
import { tokenizeSql } from '../src/lib/sqlHighlight.ts'

const plain = (sql: string) => tokenizeSql(sql).map((t) => t.text).join('')
const kinds = (sql: string) => tokenizeSql(sql).filter((t) => t.type !== 'text').map((t) => `${t.type}:${t.text}`)

test('the tokens add up to the input, whatever it is', () => {
  const samples = [
    '',
    'SELECT * FROM t WHERE a = 1;',
    "select 'it''s', \"a\"\"b\", `c`, [d e] -- note\nfrom x /* c */",
    "'unterminated",
    '/* unterminated',
    '"unterminated',
    '[unterminated',
    '-- only a comment',
    'a->b <> c != d || e <= f',
    '<script>alert(1)</script>   \ttab',
    'x'.repeat(1000),
  ]
  for (const s of samples) assert.equal(plain(s), s, s)
})

test('keywords are found in any case, names and words with digits are not keywords', () => {
  assert.deepEqual(kinds('Select name FROM t1 where selected'), ['keyword:Select', 'keyword:FROM', 'keyword:where'])
  assert.deepEqual(kinds('INSERT INTO "from"(a) VALUES (1)'), ['keyword:INSERT', 'keyword:INTO', 'identifier:"from"', 'punct:(', 'punct:)', 'keyword:VALUES', 'punct:(', 'number:1', 'punct:)'])
})

test('strings keep doubled quotes inside them', () => {
  assert.deepEqual(kinds("SELECT 'it''s ok', 'b'"), ['keyword:SELECT', "string:'it''s ok'", 'punct:,', "string:'b'"])
  assert.deepEqual(kinds('"a""b" `c` [d e]'), ['identifier:"a""b"', 'identifier:`c`', 'identifier:[d e]'])
})

test('numbers: integers, decimals, exponents, hex; not the digits of a name', () => {
  assert.deepEqual(kinds('1 2.5 .5 1e10 1.5E-3 0xFF'), ['number:1', 'number:2.5', 'number:.5', 'number:1e10', 'number:1.5E-3', 'number:0xFF'])
  assert.deepEqual(kinds('col2 _x9'), [])
})

test('comments run to the end of the line, or to the closing mark', () => {
  assert.deepEqual(kinds('a -- x\nb /* y\nz */ c'), ['comment:-- x', 'comment:/* y\nz */'])
  assert.deepEqual(kinds('SELECT 1 -- the end'), ['keyword:SELECT', 'number:1', 'comment:-- the end'])
  // a comment mark inside a string is not a comment
  assert.deepEqual(kinds("SELECT '-- no', 2"), ['keyword:SELECT', "string:'-- no'", 'punct:,', 'number:2'])
})

test('unterminated strings, identifiers and comments take the rest, and nothing hangs', () => {
  assert.deepEqual(kinds("SELECT 'abc"), ['keyword:SELECT', "string:'abc"])
  assert.deepEqual(kinds('SELECT "abc'), ['keyword:SELECT', 'identifier:"abc'])
  assert.deepEqual(kinds('SELECT /* abc'), ['keyword:SELECT', 'comment:/* abc'])
  assert.deepEqual(kinds('SELECT [abc'), ['keyword:SELECT', 'identifier:[abc'])
  assert.deepEqual(kinds("'a'' "), ["string:'a'' "])
})

test('markup in SQL stays plain data', () => {
  const t = tokenizeSql('<img src=x onerror=alert(1)>')
  assert.equal(t.map((x) => x.text).join(''), '<img src=x onerror=alert(1)>')
  assert.ok(t.every((x) => x.type !== 'string' && x.type !== 'comment'))
})
