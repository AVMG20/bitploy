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
  /** An app's address with {slug} in place of its subdomain, for previews. */
  app_url_template: string
  /** The domains this person may put apps on, the default first. */
  app_domains: AppDomainChoice[]
  public_url: string
  scope: Scope
}

/** A domain an app's address can be on: https://<slug>.<domain>. */
export interface AppDomainChoice {
  domain: string
  default: boolean
  /** The address with {slug} in place of the app's name. */
  url_template: string
}

/** A domain apps can live under, as an admin sees it. */
export interface AppDomain {
  /** apd_..., or "default" for the domain from the server settings. */
  id: string
  domain: string
  default: boolean
  /** install: the certificate set up with bitploy. own: added here. none: no matching certificate. */
  certificate: 'install' | 'own' | 'none'
  cert_expires_at: string | null
  app_count: number
  /** People (admins aside) allowed to use it. */
  user_count: number
  created_at: string | null
}

export interface AppDomainList {
  items: AppDomain[]
  server_ipv4: string
  server_ipv6: string
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
  /** GitHub sends push events here (false when the app was created without a public address). */
  push_events?: boolean
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
  /** Not your own account: only the repositories you could open on GitHub when you connected. */
  limited: boolean
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

export type PushStatus = 'ready' | 'off' | 'other_host' | 'app_missing' | 'no_webhook' | 'not_connected' | 'no_access' | 'reconnect'

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
  /**
   * The service that failed, in an app with several. The server does not send
   * it yet (it prefixes the message with "Service <name>: "); kept optional.
   */
  service?: string
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
  /** What happened to each service in this deployment, for an app with several. */
  services?: DeploymentService[]
}

/** One service of a deployment (Deployment.services). */
export interface DeploymentService {
  service: string
  /** null until it is built or pulled. */
  image: string | null
  /** Where this service got to. */
  state: DeploymentState
  /** The container of an earlier version kept running because nothing changed. */
  reused: boolean
  error: DeployError | null
}

export interface EnvVar {
  name: string
  masked: string
  updated_at: string
  /** The service of the app it is for; "": shared by every service of the app. */
  service: string
  /**
   * The ${{alias.VAR}} references in the value, like ${{postgres.DATABASE_URL}}.
   * They name other apps' variables and are not secret; the rest of the value
   * stays masked.
   */
  references?: string[]
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
  /** The ready-made app this was made from; "" when it was not. */
  catalog_id: string
  catalog_version: string
  /** A newer catalog version, when there is one. */
  catalog_update: string | null
  slug: string
  name: string
  type: 'web' | 'worker'
  url: string
  /** The domain of the address: <slug>.<domain>. */
  domain: string
  owner_id: string
  status: AppStatus
  status_message?: string
  desired_state: string
  port: number | null
  health_path: string
  dockerfile_path: string
  context_dir: string
  /** auto: detect what the project is. node: a Node.js app. static_build: build, then serve the output folder. static: plain website files. dockerfile: needs a Dockerfile. */
  build_mode: BuildMode
  /** Folder a static build serves (dist, build, out); "" means the default of the detected framework. */
  output_dir: string
  /** Page served at "/" when a static site has several pages and no index.html; "" means decided from the files. */
  home_page: string
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
  /**
   * A static site: the live version is a plain website or a site (Vite, Astro, ...) that bitploy
   * builds itself, not the app's own Dockerfile. Fixed small size, no /data, and
   * it does not count towards the memory and CPU allowance.
   */
  static: boolean
  git_source: GitSource | null
  /** True while visitors must sign in with the app login first. */
  protected: boolean
  /** The login name while protected. The password is never returned. */
  protected_username?: string
  current_deployment: Deployment | null
  domains: Domain[] | null
  started_at?: string | null
  restart_count: number
  /** Latest 15-second sample; null when the app is not running. */
  live?: MetricPoint | null
  /**
   * The services of the app. A normal app has one, named "app"; its
   * settings are the fields above.
   */
  services: AppService[]
  /** The storage volumes of the app; /data of a normal app is named "data". */
  volumes: AppVolume[]
  /** single: one service whose settings are the app's fields; multi: several services from a compose file. */
  service_layout?: 'single' | 'multi'
  /** Where the compose file is in the source, for build_mode compose; "" means the usual names. */
  compose_path: string
  /** The project the app is in (phase 4b). Missing on a server without projects. */
  project?: ProjectRef
  /**
   * The app's short name in its project: its hostname for the other apps
   * (postgres, web) and the name in ${{alias.VAR}}. Missing: the slug.
   */
  alias?: string
  /** The service template it was made from (postgres, valkey, ...), or null. */
  template?: string | null
  /** Template apps: the version of the template's image it runs, like 18.6. */
  template_version?: string
  /** Where the other apps of the project reach it, like postgres:5432; "" when the port is not known. */
  internal_host?: string
  /** Template apps: the variables the other apps can reference, main one first. */
  connection_vars?: string[]
  /** Template apps: the variable connect_to sets in the connected apps, like DATABASE_URL. */
  main_var?: string
  /** Short names of the apps of the project this app's variables refer to. */
  references?: string[]
  created_at: string
  updated_at: string
}

/** The project an app is in, as App.project. */
export interface ProjectRef {
  id: string
  name: string
}

/** A space that holds apps: a website, its API, its database (GET /projects). */
export interface Project {
  /** prj_... */
  id: string
  name: string
  created_at: string
  updated_at?: string
  /** The private Docker network the apps share (bp-p-<id>); made once the project has two apps. */
  network?: string
  apps: App[]
}

/** A database or other ready service bitploy can add to a project (GET /service-templates). */
export interface ServiceTemplate {
  /** postgres, mysql, mariadb, redis, valkey, meilisearch */
  id: string
  /** PostgreSQL */
  name: string
  /** database, cache or search */
  category: string
  description: string
  /** The image with its pinned version, like postgres:18.6 */
  image: string
  version: string
  port: number
  memory_mb: number
  cpu_millis?: number
  volume_mb: number
  /** The variables it publishes, like DATABASE_URL, PGHOST, PGPASSWORD. */
  connection_vars: string[]
  /** The one "Connect to" adds to the other app, like DATABASE_URL. */
  main_var: string
}

/** POST /projects/{id}/services */
export interface AddServiceInput {
  template: string
  /** Its short name in the project; the template id when missing. */
  name?: string
  /** Slugs of the apps that get <main_var>=${{name.<main_var>}}; they restart once the service runs. */
  connect_to?: string[]
  /** Default: the template's. */
  memory_mb?: number
  volume_mb?: number
}

/** One container of an app (App.services). */
export interface AppService {
  /** Also its hostname for the app's other services. */
  name: string
  /** build (from a Dockerfile) or image (a ready-made image). */
  kind: 'build' | 'image'
  image?: string
  /** The app's address and domains point at this service. */
  public: boolean
  port: number | null
  health_path: string
  memory_mb: number
  cpu_millis: number
  /** Names of the volumes the service mounts. */
  volumes: string[]
  /** Services that start before this one. */
  depends_on: string[]
  /** The Docker container of the current deployment. */
  container?: string
  /** Latest 15-second sample of this service, for a running app with several services. */
  live?: MetricPoint | null
}

/** One storage volume of an app (App.volumes). */
export interface AppVolume {
  name: string
  /** The service that mounts it. */
  service: string
  /** Where the service sees it, like /data. */
  mount_path: string
  size_mb: number
  /** What the volume holds, measured hourly; null until measured. */
  used_mb: number | null
}

/** compose: several services from a compose file (compose.yaml, docker-compose.yml, ...). */
export type BuildMode = 'auto' | 'node' | 'static_build' | 'static' | 'dockerfile' | 'compose'

export interface CreateAppInput {
  name: string
  /** web (default) has an address; worker has none. */
  type?: 'web' | 'worker'
  slug?: string
  /** The project to put it in (phase 4b). Without it a new project named after the app is made. */
  project?: string
  /** One of me.app_domains; leave out for the default. */
  domain?: string
  volume?: boolean
  build_mode?: BuildMode
  output_dir?: string
  home_page?: string
  memory_mb?: number
  cpu_millis?: number
  volume_mb?: number
  port?: number
  compose_path?: string
}

export interface UpdateAppInput {
  name?: string
  /** Move it to another project (phase 4b). */
  project?: string
  /** Its short name in the project (phase 4b). */
  alias?: string
  slug?: string
  /** Move the address to another domain (one of me.app_domains). */
  domain?: string
  type?: 'web' | 'worker'
  port?: number
  health_path?: string
  dockerfile_path?: string
  context_dir?: string
  build_mode?: BuildMode
  output_dir?: string
  home_page?: string
  volume?: boolean
  /** With volume: false, also delete the files in /data and all backups. Needed when there are any. */
  delete_data?: boolean
  volume_mb?: number
  memory_mb?: number
  cpu_millis?: number
  compose_path?: string
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
  /** Set when the metrics are of one service, not the whole app. */
  service?: string
  window: MetricWindow
  memory_limit_mb: number
  points: MetricPoint[] | null
  latest: MetricPoint | null
}

export type TrafficWindow = '24h' | '7d' | '30d'
export type TrafficListName = 'pages' | 'countries' | 'referrers' | 'not_found'

export interface TrafficPoint {
  ts: string
  requests: number
  visitors: number
  /** 5xx */
  errors: number
  /** 4xx */
  client_errors: number
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

export interface TrafficTotals {
  requests: number
  visitors: number
  /** 5xx */
  errors: number
  /** 5xx as a percentage of requests */
  error_rate: number
  /** 4xx */
  client_errors: number
  /** Requests from crawlers and scripts, part of requests. */
  bots: number
}

export interface Traffic {
  app: string
  window: TrafficWindow
  granularity: 'hour' | 'day'
  visitors_today: number
  totals: TrafficTotals
  /** The equal period before; null when stats do not go back that far. */
  previous: TrafficTotals | null
  points: TrafficPoint[]
  pages: TrafficItem[]
  countries: TrafficCountry[]
  /** "(direct)" is page views without a referrer. */
  referrers: TrafficItem[]
  /** Paths answered 404. */
  not_found: TrafficItem[]
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
  /** Extra app domains this person may use (the default domain is always allowed). */
  app_domain_ids: string[]
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
  /** The volume it holds; only for an app with several services. */
  volume?: string
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

// ---------------------------------------------------------------------------
// Files in /data
// ---------------------------------------------------------------------------
export type DataFileType = 'file' | 'dir' | 'symlink' | 'other'

export interface DataFile {
  name: string
  type: DataFileType
  /** Bytes; 0 for everything but a file. */
  size: number
  modified_at: string
}

export interface DataFileList {
  /** The folder, relative to /data; empty for /data itself. */
  path: string
  items: DataFile[]
  /** There are more items in this folder than the list holds. */
  truncated: boolean
  /** The biggest file that can be uploaded now. */
  upload_max_bytes: number
}

// ---------------------------------------------------------------------------
// SQLite database viewer (files in /data). See src/lib/dbvalue.ts for the value encoding.
// ---------------------------------------------------------------------------

export interface DbInt {
  int: string
}
export interface DbReal {
  real: string
}
export interface DbText {
  text: string
  size: number
  truncated: true
}
export interface DbBlob {
  /** Base64 of the first 4 KiB (or of everything, when sent to the server). */
  blob: string
  size?: number
  truncated?: boolean
}
/** One SQLite value as JSON: null, a number, a string (TEXT), or one of the wrapper shapes. */
export type DbValue = null | number | string | DbInt | DbReal | DbText | DbBlob

export interface DbColumn {
  name: string
  type: string
  notnull: boolean
  default: string | null
  pk: number
  /** A hidden column (of a virtual table). */
  hidden: boolean
  /** A generated column: SQLite computes it, it cannot be written. */
  generated: boolean
  /** Admin database only: the server hides this column's values (secrets). */
  redacted?: boolean
}
export interface DbIndex {
  name: string
  unique: boolean
  columns: string[]
  sql: string | null
}
export interface DbTable {
  name: string
  type: 'table' | 'view'
  without_rowid: boolean
  editable: boolean
  rows: number | null
  sql: string | null
  columns: DbColumn[]
  indexes: DbIndex[]
}
export interface DbSchema {
  path: string
  size: number
  journal_mode: string
  page_size: number
  sqlite_version: string
  tables: DbTable[]
}

export type DbKey = Record<string, DbValue>
export type DbFilterOp = '=' | '!=' | '<' | '<=' | '>' | '>=' | 'like' | 'not like' | 'is null' | 'is not null'
export interface DbFilter {
  column: string
  op: DbFilterOp
  value?: DbValue
}
export interface DbRowsQuery {
  table: string
  limit: number
  offset: number
  order_by?: string
  desc?: boolean
  filters?: DbFilter[]
  /** Text to find in any column (the server trims it; at most 200 characters). */
  search?: string
}
export interface DbRow {
  key: DbKey | null
  values: DbValue[]
}
export interface DbRows {
  columns: string[]
  key_columns: string[]
  editable: boolean
  rows: DbRow[]
  total: number | null
  offset: number
  limit: number
}

export interface DbSqlResult {
  sql: string
  columns: string[]
  rows: DbValue[][]
  truncated: boolean
  rows_affected: number | null
  last_insert_rowid: number | null
  took_ms: number
}
export interface DbSqlResponse {
  results: DbSqlResult[]
  took_ms: number
}
/** `details` of a `sql_error`: the failed statement (1-based, as the server counts) and the ones that ran before it. */
export interface DbSqlErrorDetails {
  statement?: number
  results?: DbSqlResult[]
}

export interface CatalogEnv {
  name: string
  description: string
  /** Bitploy makes the value; it is never shown here. */
  generated: boolean
}

export interface CatalogApp {
  id: string
  name: string
  tagline: string
  description: string
  category: string
  version: string
  website: string
  port: number
  memory_mb: number
  volume: boolean
  volume_mb: number
  health_path: string
  env: CatalogEnv[]
  /** May contain "{url}", which stands for the app's address. */
  first_steps: string[]
  dockerfile: string
  /** Databases (like PostgreSQL) deployed next to the app in its project; empty for a single-container app. */
  services?: CatalogService[]
}

/** A service a catalog app brings along (CatalogApp.services). */
export interface CatalogService {
  /** The service template id, like postgres. */
  template: string
  /** Its short name in the project, like db. */
  name: string
  image: string
  memory_mb: number
  volume_mb: number
}

/** Deploying a catalog app always makes a project named after it. */
export interface CatalogDeployInput {
  name?: string
  slug?: string
  domain?: string
  memory_mb?: number
  volume_mb?: number
}

export interface CatalogDeployResult {
  app: App
  deployment: Deployment
  first_steps: string[]
}

export interface AppDockerfile {
  path: string
  content: string
  editable: boolean
  reason: string
}

export interface DockerfileFinding {
  code: string
  severity: 'error' | 'warning' | 'info'
  message: string
  hint?: string
  line?: number
}

export interface DockerfileCheck {
  ok: boolean
  findings: DockerfileFinding[]
  port?: number
}

// ---------------------------------------------------------------------------
// Compose files: POST /api/v1/compose/validate (compose phase 2, built in parallel)
// ---------------------------------------------------------------------------

/** The file names bitploy looks for, in this order (docs/compose-plan.md "Deploy"). */
export const COMPOSE_FILE_NAMES = ['compose.yaml', 'compose.yml', 'docker-compose.yaml', 'docker-compose.yml'] as const

export interface ComposeService {
  name: string
  kind: 'build' | 'image'
  image?: string
  /** How a build service is built (paths from the project root); null for an image service. */
  build?: { context: string; dockerfile: string; target?: string } | null
  public: boolean
  /** The port of the public service; null when it comes from the image. */
  port: number | null
  health_path?: string
  memory_mb: number
  cpu_millis: number
  depends_on: string[] | null
  /** The names of the volumes it mounts. */
  volumes: string[] | null
}

export interface ComposeVolume {
  name: string
  size_mb: number
  /** The service that mounts it, and where. */
  service: string
  mount_path: string
}

/** A problem with a compose file, with the service and the compose key at fault. */
export interface ComposeIssue {
  code: string
  service?: string
  key?: string
  message: string
  hint?: string
}

/** How a compose file fits the account's allowance; the app's own use is left out. */
export interface ComposeQuota {
  memory_mb_left: number
  cpu_millis_left: number
  /** null: no storage limit. */
  storage_mb_left: number | null
  fits: boolean
  /** Like: This needs 768 MB of memory of the 1.5 GB you have left. */
  message: string
}

/** POST /compose/validate: a file with problems is a 200 with ok false. */
export interface ComposeCheck {
  ok?: boolean
  services: ComposeService[] | null
  volumes: ComposeVolume[] | null
  memory_mb_total: number
  cpu_millis_total: number
  volume_mb_total?: number
  quota?: ComposeQuota
  warnings: ComposeIssue[] | null
  errors: ComposeIssue[] | null
}
