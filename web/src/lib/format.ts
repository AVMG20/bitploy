import type { App, AppStatus, Deployment, DeploymentState, DomainStatus } from './types'

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'muted'

export function appStatusInfo(status: string): { label: string; tone: Tone } {
  // Shown while a restart or start request runs.
  if (status === 'restarting') return { label: 'Restarting', tone: 'info' }
  if (status === 'starting') return { label: 'Starting', tone: 'info' }
  switch (status as AppStatus) {
    case 'running':
      return { label: 'Running', tone: 'success' }
    case 'deploying':
      return { label: 'Deploying', tone: 'info' }
    case 'failed':
      return { label: 'Failed', tone: 'danger' }
    case 'crashed':
      return { label: 'Crashed', tone: 'danger' }
    case 'stopped':
      return { label: 'Stopped', tone: 'muted' }
    case 'new':
      return { label: 'Not deployed yet', tone: 'muted' }
    default:
      return { label: status || 'Unknown', tone: 'muted' }
  }
}

/** States of a build that can still be stopped. Not `swapping`: by then it is going live. */
const CANCELLABLE = ['queued', 'fetching', 'validating', 'building', 'starting', 'health_check']
export function canCancelDeployment(state: string): boolean {
  return CANCELLABLE.includes(state)
}

/**
 * A deployment's state in words. A finished deployment (`running`) is the
 * version the app runs: "Live" while the app runs, "Current" when the app is
 * stopped or crashed, so it never says "Running" next to a stopped app.
 */
export function deploymentStateInfo(state: string, appStatus?: string): { label: string; tone: Tone } {
  if (state === 'running') {
    return appStatus === 'stopped' || appStatus === 'crashed' || appStatus === 'failed' ? { label: 'Current', tone: 'muted' } : { label: 'Live', tone: 'success' }
  }
  switch (state as DeploymentState) {
    case 'queued':
      return { label: 'Waiting', tone: 'info' }
    case 'fetching':
      return { label: 'Getting code', tone: 'info' }
    case 'validating':
      return { label: 'Checking', tone: 'info' }
    case 'building':
      return { label: 'Building', tone: 'info' }
    case 'starting':
      return { label: 'Starting', tone: 'info' }
    case 'health_check':
      return { label: 'Testing', tone: 'info' }
    case 'swapping':
      return { label: 'Going live', tone: 'info' }
    case 'failed':
      return { label: 'Failed', tone: 'danger' }
    case 'superseded':
      return { label: 'Replaced', tone: 'muted' }
    case 'cancelled':
      return { label: 'Cancelled', tone: 'muted' }
    default:
      return { label: state, tone: 'muted' }
  }
}

export function domainStatusInfo(status: string): { label: string; tone: Tone } {
  switch (status as DomainStatus) {
    case 'active':
      return { label: 'Active', tone: 'success' }
    case 'pending_dns':
      return { label: 'Waiting for DNS', tone: 'warning' }
    case 'issuing_cert':
      return { label: 'Getting certificate', tone: 'info' }
    case 'failed':
      return { label: 'Failed', tone: 'danger' }
    default:
      return { label: status, tone: 'muted' }
  }
}

export function channelLabel(channel: string): string {
  switch (channel) {
    case 'mcp':
      return 'AI assistant (MCP)'
    case 'api':
      return 'API'
    case 'dashboard':
      return 'Dashboard'
    case 'system':
      return 'System'
    case 'github':
      return 'GitHub push'
    default:
      return channel
  }
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

export function relativeTime(date: string | Date | null | undefined, now: number = Date.now()): string {
  if (!date) return 'never'
  const t = typeof date === 'string' ? new Date(date).getTime() : date.getTime()
  if (Number.isNaN(t)) return 'unknown'
  const diff = Math.round((t - now) / 1000)
  const abs = Math.abs(diff)
  if (abs < 45) return 'just now'
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), 'day')
  if (abs < 86400 * 365) return rtf.format(Math.round(diff / (86400 * 30)), 'month')
  return rtf.format(Math.round(diff / (86400 * 365)), 'year')
}

export function exactTime(date: string | Date | null | undefined): string {
  if (!date) return ''
  const d = typeof date === 'string' ? new Date(date) : date
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}

/** "Oct 29, 2026, 14:03": a date and time without the seconds. */
export function formatDateTime(date: string | Date | null | undefined): string {
  if (!date) return ''
  const d = typeof date === 'string' ? new Date(date) : date
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return '-'
  const s = Math.round(seconds)
  if (s < 60) return `${s} s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} min ${s % 60} s`
  const h = Math.floor(m / 60)
  return `${h} h ${m % 60} min`
}

/** "Up 3 d 4 h" style uptime from a start time. */
export function uptime(startedAt: string | null | undefined, now: number = Date.now()): string {
  if (!startedAt) return '-'
  const t = new Date(startedAt).getTime()
  if (Number.isNaN(t)) return '-'
  let s = Math.max(0, Math.floor((now - t) / 1000))
  const d = Math.floor(s / 86400)
  s -= d * 86400
  const h = Math.floor(s / 3600)
  s -= h * 3600
  const m = Math.floor(s / 60)
  if (d > 0) return `${d} d ${h} h`
  if (h > 0) return `${h} h ${m} min`
  if (m > 0) return `${m} min`
  return `${s} s`
}

export function formatMB(mb: number | null | undefined): string {
  if (mb === null || mb === undefined || Number.isNaN(mb)) return '-'
  if (mb > 0 && mb < 1) return '<1 MB'
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${Math.round(mb)} MB`
}

export function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || Number.isNaN(bytes)) return '-'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function formatGB(gb: number | null | undefined): string {
  if (gb === null || gb === undefined || Number.isNaN(gb)) return '-'
  return `${gb.toFixed(1)} GB`
}

/**
 * CPU is always shown in CPUs: 1 CPU is one processor core, so "0.25 CPU" is a
 * quarter of one. Limits arrive in millis (1000 = 1 CPU), live use as a percent
 * of one core (100 = 1 CPU); both go through here.
 */
export function formatCPU(cpus: number | null | undefined): string {
  if (cpus === null || cpus === undefined || Number.isNaN(cpus)) return '-'
  if (cpus > 0 && cpus < 0.01) return '<0.01 CPU'
  const v = cpus >= 10 ? Math.round(cpus * 10) / 10 : Math.round(cpus * 100) / 100
  return `${v} CPU`
}

/** A CPU limit (1000 millis = 1 CPU). */
export function formatCPUMillis(millis: number): string {
  return formatCPU(millis / 1000)
}

/** Live CPU use, sent as a percent of one core (100% = 1 CPU). */
export function cpuFromPercent(p: number): number {
  return p / 100
}

export const CPU_HINT = '1 CPU is one processor core.'

/** "Version 12": how a deployment is named everywhere. */
export function versionLabel(seq: number | null | undefined): string {
  return seq ? `Version ${seq}` : 'Version'
}

/** How a deployment was started, as the end of "Deployed 5 min ago ...". */
export function deployedVia(channel: string): string {
  switch (channel) {
    case 'mcp':
      return 'by an AI assistant'
    case 'api':
      return 'through the API'
    case 'dashboard':
      return 'from the dashboard'
    case 'system':
      return 'by bitploy'
    case 'github':
      return 'by a GitHub push'
    default:
      return channel ? `via ${channel}` : ''
  }
}

export function formatPercent(p: number | null | undefined): string {
  if (p === null || p === undefined || Number.isNaN(p)) return '-'
  return `${p.toFixed(p < 10 ? 1 : 0)}%`
}

export function pct(used: number, total: number): number {
  if (!total) return 0
  return Math.min(100, Math.max(0, (used / total) * 100))
}

export function shortSha(sha: string | null | undefined): string {
  return sha ? sha.slice(0, 7) : ''
}

/** Link to a commit on GitHub, or '' when the app's repository is not on GitHub. */
export function commitUrl(app: App | null | undefined, sha: string | null | undefined): string {
  const base = app?.git_source?.repo_html_url
  return base && sha ? `${base}/commit/${sha}` : ''
}

export function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/**
 * Plain-words description of where a deployment's code came from. With the
 * app's deployments, a step back names the version it went back to.
 */
export function describeSource(d: Deployment, app?: App | null, list?: Deployment[]): string {
  switch (d.source_kind) {
    case 'git': {
      const gs = app?.git_source
      if (!gs) return 'A git repository'
      const name = gs.repo ?? gs.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, '')
      return `${gs.repo ? 'GitHub' : 'Git'}: ${name} (${gs.branch})`
    }
    case 'upload':
      return 'Uploaded folder (zip)'
    case 'files':
      return 'Files sent by an assistant'
    case 'rollback': {
      const to = d.source_ref ? list?.find((x) => x.id === d.source_ref) : undefined
      return to ? `Went back to ${versionLabel(to.seq).toLowerCase()}` : 'Went back to an earlier version'
    }
    default:
      return d.source_kind
  }
}

/** Text order as people expect it: no case, numbers by value ("app 2" before "app 10"). */
export function compareText(a: string, b: string): number {
  return a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true })
}

export function formatCount(n: number, locale?: string): string {
  return n.toLocaleString(locale)
}

const COMPACT_UNITS: [number, string][] = [
  [1e3, 'k'],
  [1e6, 'm'],
  [1e9, 'b'],
]

/**
 * A count short enough for a tile or a narrow column: in full up to 9,999,
 * then three digits at most, "10k", "12.3k", "999k", "1m", "1.2m", "1b".
 * Rounded half up, so 10,050 is "10.1k" and 999,999 is "1m". Pair it with
 * formatCount for the exact number (on hover, for screen readers).
 */
export function compactCount(n: number, locale?: string): string {
  if (!Number.isFinite(n)) return '-'
  const abs = Math.abs(n)
  if (abs < 10_000) return formatCount(n, locale)
  let i = 0
  while (i < COMPACT_UNITS.length - 1 && abs >= COMPACT_UNITS[i + 1]![0]) i++
  let v = 0
  for (; i < COMPACT_UNITS.length; i++) {
    const unit = COMPACT_UNITS[i]![0]
    // One decimal under 100 of the unit (12.3k), none from 100 (123k).
    v = abs / unit < 100 ? Math.round(abs / (unit / 10)) / 10 : Math.round(abs / unit)
    // 999,999 rounds to 1000k: that is 1m. The largest unit keeps going (2,000b).
    if (v < 1000 || i === COMPACT_UNITS.length - 1) break
  }
  const num = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(v)
  return `${n < 0 ? '-' : ''}${num}${COMPACT_UNITS[i]![1]}`
}

let regionNames: Intl.DisplayNames | null | undefined
export function countryName(code: string): string {
  if (!/^[A-Za-z]{2}$/.test(code)) return 'Unknown'
  if (regionNames === undefined) {
    try {
      regionNames = new Intl.DisplayNames(undefined, { type: 'region' })
    } catch {
      regionNames = null
    }
  }
  return regionNames?.of(code.toUpperCase()) ?? code.toUpperCase()
}
