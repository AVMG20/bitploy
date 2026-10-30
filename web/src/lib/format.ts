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
      return { label: 'Building', tone: 'info' }
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

export function deploymentStateInfo(state: string): { label: string; tone: Tone } {
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
    case 'running':
      return { label: 'Running', tone: 'success' }
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

export function formatGB(gb: number | null | undefined): string {
  if (gb === null || gb === undefined || Number.isNaN(gb)) return '-'
  return `${gb.toFixed(1)} GB`
}

export function formatCPUMillis(millis: number): string {
  return `${millis / 1000} CPU`
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

export function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

/** Plain-words description of where a deployment's code came from. */
export function describeSource(d: Deployment, app?: App | null): string {
  switch (d.source_kind) {
    case 'git': {
      const repo = app?.git_source ? `${app.git_source.repo_url.replace(/^https?:\/\//, '').replace(/\.git$/, '')} (${app.git_source.branch})` : 'a git repository'
      return `GitHub: ${repo}`
    }
    case 'upload':
      return 'Uploaded folder (zip)'
    case 'files':
      return 'Files sent by an assistant'
    case 'rollback':
      return d.source_ref ? `Rollback to ${d.source_ref}` : 'Rollback to an earlier version'
    default:
      return d.source_kind
  }
}
