// Package catalog is the list of one-click apps: popular self-contained tools
// that keep their data in SQLite under /data. Each entry is defined here and
// has its Dockerfile in <id>/Dockerfile, embedded like internal/templates.
//
// Every entry pins an exact version, listens on 0.0.0.0 at its port, keeps all
// persistent data under /data and runs inside bitploy's container sandbox
// (all capabilities dropped but a few, no-new-privileges, 256 processes).
package catalog

import (
	"embed"
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
	Dockerfile string // filled in from the embedded file
}

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
			"Turn sign-ups off: set the environment variable SIGNUPS_ALLOWED to false (set_env), then deploy again.",
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
			"Add your first monitor. HTTP, TCP port and DNS checks work here; ping checks do not, because bitploy's sandbox gives the app no raw network access.",
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
			"Open {url} and create the first account: it becomes the host (admin). Do it right away.",
			"In Settings, turn off new sign-ups if you are the only user.",
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
			"Create the first superuser: open the app's logs, find the line with the installer link ending in /_/#/pbinstal/..., replace the host with {url} and open it.",
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
		MemoryMB:    384,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/health",
		FirstSteps: []string{
			"Open {url} and set the server password at once: the first visitor chooses it.",
			"Create a budget file, or import one from YNAB or another Actual server.",
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
	return e
}
