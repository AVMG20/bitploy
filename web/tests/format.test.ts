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

test('the version an app runs never says Running next to a stopped app', () => {
  assert.equal(deploymentStateInfo('running').label, 'Live')
  assert.equal(deploymentStateInfo('running', 'running').label, 'Live')
  assert.equal(deploymentStateInfo('running', 'deploying').label, 'Live')
  for (const s of ['stopped', 'crashed', 'failed']) assert.deepEqual(deploymentStateInfo('running', s), { label: 'Current', tone: 'muted' }, s)
})

test('CPU is always shown in CPUs', async () => {
  const { formatCPU, formatCPUMillis, cpuFromPercent } = await import('../src/lib/format.ts')
  assert.equal(formatCPUMillis(500), '0.5 CPU')
  assert.equal(formatCPUMillis(2000), '2 CPU')
  assert.equal(formatCPU(cpuFromPercent(12.345)), '0.12 CPU')
  assert.equal(formatCPU(cpuFromPercent(0.2)), '<0.01 CPU')
  assert.equal(formatCPU(0), '0 CPU')
})

test('big counts are short from 10,000: 10k, 12.3k, 1m, 1b', async () => {
  const { compactCount } = await import('../src/lib/format.ts')
  const c = (n: number) => compactCount(n, 'en-US')
  assert.equal(c(0), '0')
  assert.equal(c(999), '999')
  assert.equal(c(9999), '9,999')
  assert.equal(c(10_000), '10k')
  assert.equal(c(10_049), '10k')
  assert.equal(c(10_050), '10.1k')
  assert.equal(c(12_345), '12.3k')
  assert.equal(c(99_949), '99.9k')
  assert.equal(c(99_950), '100k')
  assert.equal(c(123_456), '123k')
  assert.equal(c(999_499), '999k')
  assert.equal(c(999_500), '1m')
  assert.equal(c(999_999), '1m')
  assert.equal(c(1_000_000), '1m')
  assert.equal(c(1_200_000), '1.2m')
  assert.equal(c(1_250_000), '1.3m')
  assert.equal(c(999_999_999), '1b')
  assert.equal(c(1_500_000_000), '1.5b')
  assert.equal(c(2_000_000_000_000), '2,000b')
  assert.equal(c(-1), '-1')
  assert.equal(c(-9999), '-9,999')
  assert.equal(c(-12_345), '-12.3k')
  assert.equal(c(-1_250_000), '-1.3m')
  assert.equal(c(Number.NaN), '-')
})

test('a short count follows the language for its decimal', async () => {
  const { compactCount } = await import('../src/lib/format.ts')
  assert.equal(compactCount(12_345, 'nl-NL'), '12,3k')
  assert.equal(compactCount(9999, 'nl-NL'), '9.999')
})
