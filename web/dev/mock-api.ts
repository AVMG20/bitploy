// Dev only: never imported by the build.
//
// An in-memory stand-in for bitploy's /api/v1, so the dashboard can be looked
// at without the Go server (and before the projects API exists). Start it with
//   npx vite --config vite.mock.config.ts --port 5174
// MOCK_NO_PROJECTS=1 (or the request header x-mock-no-projects: 1) makes
// /projects and /service-templates answer 404, to try the one-project-per-app
// fallback. MOCK_OVERALLOCATE=1 lets the user allocate memory and CPU without a
// limit (the admin switch), to see the Projects usage cards for such a user.

import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Plugin } from 'vite'
import type {
  App,
  AppService,
  AdminUser,
  AppDomain,
  AppVolume,
  AuditEntry,
  Backup,
  BackupList,
  CatalogApp,
  ComposeCheck,
  ComposeService,
  DataFileList,
  DbSchema,
  DbTable,
  DbValue,
  Deployment,
  DeploymentState,
  EnvVar,
  GitHubOverview,
  GitHubRepo,
  Invite,
  Me,
  MetricPoint,
  Metrics,
  Project,
  ProjectInvite,
  ProjectInviteInfo,
  ProjectMember,
  ProjectSignup,
  PublicPort,
  PublicPortsOverview,
  ServerMetricPoint,
  ServerMetrics,
  ServerStats,
  ServiceTemplate,
  Session,
  SessionInfo,
  Token,
  Traffic,
  TrafficPoint,
  User,
  UserBrief,
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

// A cache or search index has no backups (servicetpl.Template.Backups).
const noBackups = (template?: string | null) => template === 'valkey' || template === 'redis' || template === 'meilisearch'

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

// ---------------------------------------------------------------- sample logs and files

const SAMPLE_DOCKERFILE = `# syntax=docker/dockerfile:1
ARG NODE_VERSION=22

FROM node:\${NODE_VERSION}-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \\
    npm ci --no-audit && \\
    npm cache verify
COPY . .
RUN npm run build

FROM node:\${NODE_VERSION}-alpine
ENV NODE_ENV=production PORT=3000
WORKDIR /app
COPY --from=build --chown=node:node /app/dist ./dist
COPY --from=build /app/node_modules ./node_modules
USER node
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- "http://localhost:$PORT/healthz" || exit 1
CMD ["node", "dist/server.js"]
`

const BUILD_LOG = [
  '[1/3] Fetching the source',
  '[2/3] Building the image',
  '#0 building with "default" instance using docker driver',
  '#1 [internal] load build definition from Dockerfile',
  '#1 transferring dockerfile: 712B done',
  '#1 DONE 0.0s',
  '#2 [internal] load metadata for docker.io/library/node:22-alpine',
  '#2 DONE 1.2s',
  '#3 [internal] load .dockerignore',
  '#3 DONE 0.0s',
  '#4 [build 1/6] FROM docker.io/library/node:22-alpine@sha256:9fcc1a6da2b9eee38638df75c5f826e06e9c79f6a0f97f16ed98fe0ebb0725a3',
  '#4 resolve docker.io/library/node:22-alpine@sha256:9fcc1a6da2b9 0.0s done',
  '#4 CACHED',
  '#5 [build 2/6] WORKDIR /app',
  '#5 DONE 0.1s',
  '#6 [build 3/6] COPY package.json package-lock.json ./',
  '#6 DONE 0.0s',
  '#7 [build 4/6] RUN --mount=type=cache,target=/root/.npm npm ci --no-audit',
  '#7 3.912 npm warn deprecated inflight@1.0.6: This module is not supported, and leaks memory.',
  '#7 4.205 npm warn deprecated glob@7.2.3: Glob versions prior to v9 are no longer supported',
  '#7 9.871 added 412 packages in 9s',
  '#7 DONE 10.3s',
  '#8 [build 5/6] COPY . .',
  '#8 DONE 0.2s',
  '#9 [build 6/6] RUN npm run build',
  '#9 0.512 > shop@1.4.0 build',
  '#9 0.513 > vite build',
  '#9 1.334 vite v6.0.3 building for production...',
  '#9 4.981 ✓ 1243 modules transformed.',
  '#9 5.402 (!) Some chunks are larger than 500 kB after minification.',
  '#9 5.610 ✓ built in 4.28s',
  '#9 DONE 5.9s',
  '#10 [stage-1 3/4] COPY --from=build --chown=node:node /app/dist ./dist',
  '#10 DONE 0.1s',
  '#11 exporting to image',
  '#11 exporting layers 0.4s done',
  '#11 writing image sha256:4f1d0c2be3a5c1e9b8a7d6e5f4c3b2a1 done',
  '#11 naming to docker.io/bitploy/shop-web:4 done',
  '#11 DONE 0.5s',
  '[3/3] Starting the new version',
  'Health check passed: GET /healthz 200 in 38ms',
  'Live at https://shop-web.bitploy.nl',
]

const FAILED_BUILD_LOG = [
  ...BUILD_LOG.slice(0, 26),
  '#9 [build 6/6] RUN npm run build',
  '#9 0.498 > shop@1.4.0 build',
  '#9 0.499 > vite build',
  '#9 1.287 vite v6.0.3 building for production...',
  '#9 3.950 error during build:',
  '#9 3.950 src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".',
  '#9 3.951     at getRollupError (file:///app/node_modules/rollup/dist/es/shared/parseAst.js:395:41)',
  '#9 3.951     at error (file:///app/node_modules/rollup/dist/es/shared/parseAst.js:391:42)',
  '#9 ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
  '------',
  ' > [build 6/6] RUN npm run build:',
  '------',
  'Dockerfile:11',
  'ERROR: failed to solve: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
]

function runtimeLog(port: number): string[] {
  return [
    '> shop@1.4.0 start',
    '> node dist/server.js',
    `2026-11-06T09:59:58.114Z INFO  Server listening on http://0.0.0.0:${port}`,
    '2026-11-06T09:59:58.120Z DEBUG pool created size=10 idle_timeout=30s',
    'time=2026-11-06T10:00:01Z level=info msg="connected to database" host=postgres:5432 duration=12ms',
    '10.0.1.7 - - [06/Nov/2026:10:00:02 +0000] "GET / HTTP/1.1" 200 5123 "-" "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)"',
    '10.0.1.7 - - [06/Nov/2026:10:00:02 +0000] "GET /assets/index-B2x9.js HTTP/1.1" 304 0 "https://shop-web.bitploy.nl/" "Mozilla/5.0"',
    'GET /products 200 11ms',
    'POST /cart 201 23ms',
    'GET /products/does-not-exist 404 3ms',
    'GET /healthz 200 1ms',
    '{"level":"warn","time":"2026-11-06T10:00:04Z","msg":"slow query","query":"SELECT * FROM orders","duration_ms":812}',
    '2026-11-06T10:00:05.201Z WARN  Deprecated option `legacyCookies` is set; it goes away in 2.0',
    'POST /checkout 500 87ms',
    "2026-11-06T10:00:05.288Z ERROR Unhandled error in POST /checkout",
    "TypeError: Cannot read properties of undefined (reading 'total')",
    '    at checkout (/app/dist/routes/checkout.js:42:31)',
    '    at Layer.handle [as handle_request] (/app/node_modules/express/lib/router/layer.js:95:5)',
    '    at next (/app/node_modules/express/lib/router/route.js:149:13)',
    '    at process.processTicksAndRejections (node:internal/process/task_queues:105:5)',
    '[notice] cache warmed: 128 products',
    'GET /products?page=2 200 9ms',
  ]
}

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
    dockerfile: `FROM alpine:3.20
ARG PB_VERSION=0.22.21
# Download the release for this version and unpack it in /pb.
RUN apk add --no-cache unzip ca-certificates && \\
    wget -q "https://github.com/pocketbase/pocketbase/releases/download/v\${PB_VERSION}/pocketbase_\${PB_VERSION}_linux_amd64.zip" -O /tmp/pb.zip && \\
    unzip /tmp/pb.zip -d /pb && rm /tmp/pb.zip
USER 1000
EXPOSE 8090
CMD ["/pb/pocketbase", "serve", "--http=0.0.0.0:8090", "--dir=/data/pb_data"]
`,
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
  /** Someone else's project; left out: Ada's own. */
  owner?: UserBrief
}

// The other people of the demo (see adminUsers).
const people = {
  linus: { id: 'usr_linus', name: 'Linus', email: 'linus@example.com' },
  grace: { id: 'usr_grace', name: 'Grace', email: 'grace@example.com' },
  ken: { id: 'usr_ken', name: 'Ken', email: 'ken@example.com' },
} satisfies Record<string, UserBrief>

const projects: ProjectRow[] = [
  { id: 'prj_portfolio', name: 'Portfolio', created_at: ago(60 * 24 * 60) },
  { id: 'prj_shop', name: 'Shop', created_at: ago(60 * 24 * 30) },
  { id: 'prj_notes', name: 'Notes stack', created_at: ago(60 * 24 * 12) },
  { id: 'prj_status', name: 'Status page', created_at: ago(60 * 24 * 2) },
  { id: 'prj_market', name: 'Marketplace', created_at: ago(60 * 24 * 20) },
  // Grace shared her wiki with Ada.
  { id: 'prj_wiki', name: 'Team wiki', created_at: ago(60 * 24 * 8), owner: people.grace },
  // Linus invites Ada to this one with the link /join/mock-join; until then Ada does not see it.
  { id: 'prj_design', name: 'Design system', created_at: ago(60 * 24 * 5), owner: people.linus },
]

// ---------------------------------------------------------------- project members

interface MemberRow {
  user: UserBrief
  created_at: string
  invited_by: string | null
}

/** The members of each project besides its owner. */
const members: Record<string, MemberRow[]> = {
  prj_shop: [{ user: people.linus, created_at: ago(60 * 24 * 10), invited_by: 'usr_ada' }],
  prj_wiki: [
    { user: { id: 'usr_ada', name: 'Ada', email: 'ada@example.com' }, created_at: ago(60 * 24 * 6), invited_by: 'usr_grace' },
    { user: people.ken, created_at: ago(60 * 24 * 4), invited_by: 'usr_grace' },
  ],
}

/** Open invites of Ada's projects. */
const projectInvites: Record<string, ProjectInvite[]> = {
  prj_shop: [{ id: 'pin_ken', email: 'ken@example.com', role: 'member', created_at: ago(60 * 5), expires_at: ago(-60 * 24 * 7 + 60 * 5) }],
}

/** The invite links the demo knows: /join/mock-join for anyone, /join/mock-join-other for another address. */
const joinCodes: Record<string, { project: string; email: string | null; used?: boolean }> = {
  'mock-join': { project: 'prj_design', email: null },
  'mock-join-other': { project: 'prj_design', email: 'someone@example.com' },
  'mock-join-used': { project: 'prj_design', email: null, used: true },
}

function visible(row: ProjectRow): boolean {
  return !row.owner || (members[row.id] ?? []).some((m) => m.user.id === 'usr_ada')
}

function needProject(id: string, owner = false): ProjectRow {
  const row = projects.find((p) => p.id === id || p.name.toLowerCase() === id.toLowerCase())
  if (!row || !visible(row)) throw new HttpError(404, 'project_not_found', `There is no project called "${id}" in your account.`, 'Call list_projects to see your projects.')
  if (owner && row.owner)
    throw new HttpError(403, 'project_owner_only', 'Only the owner of this project can do this.', 'The project was shared with you: you can deploy and change its apps, but renaming or deleting the project and managing its members is up to its owner.')
  return row
}

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

const AFTER_DEPLOY_WARNING = {
  code: 'after_deploy_failed',
  severity: 'warning' as const,
  message: 'The after-deploy command failed. It stopped with exit code 1. The new version stays live.',
  hint: 'Read its output in the deploy log (get_logs kind=build, the lines marked [after-deploy]). Fix the command (update_app after_deploy_command=...) or the code; it runs again on the next deploy.',
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
  /** A template app that runs an older version than the template's. */
  templateVersion?: string
  deploys?: number
}

// ---------------------------------------------------------------- public ports

const PUBLIC_PORTS = { from: 30000, to: 30099, host: DOMAIN, per_user: 3 }

/** App.public_port of an app without an open port. */
function closedPort(a: Pick<App, 'type' | 'services'>): PublicPort {
  const web = a.type === 'web' && a.services.length <= 1
  return { available: !web, ...(web ? { reason: 'web_app' as const } : {}), enabled: false, running: false, allow_from: [], expires_at: null }
}

/** The generated passwords of a template app, as the reveal endpoint gives them. */
function mockSecrets(a: App): Record<string, string> {
  switch (a.template) {
    case 'postgres':
    case 'pgvector':
      return { POSTGRES_PASSWORD: 'f3k9Qm2xLr7Vt1Zp' }
    case 'mariadb':
    case 'mysql':
      return { MYSQL_PASSWORD: 'h8Wq2Nc5Jv0Kd4Rs', MYSQL_ROOT_PASSWORD: 'p1Ls6Ty9Bx3Mg7Fe' }
    case 'valkey':
      return { REDIS_PASSWORD: 'c4Hn8Dz1Qw6Ys2Ua' }
    case 'meilisearch':
      return { MEILI_MASTER_KEY: 'm5Rt0Gk3Vb8Xe2Nj9Pq4' }
    default:
      return {}
  }
}

/** The connection URL of a template app at a public address; masked puts ******** for the password. */
function mockConnectionURL(a: App, address: string, masked: boolean): string {
  const pw = (name: string) => (masked ? '********' : (mockSecrets(a)[name] ?? ''))
  switch (a.template) {
    case 'postgres':
    case 'pgvector':
      return `postgresql://postgres:${pw('POSTGRES_PASSWORD')}@${address}/postgres`
    case 'mariadb':
    case 'mysql':
      return `mysql://app:${pw('MYSQL_PASSWORD')}@${address}/app`
    case 'valkey':
      return `redis://default:${pw('REDIS_PASSWORD')}@${address}`
    case 'meilisearch':
      return `http://${address}`
    default:
      return ''
  }
}

function openPort(a: App, port: number, opts: { service?: string; minutes: number | null; allow: string[] }): PublicPort {
  const address = `${PUBLIC_PORTS.host}:${port}`
  const svc = opts.service ? a.services.find((s) => s.name === opts.service) : a.services[0]
  return {
    available: true,
    enabled: true,
    running: true,
    ...(opts.service ? { service: opts.service } : {}),
    host: PUBLIC_PORTS.host,
    port,
    address,
    target_port: svc?.port ?? a.port ?? undefined,
    connection_url: mockConnectionURL(a, address, true) || undefined,
    allow_from: opts.allow,
    expires_at: opts.minutes === null ? null : new Date(Date.now() + opts.minutes * 60_000).toISOString(),
    opened_at: new Date().toISOString(),
  }
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
    backups_mb: volumes.length && !noBackups(s.template) ? 24 : 0,
    backups_supported: !noBackups(s.template),
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
    ...(s.template ? { template_version: s.templateVersion ?? templates.find((t) => t.id === s.template)?.version, template_latest: templates.find((t) => t.id === s.template)?.version, template_update_available: !!s.templateVersion && s.templateVersion !== templates.find((t) => t.id === s.template)?.version, connection_vars: templates.find((t) => t.id === s.template)?.connection_vars, main_var: templates.find((t) => t.id === s.template)?.main_var } : {}),
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
    // shop-web has a failed build before its current version, to show an error with its log tail.
    if (a.slug === 'shop-web' && i === n - 1) {
      list.unshift(
        deployment(a, i, 'failed', 60 * 24 * (n - i) + 90, {
          // As the server words it: BuildFailureSummary puts the cause after "The build failed: ".
          error: {
            code: 'build_failed',
            message: 'The build failed: src/pages/Checkout.tsx (14:9): "formatPrice" is not exported by "src/lib/money.ts", imported by "src/pages/Checkout.tsx".',
            hint: 'Read log_tail for the failing step, fix the Dockerfile or the code, then deploy again.',
          },
          log_tail: FAILED_BUILD_LOG.slice(-14),
        }),
      )
      continue
    }
    list.unshift(deployment(a, i, state, 60 * 24 * (n - i) + 90))
  }
  deployments[a.slug] = list
  a.current_deployment = list.find((d) => d.state === 'running') ?? null
  a.public_port = closedPort(a)
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

/** EnvVar.masked as the server words it: all dots, or the references with dots for the text around them. */
function maskOf(value: string | undefined): string {
  if (!value || !refsOf(value)) return MASK
  return value.split(/(\$\{\{[^}]+\}\})/).map((p) => (p.startsWith('${{') ? p : p ? '••••' : '')).join('')
}

function seedEnv(slug: string, vars: { name: string; value?: string; service?: string; hidden?: boolean }[]) {
  env[slug] = vars.map((v) => ({ name: v.name, masked: maskOf(v.value), hidden: v.hidden ?? false, ...(v.hidden ? {} : { value: v.value ?? '' }), references: refsOf(v.value), service: v.service ?? '', updated_at: ago(60 * 24 * 3) }))
}

apps.push(
  makeApp({ slug: 'portfolio', name: 'Portfolio', project: 'prj_portfolio', alias: 'portfolio', port: 80, repo: 'ada/portfolio', static: true, memory: 32, cpu: 100, createdDaysAgo: 60, deploys: 5 }),
  makeApp({ slug: 'shop-web', name: 'web', project: 'prj_shop', alias: 'web', port: 3000, repo: 'ada/shop', memory: 512, cpu: 500, createdDaysAgo: 30, deploys: 4,
    volumes: [{ name: 'data', service: 'app', mount_path: '/data', size_mb: 1024, used_mb: 24 }] }),
  makeApp({ slug: 'shop-api', name: 'api', project: 'prj_shop', alias: 'api', type: 'worker', port: 8080, repo: 'ada/api-go', memory: 256, cpu: 250, createdDaysAgo: 29, deploys: 3 }),
  makeApp({ slug: 'shop-postgres', name: 'postgres', project: 'prj_shop', alias: 'postgres', template: 'postgres', templateVersion: '18.4', type: 'worker', port: 5432, memory: 256, cpu: 250, createdDaysAgo: 28, deploys: 1,
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
// Grace's wiki, shared with Ada; Linus's design system, not yet.
apps.push(
  makeApp({ slug: 'wiki', name: 'Wiki', project: 'prj_wiki', alias: 'wiki', port: 3000, repo: 'grace/wiki', memory: 512, cpu: 500, createdDaysAgo: 8, deploys: 2 }),
  makeApp({ slug: 'wiki-postgres', name: 'postgres', project: 'prj_wiki', alias: 'postgres', template: 'postgres', type: 'worker', port: 5432, memory: 256, createdDaysAgo: 8, deploys: 1 }),
  makeApp({ slug: 'design-docs', name: 'Design docs', project: 'prj_design', alias: 'docs', port: 80, static: true, memory: 32, cpu: 100, createdDaysAgo: 5, deploys: 1 }),
)
for (const a of apps) {
  const owner = projects.find((p) => p.id === a.project?.id)?.owner
  if (owner) Object.assign(a, { owner_id: owner.id, shared: true })
}
// A bigger project for the canvas: ten apps in three lanes, many lines.
apps.push(
  makeApp({ slug: 'mk-store', name: 'storefront', project: 'prj_market', alias: 'store', port: 3000, repo: 'ada/shop', memory: 512, cpu: 500, createdDaysAgo: 20 }),
  makeApp({ slug: 'mk-admin', name: 'admin', project: 'prj_market', alias: 'admin', port: 3000, repo: 'ada/shop', memory: 256, cpu: 250, createdDaysAgo: 19 }),
  makeApp({ slug: 'mk-docs', name: 'docs', project: 'prj_market', alias: 'docs', port: 80, repo: 'ada/portfolio', static: true, memory: 32, cpu: 100, createdDaysAgo: 18 }),
  makeApp({ slug: 'mk-api', name: 'api', project: 'prj_market', alias: 'api', type: 'worker', port: 8080, repo: 'ada/api-go', createdDaysAgo: 20 }),
  makeApp({ slug: 'mk-worker', name: 'worker', project: 'prj_market', alias: 'worker', type: 'worker', port: null, repo: 'ada/api-go', createdDaysAgo: 19 }),
  makeApp({ slug: 'mk-mailer', name: 'mailer', project: 'prj_market', alias: 'mailer', type: 'worker', port: 8025, repo: 'ada/api-go', memory: 128, createdDaysAgo: 18 }),
  makeApp({ slug: 'mk-indexer', name: 'indexer', project: 'prj_market', alias: 'indexer', type: 'worker', port: null, repo: 'ada/api-go', memory: 128, createdDaysAgo: 17 }),
  makeApp({ slug: 'mk-postgres', name: 'postgres', project: 'prj_market', alias: 'postgres', template: 'postgres', type: 'worker', port: 5432, memory: 512, createdDaysAgo: 20, deploys: 1 }),
  makeApp({ slug: 'mk-redis', name: 'redis', project: 'prj_market', alias: 'redis', template: 'valkey', type: 'worker', port: 6379, memory: 128, createdDaysAgo: 20, deploys: 1 }),
  makeApp({ slug: 'mk-search', name: 'search', project: 'prj_market', alias: 'search', template: 'meilisearch', type: 'worker', port: 7700, memory: 256, createdDaysAgo: 17, deploys: 1 }),
)
// One database with its public port open, for the badge on the canvas and the admin list.
{
  const pg = findApp('mk-postgres')!
  pg.public_port = openPort(pg, 30000, { minutes: 42, allow: [] })
}
seedEnv('mk-store', [
  { name: 'API_URL', value: 'http://${{api.HOST}}:8080' },
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'MEILI_URL', value: '${{search.MEILI_URL}}' },
  { name: 'ADMIN_URL', value: 'http://${{admin.HOST}}:3000' },
])
seedEnv('mk-admin', [
  { name: 'API_URL', value: 'http://${{api.HOST}}:8080' },
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' },
])
seedEnv('mk-docs', [{ name: 'MEILI_URL', value: '${{search.MEILI_URL}}' }])
seedEnv('mk-api', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' },
  { name: 'MEILI_URL', value: '${{search.MEILI_URL}}' },
  { name: 'MAILER_URL', value: 'http://${{mailer.HOST}}:8025' },
])
seedEnv('mk-worker', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' },
  { name: 'API_URL', value: 'http://${{api.HOST}}:8080' },
])
seedEnv('mk-mailer', [{ name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' }, { name: 'SMTP_URL' }])
seedEnv('mk-indexer', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'MEILI_URL', value: '${{search.MEILI_URL}}' },
  { name: 'MEILI_MASTER_KEY', value: '${{search.MEILI_MASTER_KEY}}' },
])
seedEnv('mk-postgres', ['DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'].map((name) => ({ name })))
seedEnv('mk-redis', ['REDIS_URL', 'REDIS_HOST', 'REDIS_PORT', 'REDIS_PASSWORD', 'VALKEY_URL'].map((name) => ({ name })))
seedEnv('mk-search', ['MEILI_URL', 'MEILI_HOST', 'MEILI_MASTER_KEY'].map((name) => ({ name })))

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
  { name: 'NEXT_PUBLIC_SHOP_NAME', value: 'Ada Shop' },
  { name: 'SENTRY_AUTH_TOKEN', hidden: true },
])
// shop-web migrates its database before each deploy and warms a cache after it; its current version's cache step failed.
{
  const web = findApp('shop-web')!
  web.before_deploy_command = 'npx prisma migrate deploy'
  web.after_deploy_command = 'node scripts/warm-cache.js'
  const cur = web.current_deployment!
  cur.after_deploy = 'failed'
  cur.warnings = [AFTER_DEPLOY_WARNING]
  // shop-api's last deploy stopped at its migration.
  const api = findApp('shop-api')!
  api.before_deploy_command = 'go run ./cmd/migrate'
  const list = deployments['shop-api']!
  list.unshift(
    deployment(api, list[0]!.seq + 1, 'failed', 30, {
      error: {
        code: 'before_deploy_failed',
        message: 'The before-deploy command failed with exit code 1. The new version was not started; the version that was live keeps running.',
        hint: 'Read the command\'s output in the deploy log (get_logs kind=build, the lines marked [before-deploy]). Fix the code or the command (update_app before_deploy_command=...), then deploy again.',
      },
      log_tail: ['#14 DONE 2.1s', 'Running before-deploy command: go run ./cmd/migrate', 'It runs in a one-off container of the new version (up to 10 minutes). The live version keeps serving meanwhile.', '[before-deploy] migrate: connecting to postgres:5432', '[before-deploy] migrate: 0007_orders.sql: ERROR: column "total" of relation "orders" already exists', '[before-deploy] exit status 1'],
    }),
  )
}
seedEnv('shop-api', [
  { name: 'DATABASE_URL', value: '${{postgres.DATABASE_URL}}' },
  { name: 'REDIS_URL', value: '${{redis.REDIS_URL}}' },
  { name: 'STRIPE_KEY' },
])
seedEnv('shop-postgres', ['DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'].map((name) => ({ name })))
seedEnv('shop-redis', ['REDIS_URL', 'REDIS_HOST', 'REDIS_PORT', 'REDIS_PASSWORD', 'VALKEY_URL'].map((name) => ({ name })))
seedEnv('notes', [{ name: 'POSTGRES_PASSWORD' }, { name: 'SECRET_KEY' }, { name: 'SMTP_URL', service: 'worker' }])
seedEnv('uptime', [])
for (const a of apps) syncRefs(a)

function projectOf(row: ProjectRow): Project {
  const own = apps.filter((a) => a.project?.id === row.id)
  for (const a of own) syncRefs(a)
  const { owner, ...rest } = row
  return {
    ...rest,
    updated_at: row.updated_at ?? row.created_at,
    network: `bp-p-${row.id.replace(/^prj_/, '')}`,
    role: owner ? 'member' : 'owner',
    shared: !!owner,
    owner: owner ?? { id: user.id, name: user.name, email: user.email },
    member_count: (members[row.id] ?? []).length,
    apps: own,
  }
}

function membersOf(row: ProjectRow): ProjectMember[] {
  const owner = row.owner ?? { id: user.id, name: user.name, email: user.email }
  return [
    { user_id: owner.id, name: owner.name, email: owner.email, role: 'owner', invited_by: null, created_at: row.created_at, you: owner.id === user.id },
    ...(members[row.id] ?? []).map((m): ProjectMember => ({ user_id: m.user.id, name: m.user.name, email: m.user.email, role: 'member', invited_by: m.invited_by, created_at: m.created_at, you: m.user.id === user.id })),
  ]
}

function joinCode(code: string) {
  const j = joinCodes[code]
  if (!j) throw new HttpError(404, 'invite_invalid', 'This invite link is not valid.', 'Ask the person who invited you for a new invite link.')
  if (j.used) throw new HttpError(410, 'invite_invalid', 'This invite link has already been used.', 'Ask the person who invited you for a new invite link.')
  return { ...j, row: projects.find((p) => p.id === j.project)! }
}

/** /projects/{id}/members[/{user}], /projects/{id}/invites[/{id}] and /project-invites/{code}[/accept|/signup]. */
function memberRoutes(method: string, parts: string[], body: Record<string, unknown>, res: ServerResponse): boolean {
  if (parts[0] === 'project-invites' && parts[1]) {
    const j = joinCode(parts[1])
    if (method === 'GET' && !parts[2]) {
      const owner = j.row.owner ?? user
      send(res, 200, { project_name: j.row.name, invited_by: owner.name, email: j.email, role: 'member', expires_at: ago(-60 * 24 * 6) } satisfies ProjectInviteInfo)
      return true
    }
    if (method === 'POST' && (parts[2] === 'accept' || parts[2] === 'signup')) {
      if (j.email && parts[2] === 'accept' && j.email !== user.email)
        throw new HttpError(403, 'invite_email_mismatch', 'This invite is for a different email address.', `Log in with the account of ${j.email}, or ask for an invite for ${user.email}.`)
      const list = (members[j.row.id] ??= [])
      if (!list.some((m) => m.user.id === user.id)) list.push({ user: { id: user.id, name: user.name, email: user.email }, created_at: new Date().toISOString(), invited_by: j.row.owner?.id ?? null })
      j.used = true
      joinCodes[parts[1]] = { project: j.project, email: j.email, used: true }
      if (parts[2] === 'accept') send(res, 200, projectOf(j.row))
      else send(res, 201, { user, csrf_token: 'mock-csrf', project: { id: j.row.id, name: j.row.name } } satisfies ProjectSignup)
      return true
    }
    return false
  }
  if (parts[0] !== 'projects' || !parts[1] || (parts[2] !== 'members' && parts[2] !== 'invites')) return false
  if (parts[2] === 'members') {
    if (method === 'GET' && !parts[3]) {
      send(res, 200, items(membersOf(needProject(parts[1]))))
      return true
    }
    if (method === 'DELETE' && parts[3]) {
      const leaving = parts[3] === 'me' || parts[3] === user.id
      const row = needProject(parts[1], !leaving)
      if (leaving && !row.owner) throw new HttpError(409, 'owner_cannot_leave', 'The owner cannot leave their own project.', 'Delete the project instead, or keep it.')
      const list = members[row.id] ?? []
      const i = list.findIndex((m) => m.user.id === (leaving ? user.id : parts[3]))
      if (i < 0) throw new HttpError(404, 'member_not_found', 'That user is not a member of this project.')
      list.splice(i, 1)
      send(res, 204)
      return true
    }
    return false
  }
  const row = needProject(parts[1], true)
  const list = (projectInvites[row.id] ??= [])
  if (method === 'GET' && !parts[3]) {
    send(res, 200, items(list))
    return true
  }
  if (method === 'POST' && !parts[3]) {
    const email = typeof body.email === 'string' && body.email.trim() ? body.email.trim() : null
    if (email && membersOf(row).some((m) => m.email.toLowerCase() === email.toLowerCase()))
      throw new HttpError(409, 'already_member', 'The user with that address is already a member of this project.', 'There is nothing to do.')
    const inv: ProjectInvite = { id: newId('pin'), email, role: 'member', created_at: new Date().toISOString(), expires_at: ago(-60 * 24 * 7) }
    list.unshift(inv)
    send(res, 201, { ...inv, url: 'http://localhost:5174/join/mock-join' })
    return true
  }
  if (method === 'DELETE' && parts[3]) {
    const i = list.findIndex((x) => x.id === parts[3])
    if (i < 0) throw new HttpError(404, 'invite_not_found', 'This project has no unused invite with that id.')
    list.splice(i, 1)
    send(res, 204)
    return true
  }
  return false
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
  // Going back is done from the dashboard (or by an assistant), never by a push.
  const d = deployment(app, seq, 'queued', 0, { source_kind: kind, finished_at: null, duration_seconds: null, started_at: null, ...(kind === 'rollback' ? { channel: 'dashboard' } : {}) })
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
        // Uploaded files that auto-detect builds as a site (the mock's guess): served by bitploy, fixed small size.
        if ((kind === 'upload' || kind === 'files') && ['auto', 'static', 'static_build'].includes(app.build_mode) && !app.template && !app.catalog_id) {
          app.static = true
          app.memory_mb = 32
          app.cpu_millis = 100
        }
        app.live = liveOf(app.slug, app.memory_mb, app.cpu_millis)
        // The after-deploy command runs in the live version; "fail" in it makes it fail, to show the warning.
        const after = kind === 'rollback' ? '' : (app.after_deploy_command ?? '')
        if (after) {
          d.after_deploy = 'running'
          setTimeout(() => {
            if (/fail/.test(after)) {
              d.after_deploy = 'failed'
              d.warnings = [...(d.warnings ?? []), AFTER_DEPLOY_WARNING]
            } else d.after_deploy = 'succeeded'
          }, 10_000)
        }
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
    backups_supported: true,
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
  a.public_port ??= closedPort(a)
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

/** Realistic-looking traffic: busier by day than by night and on weekdays, a few bots, a slow spell. */
function trafficOf(app: App, window: string, list = ''): Traffic {
  const r = seeded(`${app.slug}-traffic-${window}`)
  const hourly = window === '24h'
  const n = window === '30d' ? 30 : window === '7d' ? 7 : 24
  const scale = app.template || app.type === 'worker' ? 0 : app.static ? 0.4 : 1
  const HOUR = 3_600_000
  const DAY = 24 * HOUR
  const lastStart = hourly ? Math.floor(NOW / HOUR) * HOUR : Math.floor(NOW / DAY) * DAY
  // Share of the newest hour or day that has passed: it is still counting.
  const passed = (NOW - lastStart) / (hourly ? HOUR : DAY)

  const points: TrafficPoint[] = Array.from({ length: n }, (_, i) => {
    const ts = lastStart - (n - 1 - i) * (hourly ? HOUR : DAY)
    const d = new Date(ts)
    const busy = hourly ? 0.25 + Math.max(0, Math.sin(((d.getUTCHours() - 6) / 24) * Math.PI * 2)) : [0.6, 1, 1, 0.95, 1, 0.9, 0.55][d.getUTCDay()]!
    const share = i === n - 1 ? passed : 1
    const visitors = Math.round((hourly ? 22 : 260) * busy * (0.8 + r() * 0.4) * scale * share)
    const page_views = Math.round(visitors * (2.2 + r() * 0.8))
    const bots = Math.round((hourly ? 30 : 700) * (0.7 + r() * 0.6) * scale * share)
    const requests = page_views * 6 + bots
    const slowSpell = !hourly && i === n - 3
    const median = visitors ? Math.round((slowSpell ? 260 : 90) * (0.85 + r() * 0.3)) : null
    return {
      ts: new Date(ts).toISOString(),
      requests,
      visitors,
      page_views,
      bots,
      errors: slowSpell ? Math.round(requests * 0.03) : r() < 0.25 ? Math.round(r() * 3 * scale) : 0,
      client_errors: Math.round(requests * (0.01 + r() * 0.02)),
      median_ms: median,
      p95_ms: median === null ? null : Math.round(median * (slowSpell ? 6 : 3.5 + r())),
    }
  })
  const sum = (k: 'requests' | 'visitors' | 'errors' | 'client_errors' | 'bots' | 'page_views') => points.reduce((s, p) => s + p[k], 0)
  const requests = sum('requests')
  const errors = sum('errors')
  const timed = Math.round(sum('page_views') * 1.3)
  const medians = points.filter((p) => p.median_ms !== null).map((p) => p.median_ms!).sort((a, b) => a - b)
  const p95s = points.filter((p) => p.p95_ms !== null).map((p) => p.p95_ms!).sort((a, b) => a - b)
  const totals = {
    requests,
    visitors: sum('visitors'),
    errors,
    error_rate: requests ? Math.round((errors / requests) * 1000) / 10 : 0,
    client_errors: sum('client_errors'),
    bots: sum('bots'),
    page_views: sum('page_views'),
    timed,
    slow: Math.round(timed * 0.012),
    median_ms: medians[Math.floor(medians.length / 2)] ?? null,
    p95_ms: p95s[Math.floor(p95s.length * 0.9)] ?? null,
  }
  const prev = (f: number) => Math.round(f * (0.75 + r() * 0.5))
  const previous = window === '30d'
    ? null
    : {
        ...totals,
        requests: prev(totals.requests),
        visitors: prev(totals.visitors),
        page_views: prev(totals.page_views),
        errors: prev(totals.errors + 2),
        median_ms: totals.median_ms === null ? null : prev(totals.median_ms),
      }

  const k = (hourly ? 2 : n) * scale // lists cover whole days: 24 hours means today and yesterday
  const items = (rows: [string, number][]) => rows.map(([name, c]) => ({ name, count: Math.round(c * k) })).filter((i) => i.count > 0)
  const pages = items([
    ['/', 210], ['/products', 120], ['/products/oak-table', 64], ['/cart', 41], ['/about', 30], ['/blog/how-we-build-tables', 26],
    ['/contact', 18], ['/products/walnut-chair', 15], ['/blog', 12], ['/checkout', 9], ['/faq', 7], ['/products/pine-shelf', 5],
    ['/blog/caring-for-wood', 4], ['/privacy', 2],
  ])
  const referrers = items([
    ['(direct)', 260], ['google.com', 140], ['duckduckgo.com', 22], ['instagram.com', 18], ['bing.com', 9], ['news.ycombinator.com', 7],
    ['facebook.com', 6], ['pinterest.com', 5], ['reddit.com', 3], ['linkedin.com', 2], ['t.co', 1],
  ])
  const countries = ([
    ['NL', 120], ['BE', 34], ['DE', 30], ['US', 22], ['GB', 14], ['FR', 9], ['unknown', 6], ['ES', 5], ['SE', 4], ['PL', 3], ['IT', 2], ['CA', 1],
  ] as [string, number][])
    .map(([code, v]) => ({ code, visitors: Math.round(v * k), requests: Math.round(v * k * 14) }))
    .filter((c) => c.visitors > 0)
  const not_found = items([['/wp-login.php', 9], ['/images/old-logo.png', 4], ['/products/maple-desk', 2], ['/.env', 1]])
  const pick = <T,>(name: string, rows: T[]): T[] => (!list ? rows.slice(0, 10) : list === name ? rows : [])
  if (list) points.length = 0

  return {
    app: app.slug,
    window: window as Traffic['window'],
    granularity: hourly ? 'hour' : 'day',
    visitors_today: Math.round(180 * scale),
    totals,
    previous,
    points: window === 'today' ? [] : points,
    pages: pick('pages', pages),
    countries: pick('countries', countries),
    referrers: pick('referrers', referrers),
    not_found: pick('not_found', not_found),
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
  // Like the server: what is left never goes below zero.
  const left = Math.max(0, QUOTA.memory_mb - usedMemory())
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
      cpu_millis_left: Math.max(0, QUOTA.cpu_millis - apps.reduce((n, a) => n + (a.static ? 0 : a.cpu_millis), 0)),
      storage_mb_left: 20480 - 4200,
      fits: memory <= left,
      message: `This needs ${memory} MB of memory of the ${left} MB you have left.`,
    },
    warnings: services.some((s) => s.kind === 'build') ? [] : [],
    errors,
  }
}

// Room for the seeded apps and a few more, so the dashboard can add a database or deploy a site.
const QUOTA = { apps: 30, memory_mb: 8192, cpu_millis: 8000, deploys_per_hour: 30, domains_per_app: 5, volume_mb: 20480 }

function usedMemory(): number {
  return apps.reduce((n, a) => n + (a.static ? 0 : a.memory_mb), 0)
}

function me(): Me {
  const running = apps.filter((a) => a.status === 'running')
  return {
    user,
    quota: process.env.MOCK_OVERALLOCATE === '1' ? { ...QUOTA, overallocate: true } : QUOTA,
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
    readonly details?: unknown,
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

// ---------------------------------------------------------------- account and admin

const tokens: Token[] = [
  { id: 'tok_claude', name: 'Claude Code on the laptop', prefix: 'bp_7Kq2', scope: 'deploy', created_at: ago(60 * 24 * 30), expires_at: null, last_used_at: ago(42), last_used_ip: '203.0.113.7' },
  { id: 'tok_ci', name: 'GitHub Actions', prefix: 'bp_Lm9x', scope: 'deploy', created_at: ago(60 * 24 * 12), expires_at: ago(-60 * 24 * 78), last_used_at: ago(60 * 26), last_used_ip: '140.82.112.4' },
  { id: 'tok_read', name: 'Status dashboard', prefix: 'bp_Qa31', scope: 'read', created_at: ago(60 * 24 * 60), expires_at: null, last_used_at: null, last_used_ip: null },
]
const sessions: Session[] = [
  { id: 'ses_now', current: true, ip: '203.0.113.7', user_agent: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36', created_at: ago(60 * 5), last_seen_at: ago(1) },
  { id: 'ses_phone', current: false, ip: '198.51.100.23', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1', created_at: ago(60 * 24 * 3), last_seen_at: ago(60 * 7) },
]

// What PATCH /admin/users/{id} changed (adminUsers() builds the list fresh each time).
const userEdits: Record<string, { status?: AdminUser['status']; quota?: Partial<AdminUser['quota']> }> = {}
function adminUsers(): AdminUser[] {
  const m = me()
  const people: [string, string, string, 'user' | 'admin', 'active' | 'suspended', number][] = [
    ['usr_ada', 'ada@example.com', 'Ada', 'admin', 'active', apps.length],
    ['usr_linus', 'linus@example.com', 'Linus', 'user', 'active', 3],
    ['usr_grace', 'grace@example.com', 'Grace', 'user', 'active', 1],
    ['usr_ken', 'ken@example.com', 'Ken', 'user', 'suspended', 0],
  ]
  return people.map(([id, email, name, role, status, count], i) => ({
    id,
    email,
    name,
    role,
    status: userEdits[id]?.status ?? status,
    created_at: ago(60 * 24 * (90 - i * 20)),
    // The admin runs the demo projects and has room for them.
    quota: { ...(role === 'admin' ? { ...m.quota, apps: 30, memory_mb: 8192 } : m.quota), ...userEdits[id]?.quota },
    usage: {
      ...m.usage,
      apps: count,
      apps_running: status === 'active' ? count : 0,
      memory_reserved_mb: Math.round((m.usage.memory_reserved_mb * count) / Math.max(1, apps.length)),
      deploys_month: [38, 12, 3, 0][i]!,
      build_minutes_month: [42, 15, 4, 0][i]!,
      running_hours_month: [3100, 2160, 720, 0][i]!,
    },
    app_count: count,
    app_domain_ids: id === 'usr_linus' ? ['apd_shop'] : [],
  }))
}
const invites: Invite[] = [
  { id: 'inv_open', email: 'margaret@example.com', role: 'user', created_at: ago(60 * 20), expires_at: ago(-60 * 24 * 6), used_at: null },
  { id: 'inv_used', email: 'grace@example.com', role: 'user', created_at: ago(60 * 24 * 40), expires_at: ago(60 * 24 * 33), used_at: ago(60 * 24 * 39) },
]
const appDomains: AppDomain[] = [
  { id: 'default', domain: DOMAIN, default: true, certificate: 'install', cert_expires_at: ago(-60 * 24 * 61), app_count: 14, user_count: 0, created_at: null },
  { id: 'apd_shop', domain: 'shop.example.com', default: false, certificate: 'own', cert_expires_at: ago(-60 * 24 * 20), app_count: 2, user_count: 1, created_at: ago(60 * 24 * 15) },
  { id: 'apd_new', domain: 'apps.example.org', default: false, certificate: 'none', cert_expires_at: null, app_count: 0, user_count: 0, created_at: ago(60 * 3) },
]
const auditRows: [string, string, string, string, Record<string, unknown> | null][] = [
  ['deployment.queue', 'app', 'mk-api', 'mcp', { source: 'git', commit: '4f2a9c1' }],
  ['env.set', 'app', 'mk-store', 'dashboard', { names: ['API_URL'] }],
  ['token.create', 'token', 'tok_ci', 'dashboard', { scope: 'deploy' }],
  ['invite.create', 'invite', 'inv_open', 'dashboard', { role: 'user' }],
  ['app.stop', 'app', 'notes', 'api', null],
  ['auth.login', 'user', 'usr_ada', 'dashboard', null],
  ['domain.add', 'app_domain', 'apd_new', 'dashboard', { domain: 'apps.example.org' }],
]
const audit: AuditEntry[] = auditRows.map(([action, target_type, target_id, channel, details], i) => ({
  id: 900 - i,
  ts: ago(i * 37 + 3),
  user_id: 'usr_ada',
  channel,
  token_id: channel === 'mcp' ? 'tok_claude' : null,
  ip: '203.0.113.7',
  action,
  target_type,
  target_id: target_type === 'app' ? (findApp(target_id)?.id ?? target_id) : target_id,
  details,
}))
function serverStats(): ServerStats {
  return {
    cpu_percent: 23,
    load1: 0.84,
    mem_total_mb: 16384,
    mem_used_mb: 7310,
    mem_reserved_mb: usedMemory() + 2048,
    disk_total_gb: 160,
    disk_used_gb: 61,
    docker_images_gb: 18.4,
    docker_build_cache_gb: 6.2,
    docker_volumes_gb: 3.1,
    builds_running: 1,
    builds_queued: 0,
    apps_running: apps.filter((a) => a.status === 'running').length,
    apps_total: apps.length,
    apps_max: 60,
  }
}

function serverMetricsOf(window: string): ServerMetrics {
  const total = serverStats().mem_total_mb
  const stepMin = window === '7d' ? 168 : window === '24h' ? 24 : 1
  const r = seeded(`server-${window}`)
  const points: ServerMetricPoint[] = []
  let m = total * 0.42
  let c = 18
  for (let i = 59; i >= 0; i--) {
    m = Math.max(total * 0.25, Math.min(total * 0.8, m + (r() - 0.48) * total * 0.02))
    c = Math.max(2, Math.min(85, c + (r() - 0.5) * 8))
    points.push({ ts: ago(i * stepMin), cpu: Math.round(c * 10) / 10, memory_mb: Math.round(m), memory_total_mb: total })
  }
  return { window: window as ServerMetrics['window'], mem_total_mb: total, points }
}

// ---------------------------------------------------------------- a SQLite file in /data, and bitploy's own database

const col = (name: string, type: string, pk = 0, notnull = false, redacted = false) => ({ name, type, notnull, default: null, pk, hidden: false, generated: false, redacted })
const dbTables: Record<'app' | 'admin', { table: DbTable; rows: DbValue[][] }[]> = {
  app: [
    {
      table: { name: 'notes', type: 'table', without_rowid: false, editable: true, rows: 4, sql: 'CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT, created_at TEXT)', columns: [col('id', 'INTEGER', 1), col('title', 'TEXT', 0, true), col('body', 'TEXT'), col('created_at', 'TEXT')], indexes: [] },
      rows: [
        [1, 'Groceries', 'Milk, eggs, coffee', ago(60 * 24 * 3)],
        [2, 'Deploy checklist', 'Run the tests, then push to main', ago(60 * 30)],
        [3, 'Ideas', null, ago(60 * 5)],
        [4, 'Meeting', 'Thursday 10:00 with the design team', ago(25)],
      ],
    },
    {
      table: { name: 'tags', type: 'table', without_rowid: false, editable: true, rows: 2, sql: 'CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT UNIQUE)', columns: [col('id', 'INTEGER', 1), col('name', 'TEXT')], indexes: [{ name: 'sqlite_autoindex_tags_1', unique: true, columns: ['name'], sql: null }] },
      rows: [
        [1, 'home'],
        [2, 'work'],
      ],
    },
  ],
  admin: [
    {
      table: { name: 'users', type: 'table', without_rowid: false, editable: false, rows: 4, sql: 'CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT NOT NULL, role TEXT, password_hash TEXT)', columns: [col('id', 'TEXT', 1), col('email', 'TEXT', 0, true), col('role', 'TEXT'), col('password_hash', 'TEXT', 0, false, true)], indexes: [] },
      rows: [],
    },
    {
      table: { name: 'apps', type: 'table', without_rowid: false, editable: false, rows: null, sql: 'CREATE TABLE apps (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT)', columns: [col('id', 'TEXT', 1), col('slug', 'TEXT', 0, true), col('status', 'TEXT')], indexes: [] },
      rows: [],
    },
  ],
}
function dbTablesOf(kind: 'app' | 'admin') {
  if (kind === 'admin') {
    dbTables.admin[0]!.rows = adminUsers().map((u) => [u.id, u.email, u.role, null])
    dbTables.admin[1]!.rows = apps.map((a) => [a.id, a.slug, a.status])
  }
  return dbTables[kind]
}
function dbSchema(kind: 'app' | 'admin', path: string): DbSchema {
  const tables = dbTablesOf(kind).map((t) => ({ ...t.table, rows: t.rows.length }))
  return { path: kind === 'admin' ? '/var/lib/bitploy/bitploy.db' : `/data/${path}`, size: kind === 'admin' ? 1_482_752 : 24_576, journal_mode: 'wal', page_size: 4096, sqlite_version: '3.46.1', tables }
}
function dbRows(kind: 'app' | 'admin', body: Record<string, unknown>) {
  const t = dbTablesOf(kind).find((x) => x.table.name === body.table)
  if (!t) throw new HttpError(404, 'table_not_found', `There is no table called ${String(body.table)}.`)
  const columns = t.table.columns.map((c) => c.name)
  const limit = Number(body.limit ?? 100)
  const offset = Number(body.offset ?? 0)
  const search = String(body.search ?? '').toLowerCase()
  const list = t.rows.filter((r) => !search || r.some((v) => String(v ?? '').toLowerCase().includes(search)))
  const app = kind === 'app'
  return {
    columns,
    key_columns: app ? [columns[0]!] : [],
    editable: app,
    rows: list.slice(offset, offset + limit).map((r) => ({ key: app ? { [columns[0]!]: r[0]! } : null, values: r })),
    total: list.length,
    offset,
    limit,
  }
}
function dbSql(kind: 'app' | 'admin', sql: string) {
  const name = /from\s+["`]?(\w+)/i.exec(sql)?.[1]
  const t = dbTablesOf(kind).find((x) => x.table.name === name)
  if (!t) throw new HttpError(422, 'sql_error', name ? `no such table: ${name}` : 'The mock runs only SELECT ... FROM <table>.')
  return { results: [{ sql, columns: t.table.columns.map((c) => c.name), rows: t.rows, truncated: false, rows_affected: null, last_insert_rowid: null, took_ms: 0.4 }], took_ms: 0.4 }
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

  if (method === 'PATCH' && parts[0] === 'admin' && parts[1] === 'users' && parts[2] && !parts[3]) {
    if (!adminUsers().some((u) => u.id === parts[2])) throw new HttpError(404, 'not_found', 'No such person.')
    const e = (userEdits[parts[2]] ??= {})
    if (body.status === 'active' || body.status === 'suspended') e.status = body.status
    if (body.quota && typeof body.quota === 'object') e.quota = { ...e.quota, ...(body.quota as Partial<AdminUser['quota']>) }
    return send(res, 200, adminUsers().find((u) => u.id === parts[2]))
  }

  if (memberRoutes(method, parts, body, res)) return

  // DELETE /admin/public-ports/{app}: the router only knows ids after apps, projects, catalog and deployments.
  if (method === 'DELETE' && parts[0] === 'admin' && parts[1] === 'public-ports' && parts[2]) {
    const a = needApp(parts[2])
    a.public_port = closedPort(a)
    return send(res, 204)
  }

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
      return send(res, 200, items(tokens))
    case 'GET /sessions':
      return send(res, 200, items(sessions))

    // ---------------------------------------------------------------- admin
    case 'GET /admin/users':
      return send(res, 200, items(adminUsers()))
    case 'GET /admin/invites':
      return send(res, 200, items(invites))
    case 'POST /admin/invites': {
      const inv: Invite = { id: newId('inv'), email: body.email ? String(body.email) : null, role: body.role === 'admin' ? 'admin' : 'user', created_at: ago(0), expires_at: ago(-60 * 24 * 7), used_at: null, url: 'http://localhost:5177/invite/mock-code' }
      invites.unshift(inv)
      return send(res, 201, inv)
    }
    case 'GET /admin/server':
      return send(res, 200, serverStats())
    case 'GET /admin/server/metrics':
      return send(res, 200, serverMetricsOf(q.get('window') ?? '24h'))
    case 'GET /admin/public-ports':
      return send(res, 200, {
        enabled: true,
        ...PUBLIC_PORTS,
        open: apps
          .filter((a) => a.public_port?.enabled)
          .map((a) => ({ app: a.slug, app_name: a.name, owner_email: user.email, service: a.public_port!.service, port: a.public_port!.port!, allow_from: a.public_port!.allow_from, expires_at: a.public_port!.expires_at, opened_at: a.public_port!.opened_at! })),
      } satisfies PublicPortsOverview)
    case 'GET /admin/app-domains':
      return send(res, 200, { items: appDomains, server_ipv4: '203.0.113.10', server_ipv6: '2001:db8::10' })
    case 'GET /admin/audit':
      return send(res, 200, { items: audit })
    case 'GET /admin/github':
      return send(res, 200, { configured: true, name: 'bitploy-ada', slug: 'bitploy-ada', html_url: 'https://github.com/apps/bitploy-ada', manage_url: 'https://github.com/settings/apps/bitploy-ada', owner: 'ada', webhook_url: 'https://bitploy.example.com/api/v1/github/webhook', push_events: true, created_at: ago(60 * 24 * 80) })
    case 'GET /admin/db':
      return send(res, 200, dbSchema('admin', ''))
    case 'POST /admin/db/rows':
      return send(res, 200, dbRows('admin', body))
    case 'POST /admin/db/sql':
      return send(res, 200, dbSql('admin', String(body.sql ?? '')))

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
      return send(res, 200, items(projects.filter(visible).map(projectOf)))
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
      const row = needProject(id, method !== 'GET')
      if (method === 'PATCH') {
        if (typeof body.name === 'string' && body.name.trim()) row.name = body.name.trim()
        for (const a of apps) if (a.project?.id === row.id) a.project = { id: row.id, name: row.name }
      }
      if (method === 'DELETE') {
        if (q.get('confirm') !== row.name) throw new HttpError(422, 'confirm_mismatch', `Type ${row.name} to confirm.`)
        const own = apps.filter((a) => a.project?.id === row.id)
        if (own.length && q.get('with_apps') !== 'true')
          throw new HttpError(409, 'project_not_empty', `${row.name} still has ${own.length} apps.`, 'Delete them first, or pass with_apps=true.')
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
      // Like the server: connecting never replaces a variable the app already has.
      for (const target of (body.connect_to as string[] | undefined) ?? []) {
        const t = findApp(target)
        if (t && (env[t.slug] ?? []).some((v) => v.name === tpl.main_var && !v.service))
          throw new HttpError(409, 'connect_to_var_set', `The app ${t.slug} already has a variable ${tpl.main_var}.`, `Remove it with set_env unset=[${tpl.main_var}] and add the service again, or set ${tpl.main_var}=\${{${alias}.${tpl.main_var}}} yourself after adding it.`)
      }
      const slug = uniqueSlug(`${slugish(row.name)}-${alias}`)
      const a = blankApp(alias, slug, row.id, {
        type: 'worker',
        url: '',
        port: tpl.port,
        template: tpl.id,
        backups_supported: !noBackups(tpl.id),
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
      env[a.slug] = tpl.connection_vars.map((name) => ({ name, masked: MASK, hidden: true, service: '', updated_at: new Date().toISOString() }))
      for (const target of (body.connect_to as string[] | undefined) ?? []) {
        const t = findApp(target)
        if (!t) continue
        const list = (env[t.slug] ??= [])
        const ref = `\${{${alias}.${tpl.main_var}}}`
        const existing = list.find((v) => v.name === tpl.main_var && !v.service)
        if (existing) Object.assign(existing, { masked: ref, hidden: false, value: ref, references: [ref], updated_at: new Date().toISOString() })
        else list.push({ name: tpl.main_var, masked: ref, hidden: false, value: ref, references: [ref], service: '', updated_at: new Date().toISOString() })
        syncRefs(t)
        if (t.status === 'running') startDeploy(t, 'rollback')
      }
      startDeploy(a, 'files')
      return send(res, 202, a)
    }

    // ---------------------------------------------------------------- apps
    case 'GET /apps':
      return send(res, 200, items(apps.filter((a) => projects.some((p) => p.id === a.project?.id && visible(p)))))
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
      if (body.project !== undefined) throw new HttpError(422, 'project_move_unsupported', 'An app cannot move to another project.', 'Create a new app in the other project and deploy the code to it.')
      if (typeof body.name === 'string') a.name = body.name
      if (typeof body.alias === 'string') {
        const taken = apps.some((x) => x !== a && x.project?.id === a.project?.id && (x.alias || x.slug) === body.alias)
        if (taken) throw new HttpError(409, 'alias_taken', `Another app in ${a.project?.name} is called ${body.alias}.`, 'Pick another short name.')
        a.alias = body.alias
        if (a.port) a.internal_host = `${a.alias}:${a.port}`
      }
      for (const k of ['memory_mb', 'cpu_millis', 'port', 'volume_mb'] as const) if (typeof body[k] === 'number') (a[k] as number) = body[k] as number
      if (body.port === 0) a.port = null
      if (body.port === 22) throw new HttpError(422, 'port_reserved', 'Port 22 is reserved.', 'Use the port your app listens on, like 3000.')
      if (body.type === 'web' || body.type === 'worker') {
        a.type = body.type
        a.url = a.type === 'web' ? `https://${a.slug}.${DOMAIN}` : ''
      }
      for (const k of ['health_path', 'dockerfile_path', 'context_dir'] as const) if (typeof body[k] === 'string') a[k] = body[k] as string
      for (const k of ['before_deploy_command', 'after_deploy_command'] as const)
        if (typeof body[k] === 'string') {
          if (body[k] && (a.template || a.catalog_id || a.static || a.build_mode === 'compose'))
            throw new HttpError(422, 'deploy_commands_unsupported', 'This app is a static site: it runs no code of its own, so it has no deploy commands.', 'Leave before_deploy_command and after_deploy_command out for a static site.')
          a[k] = (body[k] as string).trim()
        }
      return send(res, 200, a)
    }
    case 'DELETE /apps/:id': {
      const a = needApp(id)
      // Apps of the project whose variables point at this one keep it: the server answers app_in_use.
      const alias = a.alias || a.slug
      const dependents = apps
        .filter((x) => x !== a && x.project?.id === a.project?.id)
        .map((x) => ({ slug: x.slug, alias: x.alias || x.slug, vars: (env[x.slug] ?? []).filter((v) => (v.references ?? []).some((r) => r.startsWith(`\${{${alias}.`))).map((v) => v.name) }))
        .filter((d) => d.vars.length)
      if (dependents.length)
        throw new HttpError(409, 'app_in_use', `${dependents.map((d) => d.alias).join(', ')} still ${dependents.length === 1 ? 'reads' : 'read'} variables of ${alias}.`, 'Remove those variables first.', { dependents })
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
    case 'POST /apps/:id/rollback': {
      const a = needApp(id)
      if (!(deployments[a.slug] ?? []).some((d) => d.id === body.deployment_id))
        throw new HttpError(404, 'not_found', 'That deployment is not one of this app.')
      return send(res, 202, startDeploy(a, 'rollback'))
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
    case 'PUT /apps/:id/public-port': {
      const a = needApp(id)
      const cur = a.public_port ?? closedPort(a)
      if (!body.enabled) {
        a.public_port = closedPort(a)
        return send(res, 200, a)
      }
      if (!cur.available && !cur.enabled) throw new HttpError(422, 'public_port_web_app', `The app ${a.slug} has a web address, so it gets no public port.`, 'Open the app at its URL.')
      const minutes = body.close_after === 'never' ? null : body.close_after === '24h' ? 24 * 60 : body.close_after === '1h' || !cur.enabled ? 60 : undefined
      let allow = Array.isArray(body.allow_from) ? (body.allow_from as string[]) : cur.allow_from
      if (allow.some((x) => !/^\d{1,3}(\.\d{1,3}){3}(\/\d{1,2})?$/.test(x)))
        throw new HttpError(400, 'allow_from_invalid', 'allow_from: not an IPv4 address or network.', 'Use IPv4 addresses like 203.0.113.7.')
      if (body.only_my_ip) allow = [...allow, '198.51.100.23/32']
      allow = allow.map((x) => (x.includes('/') ? x : `${x}/32`))
      const multi = a.services.length > 1
      const service = multi ? String(body.service || cur.service || a.services.find((s) => !(s.public && a.type === 'web'))?.name || '') : undefined
      if (!cur.enabled) {
        const mine = apps.filter((x) => x.public_port?.enabled).length
        if (mine >= PUBLIC_PORTS.per_user)
          throw new HttpError(409, 'public_port_limit', `You have ${mine} public ports open, the most allowed.`, 'Close another one first.')
      }
      const used = new Set(apps.map((x) => x.public_port?.enabled && x !== a ? x.public_port.port : 0))
      let port = cur.enabled ? cur.port! : PUBLIC_PORTS.from
      while (!cur.enabled && used.has(port)) port++
      const next = openPort(a, port, { service, minutes: minutes === undefined ? null : minutes, allow })
      if (cur.enabled && minutes === undefined) next.expires_at = cur.expires_at
      if (cur.enabled) next.opened_at = cur.opened_at
      a.public_port = next
      return send(res, 200, a)
    }
    case 'POST /apps/:id/public-port/reveal': {
      const a = needApp(id)
      const p = a.public_port
      if (!p?.enabled) throw new HttpError(409, 'public_port_closed', 'The public port is closed.', 'Open it first.')
      return send(res, 200, { connection_url: mockConnectionURL(a, p.address!, false), address: p.address!, secrets: mockSecrets(a) })
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
      const hiddenFlags = (body.hidden as Record<string, boolean> | undefined) ?? {}
      for (const [name, value] of Object.entries((body.set as Record<string, string>) ?? {})) {
        const isRef = /\$\{\{/.test(value)
        const v = list.find((x) => x.name === name && x.service === svc)
        // A name left out of hidden keeps its flag; a new variable is readable.
        const hidden = hiddenFlags[name] ?? v?.hidden ?? false
        const next = { masked: maskOf(value), hidden, value: hidden ? undefined : value, references: isRef ? refsOf(value) : undefined, updated_at: new Date().toISOString() }
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
      return send(res, 200, trafficOf(needApp(id), q.get('window') ?? '24h', q.get('list') ?? ''))
    case 'GET /apps/:id/deployments':
      needApp(id)
      return send(res, 200, items(deployments[id] ?? []))
    case 'GET /apps/:id/domains':
      needApp(id)
      return send(res, 200, items([]))
    case 'GET /apps/:id/backups': {
      const a = needApp(id)
      // A database service is backed up as a dump made by its own tool.
      const dumpTool = ({ postgres: 'pg_dumpall', pgvector: 'pg_dumpall', mariadb: 'mariadb-dump', mysql: 'mysqldump' } as Record<string, string>)[a.template ?? '']
      const version = dumpTool === 'pg_dumpall' ? `pg_dumpall (PostgreSQL) ${a.template_version || '18.4'}` : `${dumpTool} ${a.template_version ?? ''}`.trim()
      const dumps: Backup[] = dumpTool
        ? [
            { id: `bak_${id}_3`, kind: 'scheduled', format: 'dump', tool: version, status: 'ready', size_bytes: 3_412_000, created_at: ago(60 * 9) },
            { id: `bak_${id}_2`, kind: 'manual', format: 'dump', tool: version, status: 'ready', size_bytes: 3_350_000, created_at: ago(60 * 26) },
            { id: `bak_${id}_1`, kind: 'scheduled', format: 'volume', status: 'ready', size_bytes: 41_900_000, created_at: ago(60 * 33) },
          ]
        : []
      return send(res, 200, {
        items: dumps,
        settings: { schedule: 'daily', keep_mode: 'days', keep: 14, time_utc: '03:20', next_at: ago(-300) },
        restore: null,
        busy: false,
        total_bytes: dumps.reduce((n, b) => n + b.size_bytes, 0),
        dump_tool: dumpTool,
        upload_max_bytes: 512 * 1024 * 1024,
        volumes: a.volumes.map((v) => ({ ...v, upload_max_bytes: 512 * 1024 * 1024 })),
      } satisfies BackupList & { volumes: unknown[] })
    }
    case 'GET /apps/:id/files': {
      const a = needApp(id)
      const at = q.get('path') ?? ''
      // An app of your own with saved files keeps a SQLite database and an uploads folder at the top of /data.
      const top: DataFileList['items'] =
        a.volume && !a.catalog_id && !a.template && !at
          ? [
              { name: 'app.db', type: 'file', size: 24_576, modified_at: ago(25) },
              { name: 'uploads', type: 'dir', size: 0, modified_at: ago(60 * 24) },
            ]
          : []
      return send(res, 200, { path: at, items: top, truncated: false, upload_max_bytes: 100 * 1024 * 1024 } satisfies DataFileList)
    }
    case 'GET /apps/:id/db': {
      const a = needApp(id)
      // A database app answers like its server: the same tables, named databases instead of a file.
      if (a.template === 'postgres' || a.template === 'mariadb' || a.template === 'mysql') {
        const database = q.get('path') || 'app'
        return send(res, 200, { ...dbSchema('app', database), path: database, journal_mode: '', page_size: 0, sqlite_version: '', engine: a.template, server_version: a.template === 'postgres' ? '18.4' : '11.4.2-MariaDB', database, databases: ['app', 'postgres'] })
      }
      return send(res, 200, dbSchema('app', q.get('path') ?? 'app.db'))
    }
    case 'POST /apps/:id/db/rows':
      needApp(id)
      return send(res, 200, dbRows('app', body))
    case 'POST /apps/:id/db/sql':
      needApp(id)
      return send(res, 200, dbSql('app', String(body.sql ?? '')))
    case 'GET /apps/:id/logs': {
      const a = needApp(id)
      if (q.get('follow')) throw new HttpError(404, 'not_found', 'The mock has no live log stream.')
      const failed = deployments[a.slug]?.find((d) => d.id === q.get('deployment') && d.state === 'failed')
      const lines =
        q.get('kind') === 'build'
          ? failed
            ? FAILED_BUILD_LOG
            : BUILD_LOG
          : a.template
            ? [
                '2026-11-06 09:59:58.101 UTC [1] LOG:  starting PostgreSQL 16.4 on x86_64-pc-linux-musl',
                '2026-11-06 09:59:58.120 UTC [1] LOG:  listening on IPv4 address "0.0.0.0", port 5432',
                '2026-11-06 09:59:58.204 UTC [1] LOG:  database system is ready to accept connections',
                '2026-11-06 10:04:58.311 UTC [27] LOG:  checkpoint starting: time',
                '2026-11-06 10:05:01.012 UTC [44] ERROR:  relation "order_items" does not exist at character 15',
                '2026-11-06 10:05:01.012 UTC [44] STATEMENT:  SELECT * FROM order_items WHERE order_id = $1',
                '2026-11-06 10:05:03.870 UTC [27] LOG:  checkpoint complete: wrote 12 buffers (0.1%); write=1.204 s',
              ]
            : runtimeLog(a.port ?? 3000)
      return send(res, 200, { lines: lines.map((text, i) => ({ text, time: ago(lines.length - i) })) })
    }
    case 'GET /apps/:id/dockerfile': {
      const a = needApp(id)
      const c = catalog.find((x) => x.id === a.catalog_id)
      return send(res, 200, { path: 'Dockerfile', content: c?.dockerfile ?? SAMPLE_DOCKERFILE, editable: !a.git_source, reason: a.git_source ? 'The Dockerfile comes from the linked repository.' : '' })
    }
    case 'GET /deployments/:id/log': {
      const failed = Object.values(deployments).some((list) => list.some((d) => d.id === id && d.state === 'failed'))
      return send(res, 200, { lines: (failed ? FAILED_BUILD_LOG : BUILD_LOG).map((text) => ({ text })) })
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
      const alias = slugish(String(body.alias || c.id))
      if (apps.some((x) => x.project?.id === pid && (x.alias || x.slug) === alias))
        throw new HttpError(409, 'alias_taken', `That project already has an app called ${alias}.`, 'Pick another short name.')
      const a = blankApp(name, slug, pid, { catalog_id: c.id, catalog_version: c.version, port: c.port, memory_mb: c.memory_mb, volume: c.volume, volume_mb: c.volume_mb, alias })
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
          if (e instanceof HttpError) send(res, e.status, { error: { code: e.code, message: e.message, hint: e.hint || undefined, details: e.details } })
          else {
            console.error(e)
            send(res, 500, { error: { code: 'mock_failed', message: String(e) } })
          }
        })
      })
    },
  }
}
