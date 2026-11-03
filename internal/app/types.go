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
	PublicURL      string `json:"public_url"`
	Scope          Scope  `json:"scope"`
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
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Type          string `json:"type"` // web | worker
	URL           string `json:"url"`
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
	BuildMode         string `json:"build_mode"` // dockerfile | auto | node | static_build | static
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
	MemoryMB       int64      `json:"memory_mb"`
	CPUMillis      int64      `json:"cpu_millis"`
	// Static is true while the live version is a static site: built from
	// bitploy's own nginx templates (a plain website, or a Vite site built by
	// auto mode), never from the app's own Dockerfile. A static site has fixed
	// limits (memory_mb and cpu_millis cannot be changed), cannot have a /data
	// volume and does not count towards the memory and CPU allowance.
	Static       bool            `json:"static" doc:"A static site: fixed small size, no /data, not counted in the memory and CPU allowance"`
	GitSource    *GitSourceState `json:"git_source"`
	Current      *Deployment     `json:"current_deployment"`
	Domains      []Domain        `json:"domains"`
	StartedAt    *time.Time      `json:"started_at,omitempty"` // current container start, for uptime
	RestartCount int64           `json:"restart_count"`
	// Live is the latest 15-second sample (CPU % of one core, memory MB); nil when not running.
	Live      *MetricPoint `json:"live"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type DeployError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Next    string `json:"next_tool,omitempty"`
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
}

// Terminal reports whether the deployment stopped moving.
func (d Deployment) Terminal() bool {
	switch d.State {
	case "running", "failed", "superseded", "cancelled":
		return true
	}
	return false
}

type EnvVar struct {
	Name      string    `json:"name"`
	Masked    string    `json:"masked"` // e.g. "••••••••" — values are never returned
	UpdatedAt time.Time `json:"updated_at"`
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
	App           string        `json:"app"`
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
}

type TrafficPoint struct {
	TS           time.Time `json:"ts"`
	Requests     int64     `json:"requests"`
	Visitors     int64     `json:"visitors"`
	Errors       int64     `json:"errors"`
	ClientErrors int64     `json:"client_errors"`
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
	Port      *int64 `json:"port,omitempty"`
	Volume    bool   `json:"volume,omitempty"`
	VolumeMB  *int64 `json:"volume_mb,omitempty"` // storage limit of /data, default 5120
	MemoryMB  *int64 `json:"memory_mb,omitempty"`
	CPUMillis *int64 `json:"cpu_millis,omitempty"`
	// BuildMode is how the source is built: dockerfile (default), auto, node, static_build or static.
	BuildMode string `json:"build_mode,omitempty" enum:"dockerfile,auto,node,static_build,static" doc:"dockerfile: the source needs a Dockerfile (default). auto: a Dockerfile if there is one, else detect: a Node.js app, a static build (Vite, Astro, SvelteKit with adapter-static, ...) or a plain website (index.html at the top). node: always a Node.js app from package.json. static_build: build with the build script and serve the output folder with nginx (a static site). static: always a plain website."`
	// OutputDir is the folder a static build serves.
	OutputDir string `json:"output_dir,omitempty" doc:"Build modes auto and static_build: the folder the build writes to and nginx serves, relative to the project, like dist, build or out. Empty: the default of the detected framework."`
	// HomePage is the page for "/" when a static site has no index.html and several pages.
	HomePage string `json:"home_page,omitempty" doc:"Build modes auto and static only: the .html file at the top of the source to serve at / when there is no index.html and several pages. Empty: index.html, else the only page."`
}

// UpdateAppInput: nil fields are left unchanged.
type UpdateAppInput struct {
	Name           *string `json:"name,omitempty"`
	Slug           *string `json:"slug,omitempty"`
	Type           *string `json:"type,omitempty"`
	Port           *int64  `json:"port,omitempty"` // 0 clears it (use EXPOSE)
	HealthPath     *string `json:"health_path,omitempty"`
	DockerfilePath *string `json:"dockerfile_path,omitempty"`
	ContextDir     *string `json:"context_dir,omitempty"`
	BuildMode      *string `json:"build_mode,omitempty" enum:"dockerfile,auto,node,static_build,static" doc:"dockerfile, auto, node, static_build or static (see create app). Applies on the next deploy."`
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
}

// Backup is one archive of an app's /data folder.
type Backup struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`   // manual | scheduled | uploaded | before_restore
	Status    string    `json:"status"` // running | ready | failed
	SizeBytes int64     `json:"size_bytes"`
	Error     string    `json:"error,omitempty"` // why a failed backup failed, in plain words
	CreatedAt time.Time `json:"created_at"`
}

// BackupSettings are the automatic backups of one app.
type BackupSettings struct {
	Schedule string `json:"schedule"`  // off | daily | weekly
	KeepMode string `json:"keep_mode"` // last (keep the newest N) | days (keep N days)
	Keep     int64  `json:"keep"`
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
	// UploadMaxBytes is the biggest backup file that can be uploaded right now.
	UploadMaxBytes int64 `json:"upload_max_bytes"`
}

// BackupSettingsInput: nil fields are left unchanged.
type BackupSettingsInput struct {
	Schedule *string `json:"schedule,omitempty" doc:"off, daily or weekly"`
	KeepMode *string `json:"keep_mode,omitempty" doc:"last (keep the newest backups) or days (keep backups for some days)"`
	Keep     *int64  `json:"keep,omitempty" doc:"How many backups, or how many days, depending on keep_mode"`
}
