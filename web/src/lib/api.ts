import type {
  AdminUser,
  ApiErrorBody,
  App,
  AuditEntry,
  CreateTokenInput,
  Backup,
  BackupList,
  BackupRestore,
  BackupSettings,
  Deployment,
  Domain,
  EnvVar,
  GitHubStatus,
  Invite,
  Me,
  Metrics,
  MetricWindow,
  Page,
  ServerStats,
  Session,
  SessionInfo,
  Token,
  TokenCreated,
  UpdateAppInput,
  User,
} from './types'

// ---------------------------------------------------------------------------
// All endpoint paths live here so they are easy to adjust.
// ---------------------------------------------------------------------------
const enc = encodeURIComponent
const V1 = '/api/v1'

export const paths = {
  login: `${V1}/auth/login`,
  logout: `${V1}/auth/logout`,
  session: `${V1}/session`,
  acceptInvite: (code: string) => `${V1}/invites/${enc(code)}/accept`,
  me: `${V1}/me`,
  meProfile: `${V1}/me`, // PATCH
  mePassword: `${V1}/me/password`,
  githubToken: `${V1}/me/github-token`,
  sessions: `${V1}/sessions`,
  sessionById: (id: string) => `${V1}/sessions/${enc(id)}`,
  sessionsRevokeOthers: `${V1}/sessions/revoke-others`,
  tokens: `${V1}/tokens`,
  tokenById: (id: string) => `${V1}/tokens/${enc(id)}`,
  apps: `${V1}/apps`,
  app: (slug: string) => `${V1}/apps/${enc(slug)}`,
  appRedeploy: (slug: string) => `${V1}/apps/${enc(slug)}/redeploy`,
  appRestart: (slug: string) => `${V1}/apps/${enc(slug)}/restart`,
  appStart: (slug: string) => `${V1}/apps/${enc(slug)}/start`,
  appStop: (slug: string) => `${V1}/apps/${enc(slug)}/stop`,
  appRollback: (slug: string) => `${V1}/apps/${enc(slug)}/rollback`,
  appDeployments: (slug: string) => `${V1}/apps/${enc(slug)}/deployments`,
  appEnv: (slug: string) => `${V1}/apps/${enc(slug)}/env`,
  appLogs: (slug: string) => `${V1}/apps/${enc(slug)}/logs`,
  appMetrics: (slug: string) => `${V1}/apps/${enc(slug)}/metrics`,
  appDomains: (slug: string) => `${V1}/apps/${enc(slug)}/domains`,
  appDomain: (slug: string, host: string) => `${V1}/apps/${enc(slug)}/domains/${enc(host)}`,
  appDomainCheck: (slug: string, host: string) => `${V1}/apps/${enc(slug)}/domains/${enc(host)}/check`,
  appBackups: (slug: string) => `${V1}/apps/${enc(slug)}/backups`,
  appBackup: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}`,
  appBackupDownload: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}/download`,
  appBackupRestore: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}/restore`,
  appBackupUpload: (slug: string) => `${V1}/apps/${enc(slug)}/backups/upload`,
  appBackupSettings: (slug: string) => `${V1}/apps/${enc(slug)}/backup-settings`,
  deploymentLog: (id: string) => `${V1}/deployments/${enc(id)}/log`,
  adminUsers: `${V1}/admin/users`,
  adminUser: (id: string) => `${V1}/admin/users/${enc(id)}`,
  adminInvites: `${V1}/admin/invites`,
  adminInvite: (id: string) => `${V1}/admin/invites/${enc(id)}`,
  adminServer: `${V1}/admin/server`,
  adminApps: `${V1}/admin/apps`,
  adminAppStop: (slug: string) => `${V1}/admin/apps/${enc(slug)}/stop`,
  adminAudit: `${V1}/admin/audit`,
} as const

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly hint: string
  readonly next: string
  readonly details: unknown

  constructor(status: number, body: Partial<ApiErrorBody['error']>) {
    super(body.message || 'Something went wrong.')
    this.name = 'ApiError'
    this.status = status
    this.code = body.code || 'unknown'
    this.hint = body.hint || ''
    this.next = body.next || ''
    this.details = body.details
  }
}

function fallbackMessage(status: number): string {
  if (status === 0) return 'Cannot reach bitploy.'
  if (status === 401) return 'Please log in again.'
  if (status === 403) return 'You are not allowed to do that.'
  if (status === 404) return 'That does not exist (anymore).'
  if (status === 429) return 'Too many requests. Wait a moment and try again.'
  if (status >= 500) return 'The server had a problem.'
  return 'Something went wrong.'
}

// ---------------------------------------------------------------------------
// Session state (CSRF token lives in memory only)
// ---------------------------------------------------------------------------
let csrfToken = ''
let unauthorizedHandler: (() => void) | null = null

export function setCsrfToken(token: string) {
  csrfToken = token
}

export function onUnauthorized(fn: () => void) {
  unauthorizedHandler = fn
}

// ---------------------------------------------------------------------------
// Core request
// ---------------------------------------------------------------------------
interface RequestOptions {
  body?: unknown
  query?: Record<string, string | number | boolean | undefined | null>
  signal?: AbortSignal
  /** Do not treat 401 as "session expired" (login form, session probe). */
  noRedirect?: boolean
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  if (!query) return path
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
  }
  const s = qs.toString()
  return s ? `${path}?${s}` : path
}

async function send(method: string, path: string, opts: RequestOptions = {}, accept = 'application/json'): Promise<Response> {
  const headers: Record<string, string> = { Accept: accept }
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && method !== 'HEAD' && csrfToken) headers['X-CSRF-Token'] = csrfToken

  let res: Response
  try {
    res = await fetch(buildUrl(path, opts.query), {
      method,
      headers,
      credentials: 'same-origin',
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, {
      code: 'network_error',
      message: 'Cannot reach bitploy.',
      hint: 'Check your internet connection and try again.',
    })
  }

  if (!res.ok) {
    let body: Partial<ApiErrorBody['error']> = {}
    try {
      const json = (await res.json()) as Partial<ApiErrorBody>
      if (json && typeof json === 'object' && json.error) body = json.error
    } catch {
      // not JSON (proxy error page, empty body)
    }
    if (!body.message) body.message = fallbackMessage(res.status)
    if (!body.code) body.code = `http_${res.status}`
    // A wrong current password is also a 401 (invalid_credentials), but the session is fine.
    const sessionGone = (res.status === 401 && body.code !== 'invalid_credentials') || body.code === 'account_suspended'
    if (sessionGone && !opts.noRedirect) unauthorizedHandler?.()
    throw new ApiError(res.status, body)
  }
  return res
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await send(method, path, opts)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new ApiError(res.status, { code: 'bad_response', message: 'The server sent an answer the dashboard could not read.' })
  }
}

/** POSTs a file as the raw request body, with upload progress (fetch cannot report it). */
function uploadFile<T>(path: string, file: File, onProgress?: (fraction: number) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', path)
    xhr.withCredentials = true
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.setRequestHeader('Content-Type', 'application/gzip')
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total)
    }
    xhr.onerror = () =>
      reject(new ApiError(0, { code: 'network_error', message: 'Cannot reach bitploy.', hint: 'Check your internet connection and try again.' }))
    xhr.onload = () => {
      let json: unknown = null
      try {
        json = JSON.parse(xhr.responseText)
      } catch {
        // not JSON (proxy error page, empty body)
      }
      if (xhr.status >= 200 && xhr.status < 300 && json) {
        resolve(json as T)
        return
      }
      const body: Partial<ApiErrorBody['error']> = (json as Partial<ApiErrorBody> | null)?.error ?? {}
      if (!body.message) body.message = xhr.status === 413 ? 'That file is too big.' : fallbackMessage(xhr.status)
      if (!body.code) body.code = `http_${xhr.status}`
      if (xhr.status === 401 || body.code === 'account_suspended') unauthorizedHandler?.()
      reject(new ApiError(xhr.status, body))
    }
    xhr.send(file)
  })
}

/** Accepts `[...]`, `{items:[...]}` or `{<key>:[...]}` and always returns an array. */
function listOf<T>(data: unknown, key?: string): T[] {
  if (Array.isArray(data)) return data as T[]
  if (data && typeof data === 'object') {
    const o = data as Record<string, unknown>
    if (Array.isArray(o.items)) return o.items as T[]
    if (key && Array.isArray(o[key])) return o[key] as T[]
  }
  return []
}

/** Follows next_cursor so lists are not silently cut off at one page. */
async function listAll<T>(path: string, query: RequestOptions['query'] = {}, key?: string): Promise<T[]> {
  const out: T[] = []
  let cursor: string | undefined
  for (let i = 0; i < 20; i++) {
    const data = await request<unknown>('GET', path, { query: { ...query, limit: 100, cursor } })
    out.push(...listOf<T>(data, key))
    const next = data && typeof data === 'object' ? (data as { next_cursor?: unknown }).next_cursor : undefined
    if (typeof next !== 'string' || !next) break
    cursor = next
  }
  return out
}

// ---------------------------------------------------------------------------
// Typed endpoints
// ---------------------------------------------------------------------------
export const api = {
  // auth
  async session(): Promise<SessionInfo> {
    const s = await request<SessionInfo>('GET', paths.session, { noRedirect: true })
    setCsrfToken(s.csrf_token)
    return s
  },
  async login(email: string, password: string): Promise<void> {
    await request('POST', paths.login, { body: { email, password }, noRedirect: true })
  },
  async logout(): Promise<void> {
    await request('POST', paths.logout, { noRedirect: true })
    setCsrfToken('')
  },
  acceptInvite(code: string, input: { name: string; email: string; password: string }): Promise<unknown> {
    return request('POST', paths.acceptInvite(code), { body: input, noRedirect: true })
  },

  // account
  me: () => request<Me>('GET', paths.me),
  updateProfile: (input: { name?: string; email?: string; current_password?: string }) => request<User>('PATCH', paths.meProfile, { body: input }),
  changePassword: (input: { current_password: string; new_password: string }) =>
    request<void>('PUT', paths.mePassword, { body: input }),
  githubStatus: () => request<GitHubStatus>('GET', paths.githubToken),
  saveGithubToken: (token: string) => request<GitHubStatus>('PUT', paths.githubToken, { body: { token } }),
  removeGithubToken: () => request<void>('DELETE', paths.githubToken),
  async sessions(): Promise<Session[]> {
    return listAll<Session>(paths.sessions)
  },
  revokeSession: (id: string) => request<void>('DELETE', paths.sessionById(id)),
  revokeOtherSessions: () => request<void>('POST', paths.sessionsRevokeOthers),

  // tokens
  async tokens(): Promise<Token[]> {
    return listAll<Token>(paths.tokens)
  },
  createToken: (input: CreateTokenInput) => request<TokenCreated>('POST', paths.tokens, { body: input }),
  revokeToken: (id: string) => request<void>('DELETE', paths.tokenById(id)),

  // apps
  async apps(): Promise<App[]> {
    return listAll<App>(paths.apps)
  },
  app: (slug: string) => request<App>('GET', paths.app(slug)),
  updateApp: (slug: string, input: UpdateAppInput) => request<App>('PATCH', paths.app(slug), { body: input }),
  deleteApp: (slug: string) =>
    request<void>('DELETE', paths.app(slug), { query: { confirm_slug: slug }, body: { confirm_slug: slug } }),
  redeploy: (slug: string) => request<Deployment>('POST', paths.appRedeploy(slug)),
  restart: (slug: string) => request<App>('POST', paths.appRestart(slug)),
  start: (slug: string) => request<App>('POST', paths.appStart(slug)),
  stop: (slug: string) => request<App>('POST', paths.appStop(slug)),
  rollback: (slug: string, deploymentId: string) =>
    request<Deployment>('POST', paths.appRollback(slug), { body: { deployment_id: deploymentId } }),
  async deployments(slug: string): Promise<Deployment[]> {
    return listOf<Deployment>(await request<unknown>('GET', paths.appDeployments(slug), { query: { limit: 50 } }))
  },
  async env(slug: string): Promise<EnvVar[]> {
    return listAll<EnvVar>(paths.appEnv(slug), {}, 'env')
  },
  setEnv: (slug: string, set: Record<string, string>, unset: string[]) =>
    request<unknown>('PUT', paths.appEnv(slug), { body: { set, unset } }),
  metrics: (slug: string, window: MetricWindow) =>
    request<Metrics>('GET', paths.appMetrics(slug), { query: { window } }),

  // domains
  async domains(slug: string): Promise<Domain[]> {
    return listAll<Domain>(paths.appDomains(slug), {}, 'domains')
  },
  addDomain: (slug: string, hostname: string) =>
    request<Domain>('POST', paths.appDomains(slug), { body: { hostname } }),
  checkDomain: (slug: string, host: string) => request<Domain>('POST', paths.appDomainCheck(slug, host)),
  removeDomain: (slug: string, host: string) => request<void>('DELETE', paths.appDomain(slug, host)),

  // backups of /data
  backups: (slug: string) => request<BackupList>('GET', paths.appBackups(slug)),
  createBackup: (slug: string) => request<Backup>('POST', paths.appBackups(slug)),
  deleteBackup: (slug: string, id: string) => request<void>('DELETE', paths.appBackup(slug, id)),
  restoreBackup: (slug: string, id: string) => request<BackupRestore>('POST', paths.appBackupRestore(slug, id)),
  updateBackupSettings: (slug: string, input: Partial<Pick<BackupSettings, 'schedule' | 'keep_mode' | 'keep'>>) =>
    request<BackupSettings>('PUT', paths.appBackupSettings(slug), { body: input }),
  uploadBackup: (slug: string, file: File, onProgress?: (fraction: number) => void) =>
    uploadFile<Backup>(paths.appBackupUpload(slug), file, onProgress),

  // admin
  async adminUsers(): Promise<AdminUser[]> {
    return listAll<AdminUser>(paths.adminUsers)
  },
  updateUser: (id: string, input: { status?: 'active' | 'suspended'; quota?: Partial<AdminUser['quota']> }) =>
    request<AdminUser>('PATCH', paths.adminUser(id), { body: input }),
  async invites(): Promise<Invite[]> {
    return listAll<Invite>(paths.adminInvites)
  },
  createInvite: (input: { email?: string; role?: string }) => request<Invite>('POST', paths.adminInvites, { body: input }),
  revokeInvite: (id: string) => request<void>('DELETE', paths.adminInvite(id)),
  adminServer: () => request<ServerStats>('GET', paths.adminServer),
  async adminApps(): Promise<App[]> {
    return listAll<App>(paths.adminApps)
  },
  adminStopApp: (slug: string) => request<unknown>('POST', paths.adminAppStop(slug)),
  audit: (cursor?: string) =>
    request<Page<AuditEntry>>('GET', paths.adminAudit, { query: { limit: 50, cursor } }),
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------
export type LogKind = 'build' | 'runtime'

export interface LogTarget {
  slug: string
  kind: LogKind
  /** Deployment whose build log to show (build logs only). */
  deploymentId?: string
}

export function logPath(t: LogTarget): { path: string; query: Record<string, string | number> } {
  if (t.kind === 'build' && t.deploymentId) return { path: paths.deploymentLog(t.deploymentId), query: {} }
  return { path: paths.appLogs(t.slug), query: { kind: t.kind, tail: 500 } }
}

/** One log line from either a plain text line or a JSON LogLine. */
export function parseLogLine(raw: string): string {
  const s = raw.replace(/\r$/, '')
  if (s.startsWith('{') && s.endsWith('}')) {
    try {
      const j = JSON.parse(s) as { text?: unknown }
      if (typeof j.text === 'string') return j.text
    } catch {
      // plain text that happens to look like JSON
    }
  }
  return s
}

/** Fetch a snapshot of the log. Accepts text, a JSON array or {items|lines:[...]}. */
export async function fetchLogLines(t: LogTarget, signal?: AbortSignal): Promise<string[]> {
  const { path, query } = logPath(t)
  const res = await send('GET', path, { query, signal }, 'application/json, text/plain')
  const type = res.headers.get('content-type') || ''
  const text = await res.text()
  if (type.includes('json')) {
    try {
      const data = JSON.parse(text) as unknown
      const arr = listOf<unknown>(data, 'lines')
      return arr.map((l) => (typeof l === 'string' ? l : String((l as { text?: string }).text ?? '')))
    } catch {
      // fall through to plain text
    }
  }
  return text.split('\n').filter((l, i, a) => l !== '' || i < a.length - 1).map(parseLogLine)
}

/** URL for the live tail (Server-Sent Events). */
export function logStreamUrl(t: LogTarget): string {
  const { path, query } = logPath(t)
  return buildUrl(path, { ...query, follow: 1 })
}
