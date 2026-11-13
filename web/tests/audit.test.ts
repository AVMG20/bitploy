import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activityRow, auditLabel, filterAuditRows } from '../src/lib/audit.ts'
import type { AuditRow } from '../src/lib/audit.ts'
import type { ActivityEntry } from '../src/lib/types'

const entry = (over: Partial<ActivityEntry>): ActivityEntry => ({
  id: 1,
  ts: '2026-11-13T10:00:00Z',
  actor: 'you',
  actor_name: 'You',
  channel: 'dashboard',
  action: 'app.stop',
  target_type: 'app',
  target_id: 'app_1',
  details: null,
  ...over,
})

test('an activity entry links its app while the user may open it', () => {
  const r = activityRow(entry({ ip: '203.0.113.7', app: { id: 'app_1', slug: 'shop', name: 'Shop', project_id: 'prj_1' } }))
  assert.deepEqual(r.target, { text: 'Shop', to: '/apps/shop' })
  assert.equal(r.who, 'You')
  assert.equal(r.ip, '203.0.113.7')
})

test('a deleted app shows by its old slug, without a link', () => {
  assert.deepEqual(activityRow(entry({ action: 'app.delete', app: { slug: 'old-blog', name: 'old-blog', gone: true } })).target, { text: 'old-blog' })
  assert.deepEqual(activityRow(entry({ app: { gone: true } })).target, { text: 'a deleted app' })
})

test('other people come without an address, projects and domains as targets', () => {
  const r = activityRow(entry({ actor: 'person', actor_name: 'Grace', action: 'project.member_add', target_type: 'project', project: { id: 'prj_wiki', name: 'Team wiki' }, details: { member: 'You', invite_id: 'pin_1' } }))
  assert.equal(r.who, 'Grace')
  assert.equal(r.ip, '')
  assert.deepEqual(r.target, { text: 'Team wiki', to: '/projects/prj_wiki' })
  assert.deepEqual(r.details, ['member: You'])
  assert.deepEqual(activityRow(entry({ target_type: 'domain', action: 'domain.remove', details: { hostname: 'shop.example.com' } })).target, { text: 'shop.example.com' })
  const limits = activityRow(entry({ target_type: 'user', action: 'admin.quota_set', actor: 'admin', actor_name: 'An admin' }))
  assert.equal(limits.target, null)
  assert.equal(limits.label, 'Changed your limits')
})

test('an app in a project names the project too', () => {
  const r = activityRow(entry({ app: { id: 'app_1', slug: 'mk-api', name: 'api' }, project: { id: 'prj_1', name: 'Marketplace' } }))
  assert.deepEqual(r.target, { text: 'api', to: '/apps/mk-api', sub: 'Marketplace' })
  assert.deepEqual(filterAuditRows([r], 'all', 'marketplace').length, 1)
})

test('the filter matches kind and every search word', () => {
  const rows: AuditRow[] = [
    { id: 1, ts: '', who: 'You', channel: 'mcp', action: 'deployment.queue', target: { text: 'shop' }, details: ['seq: 4'], ip: '' },
    { id: 2, ts: '', who: 'Grace', channel: 'dashboard', action: 'env.set', target: { text: 'wiki' }, details: ['set: SMTP_HOST'], ip: '' },
    { id: 3, ts: '', who: 'An admin', channel: 'dashboard', action: 'admin.quota_set', target: null, details: [], ip: '' },
  ]
  const ids = (r: AuditRow[]) => r.map((x) => x.id)
  assert.deepEqual(ids(filterAuditRows(rows, 'all', '')), [1, 2, 3])
  assert.deepEqual(ids(filterAuditRows(rows, 'deploys', '')), [1])
  assert.deepEqual(ids(filterAuditRows(rows, 'admin', '')), [3])
  assert.deepEqual(ids(filterAuditRows(rows, 'all', 'grace smtp')), [2])
  assert.deepEqual(ids(filterAuditRows(rows, 'all', 'AI assistant')), [1]) // the channel in words
  assert.deepEqual(ids(filterAuditRows(rows, 'all', 'started a deploy')), [1]) // the label
  assert.deepEqual(ids(filterAuditRows(rows, 'apps', 'shop')), [])
})

test('project and port actions read as words', () => {
  assert.equal(auditLabel('project.member_add'), 'Joined a project')
  assert.equal(auditLabel('app.public_port_opened'), 'Opened a public port')
})
