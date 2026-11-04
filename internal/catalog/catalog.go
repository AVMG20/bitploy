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
	// Version: https://hub.docker.com/v2/repositories/n8nio/n8n/tags (2.46.6 is "stable").
	{
		ID:          "n8n",
		Name:        "n8n",
		Tagline:     "Automate work between your apps with visual workflows",
		Description: "n8n connects hundreds of services, such as email, Slack, Google Sheets and webhooks, in workflows you build by dragging nodes on a canvas. Workflows run on a schedule, on a webhook or by hand. Workflows, credentials and run history are kept in a SQLite database in /data.",
		Category:    "Automation",
		Version:     "2.46.6",
		Website:     "https://n8n.io",
		Port:        5678,
		MemoryMB:    768,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/healthz",
		Env: []EnvSpec{
			{Name: "N8N_ENCRYPTION_KEY", Generate: "secret", Description: "Encrypts the credentials n8n stores. Keep it: without it saved credentials cannot be read."},
			{Name: "WEBHOOK_URL", Value: "{url}", Description: "The address n8n puts in webhook URLs."},
			{Name: "N8N_EDITOR_BASE_URL", Value: "{url}", Description: "The address n8n uses in links to the editor, such as in emails."},
		},
		FirstSteps: []string{
			"Open {url} and set up the owner account at once: the first visitor gets to do it.",
			"Schedules use the America/New_York time zone until you add the environment variable GENERIC_TIMEZONE with your own, such as Europe/Amsterdam, and restart the app.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/sissbruecker/linkding/tags
	{
		ID:          "linkding",
		Name:        "Linkding",
		Tagline:     "Save and tag your bookmarks in one place",
		Description: "Linkding is a bookmark manager: save links with tags and notes, search them, and share some publicly if you like. Browser extensions for Firefox and Chrome add the page you are on. Bookmarks are kept in a SQLite database in /data.",
		Category:    "Productivity",
		Version:     "1.47.0",
		Website:     "https://linkding.link",
		Port:        9090,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/health",
		Env: []EnvSpec{
			{Name: "LD_CSRF_TRUSTED_ORIGINS", Value: "{url}", Description: "The address of this app. Linkding refuses forms sent from other addresses."},
		},
		FirstSteps: []string{
			"Linkding has no sign-up page. Add the environment variables LD_SUPERUSER_NAME and LD_SUPERUSER_PASSWORD with a name and a long password of your own, restart the app, then sign in at {url}.",
			"Install the browser extension from Settings > Integrations to save pages in one click.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/privatebin/nginx-fpm-alpine/tags (2.1.0 is "stable").
	{
		ID:          "privatebin",
		Name:        "PrivateBin",
		Tagline:     "Share text that only the people with the link can read",
		Description: "PrivateBin is a pastebin where text is encrypted in your browser before it is sent, so the server never sees what you share. Pastes can expire after a set time or burn after the first read. The encrypted pastes are kept as files in /data.",
		Category:    "Security",
		Version:     "2.1.0",
		Website:     "https://privatebin.info",
		Port:        8080,
		MemoryMB:    128,
		Volume:      true,
		VolumeMB:    512,
		HealthPath:  "/",
		FirstSteps: []string{
			"Open {url}, type or paste your text, pick when it expires and send the link to the other person.",
			"There are no accounts: anyone who has the address can create pastes.",
		},
	},
	// Version: Excalidraw publishes only "latest"; the digest is from
	// https://hub.docker.com/v2/repositories/excalidraw/excalidraw/tags (image built 2026-05-06).
	{
		ID:          "excalidraw",
		Name:        "Excalidraw",
		Tagline:     "A whiteboard for sketches and diagrams that look hand-drawn",
		Description: "Excalidraw is a drawing board in the browser for diagrams, wireframes and quick sketches. Drawings are saved in your own browser, and you can export them as PNG, SVG or an .excalidraw file. It is a static site: it stores nothing on the server and needs no /data.",
		Category:    "Productivity",
		Version:     "2026.05.06",
		Website:     "https://excalidraw.com",
		Port:        8080,
		MemoryMB:    64,
		Volume:      false,
		HealthPath:  "/",
		FirstSteps: []string{
			"Open {url} and start drawing. Export your drawing before you clear the browser's data.",
			"Live collaboration and share links go through excalidraw.com's servers, not this app.",
		},
	},
	// Version: https://ghcr.io/v2/open-webui/open-webui/tags/list (v0.13.0-slim).
	{
		ID:          "open-webui",
		Name:        "Open WebUI",
		Tagline:     "A private chat interface for OpenAI and other AI models",
		Description: "Open WebUI is a chat app like ChatGPT that talks to any OpenAI-compatible API, such as OpenAI, OpenRouter or Groq, with your own API key. It keeps chats, users and settings in a SQLite database in /data. Searching your own documents needs a vector database, which this version does not include.",
		Category:    "AI",
		Version:     "0.13.0",
		Website:     "https://openwebui.com",
		Port:        8080,
		MemoryMB:    1024,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/health",
		Env: []EnvSpec{
			{Name: "WEBUI_SECRET_KEY", Generate: "secret", Description: "Signs sign-in sessions. Changing it signs everyone out."},
			{Name: "WEBUI_URL", Value: "{url}", Description: "The address of this app, used in links and for sign-in."},
		},
		FirstSteps: []string{
			"Open {url} and create the first account at once: it becomes the admin. Later sign-ups wait for the admin's approval.",
			"Add your API key under Admin Panel > Settings > Connections, then pick a model and start a chat.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/vikunja/vikunja/tags
	{
		ID:          "vikunja",
		Name:        "Vikunja",
		Tagline:     "To-do lists and projects, with lists, boards and a calendar",
		Description: "Vikunja organizes tasks into projects you can view as a list, a board, a table or a Gantt chart. Tasks get due dates, reminders, labels and attachments, and you can share projects with others. Tasks and attachments are kept in /data.",
		Category:    "Productivity",
		Version:     "2.7.1",
		Website:     "https://vikunja.io",
		Port:        3456,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/health",
		Env: []EnvSpec{
			{Name: "VIKUNJA_SERVICE_PUBLICURL", Value: "{url}", Description: "The address of this app, used in links and emails."},
			{Name: "VIKUNJA_SERVICE_SECRET", Generate: "secret", Description: "Signs sign-in tokens. Changing it signs everyone out."},
		},
		FirstSteps: []string{
			"Open {url} and register your account. Anyone who reaches the address can register until you turn it off.",
			"Then turn registration off: add the environment variable VIKUNJA_SERVICE_ENABLEREGISTRATION with the value false and restart the app.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/binwiederhier/ntfy/tags
	{
		ID:          "ntfy",
		Name:        "ntfy",
		Tagline:     "Send push notifications to your phone from scripts and apps",
		Description: "ntfy delivers notifications to the ntfy phone app or the browser: send a message with a plain HTTP request to a topic, and everyone subscribed to that topic gets it. Many tools, such as Uptime Kuma and backup scripts, can send to it. Messages, users and attachments are kept in /data.",
		Category:    "Monitoring",
		Version:     "2.37.2",
		Website:     "https://ntfy.sh",
		Port:        2586,
		MemoryMB:    128,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/v1/health",
		Env: []EnvSpec{
			{Name: "NTFY_BASE_URL", Value: "{url}", Description: "The address of this app. ntfy needs it for attachments and links."},
		},
		FirstSteps: []string{
			"In the ntfy phone app, add {url} as a server and subscribe to a topic. Send a test with: curl -d \"Hello\" {url}/your-topic",
			"Anyone who knows a topic name can read and send to it. Use long, hard-to-guess names, or add the environment variable NTFY_AUTH_DEFAULT_ACCESS with the value deny-all and create users (see the access control docs on ntfy.sh).",
		},
	},
	// Version: https://ghcr.io/v2/mealie-recipes/mealie/tags/list
	{
		ID:          "mealie",
		Name:        "Mealie",
		Tagline:     "Your recipes, meal plans and shopping lists in one place",
		Description: "Mealie saves recipes, including ones imported from a recipe website's address, and turns them into meal plans and shopping lists. Share it with your household. Recipes, images and the database are kept in /data.",
		Category:    "Home",
		Version:     "3.30.0",
		Website:     "https://mealie.io",
		Port:        9000,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/api/app/about",
		Env: []EnvSpec{
			{Name: "BASE_URL", Value: "{url}", Description: "The address of this app, used in links and emails."},
		},
		FirstSteps: []string{
			"Sign in at {url} with changeme@example.com and the password MyPassword, then change both right away under your profile.",
			"Invite your household under Settings > Users; sign-ups are off.",
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
