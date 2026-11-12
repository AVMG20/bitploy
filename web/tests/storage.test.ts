import assert from 'node:assert/strict'
import { test } from 'node:test'
import { storageByApp, storageByProject } from '../src/lib/storage.ts'
import type { App } from '../src/lib/types.ts'

const app = (over: Partial<App>): App =>
  ({ id: over.slug, slug: 'a', name: '', owner_id: 'me', volume: false, volume_mb: 0, volume_used_mb: null, files_mb: null, backups_mb: 0, ...over }) as App

test('counts files, backups and /data at its full size, biggest first', () => {
  const rows = storageByApp(
    [
      app({ slug: 'site', files_mb: 40 }),
      app({ slug: 'db', files_mb: 100, backups_mb: 20, volume: true, volume_mb: 1024, volume_used_mb: 300 }),
    ],
    'me',
  )
  assert.deepEqual(
    rows.map((r) => [r.app.slug, r.allocatedMB, r.usedMB]),
    [
      ['db', 1144, 420],
      ['site', 40, 40],
    ],
  )
})

test('leaves out apps of others and a volume that is off', () => {
  const rows = storageByApp([app({ slug: 'theirs', owner_id: 'you', files_mb: 10 }), app({ slug: 'mine', volume: false, volume_mb: 512, volume_used_mb: 5 })], 'me')
  assert.deepEqual(
    rows.map((r) => [r.app.slug, r.allocatedMB, r.volumeMB, r.filesMB]),
    [['mine', 0, 0, null]],
  )
})

test('adds up the apps of each project, biggest project first', () => {
  const rows = storageByProject(
    [
      { id: 'p1', name: 'Blog', created_at: '', apps: [app({ slug: 'blog', files_mb: 50 })] },
      { id: 'p2', name: 'Shop', created_at: '', apps: [app({ slug: 'web', files_mb: 100 }), app({ slug: 'db', files_mb: 80, backups_mb: 10, volume: true, volume_mb: 512, volume_used_mb: 30 })] },
      { id: 'p3', name: 'Shared', created_at: '', apps: [app({ slug: 'x', owner_id: 'you', files_mb: 900 })] },
    ],
    'me',
  )
  assert.deepEqual(
    rows.map((r) => [r.project.name, r.allocatedMB, r.usedMB, r.apps.map((a) => a.app.slug)]),
    [
      ['Shop', 702, 220, ['db', 'web']],
      ['Blog', 50, 50, ['blog']],
    ],
  )
})
