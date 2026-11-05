// Dev only: never imported by the build.
//
// An in-memory stand-in for bitploy's /api/v1, so the dashboard can be looked
// at without the Go server (and before the projects API exists). Start it with
//   npx vite --config vite.mock.config.ts --port 5174
// MOCK_NO_PROJECTS=1 (or the request header x-mock-no-projects: 1) makes
// /projects and /service-templates answer 404, to try the one-project-per-app
// fallback.

import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Plugin } from 'vite'
import type {
  App,
  AppService,
  AppVolume,
  BackupList,
  CatalogApp,
  ComposeCheck,
  ComposeService,
  DataFileList,
  Deployment,
  DeploymentState,
  EnvVar,
  GitHubOverview,
  GitHubRepo,
  Me,
  MetricPoint,
  Metrics,
  Project,
  ServiceTemplate,
  SessionInfo,
  Traffic,
  User,
} from '../src/lib/types'

// ---------------------------------------------------------------- helpers

const NOW = Date.now()
const ago = (minutes: number) => new Date(NOW - minutes * 60_000).toISOString()
const MASK = '••••••••'
const DOMAIN = 'bitploy.app'

/** A small deterministic random generator, seeded per app, so charts keep their shape between requests. */
function seeded(seed: string): () => number {
  let h = 2166136261
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619)
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507)
    h = Math.imul(h ^ (h >>> 13), 3266489909)
    h ^= h >>> 16
    return (h >>> 0) / 4294967296
  }
}

let idSeq = 100
const newId = (prefix: string) => `${prefix}_${(idSeq++).toString(36)}${Math.random().toString(36).slice(2, 8)}`

// ---------------------------------------------------------------- the person

const user: User = { id: 'usr_ada', email: 'ada@example.com', name: 'Ada', role: 'admin', status: 'active', created_at: ago(60 * 24 * 90) }

// ---------------------------------------------------------------- templates

// The shipped templates (internal/servicetpl). redis is hidden: only used when asked for by id.
const templates: ServiceTemplate[] = [
  {
    id: 'postgres',
    name: 'PostgreSQL',
    category: 'database',
    description: 'The open-source SQL database most frameworks support. Other apps of the project connect with DATABASE_URL.',
    image: 'postgres:18.6',
    version: '18.6',
    port: 5432,
    memory_mb: 256,
    cpu_millis: 500,
    volume_mb: 2048,
    connection_vars: ['DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'],
    main_var: 'DATABASE_URL',
  },
  {
    id: 'pgvector',
    name: 'PostgreSQL with pgvector',
    category: 'database',
    description: 'PostgreSQL with the pgvector extension for embeddings and similarity search. Run CREATE EXTENSION vector; once in your database.',
    image: 'pgvector/pgvector:0.8.7-pg18',
    version: '0.8.7-pg18',
    port: 5432,
    memory_mb: 256,
    cpu_millis: 500,
    volume_mb: 2048,
    connection_vars: ['DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'],
    main_var: 'DATABASE_URL',
  },
  {
    id: 'mariadb',
    name: 'MariaDB',
    category: 'database',
    description: 'A MySQL-compatible SQL database. Apps written for MySQL connect to it with DATABASE_URL or the MYSQL_ variables.',
    image: 'mariadb:11.8.9',
    version: '11.8.9',
    port: 3306,
    memory_mb: 256,
    cpu_millis: 500,
    volume_mb: 2048,
    connection_vars: ['DATABASE_URL', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_USER', 'MYSQL_PASSWORD', 'MYSQL_DATABASE'],
    main_var: 'DATABASE_URL',
  },
  {
    id: 'mysql',
    name: 'MySQL',
    category: 'database',
    description: "Oracle's MySQL server, for apps that need MySQL itself. MariaDB uses less memory and suits most apps that ask for MySQL.",
    image: 'mysql:8.4.13',
    version: '8.4.13',
    port: 3306,
    memory_mb: 512,
    cpu_millis: 500,
    volume_mb: 2048,
    connection_vars: ['DATABASE_URL', 'MYSQL_HOST', 'MYSQL_PORT', 'MYSQL_USER', 'MYSQL_PASSWORD', 'MYSQL_DATABASE'],
    main_var: 'DATABASE_URL',
  },
  {
    id: 'valkey',
    name: 'Valkey (Redis-compatible)',
    category: 'cache',
    description: 'An in-memory key-value store that speaks the Redis protocol, for caches, sessions and job queues. Other apps connect with REDIS_URL.',
    image: 'valkey/valkey:9.1.3',
    version: '9.1.3',
    port: 6379,
    memory_mb: 128,
    cpu_millis: 250,
    volume_mb: 512,
    connection_vars: ['REDIS_URL', 'REDIS_HOST', 'REDIS_PORT', 'REDIS_PASSWORD', 'VALKEY_URL'],
    main_var: 'REDIS_URL',
  },
  {
    id: 'meilisearch',
    name: 'Meilisearch',
    category: 'search',
    description: 'A search engine with typo tolerance and filters, for search boxes. Other apps connect with MEILI_URL and MEILI_MASTER_KEY.',
    image: 'getmeili/meilisearch:v1.56.1',
    version: 'v1.56.1',
    port: 7700,
    memory_mb: 512,
    cpu_millis: 500,
    volume_mb: 2048,
    connection_vars: ['MEILI_URL', 'MEILI_HOST', 'MEILI_MASTER_KEY'],
    main_var: 'MEILI_URL',
  },
]

// ---------------------------------------------------------------- catalog

const catalog: CatalogApp[] = [
  {
    id: 'uptime-kuma',
    name: 'Uptime Kuma',
    tagline: 'Watch your websites and get told when one goes down.',
    description: 'Checks websites, APIs and servers every minute and shows a status page.',
    category: 'Monitoring',
    version: '1.23.16',
    website: 'https://uptime.kuma.pet',
    port: 3001,
    memory_mb: 256,
    volume: true,
    volume_mb: 1024,
    health_path: '/',
    env: [],
    first_steps: ['Open {url} and create the admin account.', 'Add your first monitor.'],
    dockerfile: 'FROM louislam/uptime-kuma:1.23.16\nUSER node\nHEALTHCHECK NONE\n',
  },
  {
    id: 'vaultwarden',
    name: 'Vaultwarden',
    tagline: 'Your own password manager, works with the Bitwarden apps.',
    description: 'A Bitwarden-compatible server. Keeps the vault in /data.',
    category: 'Security',
    version: '1.32.5',
    website: 'https://github.com/dani-garcia/vaultwarden',
    port: 80,
    memory_mb: 128,
    volume: true,
    volume_mb: 1024,
    health_path: '/alive',
    env: [{ name: 'ADMIN_TOKEN', description: 'Opens the admin page at /admin.', generated: true }],
    first_steps: ['Open {url} and create your account.'],
    dockerfile: 'FROM vaultwarden/server:1.32.5\nHEALTHCHECK NONE\n',
  },
  {
    id: 'pocketbase',
    name: 'PocketBase',
    tagline: 'A backend in one file: database, auth and file storage.',
    description: 'SQLite database with a REST API, users and an admin UI.',
    category: 'Backend',
    version: '0.22.21',
    website: 'https://pocketbase.io',
    port: 8090,
    memory_mb: 128,
    volume: true,
    volume_mb: 2048,
    health_path: '/api/health',
    env: [],
    first_steps: ['Open {url}/_/ and create the admin account.'],
    dockerfile: 'FROM alpine:3.20\n# downloads pocketbase\nUSER 1000\n',
  },
  {
    id: 'it-tools',
    name: 'IT-Tools',
    tagline: 'Handy tools for developers: encoders, converters, generators.',
    description: 'A static website with 80+ small tools. Keeps no data.',
    category: 'Developer tools',
    version: '2024.10.22',
    website: 'https://it-tools.tech',
    port: 80,
    memory_mb: 64,
    volume: false,
    volume_mb: 0,
    health_path: '/',
    env: [],
    first_steps: ['Open {url}.'],
    dockerfile: 'FROM nginx:1.27-alpine\nCOPY dist /usr/share/nginx/html\n',
  },
]

// ---------------------------------------------------------------- apps and projects

interface ProjectRow {
  id: string
  name: string
  created_at: string
  updated_at?: string
}

const projects: ProjectRow[] = [
  { id: 'prj_portfolio', name: 'Portfolio', created_at: ago(60 * 24 * 60) },
  { id: 'prj_shop', name: 'Shop', created_at: ago(60 * 24 * 30) },
  { id: 'prj_notes', name: 'Notes stack', created_at: ago(60 * 24 * 12) },
  { id: 'prj_status', name: 'Status page', created_at: ago(60 * 24 * 2) },
]

const apps: App[] = []
const env: Record<string, EnvVar[]> = {}
const deployments: Record<string, Deployment[]> = {}

function service(extra: Partial<AppService> & { name: string }): AppService {
  return { kind: 'build', public: false, port: null, health_path: '/', memory_mb: 256, cpu_millis: 250, volumes: [], depends_on: [], ...extra }
}

function liveOf(slug: string, memoryMB: number, cpuMillis: number): MetricPoint {
  const r = seeded(`${slug}-live`)
  return { ts: new Date().toISOString(), cpu: Math.round(r() * cpuMillis * 0.12 * 10) / 10, memory_mb: Math.round(memoryMB * (0.25 + r() * 0.35)), rx_bytes: 0, tx_bytes: 0 }
}

function deployment(app: App, seq: number, state: DeploymentState, minutesAgo: number, extra: Partial<Deployment> = {}): Deployment {
  const finished = ['running', 'failed', 'superseded', 'cancelled'].includes(state)
  const git = !!app.git_source
  const sha = git ? seeded(`${app.slug}-${seq}`)().toString(16).slice(2, 14).padEnd(12, '0') + '9f3c2a1b7e6d4c5f8a90b1c2' : null
  return {
    id: `dep_${app.slug}_${seq}`,
    app_id: app.id,
    app_slug: app.slug,
    seq,
    state,
    source_kind: git ? 'git' : app.catalog_id ? 'files' : 'upload',
    commit_sha: sha,
    commit_message: git ? ['Fix checkout total rounding', 'Add product search', 'Update dependencies', 'First version'][seq % 4] : null,
    channel: git ? 'github' : 'dashboard',
    image: `bitploy/${app.slug}:${seq}`,
    error: null,
    warnings: [],
    queued_at: ago(minutesAgo),
    started_at: ago(minutesAgo - 0.1),
    finished_at: finished ? ago(minutesAgo - 1.5) : null,
    duration_seconds: finished ? 84 + seq * 3 : null,
    url: app.url,
    ...extra,
  }
}

interface AppSeed {
  slug: string
  name: string
  project: string
  alias: string
  template?: string
  type?: 'web' | 'worker'
  port: number | null
  services?: AppService[]
  volumes?: AppVolume[]
  repo?: string
  catalog?: string
  build_mode?: App['build_mode']
  static?: boolean
  status?: App['status']
  memory?: number
  cpu?: number
  createdDaysAgo: number
  deploys?: number
}

function makeApp(s: AppSeed): App {
  const type = s.type ?? 'web'
  const memory = s.memory ?? (s.services ? s.services.reduce((n, x) => n + x.memory_mb, 0) : 256)
  const cpu = s.cpu ?? (s.services ? s.services.reduce((n, x) => n + x.cpu_millis, 0) : 250)
  const proj = projects.find((p) => p.id === s.project)!
  const tpl = s.template ? templates.find((t) => t.id === s.template) : undefined
  const services = s.services ?? [
    tpl
      ? service({ name: 'app', kind: 'image', image: tpl.image, port: s.port, health_path: '', memory_mb: memory, cpu_millis: cpu, volumes: ['data'] })
      : service({ name: 'app', public: type === 'web', port: s.port, memory_mb: memory, cpu_millis: cpu }),
  ]
  const volumes: AppVolume[] =
    s.volumes ?? (tpl ? [{ name: 'data', service: 'app', mount_path: tpl.id === 'postgres' ? '/var/lib/postgresql/data' : '/data', size_mb: tpl.volume_mb, used_mb: Math.round(tpl.volume_mb * 0.07) }] : [])
  const status = s.status ?? 'running'
  const a: App = {
    id: `app_${s.slug.replace(/-/g, '_')}`,
    catalog_id: s.catalog ?? '',
    catalog_version: s.catalog ? (catalog.find((c) => c.id === s.catalog)?.version ?? '') : '',
    catalog_update: null,
    slug: s.slug,
    name: s.name,
    type,
    url: type === 'web' ? `https://${s.slug}.${DOMAIN}` : '',
    domain: DOMAIN,
    owner_id: user.id,
    status,
    desired_state: 'running',
    port: s.port,
    health_path: '/',
    dockerfile_path: 'Dockerfile',
    context_dir: '',
    build_mode: s.build_mode ?? (tpl || s.catalog ? 'dockerfile' : 'auto'),
    output_dir: '',
    home_page: '',
    volume: volumes.length > 0 && !s.services,
    volume_mb: volumes[0]?.size_mb ?? 0,
    volume_used_mb: volumes[0]?.used_mb ?? null,
    files_mb: 180 + Math.round(seeded(s.slug)() * 400),
    backups_mb: volumes.length ? 24 : 0,
    disk_measured_at: ago(40),
    memory_mb: memory,
    cpu_millis: cpu,
    static: !!s.static,
    git_source: s.repo
      ? {
          repo_url: `https://github.com/${s.repo}`,
          branch: 'main',
          repo: s.repo,
          repo_html_url: `https://github.com/${s.repo}`,
          auto_deploy: true,
          push_ready: true,
          push_status: 'ready',
          push_message: 'Every push to main deploys.',
          last_push_at: ago(60 * 5),
        }
      : null,
    protected: false,
    current_deployment: null,
    domains: [],
    started_at: status === 'running' ? ago(60 * 26 + s.createdDaysAgo) : null,
    restart_count: 0,
    live: status === 'running' ? liveOf(s.slug, memory, cpu) : null,
    services,
    volumes,
    compose_path: s.build_mode === 'compose' ? 'compose.yaml' : '',
    project: { id: proj.id, name: proj.name },
    alias: s.alias,
    template: s.template ?? null,
    ...(s.template ? { template_version: templates.find((t) => t.id === s.template)?.version, connection_vars: templates.find((t) => t.id === s.template)?.connection_vars, main_var: templates.find((t) => t.id === s.template)?.main_var } : {}),
    references: [],
    internal_host: s.port ? `${s.alias}:${s.port}` : '',
    created_at: ago(60 * 24 * s.createdDaysAgo),
    updated_at: ago(60),
  }
  const n = s.deploys ?? 3
  const list: Deployment[] = []
  for (let i = 1; i <= n; i++) {
    const last = i === n
    const state: DeploymentState = last ? (status === 'deploying' ? 'building' : 'running') : 'superseded'
    list.unshift(deployment(a, i, state, 60 * 24 * (n - i) + 90))
  }
  deployments[a.slug] = list
  a.current_deployment = list.find((d) => d.state === 'running') ?? null
  return a
}

/** The ${{alias.VAR}} tokens of a value, as EnvVar.references. */
function refsOf(value: string | undefined): string[] | undefined {
  const m = value?.match(/\$\{\{[^}]+\}\}/g)
  return m ? [...new Set(m)] : undefined
}

/** App.references: the aliases its variables refer to. */
function syncRefs(a: App) {
  const aliases = new Set<string>()
  for (const v of env[a.slug] ?? []) for (const r of v.references ?? []) aliases.add(r.slice(3, r.indexOf('.')).trim())
  a.references = [...aliases]
}

function seedEnv(slug: string, vars: { name: string; value?: string; masked?: string; service?: string }[]) {
  env[slug] = vars.map((v) => ({ name: v.name, masked: v.masked ?? MASK, references: refsOf(v.value), service: v.service ?? '', updated_at: ago(60 * 24 * 3) }))
}

apps.push(
  makeApp({ slug: 'portfolio', name: 'Portfolio', project: 'prj_portfolio', alias: 'portfolio', port: 80, repo: 'ada/portfolio', static: true, memory: 32, cpu: 100, createdDaysAgo: 60, deploys: 5 }),
  makeApp({ slug: 'shop-web', name: 'web', project: 'prj_shop', alias: 'web', port: 3000, repo: 'ada/shop', memory: 512, cpu: 500, createdDaysAgo: 30, deploys: 4 }),
  makeApp({ slug: 'shop-api', name: 'api', project: 'prj_shop', alias: 'api', type: 'worker', port: 8080, repo: 'ada/api-go', memory: 256, cpu: 250, createdDaysAgo: 29, deploys: 3 }),
  makeApp({ slug: 'shop-postgres', name: 'postgres', project: 'prj_shop', alias: 'postgres', template: 'postgres', type: 'worker', port: 5432, memory: 256, cpu: 250, createdDaysAgo: 28, deploys: 1,
    volumes: [{ name: 'pgdata', service: 'app', mount_path: '/var/lib/postgresql/data', size_mb: 2048, used_mb: 184 }] }),
  makeApp({ slug: 'shop-redis', name: 'redis', project: 'prj_shop', alias: 'redis', template: 'valkey', type: 'worker', port: 6379, memory: 128, cpu: 250, createdDaysAgo: 28, deploys: 1 }),
  makeApp({
    slug: 'notes',
    name: 'Notes',
    project: 'prj_notes',
    alias: 'notes',
    port: 3000,
    repo: 'ada/notes',
    build_mode: 'compose',
    createdDaysAgo: 12,
    services: [
      service({ name: 'web', public: true, port: 3000, memory_mb: 512, cpu_millis: 250, depends_on: ['db', 'cache'] }),
      service({ name: 'worker', port: null, memory_mb: 256, cpu_millis: 250, depends_on: ['db'] }),
      service({ name: 'db', kind: 'image', image: 'postgres:16', port: 5432, health_path: '', memory_mb: 256, cpu_millis: 250, volumes: ['pgdata'] }),
      service({ name: 'cache', kind: 'image', image: 'valkey/valkey:8', port: 6379, health_path: '', memory_mb: 64, cpu_millis: 100 }),
    ],
    volumes: [{ name: 'pgdata', service: 'db', mount_path: '/var/lib/postgresql/data', size_mb: 5120, used_mb: 312 }],
  }),
  makeApp({ slug: 'uptime', name: 'Uptime Kuma', project: 'prj_status', alias: 'uptime', port: 3001, catalog: 'uptime-kuma', status: 'deploying', memory: 256, cpu: 250, createdDaysAgo: 0.01, deploys: 1,
    volumes: [{ name: 'data', service: 'app', mount_path: '/data', size_mb: 1024, used_mb: null }] }),
)
// The status page's first deploy is still building.
{
  const up = apps.find((a) => a.slug === 'uptime')!
  up.volume = true
  up.current_deployment = null
  up.started_at = null
}

seedEnv('portfolio', [])
seedEnv('shop-web', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'API_URL', value: 'http://${{api.HOST}}:8080' },
  { name: 'SESSION_SECRET' },
])
seedEnv('shop-api', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' },
  { name: 'STRIPE_KEY', masked: 'sk_l••••' },
])
seedEnv('shop-postgres', ['DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'].map((name) => ({ name })))
seedEnv('shop-redis', ['REDIS_URL', 'REDIS_HOST', 'REDIS_PORT', 'REDIS_PASSWORD', 'VALKEY_URL'].map((name) => ({ name })))
seedEnv('notes', [{ name: 'POSTGRES_PASSWORD' }, { name: 'SECRET_KEY' }, { name: 'SMTP_URL', service: 'worker' }])
seedEnv('uptime', [])
for (const a of apps) syncRefs(a)

function projectOf(row: ProjectRow): Project {
  const own = apps.filter((a) => a.project?.id === row.id)
  for (const a of own) syncRefs(a)
  return { ...row, updated_at: row.updated_at ?? row.created_at, network: `bp-p-${row.id.replace(/^prj_/, '')}`, apps: own }
}

function findApp(slug: string): App | undefined {
  return apps.find((a) => a.slug === slug || a.id === slug)
}

function slugish(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40) || 'app'
}

function uniqueSlug(base: string): string {
  let s = base
  for (let i = 2; findApp(s); i++) s = `${base}-${i}`
  return s
}

/** Queues a deployment and lets it go live after about 6 seconds. */
function startDeploy(app: App, kind: Deployment['source_kind'] = 'upload'): Deployment {
  const list = deployments[app.slug] ?? (deployments[app.slug] = [])
  const seq = (list[0]?.seq ?? 0) + 1
  const d = deployment(app, seq, 'queued', 0, { source_kind: kind, finished_at: null, duration_seconds: null, started_at: null })
  list.unshift(d)
  app.status = 'deploying'
  const steps: [number, DeploymentState][] = [
    [800, 'fetching'],
    [1600, 'building'],
    [4500, 'starting'],
    [5200, 'health_check'],
    [6000, 'running'],
  ]
  for (const [ms, state] of steps) {
    setTimeout(() => {
      d.state = state
      if (!d.started_at) d.started_at = new Date().toISOString()
      if (state === 'running') {
        for (const old of list) if (old !== d && old.state === 'running') old.state = 'superseded'
        d.finished_at = new Date().toISOString()
        d.duration_seconds = 6
        app.status = 'running'
        app.current_deployment = d
        app.started_at = d.finished_at
        app.live = liveOf(app.slug, app.memory_mb, app.cpu_millis)
      }
    }, ms)
  }
  return d
}

function blankApp(name: string, slug: string, projectId: string, extra: Partial<App> = {}): App {
  const proj = projects.find((p) => p.id === projectId)!
  const a: App = {
    id: newId('app'),
    catalog_id: '',
    catalog_version: '',
    catalog_update: null,
    slug,
    name,
    type: 'web',
    url: `https://${slug}.${DOMAIN}`,
    domain: DOMAIN,
    owner_id: user.id,
    status: 'new',
    desired_state: 'running',
    port: null,
    health_path: '/',
    dockerfile_path: 'Dockerfile',
    context_dir: '',
    build_mode: 'dockerfile',
    output_dir: '',
    home_page: '',
    volume: false,
    volume_mb: 0,
    volume_used_mb: null,
    files_mb: null,
    backups_mb: 0,
    memory_mb: 256,
    cpu_millis: 250,
    static: false,
    git_source: null,
    protected: false,
    current_deployment: null,
    domains: [],
    started_at: null,
    restart_count: 0,
    live: null,
    services: [service({ name: 'app', public: true, port: null })],
    volumes: [],
    compose_path: '',
    project: { id: proj.id, name: proj.name },
    alias: slug,
    template: null,
    internal_host: '',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...extra,
  }
  apps.push(a)
  env[a.slug] = []
  deployments[a.slug] = []
  return a
}

// ---------------------------------------------------------------- answers that are computed

function metricsOf(app: App, window: string, svc: string): Metrics {
  const s = svc ? app.services.find((x) => x.name === svc) : undefined
  const mem = s?.memory_mb ?? app.memory_mb
  const cpu = s?.cpu_millis ?? app.cpu_millis
  const running = app.status === 'running'
  const stepMin = window === '7d' ? 168 : window === '24h' ? 24 : 1
  const r = seeded(`${app.slug}-${svc}-${window}`)
  const points: MetricPoint[] = []
  if (running) {
    let m = mem * (0.3 + r() * 0.2)
    let c = cpu * 0.04
    for (let i = 59; i >= 0; i--) {
      m = Math.max(mem * 0.1, Math.min(mem * 0.9, m + (r() - 0.48) * mem * 0.04))
      c = Math.max(0.2, Math.min(cpu * 0.09, c + (r() - 0.5) * cpu * 0.02))
      points.push({ ts: ago(i * stepMin), cpu: Math.round(c * 10) / 10, memory_mb: Math.round(m), rx_bytes: Math.round(r() * 40000), tx_bytes: Math.round(r() * 90000) })
    }
  }
  return { app: app.slug, service: svc || undefined, window: window as Metrics['window'], memory_limit_mb: mem, points, latest: points[points.length - 1] ?? null }
}

function trafficOf(app: App, window: string): Traffic {
  const r = seeded(`${app.slug}-traffic-${window}`)
  const hours = window === '30d' ? 30 : window === '7d' ? 7 : 24
  const gran = window === '24h' ? 'hour' : 'day'
  const scale = app.template || app.type === 'worker' ? 0 : 1
  const points = Array.from({ length: hours }, (_, i) => {
    const req = Math.round((40 + r() * 200) * scale * (gran === 'day' ? 20 : 1))
    return { ts: ago((hours - i) * (gran === 'day' ? 1440 : 60)), requests: req, visitors: Math.round(req / 4), errors: Math.round(req * 0.002), client_errors: Math.round(req * 0.02) }
  })
  const sum = (k: 'requests' | 'visitors' | 'errors' | 'client_errors') => points.reduce((n, p) => n + p[k], 0)
  const totals = { requests: sum('requests'), visitors: sum('visitors'), errors: sum('errors'), client_errors: sum('client_errors'), error_rate: 0.2, bots: Math.round(sum('requests') * 0.1) }
  return {
    app: app.slug,
    window: window as Traffic['window'],
    granularity: gran,
    visitors_today: Math.round(180 * scale),
    totals,
    previous: { ...totals, requests: Math.round(totals.requests * 0.9), visitors: Math.round(totals.visitors * 0.85) },
    points: window === 'today' ? [] : points,
    pages: [{ name: '/', count: 1200 * scale }, { name: '/products', count: 640 * scale }, { name: '/cart', count: 210 * scale }],
    countries: [{ code: 'NL', requests: 900 * scale, visitors: 220 * scale }, { code: 'DE', requests: 400 * scale, visitors: 110 * scale }],
    referrers: [{ name: '(direct)', count: 800 * scale }, { name: 'google.com', count: 300 * scale }],
    not_found: [{ name: '/wp-login.php', count: 12 * scale }],
  }
}

function composePreview(text: string): ComposeCheck {
  const lines = text.split('\n')
  const services: ComposeService[] = []
  let inServices = false
  let cur: ComposeService | null = null
  for (const line of lines) {
    if (/^services\s*:/.test(line)) {
      inServices = true
      continue
    }
    if (/^\S/.test(line)) inServices = false
    if (!inServices) continue
    const name = /^ {2}([a-z0-9][a-z0-9_-]*):\s*$/.exec(line)
    if (name) {
      cur = { name: name[1]!, kind: 'build', build: { context: '.', dockerfile: 'Dockerfile' }, public: false, port: null, memory_mb: 256, cpu_millis: 250, depends_on: [], volumes: [] }
      services.push(cur)
      continue
    }
    if (!cur) continue
    const image = /^ {4}image:\s*["']?([^"'\s]+)/.exec(line)
    if (image) {
      cur.kind = 'image'
      cur.image = image[1]
      cur.build = null
    }
    if (/^ {4}ports:/.test(line)) {
      cur.public = true
      const p = /(\d{2,5})/.exec(line)
      cur.port = p ? Number(p[1]) : 3000
    }
    const deps = /^ {4}depends_on:\s*\[([^\]]*)\]/.exec(line)
    if (deps) cur.depends_on = deps[1]!.split(',').map((d) => d.trim().replace(/["']/g, '')).filter(Boolean)
  }
  if (!services.some((s) => s.public) && services[0]) services[0].public = true
  const pub = services.find((s) => s.public)
  if (pub && pub.kind === 'build') pub.memory_mb = 512
  const volumes = services
    .filter((s) => s.image && /postgres|mysql|mariadb/.test(s.image))
    .map((s) => ({ name: `${s.name}-data`, service: s.name, mount_path: '/var/lib/postgresql/data', size_mb: 5120 }))
  for (const v of volumes) services.find((s) => s.name === v.service)?.volumes?.push(v.name)
  const memory = services.reduce((n, s) => n + s.memory_mb, 0)
  const cpu = services.reduce((n, s) => n + s.cpu_millis, 0)
  const vol = volumes.reduce((n, v) => n + v.size_mb, 0)
  const left = 2048 - usedMemory()
  const errors = services.length ? [] : [{ code: 'compose_invalid', message: 'The file has no services.', hint: 'Add a services: section with at least one service.' }]
  return {
    ok: errors.length === 0,
    services,
    volumes,
    memory_mb_total: memory,
    cpu_millis_total: cpu,
    volume_mb_total: vol,
    quota: {
      memory_mb_left: left,
      cpu_millis_left: 2000 - apps.reduce((n, a) => n + (a.static ? 0 : a.cpu_millis), 0),
      storage_mb_left: 20480 - 4200,
      fits: memory <= left,
      message: `This needs ${memory} MB of memory of the ${left} MB you have left.`,
    },
    warnings: services.some((s) => s.kind === 'build') ? [] : [],
    errors,
  }
}

function usedMemory(): number {
  return apps.reduce((n, a) => n + (a.static ? 0 : a.memory_mb), 0)
}

function me(): Me {
  const running = apps.filter((a) => a.status === 'running')
  return {
    user,
    quota: { apps: 12, memory_mb: 4096, cpu_millis: 4000, deploys_per_hour: 30, domains_per_app: 5, volume_mb: 20480 },
    usage: {
      apps: apps.length,
      apps_running: running.length,
      memory_reserved_mb: usedMemory(),
      cpu_reserved_millis: apps.reduce((n, a) => n + (a.static ? 0 : a.cpu_millis), 0),
      memory_used_mb: running.reduce((n, a) => n + (a.live?.memory_mb ?? 0), 0),
      cpu_now_percent: running.reduce((n, a) => n + (a.live?.cpu ?? 0), 0),
      build_minutes_month: 42,
      running_hours_month: 3100,
      deploys_month: 38,
      deploys_week: 9,
      disk_used_mb: 4200,
      volume_reserved_mb: apps.reduce((n, a) => n + a.volumes.reduce((m, v) => m + v.size_mb, 0), 0),
      volume_used_mb: 520,
      files_mb: 2300,
      backups_mb: 140,
    },
    apps_domain: DOMAIN,
    app_url_template: `https://{slug}.${DOMAIN}`,
    app_domains: [{ domain: DOMAIN, default: true, url_template: `https://{slug}.${DOMAIN}` }],
    public_url: 'http://localhost:5174',
    scope: 'session',
  }
}

const github: GitHubOverview = {
  app: { configured: true, name: 'bitploy-ada', slug: 'bitploy-ada' },
  installations: [
    { id: 1, account_login: 'ada', account_type: 'User', repository_selection: 'all', status: 'active', manage_url: 'https://github.com/settings/installations/1', repo_count: 5, limited: false, connected_at: ago(60 * 24 * 40) },
  ],
}
const repos: GitHubRepo[] = ['shop', 'portfolio', 'notes', 'blog-astro', 'api-go'].map((n, i) => ({
  full_name: `ada/${n}`,
  private: i % 2 === 0,
  html_url: `https://github.com/ada/${n}`,
  default_branch: 'main',
  installation_id: 1,
  pushed_at: ago(60 * (i * 9 + 2)),
}))

// ---------------------------------------------------------------- HTTP

class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly hint = '',
  ) {
    super(message)
  }
}

async function readBody(req: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = []
  for await (const c of req) chunks.push(c as Buffer)
  const raw = Buffer.concat(chunks).toString('utf8')
  if (!raw || !(req.headers['content-type'] ?? '').includes('json')) return {}
  try {
    return JSON.parse(raw) as Record<string, unknown>
  } catch {
    return {}
  }
}

function send(res: ServerResponse, status: number, body?: unknown) {
  if (body === undefined) {
    res.statusCode = status === 200 ? 204 : status
    res.end()
    return
  }
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json')
  res.end(JSON.stringify(body))
}

const items = <T>(list: T[]) => ({ items: list })

function needApp(slug: string): App {
  const a = findApp(slug)
  if (!a) throw new HttpError(404, 'app_not_found', `There is no app called ${slug}.`, 'List your apps with list_apps.')
  return a
}

async function handle(req: IncomingMessage, res: ServerResponse, noProjects: boolean) {
  const url = new URL(req.url ?? '/', 'http://localhost')
  const path = url.pathname.replace(/^\/api\/v1/, '').replace(/\/$/, '') || '/'
  const method = req.method ?? 'GET'
  const q = url.searchParams
  const body = method === 'GET' ? {} : await readBody(req)
  const parts = path.split('/').filter(Boolean).map(decodeURIComponent)
  const route = `${method} /${parts.map((p, i) => (i % 2 === 1 && ['apps', 'projects', 'catalog', 'deployments'].includes(parts[i - 1]!) ? ':id' : p)).join('/')}`
  const id = parts[1] ?? ''

  if (noProjects && (parts[0] === 'projects' || parts[0] === 'service-templates')) throw new HttpError(404, 'not_found', 'Not found.')

  switch (route) {
    // ---------------------------------------------------------------- session and account
    case 'GET /session':
      return send(res, 200, { user, csrf_token: 'mock-csrf' } satisfies SessionInfo)
    case 'POST /auth/login':
    case 'POST /auth/logout':
      return send(res, 200, {})
    case 'GET /me':
      return send(res, 200, me())
    case 'GET /events':
      throw new HttpError(404, 'not_found', 'The mock has no live events; the dashboard polls.')
    case 'GET /tokens':
    case 'GET /sessions':
      return send(res, 200, items([]))

    // ---------------------------------------------------------------- GitHub
    case 'GET /github':
      return send(res, 200, github)
    case 'GET /github/repositories': {
      const query = (q.get('query') ?? '').toLowerCase()
      return send(res, 200, items(repos.filter((r) => r.full_name.includes(query))))
    }
    case 'GET /github/branches':
      return send(res, 200, { default_branch: 'main', branches: ['main', 'dev'] })

    // ---------------------------------------------------------------- projects
    case 'GET /projects':
      return send(res, 200, items(projects.map(projectOf)))
    case 'POST /projects': {
      const name = String(body.name ?? '').trim()
      if (!name) throw new HttpError(422, 'name_required', 'Give the project a name.')
      const row = { id: newId('prj'), name, created_at: new Date().toISOString() }
      projects.push(row)
      return send(res, 201, projectOf(row))
    }
    case 'GET /projects/:id':
    case 'PATCH /projects/:id':
    case 'DELETE /projects/:id': {
      const row = projects.find((p) => p.id === id)
      if (!row) throw new HttpError(404, 'project_not_found', `There is no project ${id}.`)
      if (method === 'PATCH') {
        if (typeof body.name === 'string' && body.name.trim()) row.name = body.name.trim()
        for (const a of apps) if (a.project?.id === row.id) a.project = { id: row.id, name: row.name }
      }
      if (method === 'DELETE') {
        if (q.get('confirm') !== row.name) throw new HttpError(422, 'confirm_mismatch', `Type ${row.name} to confirm.`)
        const own = apps.filter((a) => a.project?.id === row.id)
        if (own.length && q.get('with_apps') !== 'true')
          throw new HttpError(409, 'project_not_empty', `${row.name} still has ${own.length} apps.`, 'Move or delete them first, or pass with_apps=true.')
        for (const a of own) apps.splice(apps.indexOf(a), 1)
        projects.splice(projects.indexOf(row), 1)
        return send(res, 204)
      }
      return send(res, 200, projectOf(row))
    }
    case 'GET /service-templates':
      return send(res, 200, items(templates))
    case 'POST /projects/:id/services': {
      const row = projects.find((p) => p.id === id)
      if (!row) throw new HttpError(404, 'project_not_found', `There is no project ${id}.`)
      const tpl = templates.find((t) => t.id === body.template)
      if (!tpl) throw new HttpError(404, 'service_template_unknown', `There is no service template ${String(body.template)}.`, `Use one of ${templates.map((x) => x.id).join(', ')}.`)
      const alias = slugish(String(body.name || tpl.id))
      if (apps.some((a) => a.project?.id === row.id && (a.alias || a.slug) === alias))
        throw new HttpError(409, 'alias_taken', `${row.name} already has an app called ${alias}.`, 'Pick another name, like ' + alias + '-2.')
      const slug = uniqueSlug(`${slugish(row.name)}-${alias}`)
      const a = blankApp(alias, slug, row.id, {
        type: 'worker',
        url: '',
        port: tpl.port,
        template: tpl.id,
        alias,
        internal_host: `${alias}:${tpl.port}`,
        template_version: tpl.version,
        connection_vars: tpl.connection_vars,
        main_var: tpl.main_var,
        memory_mb: tpl.memory_mb,
        cpu_millis: tpl.cpu_millis ?? 250,
        volume_mb: tpl.volume_mb,
        services: [service({ name: 'app', kind: 'image', image: tpl.image, port: tpl.port, health_path: '', memory_mb: tpl.memory_mb, volumes: ['data'] })],
        volumes: [{ name: 'data', service: 'app', mount_path: '/data', size_mb: tpl.volume_mb, used_mb: null }],
      })
      env[a.slug] = tpl.connection_vars.map((name) => ({ name, masked: MASK, service: '', updated_at: new Date().toISOString() }))
      for (const target of (body.connect_to as string[] | undefined) ?? []) {
        const t = findApp(target)
        if (!t) continue
        const list = (env[t.slug] ??= [])
        const ref = `\${{${alias}.${tpl.main_var}}}`
        const existing = list.find((v) => v.name === tpl.main_var && !v.service)
        if (existing) Object.assign(existing, { masked: MASK, references: [ref], updated_at: new Date().toISOString() })
        else list.push({ name: tpl.main_var, masked: MASK, references: [ref], service: '', updated_at: new Date().toISOString() })
        syncRefs(t)
        if (t.status === 'running') startDeploy(t, 'rollback')
      }
      startDeploy(a, 'files')
      return send(res, 202, a)
    }

    // ---------------------------------------------------------------- apps
    case 'GET /apps':
    case 'GET /admin/apps':
      return send(res, 200, items(apps))
    case 'POST /apps': {
      const name = String(body.name ?? '').trim()
      if (!name) throw new HttpError(422, 'name_required', 'Give the app a name.')
      const slug = String(body.slug || slugish(name))
      if (findApp(slug)) throw new HttpError(409, 'slug_taken', `The address ${slug} is taken.`, 'Pick another one.')
      let pid = typeof body.project === 'string' ? body.project : ''
      if (!pid || !projects.some((p) => p.id === pid)) {
        const row = { id: newId('prj'), name, created_at: new Date().toISOString() }
        projects.push(row)
        pid = row.id
      }
      const type = body.type === 'worker' ? 'worker' : 'web'
      const a = blankApp(name, slug, pid, {
        type,
        url: type === 'web' ? `https://${slug}.${DOMAIN}` : '',
        build_mode: (body.build_mode as App['build_mode']) ?? 'dockerfile',
        port: typeof body.port === 'number' ? body.port : null,
        volume: !!body.volume,
        volume_mb: body.volume ? 5120 : 0,
      })
      return send(res, 201, a)
    }
    case 'GET /apps/:id':
      return send(res, 200, needApp(id))
    case 'PATCH /apps/:id': {
      const a = needApp(id)
      if (typeof body.name === 'string') a.name = body.name
      if (typeof body.alias === 'string') {
        const taken = apps.some((x) => x !== a && x.project?.id === a.project?.id && (x.alias || x.slug) === body.alias)
        if (taken) throw new HttpError(409, 'alias_taken', `Another app in ${a.project?.name} is called ${body.alias}.`, 'Pick another short name.')
        a.alias = body.alias
        if (a.port) a.internal_host = `${a.alias}:${a.port}`
      }
      if (typeof body.project === 'string') {
        const row = projects.find((p) => p.id === body.project)
        if (!row) throw new HttpError(404, 'project_not_found', `There is no project ${body.project}.`)
        a.project = { id: row.id, name: row.name }
      }
      for (const k of ['memory_mb', 'cpu_millis', 'port', 'volume_mb'] as const) if (typeof body[k] === 'number') (a[k] as number) = body[k] as number
      return send(res, 200, a)
    }
    case 'DELETE /apps/:id': {
      const a = needApp(id)
      apps.splice(apps.indexOf(a), 1)
      return send(res, 204)
    }
    case 'POST /apps/:id/files':
    case 'POST /apps/:id/upload':
    case 'POST /apps/:id/deployments':
    case 'POST /apps/:id/redeploy': {
      const a = needApp(id)
      return send(res, 202, startDeploy(a, route.endsWith('upload') ? 'upload' : route.endsWith('files') ? 'files' : 'git'))
    }
    case 'PUT /apps/:id/git-source': {
      const a = needApp(id)
      const repo = String(body.repo_url ?? '').replace(/^https?:\/\/github\.com\//, '')
      a.git_source = {
        repo_url: `https://github.com/${repo}`,
        branch: String(body.branch || 'main'),
        repo,
        repo_html_url: `https://github.com/${repo}`,
        auto_deploy: true,
        push_ready: true,
        push_status: 'ready',
        push_message: 'Every push deploys.',
        last_push_at: null,
      }
      return send(res, 200, a)
    }
    case 'POST /apps/:id/restart':
    case 'POST /apps/:id/start':
    case 'POST /apps/:id/stop': {
      const a = needApp(id)
      a.status = route.endsWith('stop') ? 'stopped' : 'running'
      a.live = a.status === 'running' ? liveOf(a.slug, a.memory_mb, a.cpu_millis) : null
      return send(res, 200, a)
    }
    case 'GET /apps/:id/env':
      needApp(id)
      return send(res, 200, items(env[id] ?? []))
    case 'PUT /apps/:id/env': {
      needApp(id)
      const list = (env[id] ??= [])
      const svc = typeof body.service === 'string' ? body.service : ''
      for (const [name, value] of Object.entries((body.set as Record<string, string>) ?? {})) {
        const isRef = /\$\{\{/.test(value)
        const v = list.find((x) => x.name === name && x.service === svc)
        const next = { masked: isRef ? MASK : value.length > 4 ? `${value.slice(0, 2)}••••` : MASK, references: isRef ? refsOf(value) : undefined, updated_at: new Date().toISOString() }
        if (v) Object.assign(v, next)
        else list.push({ name, service: svc, ...next })
      }
      for (const name of (body.unset as string[]) ?? []) {
        const i = list.findIndex((x) => x.name === name && x.service === svc)
        if (i >= 0) list.splice(i, 1)
      }
      syncRefs(needApp(id))
      return send(res, 200, items(list))
    }
    case 'GET /apps/:id/metrics':
      return send(res, 200, metricsOf(needApp(id), q.get('window') ?? '1h', q.get('service') ?? ''))
    case 'GET /apps/:id/traffic':
      return send(res, 200, trafficOf(needApp(id), q.get('window') ?? '24h'))
    case 'GET /apps/:id/deployments':
      needApp(id)
      return send(res, 200, items(deployments[id] ?? []))
    case 'GET /apps/:id/domains':
      needApp(id)
      return send(res, 200, items([]))
    case 'GET /apps/:id/backups': {
      const a = needApp(id)
      return send(res, 200, {
        items: [],
        settings: { schedule: 'daily', keep_mode: 'last', keep: 7, next_at: ago(-300) },
        restore: null,
        busy: false,
        total_bytes: 0,
        upload_max_bytes: 512 * 1024 * 1024,
        volumes: a.volumes.map((v) => ({ ...v, upload_max_bytes: 512 * 1024 * 1024 })),
      } satisfies BackupList & { volumes: unknown[] })
    }
    case 'GET /apps/:id/files':
      needApp(id)
      return send(res, 200, { path: q.get('path') ?? '', items: [], truncated: false, upload_max_bytes: 100 * 1024 * 1024 } satisfies DataFileList)
    case 'GET /apps/:id/logs': {
      const a = needApp(id)
      if (q.get('follow')) throw new HttpError(404, 'not_found', 'The mock has no live log stream.')
      const lines =
        q.get('kind') === 'build'
          ? ['#1 [internal] load build definition from Dockerfile', '#2 [1/4] FROM node:22-alpine', '#3 [2/4] RUN npm ci', '#4 [3/4] RUN npm run build', '#5 exporting to image']
          : a.template
            ? ['database system is ready to accept connections', 'checkpoint starting: time', 'checkpoint complete: wrote 12 buffers']
            : ['Listening on :' + (a.port ?? 3000), 'GET / 200 4ms', 'GET /products 200 11ms', 'POST /cart 201 23ms', 'GET /healthz 200 1ms']
      return send(res, 200, { lines: lines.map((text, i) => ({ text, time: ago(lines.length - i) })) })
    }
    case 'GET /apps/:id/dockerfile': {
      const a = needApp(id)
      const c = catalog.find((x) => x.id === a.catalog_id)
      return send(res, 200, { path: 'Dockerfile', content: c?.dockerfile ?? 'FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nRUN npm ci && npm run build\nCMD ["npm", "start"]\n', editable: !a.git_source, reason: a.git_source ? 'The Dockerfile comes from the linked repository.' : '' })
    }
    case 'GET /deployments/:id/log': {
      return send(res, 200, { lines: ['[1/1] Fetching the source', 'Building the image', '#3 [2/4] RUN npm ci', 'added 412 packages in 9s'].map((text) => ({ text })) })
    }

    // ---------------------------------------------------------------- catalog and checks
    case 'GET /catalog':
      return send(res, 200, items(catalog))
    case 'GET /catalog/:id': {
      const c = catalog.find((x) => x.id === id)
      if (!c) throw new HttpError(404, 'catalog_not_found', `There is no ready-made app ${id}.`)
      return send(res, 200, c)
    }
    case 'POST /catalog/:id/deploy': {
      const c = catalog.find((x) => x.id === id)
      if (!c) throw new HttpError(404, 'catalog_not_found', `There is no ready-made app ${id}.`)
      const name = String(body.name || c.name)
      const slug = uniqueSlug(String(body.slug || slugish(name)))
      let pid = typeof body.project === 'string' ? body.project : ''
      if (!pid || !projects.some((p) => p.id === pid)) {
        const row = { id: newId('prj'), name, created_at: new Date().toISOString() }
        projects.push(row)
        pid = row.id
      }
      const a = blankApp(name, slug, pid, { catalog_id: c.id, catalog_version: c.version, port: c.port, memory_mb: c.memory_mb, volume: c.volume, volume_mb: c.volume_mb })
      const d = startDeploy(a, 'files')
      return send(res, 201, { app: a, deployment: d, first_steps: c.first_steps })
    }
    case 'POST /compose/validate':
      return send(res, 200, composePreview(String(body.compose ?? '')))
    case 'POST /dockerfile/validate':
      return send(res, 200, { ok: true, findings: [] })
  }
  throw new HttpError(404, 'not_found', `The mock does not know ${method} ${path}.`)
}

export function mockApi(): Plugin {
  const noProjectsEnv = process.env.MOCK_NO_PROJECTS === '1'
  return {
    name: 'bitploy-mock-api',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (!req.url?.startsWith('/api/v1')) return next()
        const noProjects = noProjectsEnv || req.headers['x-mock-no-projects'] === '1'
        handle(req, res, noProjects).catch((e: unknown) => {
          if (e instanceof HttpError) send(res, e.status, { error: { code: e.code, message: e.message, hint: e.hint || undefined } })
          else {
            console.error(e)
            send(res, 500, { error: { code: 'mock_failed', message: String(e) } })
          }
        })
      })
    },
  }
}
