import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canCancelDeployment, deploymentStateInfo } from '../src/lib/format.ts'

test('a build can be cancelled until it goes live', () => {
  for (const s of ['queued', 'fetching', 'validating', 'building', 'starting', 'health_check']) assert.equal(canCancelDeployment(s), true, s)
  for (const s of ['swapping', 'running', 'failed', 'superseded', 'cancelled']) assert.equal(canCancelDeployment(s), false, s)
})

test('a cancelled build is neutral, not a failure', () => {
  assert.equal(deploymentStateInfo('cancelled').tone, 'muted')
  assert.equal(deploymentStateInfo('failed').tone, 'danger')
})
