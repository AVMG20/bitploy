import assert from 'node:assert/strict'
import { test } from 'node:test'
import { change, formatSeconds, pointChange, share, shortShare, timeChange } from '../src/lib/traffic.ts'

test('an answer time gets slower or faster, not up or down', () => {
  assert.deepEqual(timeChange(107, 100), { text: '7% slower', words: '7% slower', tone: 'bad' })
  assert.deepEqual(timeChange(90, 100), { text: '10% faster', words: '10% faster', tone: 'good' })
  assert.deepEqual(timeChange(100, 100), { text: '0%', words: 'no change', tone: 'same' })
  assert.equal(timeChange(100, null), null)
})

test('an error rate changes in percentage points', () => {
  assert.deepEqual(pointChange(0.02, 0.01), { text: '↑ 1 pt', words: 'up 1 percentage point', tone: 'bad' })
  assert.deepEqual(pointChange(0.005, 0.02), { text: '↓ 1.5 pt', words: 'down 1.5 percentage points', tone: 'good' })
  assert.deepEqual(pointChange(0.0101, 0.01), { text: '0 pt', words: 'no change', tone: 'same' })
  assert.equal(pointChange(0.01, undefined), null)
})

test('answer times read as seconds, with the slowest bucket open-ended', () => {
  assert.equal(formatSeconds(4), 'under 0.01 s')
  assert.equal(formatSeconds(120), '0.12 s')
  assert.equal(formatSeconds(999), '1.0 s')
  assert.equal(formatSeconds(1450), '1.4 s')
  assert.equal(formatSeconds(5000), '5 s or more')
})

test('shares never round a real part to 0% or 100%', () => {
  assert.equal(share(0, 10), '0%')
  assert.equal(share(5, 0), '0%')
  assert.equal(share(1, 1000), 'under 1%')
  assert.equal(share(999, 1000), 'over 99%')
  assert.equal(share(10, 10), '100%')
  assert.equal(share(1, 4), '25%')
})

test('short shares fit a one-line summary', () => {
  assert.equal(shortShare(0, 10), '0%')
  assert.equal(shortShare(1, 1000), '<1%')
  assert.equal(shortShare(999, 1000), '>99%')
  assert.equal(shortShare(10, 10), '100%')
  assert.equal(shortShare(1, 4), '25%')
})

test('a change needs something to compare with', () => {
  assert.equal(change(10, null), null)
  assert.equal(change(10, undefined), null)
  assert.equal(change(10, 0), null)
  assert.equal(change(null, 10), null)
  assert.deepEqual(change(10, 10), { text: '0%', words: 'no change', tone: 'same' })
  assert.deepEqual(change(12, 10), { text: '↑ 20%', words: 'up 20%', tone: 'good' })
  assert.deepEqual(change(12, 10, true), { text: '↑ 20%', words: 'up 20%', tone: 'bad' })
  assert.deepEqual(change(5, 10, true), { text: '↓ 50%', words: 'down 50%', tone: 'good' })
  assert.equal(change(500, 1)!.text, '↑ 999+%')
})
