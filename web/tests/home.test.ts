import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  aggregateResources,
  aggregateTraffic,
  filterAndSortProjects,
  formatHomeMetricPoint,
  formatHourTick,
  isHomeMetric,
  projectLastDeploy,
  shortRelativeTime,
  sparklinePoints,
} from '../src/lib/home.ts'
import type { App, Metrics, Project, Traffic } from '../src/lib/types'

function makeApp(overrides: Partial<App> = {}): App {
  return {
    id: 'app_1',
    catalog_id: '',
    catalog_version: '',
    catalog_update: null,
    slug: 'web',
    name: 'Web',
    type: 'web',
    url: 'https://web.example.com',
    domain: 'example.com',
    root: false,
    owner_id: 'usr_1',
    status: 'running',
    desired_state: 'running',
    port: 3000,
    health_path: '/',
    dockerfile_path: '',
    context_dir: '',
    build_mode: 'auto',
    output_dir: '',
    home_page: '',
    volume: false,
    volume_mb: 0,
    volume_used_mb: null,
    files_mb: null,
    backups_mb: 0,
    backups_supported: false,
    memory_mb: 512,
    cpu_millis: 500,
    static: false,
    git_source: null,
    protected: false,
    current_deployment: null,
    domains: null,
    restart_count: 0,
    services: [],
    volumes: [],
    compose_path: '',
    created_at: '2026-11-01T10:00:00Z',
    updated_at: '2026-11-01T10:00:00Z',
    ...overrides,
  }
}

function makeProject(id: string, name: string, apps: App[]): Project {
  return { id, name, created_at: '2026-11-01T10:00:00Z', apps }
}

function makeTraffic(slug: string, visitors: number, requests: number, errors = 0, prevRequests?: number): Traffic {
  const totals = {
    requests,
    visitors,
    errors,
    error_rate: requests > 0 ? errors / requests : 0,
    client_errors: 0,
    bots: 0,
    page_views: Math.round(requests * 0.4),
    timed: requests,
    slow: 0,
    median_ms: 40,
    p95_ms: 120,
  }
  return {
    app: slug,
    window: '24h',
    granularity: 'hour',
    visitors_today: visitors,
    totals,
    previous: prevRequests !== undefined ? { ...totals, requests: prevRequests, visitors: Math.round(visitors / 2) } : null,
    points: [
      { ts: '2026-11-24T10:00:00Z', requests: Math.floor(requests / 2), visitors: Math.floor(visitors / 2), errors: 0, client_errors: 0, bots: 0, page_views: 1, median_ms: 30, p95_ms: 80 },
      { ts: '2026-11-24T11:00:00Z', requests: Math.ceil(requests / 2), visitors: Math.ceil(visitors / 2), errors, client_errors: 0, bots: 0, page_views: 2, median_ms: 35, p95_ms: 90 },
    ],
    pages: [],
    countries: [],
    referrers: [],
    not_found: [],
  }
}

test('shortRelativeTime formats compact ages', () => {
  const now = Date.parse('2026-11-24T12:00:00Z')
  assert.equal(shortRelativeTime('2026-11-24T11:59:40Z', now), 'now')
  assert.equal(shortRelativeTime('2026-11-24T11:55:00Z', now), '5m')
  assert.equal(shortRelativeTime('2026-11-24T09:00:00Z', now), '3h')
  assert.equal(shortRelativeTime('2026-11-21T12:00:00Z', now), '3d')
  assert.equal(shortRelativeTime(null, now), '')
})

test('isHomeMetric validates known keys', () => {
  assert.equal(isHomeMetric('visitors'), true)
  assert.equal(isHomeMetric('requests'), true)
  assert.equal(isHomeMetric('memory'), true)
  assert.equal(isHomeMetric('cpu'), true)
  assert.equal(isHomeMetric('bogus'), false)
})

test('sparklinePoints draws a baseline when all values are zero', () => {
  assert.equal(sparklinePoints([]), '')
  assert.equal(sparklinePoints([5]), '')
  const flat = sparklinePoints([0, 0, 0], 30, 12)
  assert.equal(flat, '0.0,10.5 15.0,10.5 30.0,10.5')
  const peaked = sparklinePoints([0, 10, 5], 30, 12)
  assert.equal(peaked, '0.0,10.5 15.0,1.5 30.0,6.0')
})

test('aggregateTraffic sums multi-app projects and ranks by selected metric', () => {
  const p1 = makeProject('p1', 'Polynux', [
    makeApp({ id: 'a1', slug: 'core', url: 'https://core.avmg.nl' }),
    makeApp({ id: 'a2', slug: 'casino', url: 'https://casino.avmg.nl' }),
    makeApp({ id: 'a3', slug: 'coredb', type: 'worker', url: '', template: 'postgres' }),
  ])
  const p2 = makeProject('p2', 'Shop', [makeApp({ id: 'a4', slug: 'shop', url: 'https://shop.avmg.nl' })])
  const p3 = makeProject('p3', 'Worker', [makeApp({ id: 'a5', slug: 'sync', type: 'worker', url: '' })])

  const traffic = {
    core: makeTraffic('core', 10, 400, 0, 200),
    casino: makeTraffic('casino', 5, 800, 2, 400),
    shop: makeTraffic('shop', 40, 300, 0, 300),
  }

  const byRequests = aggregateTraffic([p1, p2, p3], traffic, 'requests')
  assert.equal(byRequests.total, 1500)
  assert.equal(byRequests.byProject.p1?.metric, 'requests')
  assert.equal(byRequests.byProject.p1?.value, 1200)
  assert.deepEqual(byRequests.byProject.p1?.pointTimes, ['2026-11-24T10:00:00Z', '2026-11-24T11:00:00Z'])
  assert.equal(byRequests.byProject.p2?.value, 300)
  assert.equal(byRequests.byProject.p3?.value, null)
  assert.equal(byRequests.top[0]?.projectId, 'p1')
  assert.equal(byRequests.buckets.length, 2)
  assert.equal(byRequests.badge?.text, '↑ 67%')

  const byErrors = aggregateTraffic([p1, p2, p3], traffic, 'errors')
  assert.equal(byErrors.total, 2)
  assert.equal(byErrors.tone, 'danger')
  assert.equal(byErrors.byProject.p1?.tone, 'danger')
  assert.equal(byErrors.badge?.text, '0.1% rate')
})

test('aggregateResources buckets 5-minute samples by hour and sums live usage', () => {
  const p1 = makeProject('p1', 'API', [
    makeApp({ id: 'a1', slug: 'api', memory_mb: 512, live: { ts: '2026-11-24T11:55:00Z', cpu: 25, memory_mb: 180, rx_bytes: 0, tx_bytes: 0 } }),
  ])
  const p2 = makeProject('p2', 'Static', [
    makeApp({ id: 'a2', slug: 'site', static: true, memory_mb: 0 }),
  ])
  const metrics: Record<string, Metrics> = {
    api: {
      app: 'api',
      window: '24h',
      memory_limit_mb: 512,
      latest: null,
      points: [
        { ts: '2026-11-24T10:05:00Z', cpu: 10, memory_mb: 100, rx_bytes: 0, tx_bytes: 0 },
        { ts: '2026-11-24T10:15:00Z', cpu: 30, memory_mb: 200, rx_bytes: 0, tx_bytes: 0 },
        { ts: '2026-11-24T11:05:00Z', cpu: 50, memory_mb: 250, rx_bytes: 0, tx_bytes: 0 },
      ],
    },
  }

  const mem = aggregateResources([p1, p2], metrics, 'memory', 1024)
  assert.equal(mem.total, 180)
  assert.equal(mem.buckets.length, 2)
  assert.equal(mem.buckets[0]?.value, 150)
  assert.equal(mem.buckets[1]?.value, 250)
  assert.equal(mem.byProject.p1?.metric, 'memory')
  assert.deepEqual(mem.byProject.p1?.pointTimes, ['2026-11-24T10:00:00.000Z', '2026-11-24T11:00:00.000Z'])
  assert.equal(mem.byProject.p2?.unit, 'Static')
  assert.equal(mem.badge?.text, '18% of limit')

  const cpu = aggregateResources([p1, p2], metrics, 'cpu', 4)
  assert.equal(cpu.total, 0.25)
  assert.equal(cpu.buckets[0]?.value, 0.2)
  assert.equal(cpu.buckets[1]?.value, 0.5)
})

test('formatHomeMetricPoint and formatHourTick format hovered values cleanly', () => {
  assert.equal(formatHomeMetricPoint('requests', 0), '0')
  assert.equal(formatHomeMetricPoint('visitors', 1420), '1,420')
  assert.equal(formatHomeMetricPoint('memory', 256), '256 MB')
  assert.equal(formatHomeMetricPoint('cpu', 0.25), '0.25 CPU')
  assert.equal(formatHourTick(undefined), '')
  assert.equal(formatHourTick('not-a-date'), '')
  assert.match(formatHourTick('2026-11-24T10:00:00Z'), /^\d{2}:\d{2}$/)
})

test('filterAndSortProjects filters and sorts cleanly', () => {
  const d1 = { id: 'd1', app_id: 'a1', app_slug: 'one', seq: 1, state: 'running', source_kind: 'git', channel: 'github', commit_message: 'Old deploy', error: null, warnings: null, queued_at: '2026-11-20T10:00:00Z', started_at: null, finished_at: '2026-11-20T10:01:00Z', duration_seconds: 10, url: '' } as const
  const d2 = { ...d1, id: 'd2', finished_at: '2026-11-24T10:01:00Z', commit_message: 'New deploy' }

  const p1 = makeProject('p1', 'One', [makeApp({ id: 'a1', slug: 'one', current_deployment: d1 })])
  const p2 = makeProject('p2', 'Multi', [makeApp({ id: 'a2', slug: 'web', current_deployment: d2 }), makeApp({ id: 'a3', slug: 'db' })])

  assert.equal(projectLastDeploy(p2)?.what, 'New deploy')
  assert.equal(projectLastDeploy(p2)?.app, 'web')

  const values = {
    p1: { value: 5, display: '5', unit: 'reqs', points: [], active: true, tone: 'default' as const, title: '' },
    p2: { value: 50, display: '50', unit: 'reqs', points: [], active: true, tone: 'default' as const, title: '' },
  }

  assert.deepEqual(filterAndSortProjects([p1, p2], { filter: 'multi', sort: 'order', values }).map((p) => p.id), ['p2'])
  assert.deepEqual(filterAndSortProjects([p1, p2], { filter: 'all', sort: 'metric', values }).map((p) => p.id), ['p2', 'p1'])
  assert.deepEqual(filterAndSortProjects([p1, p2], { filter: 'all', sort: 'recent', values }).map((p) => p.id), ['p2', 'p1'])
})
