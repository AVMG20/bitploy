import assert from 'node:assert/strict'
import { test } from 'node:test'
import { bandsAt, bandTooltipLines, buildBands, buildLabel, buildResultInfo, escapeHtml, MIN_BAND_SHARE, peakPercent } from '../src/lib/builds.ts'
import type { BuildRecord } from '../src/lib/types.ts'

const T0 = Date.parse('2026-11-13T10:00:00Z')
const at = (sec: number) => new Date(T0 + sec * 1000).toISOString()

function build(id: string, start: number, end: number | null, extra: Partial<BuildRecord> = {}): BuildRecord {
  return {
    deployment_id: id,
    app_id: 'app_1',
    app: 'shop',
    app_name: 'Shop',
    person_id: 'usr_ann',
    person: 'Ann',
    channel: 'mcp',
    source_kind: 'files',
    queued_at: at(start - 5),
    started_at: at(start),
    ended_at: end === null ? null : at(end),
    finished_at: end === null ? null : at(end + 10),
    wait_seconds: 5,
    build_seconds: end === null ? 40 : end - start,
    result: end === null ? 'in_progress' : 'live',
    error_code: null,
    peak_cpu: null,
    peak_memory_mb: null,
    ...extra,
  }
}

test('a build reads as app, person and time', () => {
  assert.equal(buildLabel(build('a', 0, 125)), 'Build: shop by Ann, 2 min 5 s')
  assert.equal(buildLabel(build('b', 0, 30, { result: 'failed' })), 'Build: shop by Ann, 30 s (failed)')
  assert.equal(buildLabel(build('c', 0, null)), 'Build: shop by Ann, building for 40 s')
  assert.equal(buildLabel(build('d', 0, 10, { person: '' })), 'Build: shop by a removed user, 10 s')
})

test('builds become bands within the chart range', () => {
  const range: [number, number] = [T0, T0 + 3600_000]
  const bands = buildBands(
    [
      build('before', -600, -60), // ends before the range: left out
      build('clipped', -60, 120), // starts before it: clipped
      build('inside', 600, 900, { result: 'failed' }),
      build('running', 3000, null), // runs until now
      build('after', 4000, 4100), // after the range
    ],
    range,
    T0 + 3300_000,
  )
  assert.deepEqual(
    bands.map((b) => b.id),
    ['clipped', 'inside', 'running'],
  )
  assert.equal(bands[0]!.from, T0)
  assert.equal(bands[0]!.to, T0 + 120_000)
  assert.equal(bands[1]!.failed, true)
  assert.equal(bands[2]!.to, T0 + 3300_000)
  assert.deepEqual(buildBands(null, range), [])
  assert.deepEqual(buildBands([build('x', 0, 10)], [T0, T0]), [])
})

test('a short build still gets a visible band, inside the range', () => {
  const range: [number, number] = [T0, T0 + 86_400_000]
  const min = 86_400_000 * MIN_BAND_SHARE
  const [mid] = buildBands([build('short', 43_200, 43_205)], range)
  assert.ok(Math.abs(mid!.to - mid!.from - min) < 1)
  assert.ok(mid!.from < T0 + 43_200_000 && mid!.to > T0 + 43_205_000)
  const [edge] = buildBands([build('edge', 86_398, 86_400)], range)
  assert.equal(edge!.to, range[1])
  assert.ok(Math.abs(edge!.to - edge!.from - min) < 1)
})

test('the bands at a time, with the crosshair snapping to points', () => {
  const bands = buildBands([build('a', 100, 200), build('b', 150, 400)], [T0, T0 + 3600_000])
  assert.deepEqual(
    bandsAt(bands, T0 + 160_000).map((b) => b.id),
    ['a', 'b'],
  )
  assert.deepEqual(
    bandsAt(bands, T0 + 300_000).map((b) => b.id),
    ['b'],
  )
  assert.deepEqual(bandsAt(bands, T0 + 410_000), [])
  assert.deepEqual(
    bandsAt(bands, T0 + 410_000, 15_000).map((b) => b.id),
    ['b'],
  )
})

test('tooltip lines stop after three builds', () => {
  const bands = buildBands([build('a', 0, 10), build('b', 0, 10), build('c', 0, 10), build('d', 0, 10), build('e', 0, 10)], [T0, T0 + 60_000])
  const lines = bandTooltipLines(bands)
  assert.equal(lines.length, 4)
  assert.equal(lines[3], 'and 2 more builds')
  assert.equal(bandTooltipLines(bands.slice(0, 4))[3], 'and 1 more build')
})

test('html in names is escaped', () => {
  assert.equal(escapeHtml(`<b>"Ann" & 'Bo'</b>`), '&lt;b&gt;&quot;Ann&quot; &amp; &#39;Bo&#39;&lt;/b&gt;')
})

test('results and peaks in words', () => {
  assert.equal(buildResultInfo(build('a', 0, 10)).label, 'Went live')
  assert.deepEqual(buildResultInfo(build('a', 0, 10, { result: 'failed', error_code: 'interrupted' })), { label: 'Interrupted', tone: 'danger' })
  assert.equal(buildResultInfo(build('a', 0, null)).label, 'Building')
  assert.equal(buildResultInfo(build('a', 0, 10, { result: 'in_progress' })).label, 'Deploying')
  assert.equal(peakPercent(85.25), '85%')
  assert.equal(peakPercent(null), '-')
})
