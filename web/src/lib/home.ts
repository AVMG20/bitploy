// Helpers for the Home page: selectable 24h fleet metric (visitors, requests,
// page views, errors, memory, CPU), project filtering/sorting, sparklines and
// compact timestamps. Only erasable TypeScript, so Node can test it directly.

import { compactCount, cpuFromPercent, formatCPU, formatCount, formatMB, hostOf, versionLabel } from './format.ts'
import { aliasOf, isPublicApp, mainApp, projectUsage } from './projects.ts'
import { change } from './traffic.ts'
import type { Change } from './traffic.ts'
import type { App, Metrics, Project, Traffic } from './types'

export type HomeMetric = 'visitors' | 'requests' | 'page_views' | 'errors' | 'memory' | 'cpu'
export type HomeFilter = 'all' | 'multi' | 'active'
export type HomeSort = 'order' | 'metric' | 'recent'
export type HomeView = 'grid' | 'list'

export interface HomeMetricMeta {
  key: HomeMetric
  label: string
  /** Short unit shown next to the card value ("visits", "reqs", "RAM", "CPU"). */
  cardUnit: string
  /** Unit shown next to the big total in the pulse card. */
  heroUnit: string
  /** Label in the Projects sort dropdown. */
  sortLabel: string
  kind: 'traffic' | 'resource'
  upIsBad?: boolean
}

export const HOME_METRICS: readonly HomeMetricMeta[] = [
  { key: 'visitors', label: 'Visitors', cardUnit: 'visits 24h', heroUnit: 'visitors', sortLabel: 'Most visitors', kind: 'traffic' },
  { key: 'requests', label: 'Requests', cardUnit: 'reqs 24h', heroUnit: 'requests', sortLabel: 'Most requests', kind: 'traffic' },
  { key: 'page_views', label: 'Page views', cardUnit: 'views 24h', heroUnit: 'page views', sortLabel: 'Most page views', kind: 'traffic' },
  { key: 'errors', label: 'Errors (5xx)', cardUnit: 'errors 24h', heroUnit: 'server errors', sortLabel: 'Most errors', kind: 'traffic', upIsBad: true },
  { key: 'memory', label: 'Memory', cardUnit: 'RAM now', heroUnit: 'in use', sortLabel: 'Most memory', kind: 'resource' },
  { key: 'cpu', label: 'CPU', cardUnit: 'CPU now', heroUnit: 'in use', sortLabel: 'Most CPU', kind: 'resource' },
]

export function homeMetricMeta(key: HomeMetric): HomeMetricMeta {
  return HOME_METRICS.find((m) => m.key === key) ?? HOME_METRICS[0]!
}

export function isHomeMetric(v: unknown): v is HomeMetric {
  return typeof v === 'string' && HOME_METRICS.some((m) => m.key === v)
}

export function isHomeView(v: unknown): v is HomeView {
  return v === 'grid' || v === 'list'
}

/** Compact relative age for right-aligned columns ("now", "1m", "25m", "3h", "2d"). */
export function shortRelativeTime(date: string | Date | null | undefined, now: number = Date.now()): string {
  if (!date) return ''
  const t = typeof date === 'string' ? Date.parse(date) : date.getTime()
  if (Number.isNaN(t)) return ''
  const sec = Math.max(0, Math.round((now - t) / 1000))
  if (sec < 45) return 'now'
  if (sec < 3600) return `${Math.max(1, Math.round(sec / 60))}m`
  if (sec < 86400) return `${Math.round(sec / 3600)}h`
  if (sec < 86400 * 30) return `${Math.round(sec / 86400)}d`
  if (sec < 86400 * 365) return `${Math.round(sec / (86400 * 30))}mo`
  return `${Math.round(sec / (86400 * 365))}y`
}

export interface ProjectDeployInfo {
  who: string
  channel: string
  /** Set only when the project has more than one app. */
  app: string
  what: string
  at: string
  tsMs: number
}

const DEPLOY_WHO: Record<string, string> = {
  mcp: 'AI',
  github: 'Git',
  dashboard: 'Dashboard',
  api: 'API',
  system: 'bitploy',
}

/** The newest deployment across all apps of a project, or null if nothing was deployed. */
export function projectLastDeploy(project: Pick<Project, 'apps'>): ProjectDeployInfo | null {
  let best: { app: App; at: string; tsMs: number } | null = null
  for (const a of project.apps) {
    const d = a.current_deployment
    const at = d?.finished_at ?? d?.queued_at
    if (!d || !at) continue
    const tsMs = Date.parse(at)
    if (!best || (Number.isFinite(tsMs) ? tsMs > best.tsMs : at > best.at)) {
      best = { app: a, at, tsMs: Number.isFinite(tsMs) ? tsMs : 0 }
    }
  }
  if (!best) return null
  const d = best.app.current_deployment!
  const what = d.commit_message?.split('\n')[0]?.trim() || versionLabel(d.seq)
  return {
    who: DEPLOY_WHO[d.channel] ?? d.channel,
    channel: d.channel,
    app: project.apps.length > 1 ? aliasOf(best.app) : '',
    what,
    at: best.at,
    tsMs: best.tsMs,
  }
}

/** SVG polyline points for an inline micro-sparkline. Empty when fewer than 2 points. */
export function sparklinePoints(values: readonly number[], width = 36, height = 13): string {
  if (values.length < 2) return ''
  const max = Math.max(0, ...values)
  const pad = 1.5
  const inner = height - pad * 2
  const n = values.length - 1
  return values
    .map((v, i) => {
      const x = (i / n) * width
      const y = max > 0 ? height - pad - (Math.max(0, v) / max) * inner : height - pad
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
}

export interface HourlyBucket {
  ts: string
  value: number
  label: string
}

export interface ProjectMetricSummary {
  /** Numeric value used for sorting and "Active" filtering; null when not applicable (e.g. worker for traffic). */
  value: number | null
  /** Short formatted value for the card and table cell ("47", "1.2k", "142 MB", "0.08"). */
  display: string
  /** Unit shown under the card value ("visits 24h", "RAM now", ...). */
  unit: string
  /** Hourly values for the micro-sparkline. */
  points: number[]
  /** True when there is non-zero activity to highlight. */
  active: boolean
  tone: 'default' | 'danger'
  /** Full tooltip explanation. */
  title: string
}

export interface TopProjectItem {
  projectId: string
  name: string
  subtitle: string
  value: number
  display: string
}

export interface HomeMetricSummary {
  metric: HomeMetric
  meta: HomeMetricMeta
  /** Raw fleet total (or null if no applicable apps). */
  total: number | null
  totalDisplay: string
  /** Exact total for hover/title. */
  totalTitle: string
  /** Optional secondary badge next to the hero number (e.g. "↑ 18%", "0.4% error rate"). */
  badge: { text: string; tone: 'good' | 'bad' | 'neutral'; title?: string } | null
  buckets: HourlyBucket[]
  top: TopProjectItem[]
  byProject: Record<string, ProjectMetricSummary>
  tone: 'primary' | 'danger'
  emptyText: string
}

type TrafficMetricKey = 'visitors' | 'requests' | 'page_views' | 'errors'

function formatMetricValue(metric: HomeMetric, value: number): string {
  if (metric === 'memory') return formatMB(value)
  if (metric === 'cpu') return formatCPU(value)
  return compactCount(Math.round(value))
}

function formatCardValue(metric: HomeMetric, value: number): string {
  if (metric === 'memory') return formatMB(value)
  if (metric === 'cpu') {
    if (value > 0 && value < 0.01) return '<0.01'
    return String(value >= 10 ? Math.round(value * 10) / 10 : Math.round(value * 100) / 100)
  }
  return compactCount(Math.round(value))
}

function hourLabel(ts: string): string {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
}

function bucketTooltip(metric: HomeMetric, ts: string, value: number): string {
  const hr = hourLabel(ts)
  const meta = homeMetricMeta(metric)
  const val =
    metric === 'memory'
      ? formatMB(value)
      : metric === 'cpu'
        ? formatCPU(value)
        : `${formatCount(Math.round(value))} ${meta.heroUnit}`
  return hr ? `${hr} · ${val}` : val
}

function changeBadge(c: Change | null): HomeMetricSummary['badge'] {
  if (!c) return null
  return {
    text: c.text,
    tone: c.tone === 'good' ? 'good' : c.tone === 'bad' ? 'bad' : 'neutral',
    title: `${c.words} compared to the previous 24 hours`,
  }
}

/** Aggregates 24h traffic across all running public apps for the selected traffic metric. */
export function aggregateTraffic(
  projects: readonly Project[],
  trafficBySlug: Readonly<Record<string, Traffic | undefined>>,
  metric: TrafficMetricKey,
): HomeMetricSummary {
  const meta = homeMetricMeta(metric)
  const byProject: Record<string, ProjectMetricSummary> = {}
  const hourTotals = new Map<string, number>()
  let fleetTotal = 0
  let fleetPrev = 0
  let hasPrev = false
  let fleetRequests = 0
  let publicProjects = 0
  const ranked: TopProjectItem[] = []

  for (const p of projects) {
    const publicRunning = p.apps.filter((a) => isPublicApp(a) && a.status === 'running')
    if (!publicRunning.length) {
      byProject[p.id] = {
        value: null,
        display: '—',
        unit: p.apps.length ? 'No web address' : '',
        points: [],
        active: false,
        tone: 'default',
        title: 'No running public website',
      }
      continue
    }
    publicProjects++
    let pTotal = 0
    let loaded = 0
    const pHours = new Map<string, number>()

    for (const a of publicRunning) {
      const tr = trafficBySlug[a.slug]
      if (!tr) continue
      loaded++
      pTotal += tr.totals[metric]
      fleetRequests += tr.totals.requests
      if (tr.previous) {
        fleetPrev += tr.previous[metric]
        hasPrev = true
      }
      for (const pt of tr.points) {
        const v = pt[metric]
        pHours.set(pt.ts, (pHours.get(pt.ts) ?? 0) + v)
        hourTotals.set(pt.ts, (hourTotals.get(pt.ts) ?? 0) + v)
      }
    }

    const sortedHours = [...pHours.entries()].sort((a, b) => a[0].localeCompare(b[0])).map((x) => x[1])
    if (loaded === 0) {
      byProject[p.id] = {
        value: null,
        display: '–',
        unit: meta.cardUnit,
        points: [],
        active: false,
        tone: 'default',
        title: `Loading ${meta.label.toLowerCase()}…`,
      }
      continue
    }

    fleetTotal += pTotal
    const display = formatCardValue(metric, pTotal)
    const isDanger = metric === 'errors' && pTotal > 0
    byProject[p.id] = {
      value: pTotal,
      display,
      unit: meta.cardUnit,
      points: sortedHours,
      active: pTotal > 0,
      tone: isDanger ? 'danger' : 'default',
      title: `${formatCount(pTotal)} ${meta.heroUnit} in the last 24 hours`,
    }

    if (pTotal > 0) {
      const main = mainApp(p)
      const host = main && isPublicApp(main) ? hostOf(main.url) : p.name
      ranked.push({ projectId: p.id, name: p.name, subtitle: host, value: pTotal, display })
    }
  }

  const buckets: HourlyBucket[] = [...hourTotals.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([ts, value]) => ({ ts, value, label: bucketTooltip(metric, ts, value) }))

  let badge: HomeMetricSummary['badge'] = null
  if (metric === 'errors' && fleetRequests > 0) {
    const rate = (fleetTotal / fleetRequests) * 100
    const rateText = fleetTotal === 0 ? '0% rate' : rate < 0.1 ? '<0.1% rate' : `${rate.toFixed(1)}% rate`
    badge = { text: rateText, tone: fleetTotal > 0 ? 'bad' : 'good', title: `${formatCount(fleetTotal)} errors out of ${formatCount(fleetRequests)} requests` }
  } else if (hasPrev) {
    badge = changeBadge(change(fleetTotal, fleetPrev, meta.upIsBad))
  }

  ranked.sort((a, b) => b.value - a.value)

  return {
    metric,
    meta,
    total: publicProjects > 0 ? fleetTotal : null,
    totalDisplay: publicProjects > 0 ? formatMetricValue(metric, fleetTotal) : '–',
    totalTitle: `${formatCount(fleetTotal)} ${meta.heroUnit} in the last 24 hours`,
    badge,
    buckets,
    top: ranked.slice(0, 3),
    byProject,
    tone: metric === 'errors' && fleetTotal > 0 ? 'danger' : 'primary',
    emptyText:
      publicProjects === 0
        ? 'No running public websites yet.'
        : metric === 'errors'
          ? 'No 5xx server errors in the last 24 hours.'
          : `No ${meta.heroUnit} recorded in the last 24 hours.`,
  }
}

/**
 * Buckets 5-minute app metrics into hourly points (averaging samples within each
 * hour per app, then summing across apps) so memory and CPU show true combined
 * fleet usage over the last 24 hours.
 */
export function aggregateResources(
  projects: readonly Project[],
  metricsBySlug: Readonly<Record<string, Metrics | undefined>>,
  metric: 'memory' | 'cpu',
  limitTotal?: number | null,
): HomeMetricSummary {
  const meta = homeMetricMeta(metric)
  const byProject: Record<string, ProjectMetricSummary> = {}
  const fleetHours = new Map<string, number>()
  let fleetLive = 0
  let runningCount = 0
  const ranked: TopProjectItem[] = []

  for (const p of projects) {
    const running = p.apps.filter((a) => a.status === 'running' && !a.static)
    const usage = projectUsage(p.apps)
    const liveVal = metric === 'memory' ? usage.memoryNowMB : cpuFromPercent(usage.cpuNowPercent)

    if (!running.length) {
      byProject[p.id] = {
        value: 0,
        display: '0',
        unit: p.apps.some((a) => a.static) ? 'Static' : meta.cardUnit,
        points: [],
        active: false,
        tone: 'default',
        title: p.apps.some((a) => a.static) ? 'Static site uses no container RAM or CPU' : 'No running containers',
      }
      continue
    }

    runningCount++
    fleetLive += liveVal
    const projHours = new Map<string, number>()

    for (const a of running) {
      const m = metricsBySlug[a.slug]
      if (!m?.points?.length) continue
      const appHourBuckets = new Map<string, { sum: number; count: number }>()
      for (const pt of m.points) {
        const d = new Date(pt.ts)
        if (Number.isNaN(d.getTime())) continue
        d.setUTCMinutes(0, 0, 0)
        const key = d.toISOString()
        const v = metric === 'memory' ? pt.memory_mb : cpuFromPercent(pt.cpu)
        const cur = appHourBuckets.get(key)
        if (cur) {
          cur.sum += v
          cur.count++
        } else {
          appHourBuckets.set(key, { sum: v, count: 1 })
        }
      }
      for (const [key, { sum, count }] of appHourBuckets) {
        const avg = sum / count
        projHours.set(key, (projHours.get(key) ?? 0) + avg)
        fleetHours.set(key, (fleetHours.get(key) ?? 0) + avg)
      }
    }

    const sortedHours = [...projHours.entries()].sort((a, b) => a[0].localeCompare(b[0])).map((x) => x[1])
    const display = formatCardValue(metric, liveVal)
    byProject[p.id] = {
      value: liveVal,
      display,
      unit: meta.cardUnit,
      points: sortedHours,
      active: liveVal > 0,
      tone: 'default',
      title: metric === 'memory' ? `${formatMB(liveVal)} in use (${formatMB(usage.memoryMB)} reserved)` : `${formatCPU(liveVal)} in use`,
    }

    if (liveVal > 0) {
      ranked.push({
        projectId: p.id,
        name: p.name,
        subtitle: p.apps.length > 1 ? `${p.apps.length} apps` : aliasOf(p.apps[0]!),
        value: liveVal,
        display: formatMetricValue(metric, liveVal),
      })
    }
  }

  const buckets: HourlyBucket[] = [...fleetHours.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .slice(-24)
    .map(([ts, value]) => ({ ts, value, label: bucketTooltip(metric, ts, value) }))

  let badge: HomeMetricSummary['badge'] = null
  if (limitTotal && limitTotal > 0 && runningCount > 0) {
    const pctVal = Math.round((fleetLive / limitTotal) * 100)
    badge = {
      text: `${pctVal}% of limit`,
      tone: pctVal >= 85 ? 'bad' : 'neutral',
      title: `${formatMetricValue(metric, fleetLive)} of ${formatMetricValue(metric, limitTotal)} limit`,
    }
  }

  ranked.sort((a, b) => b.value - a.value)

  return {
    metric,
    meta,
    total: runningCount > 0 ? fleetLive : null,
    totalDisplay: runningCount > 0 ? formatMetricValue(metric, fleetLive) : '–',
    totalTitle: `${formatMetricValue(metric, fleetLive)} currently in use across ${runningCount} running projects`,
    badge,
    buckets,
    top: ranked.slice(0, 3),
    byProject,
    tone: 'primary',
    emptyText: runningCount === 0 ? 'No running containers yet.' : 'Collecting 24h usage samples…',
  }
}

/** Filters and sorts projects for the Home workspace without mutating the input array. */
export function filterAndSortProjects(
  projects: readonly Project[],
  opts: { filter: HomeFilter; sort: HomeSort; values: Readonly<Record<string, ProjectMetricSummary | undefined>> },
): Project[] {
  let list = [...projects]
  if (opts.filter === 'multi') {
    list = list.filter((p) => p.apps.length > 1)
  } else if (opts.filter === 'active') {
    list = list.filter((p) => (opts.values[p.id]?.value ?? 0) > 0)
  }

  if (opts.sort === 'metric') {
    const index = new Map(list.map((p, i) => [p.id, i]))
    list.sort((a, b) => (opts.values[b.id]?.value ?? -1) - (opts.values[a.id]?.value ?? -1) || (index.get(a.id) ?? 0) - (index.get(b.id) ?? 0))
  } else if (opts.sort === 'recent') {
    const index = new Map(list.map((p, i) => [p.id, i]))
    list.sort((a, b) => (projectLastDeploy(b)?.tsMs ?? -1) - (projectLastDeploy(a)?.tsMs ?? -1) || (index.get(a.id) ?? 0) - (index.get(b.id) ?? 0))
  }
  return list
}
