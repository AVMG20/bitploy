// Projects in plain words. A project is a space that holds apps: a website, its
// API, a worker, the database they share. Each app is deployed on its own; the
// apps of one project reach each other by their short name (alias) and can use
// each other's variables as ${{alias.VAR}}. Only erasable TypeScript, so Node
// can run the file in a test (tests/projects.test.ts).

import { imageParts, isMulti, publicService, serviceRole } from './services.ts'
import type { Role, RoleKey } from './services.ts'
import type { App, AppStatus, EnvVar, Project, ServiceTemplate } from './types'

// ---------------------------------------------------------------- identity

/** The project an app is in. A server without projects gets one per app, with the app's id. */
export function projectIdOf(app: Pick<App, 'id' | 'project'>): string {
  return app.project?.id || app.id
}

/** The app's short name in its project: its hostname for the other apps and the name in ${{alias.VAR}}. */
export function aliasOf(app: Pick<App, 'slug' | 'alias'>): string {
  return app.alias || app.slug
}

/** Where the other apps of the project reach it, like postgres:5432; "" when it does not listen. */
export function internalHostOf(app: Pick<App, 'slug' | 'alias' | 'internal_host' | 'port'>): string {
  if (app.internal_host) return app.internal_host
  return app.port ? `${aliasOf(app)}:${app.port}` : ''
}

/** Visitors can open it: a web app with an address. Apps from a service template are private. */
export function isPublicApp(app: Pick<App, 'type' | 'url' | 'template'>): boolean {
  return app.type === 'web' && !app.template && !!app.url
}

/** One project per app, for a server without projects; apps that name a project are grouped under it. */
export function projectsFromApps(apps: readonly App[]): Project[] {
  const byId = new Map<string, Project>()
  for (const a of apps) {
    const id = projectIdOf(a)
    const p = byId.get(id)
    if (p) p.apps.push(a)
    else byId.set(id, { id, name: a.project?.name || a.name, created_at: a.created_at, apps: [a] })
  }
  return [...byId.values()]
}

// ---------------------------------------------------------------- roles

/** Where an app sits on the project page: what visitors open, what runs in the background, what keeps data. */
export type Lane = 'public' | 'background' | 'data'

export interface AppRole extends Role {
  lane: Lane
}

const CATEGORY_ROLE: Record<string, RoleKey> = {
  database: 'database',
  databases: 'database',
  cache: 'cache',
  queue: 'queue',
  search: 'search',
  storage: 'files',
  files: 'files',
}

const DATA_ROLES: RoleKey[] = ['database', 'cache', 'search', 'files']
const API_NAME = /(^|[-_])(api|backend|server|graphql|gateway)([-_]|$)/

function laneOf(key: RoleKey, publicApp: boolean): Lane {
  if (DATA_ROLES.includes(key)) return 'data'
  return publicApp ? 'public' : 'background'
}

/** "16-alpine" → "16". */
function major(version: string): string {
  const m = /^v?(\d+)/.exec(version)
  return m ? m[1]! : ''
}

/**
 * What an app is for, in plain words: Website, API, Worker, Database ·
 * PostgreSQL 16, Cache · Redis 7. From its template when it has one, else
 * from its image, its type and its name.
 */
export function appRole(app: App, templates: readonly ServiceTemplate[] = []): AppRole {
  const publicApp = isPublicApp(app)
  const tpl = app.template ? templates.find((t) => t.id === app.template) : undefined
  const first = app.services?.[0]

  // Made from a template: the template says what it is.
  if (app.template) {
    const fromImage = serviceRole({ name: aliasOf(app), kind: 'image', image: tpl?.image ?? first?.image ?? app.template, public: false, port: app.port })
    const key = (tpl && CATEGORY_ROLE[tpl.category]) || fromImage.key
    // The version it runs (18.4), the same one its Database tab shows; a newer template version shows as "18.6 available".
    const v = app.template_version || major(tpl?.version ?? '') || major(imageParts(first?.image ?? tpl?.image).tag)
    const product = tpl ? `${tpl.name}${v ? ` ${v}` : ''}` : fromImage.product
    return { key, ...labelOf(key), product, lane: laneOf(key, false) }
  }

  // A compose app: what its public service is, or a worker set.
  if (isMulti(app)) {
    const pub = publicService(app)
    const key: RoleKey = pub && app.type === 'web' ? 'website' : 'worker'
    return { key, ...labelOf(key), product: '', lane: laneOf(key, publicApp) }
  }

  // One container: a well-known image (someone ran redis from a Docker image) says what it is.
  if (first?.kind === 'image') {
    const r = serviceRole({ ...first, public: false })
    if (DATA_ROLES.includes(r.key) || r.key === 'queue') return { ...r, ...labelOf(r.key), lane: laneOf(r.key, publicApp) }
  }
  if (app.type === 'worker') {
    const key: RoleKey = app.port && API_NAME.test(aliasOf(app)) ? 'api' : 'worker'
    return { key, ...labelOf(key), product: '', lane: 'background' }
  }
  const key: RoleKey = API_NAME.test(aliasOf(app)) || API_NAME.test(app.slug) ? 'api' : 'website'
  return { key, ...labelOf(key), product: '', lane: laneOf(key, publicApp) }
}

/** What a service template makes: Database, Cache, Search... */
export function templateRole(t: Pick<ServiceTemplate, 'id' | 'category' | 'image'>): RoleKey {
  return CATEGORY_ROLE[t.category] || serviceRole({ name: t.id, kind: 'image', image: t.image, public: false, port: null }).key
}

const APP_LABELS: Record<RoleKey, { label: string; blurb: string }> = {
  website: { label: 'Website', blurb: 'What visitors open at its address.' },
  api: { label: 'API', blurb: 'Answers requests from your website or other apps.' },
  worker: { label: 'Worker', blurb: 'Runs background jobs. Has no web address.' },
  database: { label: 'Database', blurb: 'Keeps your data. Only the apps of this project can reach it.' },
  cache: { label: 'Cache', blurb: 'Keeps data in memory for speed. Only the apps of this project can reach it.' },
  queue: { label: 'Queue', blurb: 'Passes jobs between the apps of this project.' },
  files: { label: 'File storage', blurb: 'Keeps uploaded files for the apps of this project.' },
  search: { label: 'Search', blurb: 'A search index for the apps of this project.' },
  service: { label: 'Service', blurb: 'Only the apps of this project can reach it.' },
}

function labelOf(key: RoleKey): { label: string; blurb: string } {
  return APP_LABELS[key]
}

/** "Database", "Cache", "Search": what a template makes, in one word. */
export function templateLabel(t: Pick<ServiceTemplate, 'id' | 'category' | 'image'>): string {
  return APP_LABELS[templateRole(t)].label
}

/** "Database · PostgreSQL 16", or "Website". */
export function roleTitle(role: Pick<Role, 'label' | 'product'>): string {
  return role.product ? `${role.label} · ${role.product}` : role.label
}

const LANE_ORDER: Record<Lane, number> = { public: 0, background: 1, data: 2 }
const ROLE_ORDER: Record<RoleKey, number> = { website: 0, api: 1, worker: 2, service: 3, queue: 4, search: 5, files: 6, cache: 7, database: 8 }

/** What visitors open first, then the background apps, then what keeps data; oldest first within each. */
export function sortProjectApps(apps: readonly App[], templates: readonly ServiceTemplate[] = []): App[] {
  return apps
    .map((a) => ({ a, r: appRole(a, templates) }))
    .sort((x, y) => LANE_ORDER[x.r.lane] - LANE_ORDER[y.r.lane] || ROLE_ORDER[x.r.key] - ROLE_ORDER[y.r.key] || x.a.created_at.localeCompare(y.a.created_at))
    .map((x) => x.a)
}

/** The app the project is about: its oldest public app, else its oldest app. */
export function mainApp(project: Pick<Project, 'apps'>): App | undefined {
  const byAge = [...project.apps].sort((a, b) => a.created_at.localeCompare(b.created_at))
  return byAge.find((a) => isPublicApp(a)) ?? byAge[0]
}

// ---------------------------------------------------------------- where the code comes from

/** One short line: GitHub owner/repo, Uploaded files, Compose file with 3 services, PostgreSQL 16... */
export function appSourceText(app: App, templates: readonly ServiceTemplate[] = []): string {
  if (app.template) {
    const r = appRole(app, templates)
    return r.product || app.services?.[0]?.image || app.template
  }
  if (isMulti(app)) return `Compose file, ${app.services.length} services`
  if (app.git_source) return app.git_source.repo ?? app.git_source.repo_url.replace(/^https?:\/\/(www\.)?github\.com\//, '').replace(/\.git$/, '')
  if (app.catalog_id) return 'Ready-made app'
  const dep = app.current_deployment
  if (!dep) return 'Not deployed yet'
  if (app.static) return 'Static website'
  if (dep.source_kind === 'files') return 'Pasted Dockerfile'
  return 'Uploaded files'
}

// ---------------------------------------------------------------- status

const STATUS_RANK: Record<string, number> = { failed: 0, crashed: 1, deploying: 2, running: 3, stopped: 4, new: 5 }

export interface ProjectHealth {
  /** The status to show for the whole project: the most urgent one of its apps. */
  status: AppStatus | 'empty'
  running: number
  total: number
}

export function projectHealth(apps: readonly Pick<App, 'status'>[]): ProjectHealth {
  if (!apps.length) return { status: 'empty', running: 0, total: 0 }
  let status = apps[0]!.status
  for (const a of apps) if ((STATUS_RANK[a.status] ?? 9) < (STATUS_RANK[status] ?? 9)) status = a.status
  // Some running and the rest stopped on purpose: the project counts as running.
  if (status === 'stopped' && apps.some((a) => a.status === 'running')) status = 'running'
  return { status, running: apps.filter((a) => a.status === 'running').length, total: apps.length }
}

/**
 * The project's state in a few words, the same on its card and its page:
 * "api failed", "Deploying", "All 4 running", "2 of 4 running", "Stopped".
 */
export function projectHealthText(apps: readonly Pick<App, 'status' | 'slug' | 'alias'>[]): string {
  const h = projectHealth(apps)
  if (h.status === 'empty') return 'Empty'
  if (h.status === 'failed' || h.status === 'crashed') {
    const bad = apps.filter((a) => a.status === 'failed' || a.status === 'crashed').map(aliasOf)
    return `${bad.join(', ')} ${h.status === 'failed' ? 'failed' : 'crashed'}`
  }
  if (h.status === 'deploying') return 'Deploying'
  if (h.status === 'new') return 'Not deployed yet'
  if (h.running === h.total) return h.total === 1 ? 'Running' : `All ${h.total} running`
  if (h.running === 0) return 'Stopped'
  return `${h.running} of ${h.total} running`
}

/** Memory and CPU the project's apps reserve, and what they use now. */
export function projectUsage(apps: readonly App[]): { memoryMB: number; cpuMillis: number; memoryNowMB: number; cpuNowPercent: number } {
  let memoryMB = 0
  let cpuMillis = 0
  let memoryNowMB = 0
  let cpuNowPercent = 0
  for (const a of apps) {
    if (!a.static) {
      memoryMB += a.memory_mb
      cpuMillis += a.cpu_millis
    }
    if (a.status === 'running' && a.live) {
      memoryNowMB += a.live.memory_mb
      cpuNowPercent += a.live.cpu
    }
  }
  return { memoryMB, cpuMillis, memoryNowMB, cpuNowPercent }
}

// ---------------------------------------------------------------- variable references

export interface Reference {
  /** The alias of the app the value comes from. */
  app: string
  variable: string
  /** As written: ${{postgres.DATABASE_URL}} */
  raw: string
}

const REF = /\$\{\{\s*([a-z0-9](?:[a-z0-9-]*[a-z0-9])?)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g

/** Every ${{app.VAR}} in a value, in order. */
export function parseReferences(text: string | null | undefined): Reference[] {
  if (!text) return []
  const out: Reference[] = []
  for (const m of text.matchAll(REF)) out.push({ app: m[1]!, variable: m[2]!, raw: m[0] })
  return out
}

/** A value cut into plain text and references, to show each reference as a chip naming the other app. */
export function referenceParts(text: string | null | undefined): ({ text: string } | { ref: Reference })[] {
  if (!text) return []
  const out: ({ text: string } | { ref: Reference })[] = []
  let at = 0
  for (const m of text.matchAll(REF)) {
    if (m.index! > at) out.push({ text: text.slice(at, m.index) })
    out.push({ ref: { app: m[1]!, variable: m[2]!, raw: m[0] } })
    at = m.index! + m[0].length
  }
  if (at < text.length) out.push({ text: text.slice(at) })
  return out
}

/** ${{postgres.DATABASE_URL}} */
export function referenceText(alias: string, variable: string): string {
  return `\${{${alias}.${variable}}}`
}

/** The references of one variable: from the server's `references`, else from what `masked` shows. */
export function envReferences(v: Pick<EnvVar, 'references' | 'masked'>): Reference[] {
  const own = parseReferences((v.references ?? []).join(' '))
  return own.length ? own : parseReferences(v.masked)
}

/** One app uses another: its variables point there. */
export interface AppLink {
  /** id of the app that reads the variable */
  from: string
  /** id of the app the value comes from */
  to: string
  /** The variables of `from` that point at `to`, like DATABASE_URL. */
  vars: string[]
}

/**
 * Who talks to whom in a project, from the variables of each app (by slug).
 * Apps whose variables are not loaded yet have no links; references to an
 * alias that is not in the project are left out.
 */
export function projectLinks(apps: readonly App[], envBySlug: Readonly<Record<string, readonly EnvVar[] | undefined>>): AppLink[] {
  const byAlias = new Map<string, App>()
  for (const a of apps) byAlias.set(aliasOf(a), a)
  const links = new Map<string, AppLink>()
  for (const a of apps) {
    // App.references names the apps it reads from before its variables are loaded.
    for (const alias of a.references ?? []) {
      const to = byAlias.get(alias)
      if (!to || to.id === a.id) continue
      const key = `${a.id}>${to.id}`
      if (!links.has(key)) links.set(key, { from: a.id, to: to.id, vars: [] })
    }
    for (const v of envBySlug[a.slug] ?? []) {
      for (const r of envReferences(v)) {
        const to = byAlias.get(r.app)
        if (!to || to.id === a.id) continue
        const key = `${a.id}>${to.id}`
        const l = links.get(key) ?? { from: a.id, to: to.id, vars: [] }
        if (!l.vars.includes(v.name)) l.vars.push(v.name)
        links.set(key, l)
      }
    }
  }
  return [...links.values()]
}

/** The apps that read a variable of this one, like "web" for postgres. */
export function usedBy(app: App, apps: readonly App[], links: readonly AppLink[]): App[] {
  const ids = new Set(links.filter((l) => l.to === app.id).map((l) => l.from))
  return apps.filter((a) => ids.has(a.id))
}

/** The apps this one reads variables from. */
export function talksTo(app: App, apps: readonly App[], links: readonly AppLink[]): App[] {
  const ids = new Set(links.filter((l) => l.from === app.id).map((l) => l.to))
  return apps.filter((a) => ids.has(a.id))
}

// ---------------------------------------------------------------- adding a service

/** The template list in the groups the Add picker shows. */
export const TEMPLATE_GROUPS: { key: string; label: string; categories: string[] }[] = [
  { key: 'database', label: 'Databases', categories: ['database', 'databases'] },
  { key: 'cache', label: 'Cache & queue', categories: ['cache', 'queue'] },
  { key: 'search', label: 'Search', categories: ['search'] },
]

export function groupTemplates(templates: readonly ServiceTemplate[]): { key: string; label: string; items: ServiceTemplate[] }[] {
  const known = new Set(TEMPLATE_GROUPS.flatMap((g) => g.categories))
  const out = TEMPLATE_GROUPS.map((g) => ({ key: g.key, label: g.label, items: templates.filter((t) => g.categories.includes(t.category)) }))
  out.push({ key: 'other', label: 'Other services', items: templates.filter((t) => !known.has(t.category)) })
  return out.filter((g) => g.items.length)
}

/** A free short name for a new app in the project: postgres, else postgres-2, postgres-3... */
export function freeAlias(base: string, taken: Iterable<string>): string {
  const used = new Set(taken)
  const clean = base.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32) || 'app'
  if (!used.has(clean)) return clean
  for (let i = 2; i < 100; i++) {
    const c = `${clean.slice(0, 29)}-${i}`
    if (!used.has(c)) return c
  }
  return `${clean.slice(0, 24)}-${Date.now() % 100000}`
}

/** The apps "Connect to" offers: everything that runs your code on the server, not other templates or static sites. */
export function connectable(apps: readonly App[]): App[] {
  // A site bitploy serves runs none of its code on the server, so it cannot use a database.
  return apps.filter((a) => !a.template && !a.static && a.build_mode !== 'static' && a.build_mode !== 'static_build')
}

/** Ticked at first: the project's main public app, unless it already has the variable (`skip`). */
export function defaultConnect(project: Pick<Project, 'apps'>, skip: readonly string[] = []): string[] {
  const main = mainApp({ apps: connectable(project.apps) })
  return main && isPublicApp(main) && !skip.includes(main.slug) ? [main.slug] : []
}

/**
 * The variable an app already sets for all its services, like DATABASE_URL:
 * connecting a new database to it would have to replace it, so the server
 * refuses (connect_to_var_set) and the dialog does not tick it.
 */
export function sharedVar(vars: readonly EnvVar[] | undefined, name: string): EnvVar | undefined {
  if (!name) return undefined
  return vars?.find((v) => v.name === name && !v.service)
}

/**
 * Where a variable of the app points, in words: "postgres" for
 * ${{postgres.DATABASE_URL}}, else "" (its own value, or not loaded).
 */
export function varSource(v: Pick<EnvVar, 'references' | 'masked'> | undefined): string {
  if (!v) return ''
  return [...new Set(envReferences(v).map((r) => r.app))].join(', ')
}

/**
 * The project already runs this kind of service: a template app or a compose
 * service that is a database (or cache, or search engine). The canvas does not
 * suggest adding a second one.
 */
export function projectHasRole(apps: readonly App[], key: RoleKey, templates: readonly ServiceTemplate[] = []): boolean {
  return apps.some((a) => appRole(a, templates).key === key || (isMulti(a) && a.services.some((s) => serviceRole(s).key === key)))
}

// ---------------------------------------------------------------- public port

/** "57 min", "3 h", "2 days": how long until a time, for "closes in ...". */
function untilText(at: string, now: number): string {
  const min = Math.max(1, Math.round((Date.parse(at) - now) / 60_000))
  if (min < 60) return `${min} min`
  const h = Math.round(min / 60)
  if (h < 48) return `${h} h`
  return `${Math.round(h / 24)} days`
}

/**
 * An open public port in a few words, for the card and the header:
 * "Public at 203.0.113.5:30001, closes in 57 min". null while it is closed.
 */
export function publicPortText(app: Pick<App, 'public_port'>, now = Date.now()): { text: string; title: string } | null {
  const p = app.public_port
  if (!p?.enabled) return null
  const at = p.address || (p.host && p.port ? `${p.host}:${p.port}` : '')
  const closes = p.expires_at ? (Date.parse(p.expires_at) > now ? `closes in ${untilText(p.expires_at, now)}` : 'closing') : 'open until you close it'
  const who = p.allow_from.length ? `Only ${p.allow_from.join(', ')} can connect.` : 'Anyone on the internet can try to connect.'
  return {
    text: `Public${at ? ` at ${at}` : ''}, ${closes}`,
    title: `A public port is open${at ? ` at ${at}` : ''}. ${who}${p.running ? '' : ' It is not forwarding connections right now.'} Close it in Settings when you are done.`,
  }
}

// ---------------------------------------------------------------- the Docker box

/**
 * What was pasted into the one Docker box: a compose file (a top-level
 * services: key), a single image reference (ghcr.io/owner/app:1.2), or a Dockerfile.
 */
export function pastedKind(text: string): 'empty' | 'compose' | 'image' | 'dockerfile' {
  const t = text.trim()
  if (!t) return 'empty'
  if (/^services\s*:/m.test(t)) return 'compose'
  if (!/\s/.test(t) && /^[a-z0-9][a-z0-9._/-]*(:[A-Za-z0-9._-]+)?(@sha256:[a-f0-9]{64})?$/.test(t) && !/^from$/i.test(t)) return 'image'
  return 'dockerfile'
}

// ---------------------------------------------------------------- deleting an app others use

/** An app whose variables point at the one being deleted. */
export interface Dependent {
  slug: string
  alias: string
  /** Its variables that read from the app, like DATABASE_URL. */
  vars: string[]
}

/** Who uses this app, from the project's links: the delete dialog shows them before anyone tries. */
export function dependentsOf(app: Pick<App, 'id'>, apps: readonly App[], links: readonly AppLink[]): Dependent[] {
  const out: Dependent[] = []
  for (const l of links) {
    if (l.to !== app.id || l.from === app.id) continue
    const from = apps.find((a) => a.id === l.from)
    if (from) out.push({ slug: from.slug, alias: aliasOf(from), vars: [...l.vars] })
  }
  return out.sort((a, b) => a.alias.localeCompare(b.alias))
}

/** The dependents an app_in_use error lists in details.dependents; [] for anything else. */
export function dependentsFromError(details: unknown): Dependent[] {
  const list = (details as { dependents?: unknown } | null | undefined)?.dependents
  if (!Array.isArray(list)) return []
  return list.flatMap((d): Dependent[] => {
    const o = d as { slug?: unknown; alias?: unknown; vars?: unknown } | null
    if (!o || typeof o.slug !== 'string' || !o.slug) return []
    const vars = Array.isArray(o.vars) ? o.vars.filter((v): v is string => typeof v === 'string') : []
    return [{ slug: o.slug, alias: typeof o.alias === 'string' && o.alias ? o.alias : o.slug, vars }]
  })
}

/**
 * A quiet note for a database or cache whose template has a newer version:
 * "18.6 available", and a sentence for its title. null when it is up to date.
 * bitploy never upgrades it by itself.
 */
export function templateUpdate(app: Pick<App, 'template_version' | 'template_latest' | 'template_update_available'>): { text: string; title: string } | null {
  if (!app.template_update_available || !app.template_latest) return null
  return {
    text: `${app.template_latest} available`,
    title: `Runs ${app.template_version || 'an older version'}; ${app.template_latest} is available. bitploy does not update a database by itself, because a new major version can need a dump and restore.`,
  }
}
