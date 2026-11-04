import assert from 'node:assert/strict'
import { test } from 'node:test'
import { appMatches, sortApps } from '../src/lib/appList.ts'
import type { App } from '../src/lib/types.ts'

function app(p: Partial<App>): App {
  return {
    id: p.slug ?? 'x', slug: 'x', name: 'X', type: 'web', url: `https://${p.slug ?? 'x'}.example.com`, status: 'running',
    catalog_id: '', catalog_version: '', catalog_update: null, owner_id: '', desired_state: 'running', port: null,
    health_path: '/', dockerfile_path: 'Dockerfile', context_dir: '.', build_mode: 'auto', output_dir: '', home_page: '',
    volume: false, volume_mb: 0, volume_used_mb: null, files_mb: null, backups_mb: 0, memory_mb: 256, cpu_millis: 500,
    static: false, git_source: null, protected: false, current_deployment: null, domains: null, restart_count: 0,
    created_at: '', updated_at: '', ...p,
  } as App
}

test('search finds apps by name, address, type and status', () => {
  const shop = app({ slug: 'shop', name: 'Web Shop', status: 'failed' })
  assert.equal(appMatches(shop, 'shop'), true)
  assert.equal(appMatches(shop, 'web failed'), true)
  assert.equal(appMatches(shop, 'shop.example'), true)
  assert.equal(appMatches(shop, 'worker'), false)
  assert.equal(appMatches(app({ type: 'worker' }), 'background'), true)
  assert.equal(appMatches(shop, '  '), true)
})

test('sorting puts apps without a value last and breaks ties by name', () => {
  const a = app({ slug: 'a', name: 'app 10', live: { ts: '', cpu: 5, memory_mb: 100, rx_bytes: 0, tx_bytes: 0 } })
  const b = app({ slug: 'b', name: 'app 2', live: { ts: '', cpu: 50, memory_mb: 50, rx_bytes: 0, tx_bytes: 0 } })
  const c = app({ slug: 'c', name: 'Stopped', status: 'stopped' })
  assert.deepEqual(sortApps([a, b, c], 'name', false).map((x) => x.slug), ['b', 'a', 'c'])
  assert.deepEqual(sortApps([a, b, c], 'cpu', true).map((x) => x.slug), ['b', 'a', 'c'])
  assert.deepEqual(sortApps([a, b, c], 'cpu', false).map((x) => x.slug), ['a', 'b', 'c'])
  assert.deepEqual(sortApps([c, a, b], 'status', false).map((x) => x.slug), ['b', 'a', 'c'])
})
