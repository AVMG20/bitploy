import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectsOf, keys, mergeLines, parseEvent } from '../src/lib/live.ts'

test('a deployment event refreshes the app, its list and the deployments', () => {
  const fx = effectsOf({ type: 'deployment', app: 'blog', id: 'dep_1', state: 'building' })
  assert.deepEqual(fx.invalidate, [keys.apps, keys.app('blog'), keys.deployments('blog')])
  assert.deepEqual(fx.remove, [])
})

test('a finished deployment also refreshes the usage', () => {
  for (const state of ['running', 'failed', 'cancelled', 'superseded']) {
    const fx = effectsOf({ type: 'deployment', app: 'blog', id: 'dep_1', state })
    assert.ok(fx.invalidate.some((k) => k === keys.me), state)
  }
})

test('an app event refreshes everything of that app and the list', () => {
  const fx = effectsOf({ type: 'app', app: 'blog' })
  for (const k of [keys.apps, keys.me, keys.app('blog'), keys.deployments('blog'), keys.env('blog'), keys.domains('blog'), keys.backups('blog')]) {
    assert.ok(fx.invalidate.some((x) => JSON.stringify(x) === JSON.stringify(k)), JSON.stringify(k))
  }
})

test('a rename refreshes the old slug too', () => {
  const fx = effectsOf({ type: 'app', app: 'new-name', old_app: 'old-name' })
  const flat = fx.invalidate.map((k) => JSON.stringify(k))
  assert.ok(flat.includes(JSON.stringify(keys.app('new-name'))))
  assert.ok(flat.includes(JSON.stringify(keys.app('old-name'))))
})

test('a deleted app is forgotten, not refetched', () => {
  const fx = effectsOf({ type: 'app', app: 'blog', state: 'deleted' })
  assert.deepEqual(fx.invalidate, [keys.apps, keys.me])
  assert.ok(fx.remove.some((k) => JSON.stringify(k) === JSON.stringify(keys.app('blog'))))
})

test('backup and domain events refresh their own lists', () => {
  assert.ok(effectsOf({ type: 'backup', app: 'blog' }).invalidate.some((k) => k === keys.backups('blog') || JSON.stringify(k) === JSON.stringify(keys.backups('blog'))))
  assert.ok(effectsOf({ type: 'domain', app: 'blog' }).invalidate.some((k) => JSON.stringify(k) === JSON.stringify(keys.domains('blog'))))
})

test('resync refetches everything; hello and build_log touch no query', () => {
  assert.equal(effectsOf({ type: 'resync' }).all, true)
  for (const ev of [{ type: 'hello' }, { type: 'build_log', app: 'blog', id: 'dep_1', lines: ['x'] }] as const) {
    const fx = effectsOf(ev)
    assert.deepEqual(fx, { invalidate: [], remove: [] })
  }
})

test('parseEvent accepts known types only', () => {
  assert.deepEqual(parseEvent('{"type":"app","app":"blog"}'), { type: 'app', app: 'blog' })
  assert.equal(parseEvent('{"type":"nope"}'), null)
  assert.equal(parseEvent('not json'), null)
  assert.equal(parseEvent('null'), null)
})

test('mergeLines appends and does not repeat lines the log tail already has', () => {
  assert.deepEqual(mergeLines([], ['a', 'b']), ['a', 'b'])
  assert.deepEqual(mergeLines(['a', 'b'], ['c']), ['a', 'b', 'c'])
  assert.deepEqual(mergeLines(['a', 'b', 'c'], ['b', 'c', 'd']), ['a', 'b', 'c', 'd'])
  assert.deepEqual(mergeLines(['x', 'x'], ['x', 'y']), ['x', 'x', 'y'])
})

test('mergeLines keeps the newest lines when there are too many', () => {
  const base = Array.from({ length: 5 }, (_, i) => `l${i}`)
  assert.deepEqual(mergeLines(base, ['l5', 'l6'], 4), ['l3', 'l4', 'l5', 'l6'])
})

test('a queued or finished deployment refreshes the Dockerfile editor', () => {
  const has = (state: string) =>
    effectsOf({ type: 'deployment', app: 'blog', id: 'dep_1', state }).invalidate.some((k) => JSON.stringify(k) === JSON.stringify(keys.dockerfile('blog')))
  assert.ok(has('queued'))
  assert.ok(has('failed'))
  assert.ok(!has('building'))
})
