// Services of a project in plain words. A project (the API's "app") has one or
// more services; a Dockerfile, static or ready-made project has exactly one.
// The dashboard names each service by what it does ("Website", "Database")
// and keeps the Docker image as secondary detail. Only erasable TypeScript, so
// Node can run the file in a test.

import type { App, AppService, AppStatus } from './types'

export type RoleKey = 'website' | 'api' | 'worker' | 'database' | 'cache' | 'queue' | 'files' | 'search' | 'service'

export interface Role {
  key: RoleKey
  /** "Website", "Database": what the service is for. */
  label: string
  /** One sentence for a beginner. */
  blurb: string
  /** The product, when it is a well-known image: "PostgreSQL 16". */
  product: string
}

/** The fields roles are inferred from; both AppService and a compose preview service have them. */
export interface ServiceLike {
  name: string
  kind: 'build' | 'image'
  image?: string
  public: boolean
  port: number | null
}

interface Known {
  match: RegExp
  key: RoleKey
  product: string
}

// Images people put next to their app, by repository name (any registry or owner).
const KNOWN: Known[] = [
  { match: /^(postgres|postgis|timescaledb|pgvector)$/, key: 'database', product: 'PostgreSQL' },
  { match: /^(mysql|percona)$/, key: 'database', product: 'MySQL' },
  { match: /^mariadb$/, key: 'database', product: 'MariaDB' },
  { match: /^(mongo|mongodb)$/, key: 'database', product: 'MongoDB' },
  { match: /^(clickhouse|clickhouse-server)$/, key: 'database', product: 'ClickHouse' },
  { match: /^(couchdb|surrealdb|cockroach|influxdb|neo4j)$/, key: 'database', product: '' },
  { match: /^(redis|redis-stack)$/, key: 'cache', product: 'Redis' },
  { match: /^valkey$/, key: 'cache', product: 'Valkey' },
  { match: /^keydb$/, key: 'cache', product: 'KeyDB' },
  { match: /^(memcached|dragonfly)$/, key: 'cache', product: '' },
  { match: /^rabbitmq$/, key: 'queue', product: 'RabbitMQ' },
  { match: /^(nats|kafka|redpanda)$/, key: 'queue', product: '' },
  { match: /^minio$/, key: 'files', product: 'MinIO' },
  { match: /^(meilisearch|typesense|elasticsearch|opensearch)$/, key: 'search', product: '' },
  { match: /^(nginx|caddy|httpd|traefik)$/, key: 'website', product: '' },
]

const LABELS: Record<RoleKey, { label: string; blurb: string }> = {
  website: { label: 'Website', blurb: 'What visitors open at the project address.' },
  api: { label: 'API', blurb: 'Answers requests from the other services. Not open to visitors.' },
  worker: { label: 'Worker', blurb: 'Runs background jobs. Has no web address.' },
  database: { label: 'Database', blurb: 'Keeps your data. Only the other services of this project can reach it.' },
  cache: { label: 'Cache', blurb: 'Keeps data in memory for speed. Only the other services of this project can reach it.' },
  queue: { label: 'Message queue', blurb: 'Passes jobs between the other services.' },
  files: { label: 'File storage', blurb: 'Keeps uploaded files for the other services.' },
  search: { label: 'Search', blurb: 'Search index for the other services.' },
  service: { label: 'Service', blurb: 'Part of this project. Only the other services can reach it.' },
}

/** postgres:16-alpine, docker.io/library/postgres:16, ghcr.io/owner/redis@sha256:... → ["postgres", "16-alpine"]. */
export function imageParts(image: string | undefined): { repo: string; tag: string } {
  if (!image) return { repo: '', tag: '' }
  const noDigest = image.split('@')[0] ?? ''
  const last = noDigest.split('/').pop() ?? ''
  const i = last.indexOf(':')
  return i < 0 ? { repo: last.toLowerCase(), tag: '' } : { repo: last.slice(0, i).toLowerCase(), tag: last.slice(i + 1) }
}

/** "16-alpine" → "16"; "latest" and digests say nothing. */
function version(tag: string): string {
  const m = /^v?(\d+(?:\.\d+){0,2})/.exec(tag)
  return m ? m[1]! : ''
}

const API_NAME = /(^|[-_])(api|backend|server|graphql|gateway)([-_]|$)/
const WORKER_NAME = /(^|[-_])(worker|jobs?|queue|cron|scheduler|celery|sidekiq|consumer)([-_]|$)/

/** What a service is for, guessed from its image, its name, whether it is public and whether it listens on a port. */
export function serviceRole(s: ServiceLike): Role {
  const { repo, tag } = imageParts(s.image)
  const known = s.kind === 'image' ? KNOWN.find((k) => k.match.test(repo)) : undefined
  const name = s.name.toLowerCase()
  let key: RoleKey
  if (s.public) key = 'website'
  else if (known) key = known.key
  else if (WORKER_NAME.test(name) || s.port === null) key = s.kind === 'build' || WORKER_NAME.test(name) ? 'worker' : 'service'
  else if (API_NAME.test(name) || s.kind === 'build') key = 'api'
  else key = 'service'
  // A public nginx is still the website; a private one is just a service.
  if (key === 'website' && !s.public) key = 'service'
  const v = version(tag)
  const product = known?.product ? `${known.product}${v ? ` ${v}` : ''}` : ''
  return { key, ...LABELS[key], product }
}

/** Website first, then what runs your code, then what keeps data. Ties keep the project's own order. */
const ORDER: Record<RoleKey, number> = { website: 0, api: 1, worker: 2, service: 3, queue: 4, search: 5, files: 6, cache: 7, database: 8 }

export function sortServices<T extends ServiceLike>(list: readonly T[]): T[] {
  return list
    .map((s, i) => ({ s, i, r: ORDER[serviceRole(s).key] }))
    .sort((a, b) => a.r - b.r || a.i - b.i)
    .map((x) => x.s)
}

/** A project with several services gets the service grid, pickers and per-service settings; one with one service stays as simple as before. */
export function isMulti(app: Pick<App, 'services' | 'service_layout'> | null | undefined): boolean {
  if (!app) return false
  return app.service_layout === 'multi' || (app.services?.length ?? 0) > 1
}

/** The service visitors reach at the project address, if any. */
export function publicService(app: Pick<App, 'services'>): AppService | undefined {
  return app.services?.find((s) => s.public)
}

/** The service's own status when the server sends one, otherwise the project's. */
export function serviceStatus(app: Pick<App, 'status'>, s: Pick<AppService, 'status'>): AppStatus {
  return s.status ?? app.status
}

/** The services that start after this one and reach it by name: who talks to it. */
export function usedBy(app: Pick<App, 'services'>, name: string): string[] {
  return (app.services ?? []).filter((s) => s.depends_on?.includes(name)).map((s) => s.name)
}

/** "a", "a and b", "a, b and c". */
export function listWords(words: string[]): string {
  if (words.length <= 1) return words[0] ?? ''
  return `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}`
}

/** What the service runs, as secondary detail: the image, or the folder it is built from. */
export function serviceSource(s: ServiceLike & { build?: unknown }): string {
  if (s.kind === 'image') return s.image ?? ''
  const b = s.build
  const ctx = typeof b === 'string' ? b : b && typeof b === 'object' && 'context' in b ? String((b as { context?: unknown }).context ?? '') : ''
  return ctx && ctx !== '.' ? `Built from ${ctx.replace(/^\.\//, '')}/` : 'Built from your code'
}
