package app

import (
	"time"

	"github.com/AVMG20/bitploy/internal/deploy"
)

// These are the public shapes returned by the service layer. The REST API
// returns them as JSON as-is, MCP returns them as structuredContent, and the
// dashboard's TypeScript types mirror them. JSON is snake_case.

type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`   // user | admin
	Status    string    `json:"status"` // active | suspended
	CreatedAt time.Time `json:"created_at"`
}

type Quota struct {
	Apps           int64 `json:"apps"`
	MemoryMB       int64 `json:"memory_mb"`
	CPUMillis      int64 `json:"cpu_millis"` // 1000 = 1 CPU
	DeploysPerHour int64 `json:"deploys_per_hour"`
	DomainsPerApp  int64 `json:"domains_per_app"`
	VolumeMB       int64 `json:"volume_mb"` // storage allowance, shared by the /data volumes of all apps
	// Overallocate: memory and CPU are not checked at all, not against this
	// quota and not against the server's room. Admins set it, for themselves.
	Overallocate bool `json:"overallocate,omitempty" doc:"Memory and CPU are not checked: apps may ask for more than the quota and the server have"`
}

// QuotaInput is what an admin sends to set a user's limits: every number,
// and overallocate only to change it (left out keeps the switch as it is,
// so a client that does not know it cannot turn it off).
type QuotaInput struct {
	Apps           int64 `json:"apps"`
	MemoryMB       int64 `json:"memory_mb"`
	CPUMillis      int64 `json:"cpu_millis"` // 1000 = 1 CPU
	DeploysPerHour int64 `json:"deploys_per_hour"`
	DomainsPerApp  int64 `json:"domains_per_app"`
	VolumeMB       int64 `json:"volume_mb"`
	Overallocate   *bool `json:"overallocate,omitempty" doc:"Memory and CPU are not checked: apps may ask for more than the quota and the server have. Left out: unchanged"`
}

type Usage struct {
	Apps              int64   `json:"apps"`
	AppsRunning       int64   `json:"apps_running"`
	MemoryReservedMB  int64   `json:"memory_reserved_mb"`
	CPUReservedMillis int64   `json:"cpu_reserved_millis"`
	MemoryUsedMB      float64 `json:"memory_used_mb"` // live, from the latest metric samples
	CPUNowPercent     float64 `json:"cpu_now_percent"`
	BuildMinutesMonth float64 `json:"build_minutes_month"`
	RunningHoursMonth float64 `json:"running_hours_month"`
	DeploysMonth      int64   `json:"deploys_month"`
	DeploysWeek       int64   `json:"deploys_week"`
	DiskUsedMB        float64 `json:"disk_used_mb"`
	VolumeReservedMB  int64   `json:"volume_reserved_mb"` // sum of the storage limits of apps with a volume
	VolumeUsedMB      float64 `json:"volume_used_mb"`     // measured hourly
	FilesMB           float64 `json:"files_mb"`           // images and stored source of all apps, measured hourly
	BackupsMB         float64 `json:"backups_mb"`         // backups of all apps
}

// Me is returned by whoami / GET /api/v1/me.
type Me struct {
	User       User   `json:"user"`
	Quota      Quota  `json:"quota"`
	Usage      Usage  `json:"usage"`
	AppsDomain string `json:"apps_domain"`
	// AppURLTemplate is the address of an app with {slug} in place of its subdomain, for previews.
	AppURLTemplate string `json:"app_url_template"`
	// AppDomains are the domains this user may put apps on, the default first.
	AppDomains []AppDomainChoice `json:"app_domains"`
	PublicURL  string            `json:"public_url"`
	Scope      Scope             `json:"scope"`
}

type SessionInfo struct {
	User      User   `json:"user"`
	CSRFToken string `json:"csrf_token"`
}

type Session struct {
	ID         string    `json:"id"` // first 12 chars of the hash, for display and revoke
	Current    bool      `json:"current"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type Token struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      Scope      `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	LastUsedIP *string    `json:"last_used_ip"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// TokenCreated includes the token value, shown exactly once.
type TokenCreated struct {
	Token
	Value string `json:"value"`
}

type Invite struct {
	ID        string     `json:"id"`
	Email     *string    `json:"email"`
	Role      string     `json:"role"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	// URL is only set right after creation (the code is stored hashed).
	URL string `json:"url,omitempty"`
}

// GitSource is what you send to link a repository to an app.
type GitSource struct {
	RepoURL    string `json:"repo_url"`
	Branch     string `json:"branch" required:"false" doc:"Leave empty for the repository's default branch."`
	Subdir     string `json:"subdir,omitempty"`
	AutoDeploy *bool  `json:"auto_deploy,omitempty" doc:"Deploy on every push to the branch. Default true. Needs GitHub connected (Settings > GitHub)."`
}

// GitSourceState is the linked repository of an app, with what bitploy knows
// about push events.
type GitSourceState struct {
	RepoURL     string `json:"repo_url"`
	Branch      string `json:"branch"`
	Subdir      string `json:"subdir,omitempty"`
	Repo        string `json:"repo,omitempty" doc:"owner/name, for repositories on github.com"`
	RepoHTMLURL string `json:"repo_html_url,omitempty" doc:"Link to the repository on GitHub"`
	AutoDeploy  bool   `json:"auto_deploy" doc:"The setting: deploy on every push to the branch"`
	// PushReady is true when a push to the branch will start a deploy: auto-deploy
	// is on and the owner's GitHub connection covers the repository.
	PushReady bool `json:"push_ready"`
	// PushStatus is ready, off, other_host, app_missing, no_webhook, not_connected, no_access or reconnect.
	PushStatus  string     `json:"push_status"`
	PushMessage string     `json:"push_message" doc:"The status in one plain sentence"`
	LastPushAt  *time.Time `json:"last_push_at" doc:"When GitHub last told bitploy about a push to this branch"`
}

type App struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Type string `json:"type"` // web | worker
	URL  string `json:"url"`
	// Domain is the domain the app's address is on: <slug>.<domain>.
	Domain        string `json:"domain"`
	OwnerID       string `json:"owner_id"`
	Status        string `json:"status"` // new | deploying | running | stopped | failed | crashed
	StatusMessage string `json:"status_message,omitempty"`
	// Protected is true while a login prompt (username and password) stands in
	// front of the app's public addresses. The password itself is never returned.
	Protected         bool   `json:"protected"`
	ProtectedUsername string `json:"protected_username,omitempty" doc:"The username of the login prompt, while it is on"`
	DesiredState      string `json:"desired_state"`
	Port              *int64 `json:"port"`
	HealthPath        string `json:"health_path"`
	DockerfilePath    string `json:"dockerfile_path"`
	ContextDir        string `json:"context_dir"`
	BuildMode         string `json:"build_mode"` // dockerfile | auto | node | static_build | static | compose
	// ComposePath is where the compose file is in the source; "" means the
	// first of compose.yaml, compose.yml, docker-compose.yaml,
	// docker-compose.yml (in context_dir, then at the root).
	ComposePath string `json:"compose_path" doc:"Build mode compose: the compose file in the source. Empty: compose.yaml, compose.yml, docker-compose.yaml or docker-compose.yml"`
	// ServiceLayout is single (one service, named app, whose settings are the
	// fields of App) or multi (several services from a compose file).
	ServiceLayout string `json:"service_layout" enum:"single,multi" doc:"single: one service whose settings are the app's fields; multi: several services from a compose file"`
	// OutputDir is the folder a static build serves; empty means the default of
	// the detected framework (dist, build or out).
	OutputDir string `json:"output_dir"`
	// HomePage is the page served at "/" when a static site has several pages
	// and no index.html; empty means decided from the files.
	HomePage string `json:"home_page"`
	Volume   bool   `json:"volume"`
	VolumeMB int64  `json:"volume_mb"` // storage limit of /data
	// VolumeUsedMB is what /data holds, measured hourly; nil when the volume is
	// off or not measured yet.
	VolumeUsedMB *float64 `json:"volume_used_mb"`
	// FilesMB is the app's own files: its kept images (versions for rollback)
	// and stored source, measured hourly and after each deploy; nil until measured.
	FilesMB        *float64   `json:"files_mb"`
	DiskMeasuredAt *time.Time `json:"disk_measured_at,omitempty"`
	BackupsMB      float64    `json:"backups_mb"` // what the app's backups take; counts towards the storage allowance
	// BackupsSupported is false for template apps that keep a cache or a
	// search index (valkey, redis, meilisearch): bitploy makes no backups
	// of them and refuses to (backups_not_needed).
	BackupsSupported bool  `json:"backups_supported" doc:"False for a cache or search index (valkey, redis, meilisearch): bitploy makes no backups of it"`
	MemoryMB         int64 `json:"memory_mb"`
	CPUMillis        int64 `json:"cpu_millis"`
	// Static is true while the live version is a static site: built from
	// bitploy's own nginx templates (a plain website, or a Vite site built by
	// auto mode), never from the app's own Dockerfile. A static site has fixed
	// limits (memory_mb and cpu_millis cannot be changed), cannot have a /data
	// volume and does not count towards the memory and CPU allowance.
	Static bool `json:"static" doc:"A static site: fixed small size, no /data, not counted in the memory and CPU allowance"`
	// CatalogID and CatalogVersion say which one-click app this app was made
	// from ("" when it was not). CatalogUpdate is the catalog's current version
	// when it differs from CatalogVersion, else null.
	CatalogID      string          `json:"catalog_id" doc:"The one-click app this app was made from, or empty"`
	CatalogVersion string          `json:"catalog_version" doc:"The version of that one-click app the app runs"`
	CatalogUpdate  *string         `json:"catalog_update" doc:"The catalog's newer version, or null when the app is up to date or not from the catalog"`
	GitSource      *GitSourceState `json:"git_source"`
	Current        *Deployment     `json:"current_deployment"`
	Domains        []Domain        `json:"domains"`
	StartedAt      *time.Time      `json:"started_at,omitempty"` // current container start, for uptime
	RestartCount   int64           `json:"restart_count"`
	// Live is the latest 15-second sample (CPU % of one core, memory MB); nil when not running.
	Live *MetricPoint `json:"live"`
	// Services are the containers the app runs: one service named "app" for
	// an app built from one Dockerfile. memory_mb and cpu_millis above are
	// their sums.
	Services []AppService `json:"services" doc:"The services of the app; one named app unless it has several"`
	// Volumes are the app's storage volumes; the /data volume is named "data".
	Volumes []AppVolume `json:"volumes" doc:"The storage volumes of the app; /data is named data"`
	// PublicPort is the port on the server that forwards to the app, for
	// connecting with psql, TablePlus or redis-cli while debugging.
	PublicPort PublicPort `json:"public_port" doc:"A port on the server that forwards to the app while it is open (set_public_port)"`
	// Project is the project the app is in; Alias is its short name there:
	// the other apps of the project reach it at <alias> (and <alias>-<service>
	// for each service of an app with several) and refer to its variables as
	// ${{<alias>.VAR}}.
	Project ProjectRef `json:"project" doc:"The project the app is in"`
	Alias   string     `json:"alias" doc:"The app's short name in its project: its host name on the project network, and the name in ${{<alias>.VAR}} references"`
	// Template is the service template the app was made from (postgres,
	// valkey, ...; see list_service_templates), or nil.
	Template        *string `json:"template" doc:"The service template the app was made from, or null"`
	TemplateVersion string  `json:"template_version,omitempty" doc:"The version of the template's image the app runs"`
	// TemplateLatest is the template's current version; it is newer than
	// TemplateVersion when TemplateUpdateAvailable. bitploy never upgrades a
	// data store by itself (a new major version can need a dump and restore).
	TemplateLatest          string `json:"template_latest,omitempty" doc:"Template apps: the template's current version in this bitploy"`
	TemplateUpdateAvailable bool   `json:"template_update_available" doc:"True when the template has a newer version than the app runs. Never applied automatically."`
	// InternalHost is <alias>:<port>, the address the project's other apps use;
	// empty when the app has no known port.
	InternalHost string `json:"internal_host" doc:"<alias>:<port>, the address the other apps of the project use; empty when the port is not known"`
	// ConnectionVars are the variables a template app publishes to its
	// project, like DATABASE_URL; MainVar is the one "connect to" sets.
	ConnectionVars []string `json:"connection_vars,omitempty" doc:"Template apps: the variables other apps can reference, like DATABASE_URL"`
	MainVar        string   `json:"main_var,omitempty" doc:"Template apps: the variable connect_to sets in the connected apps"`
	// References are the aliases of the other apps of the project this app's
	// variables refer to (${{<alias>.VAR}}).
	References []string  `json:"references" doc:"Short names of the apps in the project this app's variables refer to"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// BeforeDeployCommand and AfterDeployCommand are the app's deploy
	// commands (deploy_commands.go); "" when it has none.
	BeforeDeployCommand string `json:"before_deploy_command" doc:"Shell command run before each new version takes traffic (database migrations), in a one-off container of the new version; empty: none"`
	AfterDeployCommand  string `json:"after_deploy_command" doc:"Shell command run in the live container after each new version takes traffic; empty: none"`
}

// ProjectRef names a project (App.project).
type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Project is a space that holds apps: they share a private network and can
// refer to each other's variables.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Network is the Docker network the apps share, created when the project
	// has two apps or more.
	Network string `json:"network" doc:"The private Docker network the project's apps share (bp-p-<id>)"`
	Apps    []App  `json:"apps"`
}

// CreateProjectInput is POST /projects.
type CreateProjectInput struct {
	Name string `json:"name" doc:"1 to 80 characters"`
}

// UpdateProjectInput is PATCH /projects/{id}.
type UpdateProjectInput struct {
	Name *string `json:"name,omitempty"`
}

// ServiceTemplate is GET /service-templates.
type ServiceTemplate struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category" doc:"database, cache or search"`
	Description    string   `json:"description"`
	Image          string   `json:"image" doc:"The image with its pinned version, like postgres:18.6"`
	Version        string   `json:"version"`
	Port           int64    `json:"port"`
	MemoryMB       int64    `json:"memory_mb"`
	CPUMillis      int64    `json:"cpu_millis"`
	VolumeMB       int64    `json:"volume_mb"`
	ConnectionVars []string `json:"connection_vars" doc:"Variables other apps can reference as ${{<name>.VAR}}"`
	MainVar        string   `json:"main_var" doc:"The variable connect_to sets in the connected apps"`
}

// AddServiceInput is POST /projects/{id}/services.
type AddServiceInput struct {
	Template string `json:"template" doc:"A service template id from list_service_templates, like postgres or valkey"`
	// Name is the alias in the project; default: the template id (postgres-2
	// when that is taken).
	Name string `json:"name,omitempty" doc:"The short name in the project, like db; default: the template id"`
	// ConnectTo are slugs of apps in the project that get
	// <main_var>=${{<name>.<main_var>}} and restart once the service runs.
	ConnectTo []string `json:"connect_to,omitempty" doc:"Slugs of apps in the project that get <main_var>=${{<name>.<main_var>}} (like DATABASE_URL) and restart once the service runs"`
	MemoryMB  *int64   `json:"memory_mb,omitempty" doc:"Default: the template's"`
	VolumeMB  *int64   `json:"volume_mb,omitempty" doc:"Default: the template's"`
}

// AppService is one service of an app (App.services).
type AppService struct {
	Name string `json:"name" doc:"The service name; also its hostname for the app's other services"`
	Kind string `json:"kind" doc:"build (from a Dockerfile) or image (a ready-made image)"`
	// Image is the image reference of an image service.
	Image      string   `json:"image,omitempty" doc:"The image of an image service"`
	Public     bool     `json:"public" doc:"The app's address and domains point at this service"`
	Port       *int64   `json:"port" doc:"The port the service listens on, or null when not known yet"`
	HealthPath string   `json:"health_path"`
	MemoryMB   int64    `json:"memory_mb"`
	CPUMillis  int64    `json:"cpu_millis"`
	Volumes    []string `json:"volumes" doc:"Names of the volumes the service mounts"`
	DependsOn  []string `json:"depends_on" doc:"Services that start before this one"`
	// Container is the Docker container of the current deployment.
	Container string `json:"container,omitempty"`
	// Live is the service's latest 15-second sample, for a running app with
	// several services; nil otherwise (App.live has the app's).
	Live *MetricPoint `json:"live,omitempty" doc:"Latest 15-second sample of this service, for a running app with several services"`
}

// AppVolume is one storage volume of an app (App.volumes).
type AppVolume struct {
	Name      string `json:"name" doc:"The volume name; pass it as volume= to the files and backups endpoints"`
	Service   string `json:"service" doc:"The service that mounts it"`
	MountPath string `json:"mount_path" doc:"Where the service sees it, like /data"`
	SizeMB    int64  `json:"size_mb" doc:"Its storage limit"`
	// UsedMB is what the volume holds, measured hourly; nil until measured.
	UsedMB *float64 `json:"used_mb"`
}

type DeployError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Next    string `json:"next_tool,omitempty"`
	// Service is the service the deployment failed in, for an app with
	// several services; the message then starts with "Service <name>: ".
	Service string `json:"service,omitempty" doc:"The service it failed in, for an app with several services"`
}

// DeploymentService is what one service did in a deployment of an app with
// several services (Deployment.services).
type DeploymentService struct {
	Service string  `json:"service"`
	Image   *string `json:"image" doc:"The image it runs; null until it is built or pulled"`
	// State is the deployment's state while it moves. When the deployment
	// failed, the service it failed in is failed and the others cancelled
	// (the version that was live keeps running).
	State string `json:"state" doc:"queued, ..., running, failed, superseded or cancelled; see Deployment.state"`
	// Reused is true when the container of an earlier deployment was kept
	// because the service's image and settings did not change.
	Reused bool         `json:"reused" doc:"The running container of an earlier version was kept: nothing changed"`
	Error  *DeployError `json:"error" doc:"Why this service failed; null for the others"`
}

type Deployment struct {
	ID            string           `json:"id"`
	AppID         string           `json:"app_id"`
	AppSlug       string           `json:"app_slug"`
	Seq           int64            `json:"seq"`
	State         string           `json:"state"`
	SourceKind    string           `json:"source_kind"` // files | upload | git | rollback
	SourceRef     string           `json:"source_ref,omitempty"`
	CommitSHA     *string          `json:"commit_sha,omitempty"`
	CommitMessage *string          `json:"commit_message,omitempty"`
	Channel       string           `json:"channel"`
	Image         *string          `json:"image,omitempty"`
	Error         *DeployError     `json:"error"`
	Warnings      []deploy.Finding `json:"warnings"`
	LogTail       []string         `json:"log_tail,omitempty"` // last 50 lines (build or runtime)
	QueuedAt      time.Time        `json:"queued_at"`
	StartedAt     *time.Time       `json:"started_at"`
	FinishedAt    *time.Time       `json:"finished_at"`
	DurationSec   *float64         `json:"duration_seconds"`
	URL           string           `json:"url"`
	NextSteps     []string         `json:"next_steps,omitempty"`
	// Services is set for a deployment of an app with several services.
	Services []DeploymentService `json:"services,omitempty" doc:"Per service, for an app with several services"`

	// AfterDeploy is what the app's after-deploy command did in this
	// deployment's live container; "" when none ran.
	AfterDeploy string `json:"after_deploy,omitempty" enum:"pending,running,succeeded,failed" doc:"The after-deploy command: pending (runs once live), running, succeeded or failed (a warning; the version stays live). Absent when none ran."`
}

// Terminal reports whether the deployment stopped moving.
func (d Deployment) Terminal() bool {
	switch d.State {
	case "running":
		return !afterDeployBusy(d.AfterDeploy) // live, but its after-deploy command still runs
	case "failed", "superseded", "cancelled":
		return true
	}
	return false
}

type EnvVar struct {
	Name string `json:"name"`
	// Service is the service the variable belongs to; "" for a variable every
	// service of the app gets (always "" for an app with one service).
	Service string `json:"service" doc:"The service it belongs to; empty when every service gets it"`
	Masked  string `json:"masked"` // e.g. "••••••••" — values are never returned
	// References are the ${{<alias>.VAR}} references in the value: names of
	// other apps' variables, shown because they are not secret.
	References []string  `json:"references,omitempty" doc:"The ${{<alias>.VAR}} references in the value, like ${{postgres.DATABASE_URL}}"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type DNSRecord struct {
	Type  string `json:"type"` // CNAME | A | AAAA | TXT
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Domain struct {
	ID            string      `json:"id"`
	Hostname      string      `json:"hostname"`
	Status        string      `json:"status"` // pending_dns | issuing_cert | active | failed
	LastError     string      `json:"last_error,omitempty"`
	Records       []DNSRecord `json:"records"`
	Notes         []string    `json:"notes,omitempty"`
	LastCheckedAt *time.Time  `json:"last_checked_at"`
	CreatedAt     time.Time   `json:"created_at"`
	ActivatedAt   *time.Time  `json:"activated_at"`
}

type Upload struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Curl      string    `json:"curl"`
	ExpiresAt time.Time `json:"expires_at"`
	MaxBytes  int64     `json:"max_bytes"`
	NextSteps []string  `json:"next_steps"`
}

type FileInput struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"` // utf8 (default) | base64
}

type LogLine struct {
	Time   *time.Time `json:"time,omitempty"`
	Stream string     `json:"stream,omitempty"` // stdout | stderr | build
	Text   string     `json:"text"`
	// Service is the service a runtime line came from, set only for an app
	// with several services.
	Service string `json:"service,omitempty"`
}

type MetricPoint struct {
	TS       time.Time `json:"ts"`
	CPU      float64   `json:"cpu"` // % of one core
	MemoryMB float64   `json:"memory_mb"`
	RxBytes  int64     `json:"rx_bytes"`
	TxBytes  int64     `json:"tx_bytes"`
	CPUMax   float64   `json:"cpu_max,omitempty"`
	MemMaxMB float64   `json:"memory_max_mb,omitempty"`
}

type Metrics struct {
	App string `json:"app"`
	// Service is set when the metrics are of one service, not the whole app.
	Service       string        `json:"service,omitempty"`
	Window        string        `json:"window"` // 1h | 24h | 7d
	MemoryLimitMB int64         `json:"memory_limit_mb"`
	Points        []MetricPoint `json:"points"`
	Latest        *MetricPoint  `json:"latest"`
}

// Traffic is the visitor stats of one app (GET /apps/{app}/traffic).
// Visitors are unique per UTC day: over several days a returning visitor counts once per day.
type Traffic struct {
	App           string           `json:"app"`
	Window        string           `json:"window"`      // 24h | 7d | 30d | today (totals only)
	Granularity   string           `json:"granularity"` // hour for 24h, day otherwise
	VisitorsToday int64            `json:"visitors_today"`
	Totals        TrafficTotals    `json:"totals"`
	Previous      *TrafficTotals   `json:"previous"` // the equal period before; null for today and when stats do not go back that far
	Points        []TrafficPoint   `json:"points"`
	Pages         []TrafficItem    `json:"pages"`
	Countries     []TrafficCountry `json:"countries"`
	Referrers     []TrafficItem    `json:"referrers"` // "(direct)" is page views without a referrer
	NotFound      []TrafficItem    `json:"not_found"` // paths answered 404, files included
}

type TrafficTotals struct {
	Requests     int64   `json:"requests"`
	Visitors     int64   `json:"visitors"`
	Errors       int64   `json:"errors"`        // answers with status 500 or more
	ErrorRate    float64 `json:"error_rate"`    // errors as a percentage of requests
	ClientErrors int64   `json:"client_errors"` // answers with status 400 to 499
	Bots         int64   `json:"bots"`          // requests from crawlers and scripts, part of requests
	PageViews    int64   `json:"page_views"`    // pages opened by people (GET, below 400, no files, no bots)
	// Answer times of people's requests for pages and data (no files, no bots),
	// from buckets (see traffic.SpeedBounds), so estimates. Null without answers.
	Timed    int64  `json:"timed"`     // answers with a known answer time
	Slow     int64  `json:"slow"`      // answers that took a second or longer
	MedianMS *int64 `json:"median_ms"` // half of the answers were faster than this
	P95MS    *int64 `json:"p95_ms"`    // 95 in 100 answers were faster than this
}

type TrafficPoint struct {
	TS           time.Time `json:"ts"`
	Requests     int64     `json:"requests"`
	Visitors     int64     `json:"visitors"`
	Errors       int64     `json:"errors"`
	ClientErrors int64     `json:"client_errors"`
	Bots         int64     `json:"bots"`
	PageViews    int64     `json:"page_views"`
	MedianMS     *int64    `json:"median_ms"` // null without timed answers in this hour or day
	P95MS        *int64    `json:"p95_ms"`
}

// TrafficItem is a page, a referrer or a 404 path with its count. Lists cover
// whole UTC days, so for 24h they span today and yesterday.
type TrafficItem struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type TrafficCountry struct {
	Code     string `json:"code"` // ISO 3166-1 alpha-2, or "unknown"
	Requests int64  `json:"requests"`
	Visitors int64  `json:"visitors"`
}

type AuditEntry struct {
	ID         int64          `json:"id"`
	TS         time.Time      `json:"ts"`
	UserID     *string        `json:"user_id"`
	Channel    string         `json:"channel"`
	TokenID    *string        `json:"token_id"`
	IP         string         `json:"ip"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Details    map[string]any `json:"details"`
}

type AdminUser struct {
	User
	Quota    Quota `json:"quota"`
	Usage    Usage `json:"usage"`
	AppCount int64 `json:"app_count"`
	// AppDomainIDs are the extra app domains this person may use (the default
	// domain is always allowed; admins may use every domain).
	AppDomainIDs []string `json:"app_domain_ids"`
}

type ServerStats struct {
	CPUPercent      float64 `json:"cpu_percent"`
	Load1           float64 `json:"load1"`
	MemTotalMB      int64   `json:"mem_total_mb"`
	MemUsedMB       int64   `json:"mem_used_mb"`
	MemReservedMB   int64   `json:"mem_reserved_mb"` // sum of app memory limits
	DiskTotalGB     float64 `json:"disk_total_gb"`
	DiskUsedGB      float64 `json:"disk_used_gb"`
	DockerImagesGB  float64 `json:"docker_images_gb"`
	DockerCacheGB   float64 `json:"docker_build_cache_gb"`
	DockerVolumesGB float64 `json:"docker_volumes_gb"`
	BuildsRunning   int64   `json:"builds_running"`
	BuildsQueued    int64   `json:"builds_queued"`
	AppsRunning     int64   `json:"apps_running"`
	AppsTotal       int64   `json:"apps_total"`
	AppsMax         int64   `json:"apps_max"` // BITPLOY_MAX_APPS
}

type Template struct {
	Stack        string `json:"stack"`
	Dockerfile   string `json:"dockerfile"`
	Dockerignore string `json:"dockerignore"`
	Readme       string `json:"readme"`
}

type ValidateResult struct {
	OK             bool             `json:"ok"`
	Findings       []deploy.Finding `json:"findings"`
	Port           int              `json:"port,omitempty"`
	HasHealthcheck bool             `json:"has_healthcheck"`
	Stack          string           `json:"stack,omitempty"`
	NextSteps      []string         `json:"next_steps,omitempty"`
}

// Page is a cursor-paginated list.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// ---- inputs ----

type CreateAppInput struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"` // web (default) | worker
	Slug      string `json:"slug,omitempty"`
	Domain    string `json:"domain,omitempty" doc:"The domain of the address, one of app_domains in whoami. Empty: the default domain."`
	Port      *int64 `json:"port,omitempty"`
	Volume    bool   `json:"volume,omitempty"`
	VolumeMB  *int64 `json:"volume_mb,omitempty"` // storage limit of /data, default 5120
	MemoryMB  *int64 `json:"memory_mb,omitempty"`
	CPUMillis *int64 `json:"cpu_millis,omitempty"`
	// BuildMode is how the source is built: dockerfile (default), auto, node, static_build, static or compose.
	BuildMode string `json:"build_mode,omitempty" enum:"dockerfile,auto,node,static_build,static,compose" doc:"dockerfile: the source needs a Dockerfile (default). auto: a Dockerfile if there is one, else detect: a Node.js app, a static build (Vite, Astro, SvelteKit with adapter-static, ...) or a plain website (index.html at the top). node: always a Node.js app from package.json. static_build: build with the build script and serve the output folder with nginx (a static site). static: always a plain website. compose: run the services of the compose file in the source (an app with a database or Redis next to it); auto also picks compose on the first deploy when the compose file has several services."`
	// ComposePath is the compose file, for build mode compose.
	ComposePath string `json:"compose_path,omitempty" doc:"Build mode compose: the compose file in the source, like deploy/compose.yaml. Empty: compose.yaml, compose.yml, docker-compose.yaml or docker-compose.yml"`
	// OutputDir is the folder a static build serves.
	OutputDir string `json:"output_dir,omitempty" doc:"Build modes auto and static_build: the folder the build writes to and nginx serves, relative to the source, like dist, build or out. Empty: the default of the detected framework."`
	// HomePage is the page for "/" when a static site has no index.html and several pages.
	HomePage string `json:"home_page,omitempty" doc:"Build modes auto and static only: the .html file at the top of the source to serve at / when there is no index.html and several pages. Empty: index.html, else the only page."`
	// Project is the project id to create the app in; empty: a new project
	// named after the app.
	Project string `json:"project,omitempty" doc:"The project to put the app in (id from list_projects); empty: a new project named after the app"`
	// Alias is the short name in the project; default: the slug.
	Alias string `json:"alias,omitempty" doc:"The app's short name in the project (a DNS label); default: the subdomain"`

	// Deploy commands; see UpdateAppInput.
	BeforeDeployCommand string `json:"before_deploy_command,omitempty" doc:"Shell command run before each new version takes traffic, like php artisan migrate --force (see update app)"`
	AfterDeployCommand  string `json:"after_deploy_command,omitempty" doc:"Shell command run in the live container after each new version takes traffic (see update app)"`
}

// UpdateAppInput: nil fields are left unchanged.
type UpdateAppInput struct {
	Name           *string `json:"name,omitempty"`
	Slug           *string `json:"slug,omitempty"`
	Domain         *string `json:"domain,omitempty" doc:"Move the address to another domain (one of app_domains in whoami). Applies at once; the old address stops working."`
	Type           *string `json:"type,omitempty"`
	Port           *int64  `json:"port,omitempty"` // 0 clears it (use EXPOSE)
	HealthPath     *string `json:"health_path,omitempty"`
	DockerfilePath *string `json:"dockerfile_path,omitempty"`
	ContextDir     *string `json:"context_dir,omitempty"`
	BuildMode      *string `json:"build_mode,omitempty" enum:"dockerfile,auto,node,static_build,static,compose" doc:"dockerfile, auto, node, static_build, static or compose (see create app). Applies on the next deploy. compose turns an app with one service into one with the services of its compose file on the next deploy (its /data volume is kept); there is no way back."`
	ComposePath    *string `json:"compose_path,omitempty" doc:"Build mode compose: the compose file in the source; empty: the usual names. Applies on the next deploy."`
	OutputDir      *string `json:"output_dir,omitempty" doc:"output folder of a static build (see create app); empty clears it. Applies on the next deploy."`
	HomePage       *string `json:"home_page,omitempty" doc:"page for / of a static site without index.html (see create app); empty clears it"`
	Volume         *bool   `json:"volume,omitempty"`
	// DeleteData confirms that turning /data off (volume=false) may delete the
	// files in /data and all backups of the app. Without it, turning /data off
	// is refused (volume_has_data) while there is anything to delete.
	DeleteData *bool  `json:"delete_data,omitempty" doc:"With volume=false: also delete the files in /data and all backups. Needed when /data holds files or the app has backups; ask the user first."`
	VolumeMB   *int64 `json:"volume_mb,omitempty"`
	MemoryMB   *int64 `json:"memory_mb,omitempty"`
	CPUMillis  *int64 `json:"cpu_millis,omitempty"`
	// Project is refused (422 project_move_unsupported): apps no longer move
	// between projects. It is kept, hidden from the API docs, so an old
	// client learns that instead of getting a generic unknown-field error.
	Project *string `json:"project,omitempty" hidden:"true"`
	Alias   *string `json:"alias,omitempty" doc:"The app's short name in its project. References to it in the project's other apps are rewritten, and those apps restart."`

	// BeforeDeployCommand runs with sh -c before each new version takes
	// traffic; AfterDeployCommand in the live container after. "" clears one.
	BeforeDeployCommand *string `json:"before_deploy_command,omitempty" doc:"Shell command (sh -c) run on every deploy before the new version takes traffic, in a one-off container of the new image with the app's variables, network and /data: database migrations like php artisan migrate --force, python manage.py migrate or npx prisma migrate deploy. A non-zero exit or 10 minutes stops the deploy and the old version keeps running. Not run on rollbacks. Empty clears it. Applies on the next deploy."`
	AfterDeployCommand  *string `json:"after_deploy_command,omitempty" doc:"Shell command (sh -c) run in the live container after each new version takes traffic (up to 5 minutes). A failure is a warning on the deployment; the new version stays live. Not run on rollbacks. Empty clears it. Applies on the next deploy."`
}

type CreateTokenInput struct {
	Name          string `json:"name"`
	Scope         Scope  `json:"scope"`                     // read | deploy
	ExpiresInDays int    `json:"expires_in_days,omitempty"` // 0 (never), 30 or 90
}

type CreateInviteInput struct {
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
}

type AcceptInviteInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LogOptions struct {
	Kind  string     // build | runtime
	Tail  int        // default 200, max 1000
	Since *time.Time // runtime only
	// Service picks one service's runtime logs; "" is the app's public (or
	// only) service.
	Service string
}

// Backup is one archive of an app's /data folder.
type Backup struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // manual | scheduled | uploaded | before_restore
	// Format is how the backup holds the data: volume (a .tar.gz of the
	// files) or dump (a gzipped SQL dump of a database service, .sql.gz).
	Format string `json:"format" enum:"volume,dump" doc:"volume: a .tar.gz of the files. dump: a gzipped SQL dump of every database of a database service (PostgreSQL, MariaDB, MySQL), made while it runs."`
	// Tool is the dump tool and its version, for dumps.
	Tool      string    `json:"tool,omitempty" doc:"Dumps: the tool and its version, like pg_dumpall (PostgreSQL) 18.1"`
	Status    string    `json:"status"` // running | ready | failed
	SizeBytes int64     `json:"size_bytes"`
	Error     string    `json:"error,omitempty"` // why a failed backup failed, in plain words
	CreatedAt time.Time `json:"created_at"`
	// Volume is the volume the backup holds, set only for an app with
	// several services (a single-service app has one volume, data).
	Volume string `json:"volume,omitempty"`
}

// BackupSettings are the automatic backups of one app.
type BackupSettings struct {
	Schedule string `json:"schedule"`  // off | daily | weekly
	KeepMode string `json:"keep_mode"` // last (keep the newest N) | days (keep N days)
	Keep     int64  `json:"keep"`
	// TimeUTC is the app's backup time (HH:MM, UTC), every day or on Sunday.
	// Each app has its own, between 02:00 and 06:00, so that the apps of a
	// server do not all back up at once.
	TimeUTC string `json:"time_utc"`
	// NextAt is when the next automatic backup is due; nil when they are off.
	NextAt *time.Time `json:"next_at,omitempty"`
}

// BackupRestore is the state of the latest restore of an app since bitploy started.
type BackupRestore struct {
	BackupID   string     `json:"backup_id"`
	State      string     `json:"state"` // running | done | failed
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// AppRestarts is true when the app was running and is stopped for the
	// restore and started again after it.
	AppRestarts bool `json:"app_restarts"`
}

// BackupList is GET /apps/{app}/backups.
type BackupList struct {
	Items    []Backup       `json:"items"`
	Settings BackupSettings `json:"settings"`
	// Restore is set while a restore runs and until the next one starts.
	Restore *BackupRestore `json:"restore"`
	// Busy is true while a backup, upload or restore of this app is in progress.
	Busy       bool  `json:"busy"`
	TotalBytes int64 `json:"total_bytes"` // what all backups of this app take
	// UploadMaxBytes is the biggest backup file that can be uploaded right now
	// (for the first volume).
	UploadMaxBytes int64 `json:"upload_max_bytes"`
	// Volumes are the app's volumes with the biggest backup file each can take.
	Volumes []BackupVolume `json:"volumes"`
	// DumpTool is set for a database service: its backups are dumps made
	// with this tool while it runs (a copy of the files while it is stopped).
	DumpTool string `json:"dump_tool,omitempty" doc:"Database services: backups are dumps made with this tool (pg_dumpall, mariadb-dump, mysqldump) while the database runs, a copy of the files while it is stopped"`
}

// BackupVolume is a volume backups can be made of and uploaded to.
type BackupVolume struct {
	Name           string `json:"name" doc:"Pass it as volume to create, upload or the database viewer"`
	Service        string `json:"service" doc:"The service that mounts it; empty when none does"`
	MountPath      string `json:"mount_path"`
	SizeMB         int64  `json:"size_mb"`
	UploadMaxBytes int64  `json:"upload_max_bytes"`
}

// BackupSettingsInput: nil fields are left unchanged.
type BackupSettingsInput struct {
	Schedule *string `json:"schedule,omitempty" doc:"off, daily or weekly"`
	KeepMode *string `json:"keep_mode,omitempty" doc:"last (keep the newest backups) or days (keep backups for some days)"`
	Keep     *int64  `json:"keep,omitempty" doc:"How many backups, or how many days, depending on keep_mode"`
}
