import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isPortError, runChanges, runErrors, runFieldOfError, runFieldsFor, runFormOf } from '../src/lib/runSettings.ts'

const app = {
  type: 'web' as const,
  port: null,
  health_path: '/',
  dockerfile_path: 'Dockerfile',
  context_dir: '.',
  build_mode: 'dockerfile' as const,
  static: false,
}

test('fields shown only where they apply', () => {
  assert.deepEqual(runFieldsFor(app, 'web'), { type: true, port: true, health_path: true, dockerfile_path: true, context_dir: true })
  // A worker has no port and no health page.
  assert.deepEqual(runFieldsFor(app, 'worker'), { type: true, port: false, health_path: false, dockerfile_path: true, context_dir: true })
  // bitploy builds it itself: no Dockerfile path or folder.
  const auto = runFieldsFor({ ...app, build_mode: 'auto' }, 'web')
  assert.equal(auto.dockerfile_path, false)
  assert.equal(auto.port, true)
  // A static site: nothing but what bitploy decides.
  assert.deepEqual(runFieldsFor({ ...app, build_mode: 'static_build' }, 'web'), { type: false, port: false, health_path: false, dockerfile_path: false, context_dir: false })
  assert.equal(runFieldsFor({ ...app, static: true }, 'web').dockerfile_path, false)
})

test('validation in plain words', () => {
  const shown = runFieldsFor(app, 'web')
  assert.deepEqual(runErrors(runFormOf(app), shown), {})
  const bad = runErrors({ type: 'web', port: '70000', health_path: 'health', dockerfile_path: '/etc/Dockerfile', context_dir: '../up' }, shown)
  assert.deepEqual(Object.keys(bad).sort(), ['context_dir', 'dockerfile_path', 'health_path', 'port'])
  assert.match(runErrors({ ...runFormOf(app), port: 'abc' }, shown).port!, /number/)
  // A hidden field is not checked.
  assert.deepEqual(runErrors({ ...runFormOf(app), port: 'abc' }, runFieldsFor(app, 'worker')), {})
})

test('only changed fields are sent; an empty port clears it', () => {
  const shown = runFieldsFor(app, 'web')
  assert.deepEqual(runChanges(app, runFormOf(app), shown), {})
  assert.deepEqual(runChanges(app, { ...runFormOf(app), port: ' 3000 ', health_path: '/healthz' }, shown), { port: 3000, health_path: '/healthz' })
  assert.deepEqual(runChanges({ ...app, port: 3000 }, { ...runFormOf({ ...app, port: 3000 }), port: '' }, shown), { port: 0 })
  assert.deepEqual(runChanges(app, { ...runFormOf(app), type: 'worker', port: '3000' }, runFieldsFor(app, 'worker')), { type: 'worker' })
  assert.deepEqual(runChanges(app, { ...runFormOf(app), context_dir: '' }, shown), {})
})

test('API errors map to their field', () => {
  assert.equal(runFieldOfError('port_reserved'), 'port')
  assert.equal(runFieldOfError('context_dir_invalid'), 'context_dir')
  assert.equal(runFieldOfError('slug_taken'), null)
  assert.equal(isPortError('port_unknown'), true)
  assert.equal(isPortError('build_failed'), false)
})
