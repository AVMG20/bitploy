// TypeScript mirror of internal/app/types.go (snake_case JSON).
// Timestamps arrive as RFC 3339 strings.

export type Role = 'user' | 'admin'
export type Scope = 'read' | 'deploy' | 'session'

export interface User {
  id: string
  email: string
  name: string
  role: Role
  status: 'active' | 'suspended'
  created_at: string
}

export interface Quota {
  apps: number
  memory_mb: number
  cpu_millis: number
  deploys_per_hour: number
  domains_per_app: number
  /** Storage allowance, shared by the /data volumes of all apps. */
  volume_mb: number
}

export interface Usage {
  apps: number
  apps_running: number
  memory_reserved_mb: number
  cpu_reserved_millis: number
  memory_used_mb: number
  cpu_now_percent: number
  build_minutes_month: number
  running_hours_month: number
  deploys_month: number
  deploys_week: number
  disk_used_mb: number
  volume_reserved_mb: number
  volume_used_mb: number
  /** Images and stored source of all apps. */
  files_mb: number
  /** Backups of all apps. */
  backups_mb: number
}

export interface Me {
  user: User
  quota: Quota
  usage: Usage
  apps_domain: string
  public_url: string
  scope: Scope
}

export interface SessionInfo {
  user: User
  csrf_token: string
}

export interface Session {
  id: string
  current: boolean
  ip: string
  user_agent: string
  created_at: string
  last_seen_at: string
}

export interface Token {
  id: string
  name: string
  prefix: string
  scope: Scope
  created_at: string
  expires_at: string | null
  last_used_at: string | null
  last_used_ip: string | null
  revoked_at?: string | null
}

export interface TokenCreated extends Token {
  value: string
}

export interface Invite {
  id: string
  email: string | null
  role: Role
  created_at: string
  expires_at: string
  used_at: string | null
  url?: string
}

/** The GitHub App of this bitploy. Everyone sees `configured`, `name` and `slug`; the rest is for admins. */
export interface GitHubAppInfo {
  configured: boolean
  name?: string
  slug?: string
  html_url?: string
  manage_url?: string
  owner?: string
  webhook_url?: string
  default_name?: string
  created_at?: string
  /** Why GitHub cannot reach this server; empty when it can. */
  public_url_problem?: string
}

export interface GitHubInstallation {
  id: number
  account_login: string
  account_type: 'User' | 'Organization'
  repository_selection: 'all' | 'selected'
  status: 'active' | 'suspended' | 'deleted'
  manage_url: string
  repo_count?: number
  connected_at: string
}

export interface GitHubOverview {
  app: GitHubAppInfo
  installations: GitHubInstallation[]
}

export interface GitHubRepo {
  full_name: string
  private: boolean
  html_url: string
  default_branch: string
  installation_id: number
  pushed_at?: string
}

export interface GitHubBranches {
  default_branch: string
  branches: string[]
}

export interface GitHubManifest {
  post_url: string
  manifest: string
  state: string
  name: string
}

/** What you send to link a repository to an app. */
export interface GitSourceInput {
  repo_url: string
  branch?: string
  subdir?: string
  auto_deploy?: boolean
}

export type PushStatus = 'ready' | 'off' | 'other_host' | 'app_missing' | 'not_connected' | 'no_access' | 'reconnect'

/** The repository linked to an app, and whether a push will deploy it. */
export interface GitSource {
  repo_url: string
  branch: string
  subdir?: string
  /** owner/name, for repositories on github.com. */
  repo?: string
  repo_html_url?: string
  auto_deploy: boolean
  push_ready: boolean
  push_status: PushStatus
  push_message: string
  last_push_at: string | null
}

export type AppStatus = 'new' | 'deploying' | 'running' | 'stopped' | 'failed' | 'crashed'

export interface DeployError {
  code: string
  message: string
  hint?: string
  next_tool?: string
}

export interface Finding {
  code: string
  severity: 'error' | 'warning' | 'info'
  message: string
  hint?: string
  line?: number
}

export type DeploymentState =
  | 'queued'
  | 'fetching'
  | 'validating'
  | 'building'
  | 'starting'
  | 'health_check'
  | 'swapping'
  | 'running'
  | 'failed'
  | 'superseded'
  | 'cancelled'

export interface Deployment {
  id: string
  app_id: string
  app_slug: string
  seq: number
  state: DeploymentState
  source_kind: 'files' | 'upload' | 'git' | 'rollback'
  source_ref?: string
  commit_sha?: string | null
  commit_message?: string | null
  channel: string
  image?: string | null
  error: DeployError | null
  warnings: Finding[] | null
  log_tail?: string[]
  queued_at: string
  started_at: string | null
  finished_at: string | null
  duration_seconds: number | null
  url: string
  next_steps?: string[]
}

export interface EnvVar {
  name: string
  masked: string
  updated_at: string
}

export interface DNSRecord {
  type: 'CNAME' | 'A' | 'AAAA' | 'TXT'
  name: string
  value: string
}

export type DomainStatus = 'pending_dns' | 'issuing_cert' | 'active' | 'failed'

export interface Domain {
  id: string
  hostname: string
  status: DomainStatus
  last_error?: string
  records: DNSRecord[] | null
  notes?: string[]
  last_checked_at: string | null
  created_at: string
  activated_at: string | null
}

export interface App {
  id: string
  slug: string
  name: string
  type: 'web' | 'worker'
  url: string
  owner_id: string
  status: AppStatus
  status_message?: string
  desired_state: string
  port: number | null
  health_path: string
  dockerfile_path: string
  context_dir: string
  volume: boolean
  /** Storage limit of /data. */
  volume_mb: number
  /** What /data holds, measured hourly; null when the volume is off or not measured yet. */
  volume_used_mb: number | null
  /** The app's own files: kept images and stored source; null until measured. */
  files_mb: number | null
  /** What the app's backups take; counts towards the storage allowance. */
  backups_mb: number
  disk_measured_at?: string
  memory_mb: number
  cpu_millis: number
  git_source: GitSource | null
  current_deployment: Deployment | null
  domains: Domain[] | null
  started_at?: string | null
  restart_count: number
  /** Latest 15-second sample; null when the app is not running. */
  live?: MetricPoint | null
  created_at: string
  updated_at: string
}

export interface UpdateAppInput {
  name?: string
  slug?: string
  type?: 'web' | 'worker'
  port?: number
  health_path?: string
  dockerfile_path?: string
  context_dir?: string
  volume?: boolean
  volume_mb?: number
  memory_mb?: number
  cpu_millis?: number
}

export interface CreateTokenInput {
  name: string
  scope: 'read' | 'deploy'
  expires_in_days?: number
}

export interface MetricPoint {
  ts: string
  cpu: number
  memory_mb: number
  rx_bytes: number
  tx_bytes: number
  cpu_max?: number
  memory_max_mb?: number
}

export type MetricWindow = '1h' | '24h' | '7d'

export interface Metrics {
  app: string
  window: MetricWindow
  memory_limit_mb: number
  points: MetricPoint[] | null
  latest: MetricPoint | null
}

export type TrafficWindow = '24h' | '7d' | '30d'

export interface TrafficPoint {
  ts: string
  requests: number
  visitors: number
  errors: number
}

export interface TrafficItem {
  name: string
  count: number
}

export interface TrafficCountry {
  code: string
  requests: number
  visitors: number
}

export interface Traffic {
  app: string
  window: TrafficWindow
  granularity: 'hour' | 'day'
  visitors_today: number
  totals: { requests: number; visitors: number; errors: number; error_rate: number }
  points: TrafficPoint[]
  pages: TrafficItem[]
  countries: TrafficCountry[]
  referrers: TrafficItem[]
}

export interface AuditEntry {
  id: number
  ts: string
  user_id: string | null
  channel: string
  token_id: string | null
  ip: string
  action: string
  target_type: string
  target_id: string
  details: Record<string, unknown> | null
}

export interface AdminUser extends User {
  quota: Quota
  usage: Usage
  app_count: number
}

export interface ServerStats {
  cpu_percent: number
  load1: number
  mem_total_mb: number
  mem_used_mb: number
  mem_reserved_mb: number
  disk_total_gb: number
  disk_used_gb: number
  docker_images_gb: number
  docker_build_cache_gb: number
  docker_volumes_gb: number
  builds_running: number
  builds_queued: number
  apps_running: number
  apps_total: number
  apps_max: number
}

export interface Page<T> {
  items: T[] | null
  next_cursor?: string
}

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    hint?: string
    next?: string
    details?: unknown
  }
}

// ---------------------------------------------------------------------------
// Backups of /data
// ---------------------------------------------------------------------------
export type BackupKind = 'manual' | 'scheduled' | 'uploaded' | 'before_restore'
export type BackupStatus = 'running' | 'ready' | 'failed'
export type BackupSchedule = 'off' | 'daily' | 'weekly'
export type BackupKeepMode = 'last' | 'days'

export interface Backup {
  id: string
  kind: BackupKind
  status: BackupStatus
  size_bytes: number
  /** Why a failed backup failed, in plain words. */
  error?: string
  created_at: string
}

export interface BackupSettings {
  schedule: BackupSchedule
  keep_mode: BackupKeepMode
  /** How many backups (keep_mode last) or how many days (keep_mode days). */
  keep: number
  /** When the next automatic backup is due; a time in the past means "soon". */
  next_at?: string
}

export interface BackupRestore {
  backup_id: string
  state: 'running' | 'done' | 'failed'
  error?: string
  started_at: string
  finished_at?: string
  /** The app was running, so it is stopped for the restore and started again. */
  app_restarts: boolean
}

export interface BackupList {
  items: Backup[]
  settings: BackupSettings
  restore: BackupRestore | null
  /** A backup, upload or restore of this app is in progress. */
  busy: boolean
  total_bytes: number
  /** The biggest backup file that can be uploaded right now. */
  upload_max_bytes: number
}
