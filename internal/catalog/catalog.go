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
	// Version: https://codeberg.org/v2/forgejo/forgejo/tags/list (17.0.1-rootless).
	{
		ID:          "forgejo",
		Name:        "Forgejo",
		Tagline:     "Host your own Git repositories, issues and pull requests",
		Description: "Forgejo is a Git server like GitHub: repositories, issues, pull requests, a wiki and releases, for you or a team. Clone and push over HTTPS; SSH is not available here. Repositories and the database are kept in /data.",
		Category:    "Developer tools",
		Version:     "17.0.1",
		Website:     "https://forgejo.org",
		Port:        3000,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    3072,
		HealthPath:  "/api/healthz",
		Env: []EnvSpec{
			{Name: "FORGEJO__server__ROOT_URL", Value: "{url}", Description: "The address of this app, used in clone URLs and links."},
			{Name: "SECRET_KEY", Generate: "secret", Description: "Encrypts secrets Forgejo stores. Also skips the install page."},
		},
		FirstSteps: []string{
			"Open {url} and register your account right away: the first account becomes the admin.",
			"Then turn registration off: add the environment variable FORGEJO__service__DISABLE_REGISTRATION with the value true and restart the app.",
			"Push with your username and an access token as the password; create the token under Settings > Applications.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/shlinkio/shlink/tags
	{
		ID:          "shlink",
		Name:        "Shlink",
		Tagline:     "Short links on your own domain, with visit counts",
		Description: "Shlink makes short links such as yourdomain/abc and counts who opens them. You manage it through its REST API or the Shlink web client in your browser. Links and visits are kept in a SQLite database in /data.",
		Category:    "Marketing",
		Version:     "5.1.8",
		Website:     "https://shlink.io",
		Port:        8080,
		MemoryMB:    384,
		Volume:      true,
		VolumeMB:    512,
		HealthPath:  "/rest/health",
		FirstSteps: []string{
			"Add the environment variable INITIAL_API_KEY with a long random value of your own and restart the app.",
			"Open https://app.shlink.io, add a server with the address {url} and that API key, and create your first short link.",
			"Short links use the app's address. To use a custom domain, add the environment variable DEFAULT_DOMAIN with just the domain name.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/nocodb/nocodb/tags
	{
		ID:          "nocodb",
		Name:        "NocoDB",
		Tagline:     "Spreadsheet-style databases, like Airtable",
		Description: "NocoDB lets you build tables with typed columns, links between tables, forms and views such as grid, gallery and kanban, all in the browser. Share a view or a form with a link, or use the REST API. Tables and uploads are kept in /data.",
		Category:    "Productivity",
		Version:     "2026.10.1",
		Website:     "https://nocodb.com",
		Port:        8080,
		MemoryMB:    1024,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/api/v1/health",
		Env: []EnvSpec{
			{Name: "NC_AUTH_JWT_SECRET", Generate: "secret", Description: "Signs sign-in tokens. Changing it signs everyone out."},
			{Name: "NC_PUBLIC_URL", Value: "{url}", Description: "The address of this app, used in links and emails."},
		},
		FirstSteps: []string{
			"Open {url} and sign up at once: the first account becomes the super admin.",
			"Invite others from the team settings rather than leaving sign-up open.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/directus/directus/tags
	{
		ID:          "directus",
		Name:        "Directus",
		Tagline:     "A backend and admin app for your content and data",
		Description: "Directus gives you a database you shape in an admin app, with a REST and GraphQL API, user roles and file uploads. Use it as a headless CMS for a website or as the backend of an app. The SQLite database, uploads and extensions are kept in /data.",
		Category:    "Backend",
		Version:     "12.6.1",
		Website:     "https://directus.io",
		Port:        8055,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/server/ping",
		Env: []EnvSpec{
			{Name: "SECRET", Generate: "secret", Description: "Signs sign-in tokens. Changing it signs everyone out."},
			{Name: "PUBLIC_URL", Value: "{url}", Description: "The address of this app, used in links, emails and file URLs."},
		},
		FirstSteps: []string{
			"Open {url} and create the admin account at once: the first visitor gets to do it.",
			"Create your first collection under Settings > Data Model; its API is at {url}/items/<collection>.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/stirlingtools/stirling-pdf/tags (3.2.2-ultra-lite).
	{
		ID:          "stirling-pdf",
		Name:        "Stirling-PDF",
		Tagline:     "Merge, split, compress and convert PDFs on your own server",
		Description: "Stirling-PDF has dozens of PDF tools in the browser: merge, split, rotate, compress, add a watermark or a signature, and convert to and from images. Files are processed on your server and not kept. This version leaves out text recognition (OCR) and Office conversion.",
		Category:    "Documents",
		Version:     "3.2.2",
		Website:     "https://www.stirlingpdf.com",
		Port:        8080,
		MemoryMB:    1024,
		Volume:      true,
		VolumeMB:    256,
		HealthPath:  "/api/v1/info/status",
		FirstSteps: []string{
			"Stirling-PDF has no sign-in: anyone with the address can use it. Turn on Password protection in the app's settings in bitploy first.",
			"Then open {url} and pick a tool.",
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
	// Version: https://hub.docker.com/v2/repositories/docmost/docmost/tags (0.97.0, 2026-10-25).
	{
		ID:          "docmost",
		Name:        "Docmost",
		Tagline:     "A wiki for your team, with pages you edit together",
		Description: "Docmost is a team wiki: pages in spaces, edited by several people at once, with comments, page history, diagrams and permissions per space. Pages live in a PostgreSQL database and live editing goes through Valkey, both added next to it as private services named db and cache. Uploaded files are kept in /data.",
		Category:    "Notes",
		Version:     "0.97.0",
		Website:     "https://docmost.com",
		Port:        3000,
		MemoryMB:    512,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/api/health",
		Services:    []ServiceSpec{{Template: "postgres", Name: "db"}, {Template: "valkey", Name: "cache"}},
		Env: []EnvSpec{
			{Name: "APP_URL", Value: "{url}", Description: "The address of this app, used in links and emails."},
			{Name: "APP_SECRET", Generate: "secret", Description: "Signs sign-in sessions. Changing it signs everyone out."},
			{Name: "DATABASE_URL", Value: "${{db.DATABASE_URL}}", Description: "The address of the PostgreSQL service db, with its generated password, filled in when the app starts."},
			{Name: "REDIS_URL", Value: "${{cache.REDIS_URL}}", Description: "The address of the Valkey service cache, with its generated password, filled in when the app starts."},
		},
		FirstSteps: []string{
			"Open {url} and create the workspace and your admin account at once: the first visitor gets to do it.",
			"Invite your team under Settings, Members. Until email is set up, copy each invite link from the list of pending invites and send it yourself.",
			"To send invites and password resets by email, add the environment variables SMTP_HOST, SMTP_PORT, SMTP_USERNAME, SMTP_PASSWORD and MAIL_FROM_ADDRESS with your mail provider's values and restart the app.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/library/ghost/tags (6.75.0, 2026-11-05).
	// Ghost 6 supports only MySQL 8 in production, not MariaDB.
	{
		ID:          "ghost",
		Name:        "Ghost",
		Tagline:     "A blog and newsletter, with paid memberships if you want them",
		Description: "Ghost publishes posts on your own website and sends them to subscribers as a newsletter, with free or paid memberships. Posts, members and settings are kept in a MySQL database that bitploy adds next to it as a private service named db. Images and themes are kept in /data.",
		Category:    "Marketing",
		Version:     "6.75.0",
		Website:     "https://ghost.org",
		Port:        2368,
		MemoryMB:    768,
		Volume:      true,
		VolumeMB:    1024,
		HealthPath:  "/ghost/api/admin/site/",
		Services:    []ServiceSpec{{Template: "mysql", Name: "db"}},
		Env: []EnvSpec{
			{Name: "url", Value: "{url}", Description: "The address of this app. Ghost uses it in every link, and sends visitors who arrive at another address there."},
			{Name: "database__connection__host", Value: "${{db.MYSQL_HOST}}", Description: "The host of the MySQL service db, filled in when the app starts."},
			{Name: "database__connection__port", Value: "${{db.MYSQL_PORT}}", Description: "The port of the MySQL service db, filled in when the app starts."},
			{Name: "database__connection__user", Value: "${{db.MYSQL_USER}}", Description: "The database user of the MySQL service db, filled in when the app starts."},
			{Name: "database__connection__password", Value: "${{db.MYSQL_PASSWORD}}", Description: "The generated password of the MySQL service db, filled in when the app starts."},
			{Name: "database__connection__database", Value: "${{db.MYSQL_DATABASE}}", Description: "The database name on the MySQL service db, filled in when the app starts."},
			{Name: "security__staffDeviceVerification", Value: "false", Description: "When true, Ghost emails a code each time staff sign in on a new device. Off until email is set up, or nobody could sign in a second time."},
		},
		FirstSteps: []string{
			"Open {url}/ghost and create the owner account at once: the first visitor gets to do it.",
			"Ghost sends staff invites, password resets and newsletters by email. Add the environment variables mail__transport (value SMTP), mail__options__host, mail__options__port, mail__options__auth__user, mail__options__auth__pass and mail__from with your mail provider's values and restart the app.",
			"Once email works, set security__staffDeviceVerification to true and restart the app.",
		},
	},
	// Version: https://ghcr.io/v2/linkwarden/linkwarden/tags/list (v2.16.3).
	{
		ID:          "linkwarden",
		Name:        "Linkwarden",
		Tagline:     "Save bookmarks with a copy of each page, so links that die stay readable",
		Description: "Linkwarden saves bookmarks in collections with tags, and can keep a screenshot, a PDF and a readable copy of every page it saves. Share collections with others or publish them. Bookmarks and accounts are kept in a PostgreSQL database that bitploy adds next to it as a private service named db; the page copies are kept in /data.",
		Category:    "Productivity",
		Version:     "2.16.3",
		Website:     "https://linkwarden.app",
		Port:        3000,
		MemoryMB:    1024,
		Volume:      true,
		VolumeMB:    2048,
		HealthPath:  "/",
		Services:    []ServiceSpec{{Template: "postgres", Name: "db"}},
		Env: []EnvSpec{
			{Name: "NEXTAUTH_URL", Value: "{url}/api/v1/auth", Description: "The sign-in address of this app."},
			{Name: "NEXTAUTH_SECRET", Generate: "secret", Description: "Signs sign-in sessions. Changing it signs everyone out."},
			{Name: "DATABASE_URL", Value: "${{db.DATABASE_URL}}", Description: "The address of the PostgreSQL service db, with its generated password, filled in when the app starts."},
		},
		FirstSteps: []string{
			"Open {url} and register your account. Anyone who reaches the address can register until you turn it off.",
			"Then turn registration off: add the environment variable NEXT_PUBLIC_DISABLE_REGISTRATION with the value true and restart the app.",
			"Install the Linkwarden browser extension and enter {url} as the server to save the page you are on.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/miniflux/miniflux/tags (2.3.5, 2026-11-02).
	{
		ID:          "miniflux",
		Name:        "Miniflux",
		Tagline:     "A fast, plain RSS reader for the news and blogs you follow",
		Description: "Miniflux checks the RSS and Atom feeds you subscribe to and shows new articles in a clean list, without ads or tracking. It works on phones and with many RSS apps. Feeds and articles are kept in a PostgreSQL database that bitploy adds next to it as a private service named db.",
		Category:    "Productivity",
		Version:     "2.3.5",
		Website:     "https://miniflux.app",
		Port:        8080,
		MemoryMB:    128,
		Volume:      false,
		HealthPath:  "/healthcheck",
		Services:    []ServiceSpec{{Template: "postgres", Name: "db"}},
		Env: []EnvSpec{
			{Name: "BASE_URL", Value: "{url}", Description: "The address of this app, used in links and for sign-in cookies."},
			// Miniflux's PostgreSQL driver asks for an encrypted connection unless told otherwise.
			{Name: "DATABASE_URL", Value: "${{db.DATABASE_URL}}?sslmode=disable", Description: "The address of the PostgreSQL service db, with its generated password, filled in when the app starts. The connection stays inside the project, so it is not encrypted."},
		},
		FirstSteps: []string{
			"Miniflux has no sign-up page. Add the environment variables ADMIN_USERNAME and ADMIN_PASSWORD with a name and a long password of your own, restart the app, then sign in at {url}.",
			"The variables create the account once. After you sign in, remove ADMIN_PASSWORD; change the password under Settings from then on.",
			"Add feeds under Feeds, Add feed, or bring the feeds of your old reader along with an OPML file under Feeds, Import.",
		},
	},
	// Version: https://hub.docker.com/v2/repositories/listmonk/listmonk/tags (v6.2.0, 2026-06-26;
	// v7.0.0-beta.1 is a beta).
	{
		ID:          "listmonk",
		Name:        "Listmonk",
		Tagline:     "Send newsletters and mailing lists through your own mail provider",
		Description: "Listmonk manages subscriber lists, sign-up forms and email campaigns, with templates and open and click counts. It sends through an SMTP server you add, such as Amazon SES, Postmark or Brevo. Lists and campaigns are kept in a PostgreSQL database that bitploy adds next to it as a private service named db; uploaded images are kept in /data.",
		Category:    "Marketing",
		Version:     "6.2.0",
		Website:     "https://listmonk.app",
		Port:        9000,
		MemoryMB:    256,
		Volume:      true,
		VolumeMB:    512,
		HealthPath:  "/health",
		Services:    []ServiceSpec{{Template: "postgres", Name: "db"}},
		Env: []EnvSpec{
			{Name: "LISTMONK_db__host", Value: "${{db.PGHOST}}", Description: "The host of the PostgreSQL service db, filled in when the app starts."},
			{Name: "LISTMONK_db__port", Value: "${{db.PGPORT}}", Description: "The port of the PostgreSQL service db, filled in when the app starts."},
			{Name: "LISTMONK_db__user", Value: "${{db.PGUSER}}", Description: "The database user of the PostgreSQL service db, filled in when the app starts."},
			{Name: "LISTMONK_db__password", Value: "${{db.PGPASSWORD}}", Description: "The generated password of the PostgreSQL service db, filled in when the app starts."},
			{Name: "LISTMONK_db__database", Value: "${{db.PGDATABASE}}", Description: "The database name on the PostgreSQL service db, filled in when the app starts."},
		},
		FirstSteps: []string{
			"Open {url}/admin and create the admin account at once: the first visitor gets to do it.",
			"Under Settings, General, set Root URL to {url}, so links in your emails point here.",
			"Add your mail provider's SMTP server under Settings, SMTP, and send yourself a test before your first campaign.",
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
