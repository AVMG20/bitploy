// Live events from the server (GET /api/v1/events) and what they mean for the
// cached queries. Pure on purpose (no Vue, no imports besides types), so Node
// can run its test: see tests/live.test.ts and docs/CONTRACT.md "Live events".

import type { MetricWindow, TrafficWindow } from './types'

export const keys = {
  me: ['me'] as const,
  apps: ['apps'] as const,
  /** Below apps on purpose: whatever refreshes the apps refreshes the projects that hold them. */
  projects: ['apps', 'projects'] as const,
  serviceTemplates: ['service-templates'] as const,
  catalog: ['catalog'] as const,
  catalogApp: (id: string) => ['catalog', id] as const,
  dockerfile: (slug: string) => ['dockerfile', slug] as const,
  app: (slug: string) => ['app', slug] as const,
  /** service: one service of a project with several; '' is the whole project. */
  metrics: (slug: string, window: MetricWindow, service = '') => ['metrics', slug, window, service] as const,
  traffic: (slug: string, window: TrafficWindow | 'today') => ['traffic', slug, window] as const,
  deployments: (slug: string) => ['deployments', slug] as const,
  env: (slug: string) => ['env', slug] as const,
  domains: (slug: string) => ['domains', slug] as const,
  backups: (slug: string) => ['backups', slug] as const,
  dbSchema: (slug: string, path: string) => ['db', slug, path, 'schema'] as const,
  /** Prefix of every rows query of one database file. */
  dbRows: (slug: string, path: string) => ['db', slug, path, 'rows'] as const,
  adminDbSchema: ['db', 'admin', 'schema'] as const,
  adminDbRows: ['db', 'admin', 'rows'] as const,
  tokens: ['tokens'] as const,
  sessions: ['sessions'] as const,
  github: ['github'] as const,
  githubRepos: (query: string) => ['github', 'repos', query] as const,
  githubBranches: (repo: string) => ['github', 'branches', repo] as const,
  adminGithub: ['admin', 'github'] as const,
  adminUsers: ['admin', 'users'] as const,
  adminInvites: ['admin', 'invites'] as const,
  adminServer: ['admin', 'server'] as const,
  adminApps: ['admin', 'apps'] as const,
  adminPublicPorts: ['admin', 'public-ports'] as const,
  adminAudit: ['admin', 'audit'] as const,
  adminAppDomains: ['admin', 'app-domains'] as const,
}

export type LiveEvent =
  | { type: 'hello' }
  | { type: 'resync' }
  | { type: 'app'; app?: string; old_app?: string; state?: string }
  | { type: 'deployment'; app: string; id: string; state: string }
  | { type: 'backup'; app: string }
  | { type: 'domain'; app: string }
  | { type: 'build_log'; app: string; id: string; lines: string[] }

/** The names to listen for on the EventSource (each frame has `event: <type>`). */
export const EVENT_TYPES = ['hello', 'resync', 'app', 'deployment', 'backup', 'domain', 'build_log'] as const

type Key = readonly unknown[]

export interface Effects {
  /** Everything: events were missed. */
  all?: boolean
  /** Mark stale and refetch what is on screen. */
  invalidate: Key[]
  /** Forget (the app is gone). */
  remove: Key[]
}

const TERMINAL = ['running', 'failed', 'superseded', 'cancelled']

/** What an event means for the query cache. */
export function effectsOf(ev: LiveEvent): Effects {
  const out: Effects = { invalidate: [], remove: [] }
  switch (ev.type) {
    case 'resync':
      out.all = true
      break
    case 'app': {
      out.invalidate.push(keys.apps, keys.me)
      if (ev.state === 'deleted') {
        if (ev.app) out.remove.push(keys.app(ev.app), keys.deployments(ev.app), keys.env(ev.app), keys.domains(ev.app), keys.backups(ev.app), keys.dockerfile(ev.app))
        break
      }
      for (const slug of [ev.app, ev.old_app]) {
        if (slug) out.invalidate.push(keys.app(slug), keys.deployments(slug), keys.env(slug), keys.domains(slug), keys.backups(slug), keys.dockerfile(slug))
      }
      break
    }
    case 'deployment':
      out.invalidate.push(keys.apps, keys.app(ev.app), keys.deployments(ev.app))
      // The Dockerfile editor shows the newest attempt's Dockerfile.
      if (ev.state === 'queued' || TERMINAL.includes(ev.state)) out.invalidate.push(keys.dockerfile(ev.app))
      if (TERMINAL.includes(ev.state)) out.invalidate.push(keys.me)
      break
    case 'backup':
      out.invalidate.push(keys.backups(ev.app), keys.app(ev.app), keys.me)
      break
    case 'domain':
      out.invalidate.push(keys.domains(ev.app), keys.app(ev.app))
      break
  }
  return out
}

/** Parse the data of a frame; null when it is not an event we know. */
export function parseEvent(data: string): LiveEvent | null {
  try {
    const v = JSON.parse(data) as { type?: unknown }
    return v && typeof v.type === 'string' && (EVENT_TYPES as readonly string[]).includes(v.type) ? (v as LiveEvent) : null
  } catch {
    return null
  }
}

/**
 * Add live build log lines to what is known. Lines that the polled log tail
 * already has (the last lines of `base` repeat at the start of `incoming`) are
 * not added twice. At most `max` lines are kept.
 */
export function mergeLines(base: readonly string[], incoming: readonly string[], max = 300): string[] {
  let skip = 0
  for (let k = Math.min(base.length, incoming.length); k > 0; k--) {
    let same = true
    for (let i = 0; i < k; i++) {
      if (base[base.length - k + i] !== incoming[i]) {
        same = false
        break
      }
    }
    if (same) {
      skip = k
      break
    }
  }
  const all = base.concat(incoming.slice(skip))
  return all.length > max ? all.slice(all.length - max) : all
}
