import assert from 'node:assert/strict'
import { test } from 'node:test'
import { parseDotenv } from '../src/lib/dotenv.ts'

test('a .env file becomes variables', () => {
  const r = parseDotenv(['# comment', '', 'A=1', 'export B = two words # note', "C='keep $this'", 'D="line\\nnext"', 'E="multi', 'line"', 'bad line', '1X=no', 'A=last'].join('\n'))
  assert.deepEqual(r.vars, [
    { name: 'A', value: 'last' },
    { name: 'B', value: 'two words' },
    { name: 'C', value: 'keep $this' },
    { name: 'D', value: 'line\nnext' },
    { name: 'E', value: 'multi\nline' },
  ])
  assert.deepEqual(r.skipped.map((s) => s.line), [9, 10])
})

test('an empty value is a value', () => {
  assert.deepEqual(parseDotenv('EMPTY=').vars, [{ name: 'EMPTY', value: '' }])
})
