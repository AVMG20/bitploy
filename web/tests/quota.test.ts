import assert from 'node:assert/strict'
import { test } from 'node:test'
import { appsFull, composeFit, fitAllowance, gbToMB, mbToGB } from '../src/lib/quota.ts'
import type { ComposeCheck, Quota, Usage } from '../src/lib/types.ts'

function me(quota: Partial<Quota> = {}, usage: Partial<Usage> = {}) {
  return {
    quota: { apps: 10, memory_mb: 1024, cpu_millis: 1000, deploys_per_hour: 30, domains_per_app: 5, volume_mb: 2048, ...quota },
    usage: { apps: 2, memory_reserved_mb: 768, cpu_reserved_millis: 500, files_mb: 100, backups_mb: 0, volume_reserved_mb: 0, ...usage } as Usage,
  }
}

test('over the memory left does not fit', () => {
  const f = fitAllowance({ memory_mb: 512 }, me())
  assert.equal(f.memoryLeft, 256)
  assert.equal(f.memoryOver, true)
  assert.equal(f.fits, false)
})

test('overallocating skips memory and CPU but not storage', () => {
  const f = fitAllowance({ memory_mb: 8192, cpu_millis: 8000 }, me({ overallocate: true }))
  assert.equal(f.memoryLeft, null)
  assert.equal(f.fits, true)
  assert.equal(fitAllowance({ memory_mb: 8192, volume_mb: 4096 }, me({ overallocate: true })).storageOver, true)
})

test('a storage allowance of 0 has no limit', () => {
  assert.equal(fitAllowance({ memory_mb: 0, volume_mb: 99999 }, me({ volume_mb: 0 })).fits, true)
})

test('the app count is full at the limit', () => {
  assert.equal(appsFull(me({ apps: 2 })), true)
  assert.equal(appsFull(me()), false)
  assert.equal(appsFull(undefined), false)
})

const check = (fits: boolean): ComposeCheck => ({
  services: [],
  volumes: [],
  memory_mb_total: 768,
  cpu_millis_total: 500,
  quota: { memory_mb_left: 0, cpu_millis_left: 0, storage_mb_left: null, fits, message: 'This needs 768 MB of memory of the 0 MB you have left.' },
  warnings: null,
  errors: null,
})

test('a compose file follows the server answer', () => {
  const no = composeFit(check(false), me())
  assert.equal(no.fits, false)
  assert.equal(no.memoryOver, true)
  assert.match(no.message, /768 MB/)
  // The server already left memory out for an overallocating account.
  const yes = composeFit(check(true), me({ overallocate: true }))
  assert.equal(yes.fits, true)
  assert.equal(yes.memoryOver, false)
  assert.equal(yes.memoryLeft, null)
})

test('without a server answer a compose file is compared with the account', () => {
  const c = { ...check(true), quota: undefined }
  assert.equal(composeFit(c, me()).fits, false)
  assert.equal(composeFit(c, me({ overallocate: true })).fits, true)
})

test('a limit left as the form showed it keeps its exact MB', () => {
  for (const mb of [640, 1000, 1500, 2048, 0, 25600, 1]) assert.equal(gbToMB(mbToGB(mb), mb), mb)
  // An edited value is converted.
  assert.equal(gbToMB(0.75, 640), 768)
  assert.equal(gbToMB(1, 1000), 1024)
})
