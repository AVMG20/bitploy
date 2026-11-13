import type {
  AddServiceInput,
  Builds,
  AdminProjectInvite,
  AdminUser,
  NewAccountQuota,
  ServerSettings,
  SignupMode,
  SignupRules,
  AppDomain,
  AppDomainList,
  ApiErrorBody,
  App,
  ActivityEntry,
  AuditEntry,
  CreateAppInput,
  CreateTokenInput,
  Backup,
  ComposeCheck,
  BackupList,
  BackupRestore,
  BackupSettings,
  DataFile,
  DataFileList,
  DbKey,
  DbRow,
  DbRows,
  DbRowsQuery,
  DbSchema,
  DbSqlResponse,
  DbValue,
  AppDockerfile,
  CatalogApp,
  CatalogDeployInput,
  CatalogDeployResult,
  Deployment,
  DockerfileCheck,
  Domain,
  EnvVar,
  GitHubBranches,
  GitHubManifest,
  GitHubOverview,
  GitHubRepo,
  GitHubAppInfo,
  GitSourceInput,
  Invite,
  Me,
  Metrics,
  MetricWindow,
  NotificationChannel,
  NotificationOptions,
  CreateNotificationChannelInput,
  UpdateNotificationChannelInput,
  Traffic,
  TrafficListName,
  TrafficWindow,
  Page,
  Project,
  PasswordReset,
  PasswordResetInfo,
  ProjectInvite,
  ProjectInviteInfo,
  ProjectMember,
  ProjectSignup,
  PublicConnection,
  PublicPortInput,
  PublicPortsOverview,
  DiskAutoCleanup,
  DiskAutoInput,
  DiskCleanupRun,
  DiskCleanupKind,
  DiskCleanupResult,
  DiskReport,
  JobList,
  ServerMetrics,
  ServerStats,
  ServiceTemplate,
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
  passwordReset: (code: string) => `${V1}/password-resets/${enc(code)}`,
  usePasswordReset: (code: string) => `${V1}/password-resets/${enc(code)}/use`,
  signupRules: `${V1}/signup-rules`,
  me: `${V1}/me`,
  meProfile: `${V1}/me`, // PATCH
  mePassword: `${V1}/me/password`,
  github: `${V1}/github`,
  githubConnect: `${V1}/github/connect`,
  githubLink: `${V1}/github/link`,
  githubInstallation: (id: number) => `${V1}/github/installations/${id}`,
  githubRepositories: `${V1}/github/repositories`,
  githubBranches: `${V1}/github/branches`,
  adminGithub: `${V1}/admin/github`,
  adminGithubManifest: `${V1}/admin/github/manifest`,
  adminGithubComplete: `${V1}/admin/github/manifest/complete`,
  appGitSource: (slug: string) => `${V1}/apps/${enc(slug)}/git-source`,
  appPasswordProtection: (slug: string) => `${V1}/apps/${enc(slug)}/password-protection`,
  appPublicPort: (slug: string) => `${V1}/apps/${enc(slug)}/public-port`,
  appPublicPortReveal: (slug: string) => `${V1}/apps/${enc(slug)}/public-port/reveal`,
  sessions: `${V1}/sessions`,
  sessionById: (id: string) => `${V1}/sessions/${enc(id)}`,
  sessionsRevokeOthers: `${V1}/sessions/revoke-others`,
  tokens: `${V1}/tokens`,
  tokenById: (id: string) => `${V1}/tokens/${enc(id)}`,
  notificationOptions: `${V1}/notifications/options`,
  notificationChannels: `${V1}/notification-channels`,
  notificationChannel: (id: string) => `${V1}/notification-channels/${enc(id)}`,
  notificationChannelTest: (id: string) => `${V1}/notification-channels/${enc(id)}/test`,
  apps: `${V1}/apps`,
  projects: `${V1}/projects`,
  projectOrder: `${V1}/project-order`,
  project: (id: string) => `${V1}/projects/${enc(id)}`,
  projectServices: (id: string) => `${V1}/projects/${enc(id)}/services`,
  projectMembers: (id: string) => `${V1}/projects/${enc(id)}/members`,
  projectMember: (id: string, user: string) => `${V1}/projects/${enc(id)}/members/${enc(user)}`,
  projectInvites: (id: string) => `${V1}/projects/${enc(id)}/invites`,
  projectInvite: (id: string, invite: string) => `${V1}/projects/${enc(id)}/invites/${enc(invite)}`,
  projectInviteInfo: (code: string) => `${V1}/project-invites/${enc(code)}`,
  acceptProjectInvite: (code: string) => `${V1}/project-invites/${enc(code)}/accept`,
  signupProjectInvite: (code: string) => `${V1}/project-invites/${enc(code)}/signup`,
  serviceTemplates: `${V1}/service-templates`,
  app: (slug: string) => `${V1}/apps/${enc(slug)}`,
  appUpload: (slug: string) => `${V1}/apps/${enc(slug)}/upload`,
  appRedeploy: (slug: string) => `${V1}/apps/${enc(slug)}/redeploy`,
  appRestart: (slug: string) => `${V1}/apps/${enc(slug)}/restart`,
  appStart: (slug: string) => `${V1}/apps/${enc(slug)}/start`,
  appStop: (slug: string) => `${V1}/apps/${enc(slug)}/stop`,
  appRollback: (slug: string) => `${V1}/apps/${enc(slug)}/rollback`,
  appDeployments: (slug: string) => `${V1}/apps/${enc(slug)}/deployments`,
  appEnv: (slug: string) => `${V1}/apps/${enc(slug)}/env`,
  appLogs: (slug: string) => `${V1}/apps/${enc(slug)}/logs`,
  appMetrics: (slug: string) => `${V1}/apps/${enc(slug)}/metrics`,
  appTraffic: (slug: string) => `${V1}/apps/${enc(slug)}/traffic`,
  appDomains: (slug: string) => `${V1}/apps/${enc(slug)}/domains`,
  appDomain: (slug: string, host: string) => `${V1}/apps/${enc(slug)}/domains/${enc(host)}`,
  appDomainCheck: (slug: string, host: string) => `${V1}/apps/${enc(slug)}/domains/${enc(host)}/check`,
  appBackups: (slug: string) => `${V1}/apps/${enc(slug)}/backups`,
  appBackup: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}`,
  appBackupDownload: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}/download`,
  appBackupRestore: (slug: string, id: string) => `${V1}/apps/${enc(slug)}/backups/${enc(id)}/restore`,
  appBackupUpload: (slug: string) => `${V1}/apps/${enc(slug)}/backups/upload`,
  appBackupSettings: (slug: string) => `${V1}/apps/${enc(slug)}/backup-settings`,
  appBackupRestoreState: (slug: string) => `${V1}/apps/${enc(slug)}/backup-restore`,
  appDockerfile: (slug: string) => `${V1}/apps/${enc(slug)}/dockerfile`,
  appCatalogUpdate: (slug: string) => `${V1}/apps/${enc(slug)}/catalog-update`,
  catalog: `${V1}/catalog`,
  catalogApp: (id: string) => `${V1}/catalog/${enc(id)}`,
  catalogDeploy: (id: string) => `${V1}/catalog/${enc(id)}/deploy`,
  dockerfileValidate: `${V1}/dockerfile/validate`,
  composeValidate: `${V1}/compose/validate`,
  appFiles: (slug: string) => `${V1}/apps/${enc(slug)}/files`,
  appFileFolder: (slug: string) => `${V1}/apps/${enc(slug)}/files/folder`,
  appFileDownload: (slug: string) => `${V1}/apps/${enc(slug)}/files/download`,
  appFileUpload: (slug: string) => `${V1}/apps/${enc(slug)}/files/upload`,
  appDb: (slug: string) => `${V1}/apps/${enc(slug)}/db`,
  appDbRows: (slug: string) => `${V1}/apps/${enc(slug)}/db/rows`,
  appDbInsert: (slug: string) => `${V1}/apps/${enc(slug)}/db/insert`,
  appDbUpdate: (slug: string) => `${V1}/apps/${enc(slug)}/db/update`,
  adminDb: `${V1}/admin/db`,
  adminDbRows: `${V1}/admin/db/rows`,
  adminDbSql: `${V1}/admin/db/sql`,
  appDbCell: (slug: string) => `${V1}/apps/${enc(slug)}/db/cell`,
  appDbUpdateMany: (slug: string) => `${V1}/apps/${enc(slug)}/db/update-many`,
  appDbDelete: (slug: string) => `${V1}/apps/${enc(slug)}/db/delete`,
  appDbSql: (slug: string) => `${V1}/apps/${enc(slug)}/db/sql`,
  builds: `${V1}/builds`,
  deploymentCancel: (id: string) => `${V1}/deployments/${enc(id)}/cancel`,
  deploymentLog: (id: string) => `${V1}/deployments/${enc(id)}/log`,
  adminUsers: `${V1}/admin/users`,
  adminUser: (id: string) => `${V1}/admin/users/${enc(id)}`,
  adminInvites: `${V1}/admin/invites`,
  adminPasswordReset: (id: string) => `${V1}/admin/users/${enc(id)}/password-reset`,
  adminInvite: (id: string) => `${V1}/admin/invites/${enc(id)}`,
  adminProjectInvites: `${V1}/admin/project-invites`,
  adminProjectInvite: (id: string) => `${V1}/admin/project-invites/${enc(id)}`,
  adminSettings: `${V1}/admin/settings`,
  adminServer: `${V1}/admin/server`,
  adminServerMetrics: `${V1}/admin/server/metrics`,
  adminDisk: `${V1}/admin/disk`,
  adminDiskCleanup: `${V1}/admin/disk/cleanup`,
  adminDiskAuto: `${V1}/admin/disk/auto`,
  adminDiskAutoRun: `${V1}/admin/disk/auto/run`,
  adminJobs: `${V1}/admin/jobs`,
  adminApps: `${V1}/admin/apps`,
  adminPublicPorts: `${V1}/admin/public-ports`,
  adminPublicPort: (slug: string) => `${V1}/admin/public-ports/${enc(slug)}`,
  adminAppStop: (slug: string) => `${V1}/admin/apps/${enc(slug)}/stop`,
  adminAudit: `${V1}/admin/audit`,
  activity: `${V1}/activity`,
  adminAppDomains: `${V1}/admin/app-domains`,
  adminAppDomain: (id: string) => `${V1}/admin/app-domains/${enc(id)}`,
  adminAppDomainCert: (id: string) => `${V1}/admin/app-domains/${enc(id)}/certificate`,
  adminUserAppDomains: (id: string) => `${V1}/admin/users/${enc(id)}/app-domains`,
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
  if (status === 401) return 'Log in again.'
  if (status === 403) return 'You are not allowed to do that.'
  if (status === 404) return 'That does not exist. It may have been deleted.'
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
    // The framework's own complaints (an unexpected field, a bad type) are for developers: say it plainly, keep the details in the console.
    if (['invalid_request', 'unsupported_media_type', 'method_not_allowed'].includes(body.code)) {
      console.error('bitploy API refused a request:', res.status, body.code, body.message)
      body.message = 'Something went wrong talking to the server.'
      body.hint = 'Reload the page and try again.'
    }
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
function uploadFile<T>(
  path: string,
  file: Blob,
  onProgress?: (fraction: number) => void,
  onSent?: () => void,
  contentType = 'application/gzip',
  signal?: AbortSignal,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', path)
    xhr.withCredentials = true
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.setRequestHeader('Content-Type', contentType)
    if (csrfToken) xhr.setRequestHeader('X-CSRF-Token', csrfToken)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total)
    }
    // Everything has been sent: the server may still need a while to check it.
    xhr.upload.onload = () => onSent?.()
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
    xhr.onabort = () => reject(new DOMException('Upload cancelled', 'AbortError'))
    signal?.addEventListener('abort', () => xhr.abort(), { once: true })
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

/** The server does not know this endpoint (an older bitploy). */
function missingEndpoint(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 404 || e.status === 405 || e.status === 501)
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
  /** Who can make an account here (no login needed). */
  signupRules: () => request<SignupRules>('GET', paths.signupRules, { noRedirect: true }),
  acceptInvite(code: string, input: { name: string; email: string; password: string }): Promise<unknown> {
    return request('POST', paths.acceptInvite(code), { body: input, noRedirect: true })
  },
  /** Whose account a password reset link is for; works without a login. */
  passwordResetInfo: (code: string) => request<PasswordResetInfo>('GET', paths.passwordReset(code), { noRedirect: true }),
  /** Sets the new password, signs out every other session and logs in. */
  async usePasswordReset(code: string, password: string): Promise<SessionInfo> {
    const s = await request<SessionInfo>('POST', paths.usePasswordReset(code), { body: { password }, noRedirect: true })
    setCsrfToken(s.csrf_token)
    return s
  },

  // account
  me: () => request<Me>('GET', paths.me),
  updateProfile: (input: { name?: string; email?: string; current_password?: string }) => request<User>('PATCH', paths.meProfile, { body: input }),
  changePassword: (input: { current_password: string; new_password: string }) =>
    request<void>('PUT', paths.mePassword, { body: input }),

  // GitHub
  github: () => request<GitHubOverview>('GET', paths.github),
  githubConnect: (mode: 'install' | 'authorize' = 'install') => request<{ url: string }>('POST', paths.githubConnect, { body: { mode } }),
  githubLink: (input: { installation_id?: number; code: string; state: string }) => request<GitHubOverview>('POST', paths.githubLink, { body: input }),
  githubUnlink: (id: number) => request<void>('DELETE', paths.githubInstallation(id)),
  githubRepos: async (query: string) => listOf<GitHubRepo>(await request<unknown>('GET', paths.githubRepositories, { query: { query } })),
  githubBranches: (repo: string) => request<GitHubBranches>('GET', paths.githubBranches, { query: { repo } }),
  adminGithub: () => request<GitHubAppInfo>('GET', paths.adminGithub),
  adminGithubManifest: (input: { name?: string; organization?: string }) => request<GitHubManifest>('POST', paths.adminGithubManifest, { body: input }),
  adminGithubComplete: (input: { code: string; state: string }) => request<GitHubAppInfo>('POST', paths.adminGithubComplete, { body: input }),
  adminGithubDisconnect: () => request<void>('DELETE', paths.adminGithub),
  setGitSource: (slug: string, input: GitSourceInput) => request<App>('PUT', paths.appGitSource(slug), { body: input }),
  clearGitSource: (slug: string) => request<App>('DELETE', paths.appGitSource(slug)),
  setPasswordProtection: (slug: string, input: { enabled: boolean; username?: string; password?: string }) =>
    request<App>('PUT', paths.appPasswordProtection(slug), { body: input }),
  setPublicPort: (slug: string, input: PublicPortInput) => request<App>('PUT', paths.appPublicPort(slug), { body: input }),
  revealPublicConnection: (slug: string) => request<PublicConnection>('POST', paths.appPublicPortReveal(slug)),
  setAutoDeploy: (slug: string, autoDeploy: boolean) => request<App>('PATCH', paths.appGitSource(slug), { body: { auto_deploy: autoDeploy } }),
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

  // notification channels of the signed-in user
  notificationOptions: () => request<NotificationOptions>('GET', paths.notificationOptions),
  async notificationChannels(): Promise<NotificationChannel[]> {
    return listAll<NotificationChannel>(paths.notificationChannels)
  },
  createNotificationChannel: (input: CreateNotificationChannelInput) =>
    request<NotificationChannel>('POST', paths.notificationChannels, { body: input }),
  updateNotificationChannel: (id: string, input: UpdateNotificationChannelInput) =>
    request<NotificationChannel>('PATCH', paths.notificationChannel(id), { body: input }),
  deleteNotificationChannel: (id: string) => request<void>('DELETE', paths.notificationChannel(id)),
  /** Sends a test message now; rejects with the reason (422 notification_test_failed). */
  testNotificationChannel: (id: string) => request<NotificationChannel>('POST', paths.notificationChannelTest(id)),

  // projects (phase 4b): spaces that hold apps
  /** null when this server has no projects yet (the endpoint is missing); the dashboard then makes one per app. */
  async projects(): Promise<Project[] | null> {
    try {
      return (await listAll<Project>(paths.projects)).map((p) => ({ ...p, apps: p.apps ?? [] }))
    } catch (e) {
      if (missingEndpoint(e)) return null
      throw e
    }
  },
  /** Saves the order the caller sees their projects in, top first. */
  orderProjects: (ids: string[]) => request<void>('PUT', paths.projectOrder, { body: { ids } }),
  project: (id: string) => request<Project>('GET', paths.project(id)),
  createProject: (input: { name: string }) => request<Project>('POST', paths.projects, { body: input }),
  updateProject: (id: string, input: { name: string }) => request<Project>('PATCH', paths.project(id), { body: input }),
  /** Refused while the project has apps, unless withApps (which deletes them too). */
  deleteProject: (id: string, name: string, withApps = false) =>
    request<void>('DELETE', paths.project(id), { query: { confirm: name, with_apps: withApps || undefined } }),
  /** The databases and other ready services; null when this server has none (the endpoint is missing). */
  async serviceTemplates(): Promise<ServiceTemplate[] | null> {
    try {
      return await listAll<ServiceTemplate>(paths.serviceTemplates)
    } catch (e) {
      if (missingEndpoint(e)) return null
      throw e
    }
  },
  // members of a project: the owner shares it with invite links
  projectMembers: (id: string) => listAll<ProjectMember>(paths.projectMembers(id)),
  /** The owner removes a member; user "me" leaves the project. */
  removeProjectMember: (id: string, user: string) => request<void>('DELETE', paths.projectMember(id, user)),
  projectInvites: (id: string) => listAll<ProjectInvite>(paths.projectInvites(id)),
  createProjectInvite: (id: string, input: { email?: string }) => request<ProjectInvite>('POST', paths.projectInvites(id), { body: input }),
  revokeProjectInvite: (id: string, invite: string) => request<void>('DELETE', paths.projectInvite(id, invite)),
  /** What an invite link is for; works without a login. */
  projectInviteInfo: (code: string) => request<ProjectInviteInfo>('GET', paths.projectInviteInfo(code), { noRedirect: true }),
  acceptProjectInvite: (code: string) => request<Project>('POST', paths.acceptProjectInvite(code)),
  /** Makes the account, joins the project and logs in. */
  async signupProjectInvite(code: string, input: { name: string; email: string; password: string }): Promise<ProjectSignup> {
    const out = await request<ProjectSignup>('POST', paths.signupProjectInvite(code), { body: input, noRedirect: true })
    setCsrfToken(out.csrf_token)
    return out
  },
  /** Makes an app from a template in the project, wires "Connect to" and deploys it. */
  addService: (projectId: string, input: AddServiceInput) => request<App>('POST', paths.projectServices(projectId), { body: input }),

  // apps
  async apps(): Promise<App[]> {
    return listAll<App>(paths.apps)
  },
  app: (slug: string) => request<App>('GET', paths.app(slug)),
  createApp: (input: CreateAppInput) => request<App>('POST', paths.apps, { body: input }),
  /** Stores a zip and queues a deployment from it (the app must not be linked to git). */
  uploadApp: (slug: string, zip: Blob, onProgress?: (fraction: number) => void, onSent?: () => void, signal?: AbortSignal) =>
    uploadFile<Deployment>(paths.appUpload(slug), zip, onProgress, onSent, 'application/zip', signal),
  /** Queues a deployment from the app's git source. */
  deployGit: (slug: string) => request<Deployment>('POST', paths.appDeployments(slug), { body: {} }),
  updateApp: (slug: string, input: UpdateAppInput) => request<App>('PATCH', paths.app(slug), { body: input }),
  deleteApp: (slug: string) =>
    request<void>('DELETE', paths.app(slug), { query: { confirm_slug: slug }, body: { confirm_slug: slug } }),
  /** Stores inline files as the app's source and queues a deployment. */
  deployFiles: (slug: string, files: { path: string; content: string; encoding?: 'utf8' | 'base64' }[]) =>
    request<Deployment>('POST', paths.appFiles(slug), { body: { files } }),
  dockerfile: (slug: string) => request<AppDockerfile>('GET', paths.appDockerfile(slug)),
  saveDockerfile: (slug: string, content: string) => request<Deployment>('PUT', paths.appDockerfile(slug), { body: { content } }),
  validateDockerfile: (dockerfile: string, port?: number) =>
    request<DockerfileCheck>('POST', paths.dockerfileValidate, { body: { dockerfile, port } }),
  /**
   * What bitploy would make of a compose file: its services, storage and memory.
   * null when this server cannot check compose files (no such endpoint yet). A
   * refusal (4xx with an error code) comes back as a check with that one error.
   */
  async validateCompose(compose: string): Promise<ComposeCheck | null> {
    try {
      return await request<ComposeCheck>('POST', paths.composeValidate, { body: { compose } })
    } catch (e) {
      if (missingEndpoint(e)) return null
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 401 && e.status !== 429) {
        const d = e.details as { service?: string; key?: string } | undefined
        return {
          ok: false,
          services: [],
          volumes: [],
          memory_mb_total: 0,
          cpu_millis_total: 0,
          warnings: [],
          errors: [{ code: e.code, message: e.message, hint: e.hint, service: d?.service, key: d?.key }],
        }
      }
      throw e
    }
  },
  async catalog(): Promise<CatalogApp[]> {
    return listAll<CatalogApp>(paths.catalog)
  },
  catalogApp: (id: string) => request<CatalogApp>('GET', paths.catalogApp(id)),
  deployCatalog: (id: string, input: CatalogDeployInput) => request<CatalogDeployResult>('POST', paths.catalogDeploy(id), { body: input }),
  catalogUpdate: (slug: string) => request<Deployment>('POST', paths.appCatalogUpdate(slug)),
  redeploy: (slug: string) => request<Deployment>('POST', paths.appRedeploy(slug)),
  /** With a service, only that service of a project with several restarts. */
  restart: (slug: string, service?: string) => request<App>('POST', paths.appRestart(slug), { query: { service } }),
  start: (slug: string) => request<App>('POST', paths.appStart(slug)),
  stop: (slug: string) => request<App>('POST', paths.appStop(slug)),
  rollback: (slug: string, deploymentId: string) =>
    request<Deployment>('POST', paths.appRollback(slug), { body: { deployment_id: deploymentId } }),
  cancelDeployment: (id: string) => request<Deployment>('POST', paths.deploymentCancel(id)),
  async deployments(slug: string): Promise<Deployment[]> {
    return listOf<Deployment>(await request<unknown>('GET', paths.appDeployments(slug), { query: { limit: 50 } }))
  },
  async env(slug: string): Promise<EnvVar[]> {
    return listAll<EnvVar>(paths.appEnv(slug), {}, 'env')
  },
  /**
   * With a service, the variables are only for that service of a project with
   * several; without one they are shared by every service. `service` in the body
   * is an assumption about the compose phase 2 API: it is only sent when set, so
   * a server without it answers with an error instead of saving a shared value.
   * `hidden` is the flag per name; a name left out keeps its flag (a new one
   * is readable).
   */
  setEnv: (slug: string, set: Record<string, string>, unset: string[], service?: string, hidden?: Record<string, boolean>) =>
    request<unknown>('PUT', paths.appEnv(slug), { body: { set, unset, ...(service ? { service } : {}), ...(hidden && Object.keys(hidden).length ? { hidden } : {}) } }),
  /** Without a service: the sum over all services. */
  metrics: (slug: string, window: MetricWindow, service?: string) =>
    request<Metrics>('GET', paths.appMetrics(slug), { query: { window, service } }),
  traffic: (slug: string, window: TrafficWindow | 'today') =>
    request<Traffic>('GET', paths.appTraffic(slug), { query: { window } }),
  /** One list in full (up to 500 rows); the rest of the answer is empty. */
  trafficList: (slug: string, window: TrafficWindow, list: TrafficListName) =>
    request<Traffic>('GET', paths.appTraffic(slug), { query: { window, list } }),

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
  /** volume: one volume of a project with several; default the first. */
  createBackup: (slug: string, volume?: string) => request<Backup>('POST', paths.appBackups(slug), { query: { volume } }),
  deleteBackup: (slug: string, id: string) => request<void>('DELETE', paths.appBackup(slug, id)),
  restoreBackup: (slug: string, id: string) => request<BackupRestore>('POST', paths.appBackupRestore(slug, id)),
  updateBackupSettings: (slug: string, input: Partial<Pick<BackupSettings, 'schedule' | 'keep_mode' | 'keep'>>) =>
    request<BackupSettings>('PUT', paths.appBackupSettings(slug), { body: input }),
  dismissBackupRestore: (slug: string) => request<void>('DELETE', paths.appBackupRestoreState(slug)),
  uploadBackup: (slug: string, file: File, onProgress?: (fraction: number) => void, onSent?: () => void, volume?: string) =>
    uploadFile<Backup>(buildUrl(paths.appBackupUpload(slug), { volume }), file, onProgress, onSent),

  // files in /data (or in one volume of a project with several; default the first)
  files: (slug: string, path: string, volume?: string) => request<DataFileList>('GET', paths.appFiles(slug), { query: { path, volume } }),
  makeFolder: (slug: string, path: string, name: string, volume?: string) =>
    request<DataFile>('POST', paths.appFileFolder(slug), { body: { path, name }, query: { volume } }),
  deleteDataFile: (slug: string, path: string, volume?: string) => request<void>('DELETE', paths.appFiles(slug), { query: { path, volume } }),
  /** A plain link: the browser downloads it with the session cookie. */
  dataFileDownloadUrl: (slug: string, path: string, volume?: string) => buildUrl(paths.appFileDownload(slug), { path, volume }),
  uploadDataFile: (slug: string, folder: string, file: File, overwrite: boolean, onProgress?: (fraction: number) => void, onSent?: () => void, volume?: string) =>
    uploadFile<DataFile>(
      buildUrl(paths.appFileUpload(slug), { path: folder, name: file.name, overwrite: overwrite ? 'true' : undefined, volume }),
      file,
      onProgress,
      onSent,
      'application/octet-stream',
    ),

  // SQLite databases in /data
  dbSchema: (slug: string, path: string) => request<DbSchema>('GET', paths.appDb(slug), { query: { path } }),
  dbRows: (slug: string, path: string, q: DbRowsQuery) => request<DbRows>('POST', paths.appDbRows(slug), { body: { path, ...q } }),
  dbInsert: (slug: string, path: string, table: string, values: Record<string, DbValue>) =>
    request<{ key: DbKey | null; row: DbRow }>('POST', paths.appDbInsert(slug), { body: { path, table, values } }),
  /** `match`: the row as it was loaded; the server refuses (row_changed) when it is different now. */
  dbUpdate: (slug: string, path: string, table: string, key: DbKey, values: Record<string, DbValue>, match?: Record<string, DbValue>) =>
    request<{ row: DbRow }>('POST', paths.appDbUpdate(slug), { body: { path, table, key, values, match } }),
  /** The whole value of one cell: a text that the grid shows cut. A string up to 4 MiB; longer ones and BLOBs come back in their wrapper shapes. */
  dbCell: (slug: string, path: string, table: string, key: DbKey, column: string) =>
    request<{ value: DbValue }>('POST', paths.appDbCell(slug), { body: { path, table, key, column } }),
  /** The same values on many rows. A row that is gone is skipped (`updated` can be lower); a changed one refuses everything (row_changed, details.index). */
  dbUpdateMany: (slug: string, path: string, table: string, keys: DbKey[], values: Record<string, DbValue>, match?: Record<string, DbValue>[]) =>
    request<{ updated: number }>('POST', paths.appDbUpdateMany(slug), { body: { path, table, keys, values, match } }),
  dbDelete: (slug: string, path: string, table: string, keys: DbKey[], match?: Record<string, DbValue>[]) =>
    request<{ deleted: number }>('POST', paths.appDbDelete(slug), { body: { path, table, keys, match } }),
  dbSql: (slug: string, path: string, sql: string, write: boolean) =>
    request<DbSqlResponse>('POST', paths.appDbSql(slug), { body: { path, sql, write } }),

  // bitploy's own database (admin, read-only)
  adminDbSchema: () => request<DbSchema>('GET', paths.adminDb),
  adminDbRows: (q: DbRowsQuery) => request<DbRows>('POST', paths.adminDbRows, { body: q }),
  adminDbSql: (sql: string) => request<DbSqlResponse>('POST', paths.adminDbSql, { body: { sql } }),

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
  /** A one-time link (24 hours) for the user to pick a new password; older unused links stop working. */
  createPasswordReset: (userId: string) => request<PasswordReset>('POST', paths.adminPasswordReset(userId)),
  revokeInvite: (id: string) => request<void>('DELETE', paths.adminInvite(id)),
  /** The open project invites of every user. */
  async adminProjectInvites(): Promise<AdminProjectInvite[]> {
    return listAll<AdminProjectInvite>(paths.adminProjectInvites)
  },
  revokeAdminProjectInvite: (id: string) => request<void>('DELETE', paths.adminProjectInvite(id)),
  /** What waits and runs of the apps you can see (the sidebar's builds card). */
  builds: () => request<Builds>('GET', paths.builds),
  adminSettings: () => request<ServerSettings>('GET', paths.adminSettings),
  updateAdminSettings: (input: { signups?: SignupMode; project_invite_apps?: boolean; new_account_quota?: NewAccountQuota; max_parallel_builds?: number; builder_cpu_millis?: number; builder_memory_mb?: number }) =>
    request<ServerSettings>('PATCH', paths.adminSettings, { body: input }),
  adminServer: () => request<ServerStats>('GET', paths.adminServer),
  adminServerMetrics: (window: MetricWindow) => request<ServerMetrics>('GET', paths.adminServerMetrics, { query: { window } }),
  /** Measured at most every two minutes unless refresh is set; measuring takes a while on a big server. */
  adminDisk: (refresh = false) => request<DiskReport>('GET', paths.adminDisk, { query: { refresh: refresh || undefined } }),
  adminDiskCleanup: (input: { kind: DiskCleanupKind; volumes?: string[] }) => request<DiskCleanupResult>('POST', paths.adminDiskCleanup, { body: input }),
  updateAdminDiskAuto: (input: DiskAutoInput) => request<DiskAutoCleanup>('PATCH', paths.adminDiskAuto, { body: input }),
  runAdminDiskAuto: () => request<DiskCleanupRun>('POST', paths.adminDiskAutoRun),
  /** The background jobs and their last run since bitploy started. */
  adminJobs: () => request<JobList>('GET', paths.adminJobs),
  async adminApps(): Promise<App[]> {
    return listAll<App>(paths.adminApps)
  },
  adminStopApp: (slug: string) => request<unknown>('POST', paths.adminAppStop(slug)),
  adminPublicPorts: () => request<PublicPortsOverview>('GET', paths.adminPublicPorts),
  adminClosePublicPort: (slug: string) => request<void>('DELETE', paths.adminPublicPort(slug)),
  adminAppDomains: () => request<AppDomainList>('GET', paths.adminAppDomains),
  addAppDomain: (input: { domain: string; certificate?: string; private_key?: string }) =>
    request<AppDomain>('POST', paths.adminAppDomains, { body: input }),
  setAppDomainCert: (id: string, input: { certificate: string; private_key: string }) =>
    request<AppDomain>('PUT', paths.adminAppDomainCert(id), { body: input }),
  removeAppDomain: (id: string) => request<void>('DELETE', paths.adminAppDomain(id)),
  setUserAppDomains: (id: string, domainIds: string[]) =>
    request<AdminUser>('PUT', paths.adminUserAppDomains(id), { body: { domain_ids: domainIds } }),
  audit: (cursor?: string) =>
    request<Page<AuditEntry>>('GET', paths.adminAudit, { query: { limit: 50, cursor } }),
  activity: (cursor?: string) =>
    request<Page<ActivityEntry>>('GET', paths.activity, { query: { limit: 50, cursor } }),
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
  /** One service of an app with several (runtime logs only). */
  service?: string
}

export function logPath(t: LogTarget): { path: string; query: Record<string, string | number> } {
  if (t.kind === 'build' && t.deploymentId) return { path: paths.deploymentLog(t.deploymentId), query: {} }
  const query: Record<string, string | number> = { kind: t.kind, tail: 500 }
  if (t.kind === 'runtime' && t.service) query.service = t.service
  return { path: paths.appLogs(t.slug), query }
}

/** One log line; runtime lines also say when they were written. */
export interface LogEntry {
  text: string
  time?: string
}

/** One log line from either a plain text line or a JSON LogLine. */
export function parseLogEntry(raw: string): LogEntry {
  const s = raw.replace(/\r$/, '')
  if (s.startsWith('{') && s.endsWith('}')) {
    try {
      const j = JSON.parse(s) as { text?: unknown; time?: unknown }
      if (typeof j.text === 'string') return { text: j.text, time: typeof j.time === 'string' ? j.time : undefined }
    } catch {
      // plain text that happens to look like JSON
    }
  }
  return { text: s }
}

export function parseLogLine(raw: string): string {
  return parseLogEntry(raw).text
}

/** Fetch a snapshot of the log. Accepts text, a JSON array or {items|lines:[...]}. */
export async function fetchLogEntries(t: LogTarget, signal?: AbortSignal): Promise<LogEntry[]> {
  const { path, query } = logPath(t)
  const res = await send('GET', path, { query, signal }, 'application/json, text/plain')
  const type = res.headers.get('content-type') || ''
  const text = await res.text()
  if (type.includes('json')) {
    try {
      const data = JSON.parse(text) as unknown
      const arr = listOf<unknown>(data, 'lines')
      return arr.map((l) => {
        if (typeof l === 'string') return { text: l }
        const o = l as { text?: string; time?: string }
        return { text: String(o.text ?? ''), time: typeof o.time === 'string' ? o.time : undefined }
      })
    } catch {
      // fall through to plain text
    }
  }
  return text.split('\n').filter((l, i, a) => l !== '' || i < a.length - 1).map(parseLogEntry)
}

export async function fetchLogLines(t: LogTarget, signal?: AbortSignal): Promise<string[]> {
  return (await fetchLogEntries(t, signal)).map((e) => e.text)
}

/** URL for the live tail (Server-Sent Events). */
export function logStreamUrl(t: LogTarget): string {
  const { path, query } = logPath(t)
  return buildUrl(path, { ...query, follow: 1 })
}
