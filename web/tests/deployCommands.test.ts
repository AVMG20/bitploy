import assert from 'node:assert/strict'
import { test } from 'node:test'
import { MAX_DEPLOY_COMMAND, deployCommandError, offersDeployCommands } from '../src/lib/deployCommands.ts'

const base = { template: null, catalog_id: '', static: false, build_mode: 'auto' as const, services: [], service_layout: 'single' as const }

test('deploy commands are offered for apps built from code with one service', () => {
  assert.equal(offersDeployCommands(base), true)
  assert.equal(offersDeployCommands({ ...base, build_mode: 'dockerfile' }), true)
  assert.equal(offersDeployCommands(null), false)
})

test('not for databases, one-click apps, static sites or compose apps', () => {
  assert.equal(offersDeployCommands({ ...base, template: 'postgres' }), false)
  assert.equal(offersDeployCommands({ ...base, catalog_id: 'vaultwarden' }), false)
  assert.equal(offersDeployCommands({ ...base, static: true }), false)
  assert.equal(offersDeployCommands({ ...base, build_mode: 'static_build' }), false)
  assert.equal(offersDeployCommands({ ...base, build_mode: 'compose' }), false)
  assert.equal(offersDeployCommands({ ...base, service_layout: 'multi' }), false)
})

test('commands that are set stay visible so they can be removed', () => {
  assert.equal(offersDeployCommands({ ...base, static: true, after_deploy_command: 'echo hi' }), true)
})

test('a command longer than the server takes is refused', () => {
  assert.equal(deployCommandError('php artisan migrate --force'), '')
  assert.match(deployCommandError('x'.repeat(MAX_DEPLOY_COMMAND + 1)), /at most 2000/)
})
