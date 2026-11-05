import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  aliasOf,
  appRole,
  appSourceText,
  defaultConnect,
  dependentsFromError,
  dependentsOf,
  envReferences,
  freeAlias,
  groupTemplates,
  internalHostOf,
  isPublicApp,
  mainApp,
  parseReferences,
  pastedKind,
  projectHealth,
  projectLinks,
  projectsFromApps,
  referenceParts,
  referenceText,
  roleTitle,
  sortProjectApps,
  talksTo,
  templateLabel,
  templateRole,
  usedBy,
} from '../src/lib/projects.ts'
import type { App, AppService, EnvVar, ServiceTemplate } from '../src/lib/types.ts'

let n = 0
function app(slug: string, extra: Partial<App> = {}): App {
  n++
  const service: AppService = { name: 'app', kind: 'build', public: true, port: 3000, health_path: '/', memory_mb: 256, cpu_millis: 250, volumes: [], depends_on: [] }
  return {
    id: `app_${slug}`,
    catalog_id: '',
    catalog_version: '',
    catalog_update: null,
    slug,
    name: slug,
    type: 'web',
    url: `https://${slug}.example.com`,
    domain: 'example.com',
    owner_id: 'usr_1',
    status: 'running',
    desired_state: 'running',
    port: 3000,
    health_path: '/',
    dockerfile_path: 'Dockerfile',
    context_dir: '',
    build_mode: 'auto',
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
    restart_count: 0,
    services: [service],
    volumes: [],
    compose_path: '',
    created_at: `2026-11-0${Math.min(9, n)}T10:00:00Z`,
    updated_at: '2026-11-04T10:00:00Z',
    ...extra,
  }
}

const POSTGRES: ServiceTemplate = {
  id: 'postgres',
  name: 'PostgreSQL',
  category: 'database',
  description: 'A relational database.',
  image: 'postgres:16-alpine',
  version: '16.4',
  port: 5432,
  memory_mb: 256,
  volume_mb: 2048,
  connection_vars: ['DATABASE_URL', 'PGHOST', 'PGPASSWORD'],
  main_var: 'DATABASE_URL',
}
const REDIS: ServiceTemplate = { ...POSTGRES, id: 'redis', name: 'Redis', category: 'cache', image: 'redis:7-alpine', version: '7.4', port: 6379, connection_vars: ['REDIS_URL'], main_var: 'REDIS_URL' }
const MEILI: ServiceTemplate = { ...POSTGRES, id: 'meilisearch', name: 'Meilisearch', category: 'search', image: 'getmeili/meilisearch:v1.10', version: '1.10', port: 7700 }

// The server sends the ${{...}} tokens of a value as `references`; the rest stays masked.
const env = (name: string, masked: string, value?: string): EnvVar => ({
  name,
  masked,
  updated_at: '2026-11-04T10:00:00Z',
  service: '',
  references: value?.match(/\$\{\{[^}]+\}\}/g) ?? undefined,
})

const db = (alias: string, template: string, image: string, port: number) =>
  app(alias, {
    template,
    alias,
    type: 'worker',
    url: '',
    port,
    internal_host: `${alias}:${port}`,
    services: [{ name: 'app', kind: 'image', image, public: false, port, health_path: '', memory_mb: 256, cpu_millis: 250, volumes: ['data'], depends_on: [] }],
  })

test('aliases and internal hosts fall back to the slug and port', () => {
  assert.equal(aliasOf(app('shop-web')), 'shop-web')
  assert.equal(aliasOf(app('shop-web', { alias: 'web' })), 'web')
  assert.equal(internalHostOf(app('shop-api', { alias: 'api', port: 8080 })), 'api:8080')
  assert.equal(internalHostOf(db('postgres', 'postgres', 'postgres:16', 5432)), 'postgres:5432')
  assert.equal(internalHostOf(app('jobs', { type: 'worker', port: null })), '')
})

test('apps from a template are private; web apps with an address are public', () => {
  assert.equal(isPublicApp(app('web')), true)
  assert.equal(isPublicApp(app('jobs', { type: 'worker' })), false)
  assert.equal(isPublicApp(db('postgres', 'postgres', 'postgres:16', 5432)), false)
})

test('appRole names each app in plain words', () => {
  const t = [POSTGRES, REDIS, MEILI]
  assert.equal(roleTitle(appRole(app('shop'), t)), 'Website')
  assert.equal(appRole(app('shop-api', { alias: 'api' }), t).key, 'api')
  assert.equal(appRole(app('jobs', { type: 'worker', port: null }), t).key, 'worker')
  const pg = appRole(db('postgres', 'postgres', 'postgres:16-alpine', 5432), t)
  assert.equal(roleTitle(pg), 'Database · PostgreSQL 16')
  assert.equal(pg.lane, 'data')
  assert.equal(roleTitle(appRole(db('cache', 'redis', 'redis:7-alpine', 6379), t)), 'Cache · Redis 7')
  assert.equal(appRole(db('search', 'meilisearch', 'getmeili/meilisearch:v1.10', 7700), t).key, 'search')
  // Without the template list, the image says what it is.
  assert.equal(roleTitle(appRole(db('postgres', 'postgres', 'postgres:16-alpine', 5432))), 'Database · PostgreSQL 16')
  // Someone ran Redis from a Docker image: still a cache.
  const own = app('cache', { type: 'worker', url: '', services: [{ name: 'app', kind: 'image', image: 'redis:7', public: false, port: 6379, health_path: '', memory_mb: 64, cpu_millis: 100, volumes: [], depends_on: [] }] })
  assert.equal(appRole(own).key, 'cache')
})

test('a compose app is a website when one of its services is public', () => {
  const compose = app('stack', {
    build_mode: 'compose',
    services: [
      { name: 'web', kind: 'build', public: true, port: 3000, health_path: '/', memory_mb: 512, cpu_millis: 250, volumes: [], depends_on: ['db'] },
      { name: 'db', kind: 'image', image: 'postgres:16', public: false, port: 5432, health_path: '', memory_mb: 256, cpu_millis: 250, volumes: ['pg'], depends_on: [] },
    ],
  })
  assert.equal(appRole(compose).key, 'website')
  assert.equal(appSourceText(compose), 'Compose file, 2 services')
})

test('sortProjectApps puts public apps first, then background, then data', () => {
  const t = [POSTGRES, REDIS]
  const list = [db('postgres', 'postgres', 'postgres:16', 5432), app('jobs', { type: 'worker', port: null }), db('redis', 'redis', 'redis:7', 6379), app('web')]
  assert.deepEqual(
    sortProjectApps(list, t).map((a) => a.slug),
    ['web', 'jobs', 'redis', 'postgres'],
  )
})

test('mainApp is the oldest public app', () => {
  const pg = db('postgres', 'postgres', 'postgres:16', 5432)
  const web = app('web2')
  assert.equal(mainApp({ apps: [pg, web] })?.slug, 'web2')
  assert.equal(mainApp({ apps: [pg] })?.slug, 'postgres')
  assert.equal(mainApp({ apps: [] }), undefined)
})

test('projectsFromApps makes one project per app without projects', () => {
  const a = app('one')
  const b = app('two', { project: { id: 'prj_1', name: 'Shop' } })
  const c = app('three', { project: { id: 'prj_1', name: 'Shop' } })
  const ps = projectsFromApps([a, b, c])
  assert.equal(ps.length, 2)
  assert.deepEqual(ps.map((p) => [p.id, p.name, p.apps.length]), [
    ['app_one', 'one', 1],
    ['prj_1', 'Shop', 2],
  ])
})

test('projectHealth shows the most urgent status', () => {
  assert.equal(projectHealth([]).status, 'empty')
  assert.deepEqual(projectHealth([{ status: 'running' }, { status: 'failed' }]), { status: 'failed', running: 1, total: 2 })
  assert.equal(projectHealth([{ status: 'running' }, { status: 'deploying' }]).status, 'deploying')
  assert.equal(projectHealth([{ status: 'running' }, { status: 'stopped' }]).status, 'running')
  assert.equal(projectHealth([{ status: 'stopped' }]).status, 'stopped')
})

test('parseReferences reads ${{app.VAR}}', () => {
  assert.deepEqual(parseReferences('${{postgres.DATABASE_URL}}'), [{ app: 'postgres', variable: 'DATABASE_URL', raw: '${{postgres.DATABASE_URL}}' }])
  assert.deepEqual(
    parseReferences('redis://${{ cache.REDIS_HOST }}:${{cache.REDIS_PORT}}/0').map((r) => `${r.app}.${r.variable}`),
    ['cache.REDIS_HOST', 'cache.REDIS_PORT'],
  )
  assert.deepEqual(parseReferences('${VAR} and ${{Bad.NAME}} and ${{-x.Y}}'), [])
  assert.deepEqual(parseReferences(undefined), [])
  assert.equal(referenceText('postgres', 'DATABASE_URL'), '${{postgres.DATABASE_URL}}')
})

test('envReferences reads the references field and falls back to masked', () => {
  assert.equal(envReferences(env('DATABASE_URL', '••••', '${{postgres.DATABASE_URL}}'))[0]?.app, 'postgres')
  assert.deepEqual(
    envReferences(env('URL', '••', 'redis://${{cache.REDIS_HOST}}:${{cache.REDIS_PORT}}')).map((r) => r.variable),
    ['REDIS_HOST', 'REDIS_PORT'],
  )
  assert.equal(envReferences(env('REDIS_URL', '${{redis.REDIS_URL}}'))[0]?.app, 'redis')
  assert.deepEqual(envReferences(env('API_KEY', 'sk-•••')), [])
})

test('projectLinks uses App.references before the variables are loaded', () => {
  const web = app('shop-web', { alias: 'web', references: ['postgres', 'gone'] })
  const pg = db('postgres', 'postgres', 'postgres:18.6', 5432)
  assert.deepEqual(projectLinks([web, pg], {}), [{ from: web.id, to: pg.id, vars: [] }])
  // Once loaded, the variables name the link.
  assert.deepEqual(projectLinks([web, pg], { 'shop-web': [env('DATABASE_URL', '••', '${{postgres.DATABASE_URL}}')] }), [
    { from: web.id, to: pg.id, vars: ['DATABASE_URL'] },
  ])
})

test('projectLinks finds who talks to whom', () => {
  const web = app('shop-web', { alias: 'web' })
  const api = app('shop-api', { alias: 'api' })
  const pg = db('postgres', 'postgres', 'postgres:16', 5432)
  const rd = db('redis', 'redis', 'redis:7', 6379)
  const apps = [web, api, pg, rd]
  const links = projectLinks(apps, {
    'shop-web': [env('API_URL', '••', 'http://${{api.HOST}}'), env('SECRET', '••••')],
    'shop-api': [env('DATABASE_URL', '••', '${{postgres.DATABASE_URL}}'), env('PGHOST', '••', '${{postgres.PGHOST}}'), env('REDIS_URL', '${{redis.REDIS_URL}}')],
    // A reference to an app of another project, or to itself, is no link.
    postgres: [env('X', '${{elsewhere.Y}}'), env('Z', '${{postgres.PGHOST}}')],
  })
  assert.deepEqual(links, [
    { from: web.id, to: api.id, vars: ['API_URL'] },
    { from: api.id, to: pg.id, vars: ['DATABASE_URL', 'PGHOST'] },
    { from: api.id, to: rd.id, vars: ['REDIS_URL'] },
  ])
  assert.deepEqual(usedBy(pg, apps, links).map((a) => a.slug), ['shop-api'])
  assert.deepEqual(talksTo(api, apps, links).map((a) => a.slug), ['postgres', 'redis'])
  // The delete dialog names them with the variables they read.
  assert.deepEqual(dependentsOf(pg, apps, links), [{ slug: 'shop-api', alias: 'api', vars: ['DATABASE_URL', 'PGHOST'] }])
  assert.deepEqual(dependentsOf(web, apps, links), [])
})

test('dependentsFromError reads details.dependents of app_in_use', () => {
  assert.deepEqual(
    dependentsFromError({ dependents: [{ slug: 'shop-web', alias: 'web', vars: ['DATABASE_URL', 7] }, { slug: 'shop-api', vars: null }, { alias: 'nope' }, null] }),
    [
      { slug: 'shop-web', alias: 'web', vars: ['DATABASE_URL'] },
      { slug: 'shop-api', alias: 'shop-api', vars: [] },
    ],
  )
  assert.deepEqual(dependentsFromError(undefined), [])
  assert.deepEqual(dependentsFromError({ dependents: 'web' }), [])
})

test('groupTemplates groups by category and keeps unknown ones', () => {
  const other: ServiceTemplate = { ...POSTGRES, id: 'minio', name: 'MinIO', category: 'storage' }
  assert.deepEqual(groupTemplates([REDIS, POSTGRES, MEILI, other]).map((g) => [g.key, g.items.map((t) => t.id)]), [
    ['database', ['postgres']],
    ['cache', ['redis']],
    ['search', ['meilisearch']],
    ['other', ['minio']],
  ])
})

test('freeAlias picks a short name nobody in the project has', () => {
  assert.equal(freeAlias('postgres', ['web']), 'postgres')
  assert.equal(freeAlias('postgres', ['postgres', 'postgres-2']), 'postgres-3')
  assert.equal(freeAlias('My DB!', []), 'my-db')
})

test('defaultConnect ticks the main public app only', () => {
  const pg = db('postgres', 'postgres', 'postgres:16', 5432)
  assert.deepEqual(defaultConnect({ apps: [app('web9'), pg, app('jobs9', { type: 'worker' })] }), ['web9'])
  assert.deepEqual(defaultConnect({ apps: [pg] }), [])
})

test('pastedKind tells a compose file, an image and a Dockerfile apart', () => {
  assert.equal(pastedKind('  '), 'empty')
  assert.equal(pastedKind('services:\n  web:\n    image: nginx'), 'compose')
  assert.equal(pastedKind('ghcr.io/owner/app:1.2.3'), 'image')
  assert.equal(pastedKind('nginx'), 'image')
  assert.equal(pastedKind('FROM nginx:1.27\nCOPY . /usr/share/nginx/html'), 'dockerfile')
  assert.equal(pastedKind('FROM nginx'), 'dockerfile')
})

test('referenceParts cuts a value into text and references', () => {
  assert.deepEqual(referenceParts('http://${{api.HOST}}:8080'), [
    { text: 'http://' },
    { ref: { app: 'api', variable: 'HOST', raw: '${{api.HOST}}' } },
    { text: ':8080' },
  ])
  assert.deepEqual(referenceParts('plain'), [{ text: 'plain' }])
  assert.deepEqual(referenceParts(''), [])
})

test('templateRole and templateLabel name what a template makes', () => {
  const t = (id: string, category: string, image: string) => ({ id, category, image })
  assert.equal(templateRole(t('postgres', 'database', 'postgres:16')), 'database')
  assert.equal(templateRole(t('redis', 'cache', 'redis:7')), 'cache')
  assert.equal(templateRole(t('meili', 'search', 'getmeili/meilisearch:v1.10')), 'search')
  // An unknown category falls back to the image.
  assert.equal(templateRole(t('kv', 'other', 'valkey/valkey:8')), 'cache')
  assert.equal(templateLabel(t('postgres', 'database', 'postgres:16')), 'Database')
})
