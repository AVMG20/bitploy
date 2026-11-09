import assert from 'node:assert/strict'
import { test } from 'node:test'
import { paletteGroups, paletteMatches } from '../src/lib/palette.ts'
import type { PaletteEntry } from '../src/lib/palette.ts'

const items: (PaletteEntry & { id: string })[] = [
  { id: 'proj-vars', label: 'Variables', group: 'Shop', groupKey: 'project:shop', keywords: 'env' },
  { id: 'p-shop', label: 'Shop', group: 'Projects', groupKey: 'projects' },
  { id: 'notes', label: 'notes', group: 'Apps', groupKey: 'apps' },
  { id: 'web', label: 'web', group: 'Apps', groupKey: 'apps', near: true },
  // An app called Shop: the same heading as the project.
  { id: 'app-shop-logs', label: 'Logs', group: 'Shop', groupKey: 'app:shop', keywords: 'shop' },
]

test('a keyword finds an item whose label does not contain it', () => {
  assert.deepEqual(
    paletteMatches(items, 'env').map((i) => i.id),
    ['proj-vars'],
  )
})

test('the apps of the project you are in come first', () => {
  const ids = paletteMatches(items, 'e').map((i) => i.id)
  assert.ok(ids.indexOf('web') < ids.indexOf('notes'))
})

test('groups with the same heading stay apart and every group is listed once', () => {
  const groups = paletteGroups(paletteMatches(items, 'shop'))
  const keys = groups.map((g) => g.key)
  assert.equal(new Set(keys).size, keys.length)
  assert.equal(groups.filter((g) => g.group === 'Shop').length, 2)
})

test('an empty search keeps the order', () => {
  assert.equal(paletteMatches(items, '  ').length, items.length)
})
