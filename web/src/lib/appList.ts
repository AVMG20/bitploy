import { appStatusInfo, compareText } from './format.ts'
import type { App } from './types'

/** Columns the apps list sorts by. */
export const APP_SORTS = ['name', 'status', 'cpu', 'memory', 'uptime', 'deployed'] as const
export type AppSort = (typeof APP_SORTS)[number]
/** These start with the biggest or newest first. */
export const APP_SORTS_DESC: AppSort[] = ['cpu', 'memory', 'uptime', 'deployed']

/** Problems first, then what is busy, then the rest. */
const STATUS_RANK: Record<string, number> = { failed: 0, crashed: 1, deploying: 2, running: 3, stopped: 4, new: 5 }

/** Every word of the search appears in the app's name, address, type or status. */
export function appMatches(a: App, search: string): boolean {
  const words = search.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return true
  const hay = [
    a.name,
    a.slug,
    a.url,
    a.type === 'worker' ? 'worker background' : 'web website',
    a.static ? 'static site' : '',
    a.git_source ? `github git ${a.git_source.repo ?? a.git_source.repo_url}` : '',
    a.catalog_id ? `ready-made ${a.catalog_id}` : '',
    a.catalog_update ? 'update available' : '',
    a.status,
    appStatusInfo(a.status).label,
    ...(a.domains ?? []).map((d) => d.hostname),
  ]
    .join(' ')
    .toLowerCase()
  return words.every((w) => hay.includes(w))
}

function running(a: App): boolean {
  return a.status === 'running'
}

function sortValue(a: App, key: AppSort, now: number): string | number | null {
  switch (key) {
    case 'name':
      return a.name
    case 'status':
      return STATUS_RANK[a.status] ?? 9
    case 'cpu':
      return running(a) && a.live ? a.live.cpu : null
    case 'memory':
      return running(a) && a.live ? a.live.memory_mb : null
    case 'uptime':
      return running(a) && a.started_at ? now - new Date(a.started_at).getTime() : null
    case 'deployed': {
      const d = a.current_deployment
      const t = d?.finished_at ?? d?.queued_at
      return t ? new Date(t).getTime() : null
    }
  }
}

/** The apps in the order of one column. Apps without a value (not running, never deployed) go last; ties go by name. */
export function sortApps(list: App[], key: AppSort, desc: boolean, now = Date.now()): App[] {
  return [...list].sort((x, y) => {
    const a = sortValue(x, key, now)
    const b = sortValue(y, key, now)
    const ea = a === null
    const eb = b === null
    if (ea || eb) {
      if (ea !== eb) return ea ? 1 : -1
    } else {
      const r = typeof a === 'number' && typeof b === 'number' ? a - b : compareText(String(a), String(b))
      if (r !== 0) return desc ? -r : r
    }
    return compareText(x.name, y.name)
  })
}
