// Package catalog is the list of one-click apps: popular self-contained tools
// that keep their data in SQLite under /data. Each entry is defined here and
// has its Dockerfile in <id>/Dockerfile, embedded like internal/templates.
//
// Every entry pins an exact version, listens on 0.0.0.0 at its port, keeps all
// persistent data under /data and runs inside bitploy's container sandbox
// (all capabilities dropped but a few, no-new-privileges, 256 processes).
//
// Multi-service entries. An entry that needs a database server lists it in
// Services: each is a service template (internal/servicetpl, like postgres)
// with a short name. Deploying such an entry creates a project named after
// the app, adds each service to it as an app of its own (private, pinned
// version, generated passwords, data in its own volume), then creates the
// entry's app in the same project. The app's Env values reach the services
// through references, ${{<name>.<VAR>}} with VAR one of the template's
// connection variables (servicetpl.Template.Connection), like
//
//	Services: []ServiceSpec{{Template: "postgres", Name: "db"}},
//	Env:      []EnvSpec{{Name: "DATABASE_URL", Value: "${{db.DATABASE_URL}}", ...}},
//
// The app's first deployment waits until its services run. The entry's own
// Dockerfile, Port, MemoryMB and Volume describe only the app itself; the
// services bring their own sizes. umami is the example.
package catalog

import (
	"embed"
	"regexp"
	"strings"
)

//go:embed */Dockerfile
var files embed.FS

// EnvSpec is one environment variable an entry sets on its app.
type EnvSpec struct {
	Name string
	// Value may contain {url}, the app's public URL, filled in when the app is created.
	Value string
	// Generate is "" (use Value) or "secret" (32 random base62 characters).
	// A generated value is stored encrypted and never shown.
	Generate    string
	Description string
}

// ServiceSpec is a service a multi-service entry adds next to its app, in
// the same project (see the package comment).
type ServiceSpec struct {
	Template string // a service template id (internal/servicetpl), like postgres
	Name     string // the service's short name in the project, used in ${{<name>.VAR}}
}

// Entry is one app of the catalog.
type Entry struct {
	ID          string
	Name        string
	Tagline     string // one line
	Description string // two or three plain sentences
	Category    string
	Version     string // the exact version the Dockerfile pins
	Website     string
	Port        int
	MemoryMB    int
	Volume      bool
	VolumeMB    int
	HealthPath  string
	Env         []EnvSpec
	// FirstSteps may contain {url}.
	FirstSteps []string
	// Services are the database servers the app needs (multi-service entries).
	Services   []ServiceSpec
	Dockerfile string // filled in from the embedded file
}

// RefPattern matches a reference to a service of the entry, ${{<name>.VAR}}.
var RefPattern = regexp.MustCompile(`\$\{\{([a-z0-9][a-z0-9-]*)\.([A-Za-z_][A-Za-z0-9_]*)\}\}`)

// URLPlaceholder stands for the app's public URL in Env values and FirstSteps.
const URLPlaceholder = "{url}"

// Fill replaces {url} in s.
func Fill(s, url string) string { return strings.ReplaceAll(s, URLPlaceholder, url) }

var entries = []Entry{
	{
		ID:          "vaultwarden",
		Name:        "Vaultwarden",
		Tagline:     "Password manager that works with the Bitwarden apps",
		Description: "Vaultwarden is a small server for the Bitwarden browser extensions and phone apps. It keeps all vaults in a SQLite database in /data. Use it only over HTTPS, which bitploy provides.",
		Category:    "Security",
		Version:     "1.37.5",
		Website:     "https://github.com/dani-garcia/vaultwarden",
		Port:        8080,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    512,
		HealthPath:  "/alive",
		Env: []EnvSpec{
			{Name: "DOMAIN", Value: "{url}", Description: "The address of this app. Vaultwarden needs it for links and the web vault."},
		},
		FirstSteps: []string{
			"Open {url} and create your account. Anyone who reaches the address can sign up until you turn sign-ups off.",
			"Then turn sign-ups off: add the environment variable SIGNUPS_ALLOWED with the value false and restart the app.",
			"Back up /data: it holds every vault.",
		},
	},
	{
		ID:          "uptime-kuma",
		Name:        "Uptime Kuma",
		Tagline:     "Watch your sites and services and get an alert when one goes down",
		Description: "Uptime Kuma checks websites, ports and other services on a schedule and shows their history on a status page. It sends alerts to email, Telegram, Discord and many others. It keeps everything in a SQLite database in /data.",
		Category:    "Monitoring",
		Version:     "2.5.9",
		Website:     "https://github.com/louislam/uptime-kuma",
		Port:        3001,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/",
		FirstSteps: []string{
			"Open {url} and create the admin account at once: the first visitor gets to do it.",
			"Add your first monitor, and a notification so you hear when it goes down.",
		},
	},
	{
		ID:          "memos",
		Name:        "Memos",
		Tagline:     "Private notes you write like short posts",
		Description: "Memos is a note-taking tool for quick thoughts, with tags, Markdown and file attachments. Everything stays on your server in a SQLite database in /data.",
		Category:    "Notes",
		Version:     "0.31.0",
		Website:     "https://github.com/usememos/memos",
		Port:        5230,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/healthz",
		FirstSteps: []string{
			"Open {url} and create the first account right away: it becomes the admin.",
			"If you are the only user, turn on Settings > System > Disallow user registration.",
		},
	},
	{
		ID:          "arnotes",
		Name:        "Arnotes",
		Tagline:     "Projects, tasks and notes: list, board, table, calendar and timeline",
		Description: "Arnotes is a project manager for a team or for yourself: tasks in a list, on a board, in a table, on a calendar or on a timeline, with comments, attachments and notes next to them. The database and attachments are in /data, which bitploy backs up, so Arnotes' own daily snapshots are off.",
		Category:    "Productivity",
		Version:     "0.1.0",
		Website:     "https://github.com/AVMG20/arnotes",
		Port:        3000,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/api/ready",
		Env: []EnvSpec{
			{Name: "ARNOTES_BACKUP_INTERVAL_HOURS", Value: "0", Description: "Hours between Arnotes' own database snapshots in /data/backups. 0 turns them off: bitploy already backs up /data, and the snapshots would count against your storage twice."},
		},
		FirstSteps: []string{
			"Open {url} and create the first account right away: it becomes the instance admin. Anyone who reaches the address can sign up until you turn sign-ups off.",
			"Then turn sign-ups off: add the environment variable ARNOTES_ALLOW_SIGNUP with the value 0 and restart the app. People you invite can still join through their invite link.",
			"For an off-site copy of the database, set the ARNOTES_BACKUP_S3_* variables and ARNOTES_BACKUP_INTERVAL_HOURS (for example 24); see DEPLOY.md in the Arnotes repository.",
		},
	},
	{
		ID:          "pocketbase",
		Name:        "PocketBase",
		Tagline:     "A small backend with a database, login and file storage",
		Description: "PocketBase gives your app a SQLite database, user accounts, file uploads and a REST API in one program, with an admin dashboard. Use it as the backend of a website or mobile app. All data and uploaded files are in /data.",
		Category:    "Backend",
		Version:     "0.40.6",
		Website:     "https://pocketbase.io",
		Port:        8090,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/api/health",
		FirstSteps: []string{
			"Create the first superuser: in the app's logs, find the installer link ending in /_/#/pbinstall/..., replace the part before /_/ with {url} and open it. The link works for 30 minutes; restart the app for a new one.",
			"The admin dashboard is at {url}/_/ and the API at {url}/api/.",
		},
	},
	{
		ID:          "actual-budget",
		Name:        "Actual Budget",
		Tagline:     "Budget your money with envelopes, and keep it private",
		Description: "Actual Budget is a personal finance app with envelope budgeting, bank-file import and reports. The server keeps your budget files and sync data in /data and the app runs in the browser.",
		Category:    "Finance",
		Version:     "26.11.0",
		Website:     "https://actualbudget.org",
		Port:        5006,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/health",
		FirstSteps: []string{
			"Open {url} and set the server password at once: the first visitor chooses it.",
			"Create a budget file, or import an export from YNAB 4, nYNAB or Actual.",
		},
	},
	{
		ID:          "it-tools",
		Name:        "IT-Tools",
		Tagline:     "Handy tools for developers: converters, generators, formatters",
		Description: "IT-Tools is a collection of small utilities in the browser, such as base64, JSON formatting, hashes, UUIDs and cron checks. It is a static site: it stores nothing on the server and needs no /data.",
		Category:    "Developer tools",
		Version:     "2024.10.22",
		Website:     "https://github.com/CorentinTh/it-tools",
		Port:        8080,
		MemoryMB:    64,
		Volume:      false,
		HealthPath:  "/",
		FirstSteps: []string{
			"Open {url} and pick a tool.",
		},
	},
	{
		ID:          "umami",
		Name:        "Umami",
		Tagline:     "Website analytics without cookies, with its own PostgreSQL",
		Description: "Umami counts visits, pages and referrers of your websites without cookies or personal data. It keeps its data in a PostgreSQL database that bitploy adds next to it as a private service named db, in the same project.",
		Category:    "Analytics",
		Version:     "3.4.1",
		Website:     "https://umami.is",
		Port:        3000,
		MemoryMB:    512,
		HealthPath:  "/api/heartbeat",
		Services:    []ServiceSpec{{Template: "postgres", Name: "db"}},
		Env: []EnvSpec{
			{Name: "DATABASE_URL", Value: "${{db.DATABASE_URL}}", Description: "The address of the PostgreSQL service db, with its generated password, filled in when the app starts."},
			{Name: "APP_SECRET", Generate: "secret", Description: "Signs the login sessions."},
		},
		FirstSteps: []string{
			"Open {url} and sign in with the username admin and the password umami.",
			"Change that password at once under Settings, Profile: anyone who reaches the address can try it.",
			"Add a website under Settings, Websites, and put the tracking code it shows in your site.",
		},
	},
}

// All returns the entries in the order shown to users, each with its Dockerfile.
func All() []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = withDockerfile(e)
	}
	return out
}

// Get returns one entry by id (case-insensitive).
func Get(id string) (Entry, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, e := range entries {
		if e.ID == id {
			return withDockerfile(e), true
		}
	}
	return Entry{}, false
}

// IDs lists the entry ids in display order.
func IDs() []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func withDockerfile(e Entry) Entry {
	b, _ := files.ReadFile(e.ID + "/Dockerfile")
	e.Dockerfile = string(b)
	e.Env = append([]EnvSpec(nil), e.Env...)
	e.FirstSteps = append([]string(nil), e.FirstSteps...)
	e.Services = append([]ServiceSpec(nil), e.Services...)
	return e
}
