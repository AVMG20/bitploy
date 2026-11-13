// The build history on Admin > Server and an admin's page of a person: builds
// as shaded bands on the server charts, and their words and numbers.
import { formatDuration } from './format.ts'
import type { Tone } from './format.ts'
import type { BuildRecord, BuildWindow } from './types'

export const BUILD_WINDOWS: { value: BuildWindow; label: string }[] = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
]

/** One build drawn on a chart, in milliseconds since the epoch. */
export interface BuildBand {
  id: string
  from: number
  to: number
  failed: boolean
  /** "Build: shop by Ann, 2 min 5 s". */
  label: string
}

/** A band is at least this share of the chart's width, so a short build on 24 hours still shows. */
export const MIN_BAND_SHARE = 0.004

/** Who a build counts for, in words. */
export function buildPerson(b: Pick<BuildRecord, 'person'>): string {
  return b.person || 'a removed user'
}

/** "Build: shop by Ann, 2 min 5 s"; "..., building for 40 s" while it runs. */
export function buildLabel(b: Pick<BuildRecord, 'app' | 'person' | 'build_seconds' | 'ended_at' | 'result'>): string {
  const time = b.ended_at ? formatDuration(b.build_seconds) : `building for ${formatDuration(b.build_seconds)}`
  const failed = b.result === 'failed' ? ' (failed)' : b.result === 'cancelled' ? ' (cancelled)' : ''
  return `Build: ${b.app} by ${buildPerson(b)}, ${time}${failed}`
}

/**
 * The bands of the builds within the chart's time range [start, end]: a
 * build that still builds runs until now, a band is clipped to the range
 * and at least MIN_BAND_SHARE of it wide. Builds outside the range are left out.
 */
export function buildBands(builds: BuildRecord[] | null | undefined, range: [number, number], now: number = Date.now()): BuildBand[] {
  const [start, end] = range
  if (!builds?.length || !(end > start)) return []
  const minWidth = (end - start) * MIN_BAND_SHARE
  const out: BuildBand[] = []
  for (const b of builds) {
    const from = new Date(b.started_at).getTime()
    let to = b.ended_at ? new Date(b.ended_at).getTime() : now
    if (Number.isNaN(from) || Number.isNaN(to)) continue
    if (to < from) to = from
    if (to < start || from > end) continue
    let f = Math.max(from, start)
    let t = Math.min(to, end)
    if (t - f < minWidth) {
      const mid = (f + t) / 2
      f = Math.max(start, mid - minWidth / 2)
      t = Math.min(end, f + minWidth)
      f = Math.max(start, t - minWidth)
    }
    out.push({ id: b.deployment_id, from: f, to: t, failed: b.result === 'failed', label: buildLabel(b) })
  }
  return out
}

/**
 * The bands at time t, give or take tolerance (half the distance between two
 * chart points: the crosshair snaps to the nearest point).
 */
export function bandsAt(bands: BuildBand[], t: number, tolerance = 0): BuildBand[] {
  return bands.filter((b) => b.from - tolerance <= t && t <= b.to + tolerance)
}

const ESCAPES: Record<string, string> = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }

/** Text made safe for the chart's HTML tooltip. */
export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESCAPES[c]!)
}

/** The lines the chart's tooltip adds for the builds at its time (at most three, then "and 2 more builds"). */
export function bandTooltipLines(bands: BuildBand[]): string[] {
  const lines = bands.slice(0, 3).map((b) => b.label)
  const more = bands.length - 3
  if (more > 0) lines.push(`and ${more} more ${more === 1 ? 'build' : 'builds'}`)
  return lines
}

/** A build's result in words, with the tone of its badge. */
export function buildResultInfo(b: Pick<BuildRecord, 'result' | 'error_code' | 'ended_at'>): { label: string; tone: Tone } {
  switch (b.result) {
    case 'live':
      return { label: 'Went live', tone: 'success' }
    case 'failed':
      return { label: b.error_code === 'interrupted' ? 'Interrupted' : 'Failed', tone: 'danger' }
    case 'cancelled':
      return { label: 'Cancelled', tone: 'muted' }
    default:
      return { label: b.ended_at ? 'Deploying' : 'Building', tone: 'progress' }
  }
}

/** "85%" for a peak, "-" without samples. */
export function peakPercent(p: number | null | undefined): string {
  return p === null || p === undefined ? '-' : `${Math.round(p)}%`
}
