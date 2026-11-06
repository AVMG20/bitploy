import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isServedSite, variableNeedsDeploy } from '../src/lib/buildVars.ts'

test('a site bitploy serves', () => {
  assert.equal(isServedSite({ build_mode: 'static_build' }), true)
  assert.equal(isServedSite({ static: true, build_mode: 'auto' }), true)
  assert.equal(isServedSite({ build_mode: 'dockerfile' }), false)
  assert.equal(isServedSite(null), false)
})

test('a change needs a deploy when the build read the variable', () => {
  const app = (mode: string, reads?: string[]) => ({ build_mode: mode, current_deployment: { build_reads: reads } })
  assert.equal(variableNeedsDeploy(app('dockerfile', ['VITE_A', 'TOKEN']), 'TOKEN'), true)
  assert.equal(variableNeedsDeploy(app('dockerfile', ['VITE_A']), 'DATABASE_URL'), false)
  assert.equal(variableNeedsDeploy(app('auto', ['*']), 'DATABASE_URL'), true)
  assert.equal(variableNeedsDeploy(app('auto'), 'VITE_A'), false)
  assert.equal(variableNeedsDeploy({ build_mode: 'static_build', current_deployment: null }, 'VITE_A'), true)
  assert.equal(variableNeedsDeploy(undefined, 'VITE_A'), false)
})
