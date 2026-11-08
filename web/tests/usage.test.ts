import assert from 'node:assert/strict'
import { test } from 'node:test'
import { usageBar } from '../src/lib/usage.ts'

test('with a max the bar is the max', () => {
  assert.deepEqual(usageBar(1024, 4096, 8192), { usedPct: 12.5, allocatedPct: 50, maxMarkPct: null, level: 'ok' })
})

test('near and at the max warn', () => {
  assert.equal(usageBar(0, 6144, 8192).level, 'warn')
  assert.equal(usageBar(0, 8192, 8192).level, 'full')
})

test('over the max fills the bar and marks the max', () => {
  const b = usageBar(512, 10240, 8192)
  assert.equal(b.allocatedPct, 100)
  assert.equal(b.maxMarkPct, 80)
  assert.equal(b.level, 'over')
})

test('without a max the allocation fills the bar', () => {
  assert.deepEqual(usageBar(964, 6144, null), { usedPct: (964 / 6144) * 100, allocatedPct: 100, maxMarkPct: null, level: 'ok' })
  assert.equal(usageBar(10, 20, 0).allocatedPct, 100)
})

test('nothing in use or allocated is an empty bar', () => {
  assert.deepEqual(usageBar(0, 0, null), { usedPct: 0, allocatedPct: 0, maxMarkPct: null, level: 'ok' })
  assert.deepEqual(usageBar(0, 0, 100), { usedPct: 0, allocatedPct: 0, maxMarkPct: null, level: 'ok' })
})

test('in use above the allocation widens the allocated part with it', () => {
  const b = usageBar(300, 200, 1000)
  assert.equal(b.usedPct, 30)
  assert.equal(b.allocatedPct, 30)
})
