import assert from 'node:assert/strict'
import { test } from 'node:test'
import { auditDetails, auditGroup, auditLabel } from '../src/lib/audit.ts'
import { NO_OWN_APPS, noOwnApps } from '../src/lib/quota.ts'
import { formToLimits, joinedText, limitsOverServer, limitsToForm, projectInviteCardText, SIGNUP_MODES } from '../src/lib/signups.ts'
import type { NewAccountQuota, Quota, Usage } from '../src/lib/types.ts'

const DEFAULTS: NewAccountQuota = { apps: 10, memory_mb: 2048, cpu_millis: 2000, domains_per_app: 5, volume_mb: 25600 }

test('the three signup modes, the middle one says project invites can create accounts', () => {
  assert.deepEqual(
    SIGNUP_MODES.map((m) => m.value),
    ['admin_invites', 'any_invite', 'open'],
  )
  assert.equal(SIGNUP_MODES[1]!.label, 'Project invites can also create accounts')
  for (const m of SIGNUP_MODES) assert.doesNotMatch(`${m.label} ${m.blurb}`, /\b(simply|just|as usual|don't worry)\b/i)
})

test('the Members invite card follows whether project invites create accounts', () => {
  assert.match(projectInviteCardText(true), /Someone without an account can sign up with it\.$/)
  assert.match(projectInviteCardText(false), /Only people who already have an account on this server can accept it\.$/)
})

test('limits go to the form in GB and cores and back unchanged', () => {
  const f = limitsToForm(DEFAULTS)
  assert.deepEqual(f, { apps: 10, memory_gb: 2, cpus: 2, domains_per_app: 5, volume_gb: 25 })
  assert.deepEqual(formToLimits(f), DEFAULTS)
  assert.equal(formToLimits({ ...f, memory_gb: 0.5, cpus: 0.25 }).memory_mb, 512)
  assert.equal(formToLimits({ ...f, memory_gb: 0.5, cpus: 0.25 }).cpu_millis, 250)
})

test('limits above what the server has are named per field; unknown sizes are not checked', () => {
  const server = { memory_mb: 4096, cpu_millis: 2000, disk_mb: 51200 }
  assert.deepEqual(limitsOverServer(DEFAULTS, server), {})
  const over = limitsOverServer({ ...DEFAULTS, memory_mb: 8192, cpu_millis: 4000, volume_mb: 102400 }, server)
  assert.deepEqual(Object.keys(over).sort(), ['cpus', 'memory_gb', 'volume_gb'])
  assert.equal(over.memory_gb, 'More than this server has.')
  assert.deepEqual(limitsOverServer({ ...DEFAULTS, memory_mb: 999999 }, { memory_mb: 0, cpu_millis: 0, disk_mb: 0 }), {})
})

test('how an account joined, in words', () => {
  const ada = { id: 'usr_ada', name: 'Ada', email: 'ada@example.com' }
  assert.equal(joinedText(null), '')
  assert.equal(joinedText({ via: 'setup', by: null, project: null }), 'Made on the command line')
  assert.equal(joinedText({ via: 'admin_invite', by: ada, project: null }), 'Admin invite from Ada')
  assert.equal(joinedText({ via: 'admin_invite', by: null, project: null }), 'Admin invite')
  assert.equal(joinedText({ via: 'open', by: null, project: null }), 'Signed up on their own')
  assert.equal(joinedText({ via: 'project_invite', by: ada, project: { id: 'prj_1', name: 'Shop' } }), 'Project invite to Shop from Ada')
  assert.equal(joinedText({ via: 'project_invite', by: null, project: null, project_deleted: true }), 'Project invite to a deleted project')
})

test('an account with 0 apps has none of its own', () => {
  const me = (apps: number) => ({ quota: { ...DEFAULTS, apps } as Quota, usage: { apps: 0 } as Usage })
  assert.equal(noOwnApps(me(0)), true)
  assert.equal(noOwnApps(me(1)), false)
  assert.equal(noOwnApps(undefined), false)
  assert.equal(NO_OWN_APPS, "Your account can't create apps of its own. Ask an admin to raise your limits.")
})

test('the activity log names the new admin actions and shows changes as from → to', () => {
  assert.equal(auditLabel('admin.settings_update'), 'Changed who can sign up or the limits for new accounts')
  assert.equal(auditLabel('admin.project_invite_revoke'), 'Revoked someone’s project invite')
  assert.equal(auditGroup('admin.settings_update'), 'admin')
  assert.deepEqual(auditDetails({ signups: { from: 'any_invite', to: 'admin_invites' }, project_invite_apps: { from: true, to: false } }), [
    'signups: any_invite → admin_invites',
    'project invite apps: true → false',
  ])
  assert.deepEqual(auditDetails({ new_account_quota: { from: DEFAULTS, to: { ...DEFAULTS, apps: 2, memory_mb: 512 } } }), [
    'new account quota: apps 10 → 2, memory mb 2048 → 512',
  ])
})
