package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/app"
)

// The tools for env names, traffic, the backup schedule and catalog updates.
// Each has the scope of its REST endpoint: listEnv and getTraffic read,
// updateBackupSettings and updateCatalogApp deploy.

type listEnvIn struct {
	App   string   `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Names []string `json:"names,omitempty" jsonschema:"Variable names to check, like [\"DATABASE_URL\",\"DISCORD_TOKEN\"]. The result then says for each one whether it is set (missing lists those that are not). Leave it out to list every variable."`
}

type trafficIn struct {
	App    string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Window string `json:"window,omitempty" jsonschema:"24h (default, per hour), 7d or 30d (per day)."`
}

type backupScheduleIn struct {
	App      string `json:"app" jsonschema:"The app's slug or ID (app_...). Find it with list_apps."`
	Schedule string `json:"schedule,omitempty" jsonschema:"off, daily (every night) or weekly (every Sunday), at the app's own backup time between 02:00 and 06:00 UTC. Leave it out to keep the current schedule."`
	KeepMode string `json:"keep_mode,omitempty" jsonschema:"last: keep the newest keep backups; days: keep backups for keep days. Leave it out to keep the current one."`
	Keep     *int64 `json:"keep,omitempty" jsonschema:"How many backups (keep_mode last, 1 to 100) or how many days (keep_mode days, 1 to 365). Leave it out to keep the current number."`
}

// scheduleLine says in one sentence when an app's automatic backups run.
func scheduleLine(st app.BackupSettings) string {
	keep := fmt.Sprintf("the newest %d are kept", st.Keep)
	if st.KeepMode == "days" {
		keep = fmt.Sprintf("kept %d days", st.Keep)
	}
	switch st.Schedule {
	case "daily":
		return fmt.Sprintf("Automatic backups: every night at %s UTC, %s.", st.TimeUTC, keep)
	case "weekly":
		return fmt.Sprintf("Automatic backups: every Sunday at %s UTC, %s.", st.TimeUTC, keep)
	}
	return "Automatic backups are off."
}

func (s *srv) registerAppTools() {
	svc := s.svc

	addTool(s, "list_env", "List environment variable names",
		"The names of an app's environment variables, never their values (they are stored encrypted and nobody can read them back). Use it when an app complains about a missing variable: pass names to check which of them are set. Per variable: service (empty when every service of the app gets it), references (the ${{<alias>.VAR}} it uses, like ${{postgres.DATABASE_URL}}) and updated_at. Change them with set_env; a change reaches the running app at restart_app.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in listEnvIn) (*result, error) {
			vars, err := svc.ListEnv(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			type envName struct {
				Name       string   `json:"name"`
				Service    string   `json:"service,omitempty"`
				References []string `json:"references,omitempty"`
				UpdatedAt  string   `json:"updated_at"`
			}
			out := make([]envName, len(vars))
			set := map[string]bool{}
			names := make([]string, 0, len(vars))
			for i, v := range vars {
				out[i] = envName{Name: v.Name, Service: v.Service, References: v.References, UpdatedAt: v.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")}
				if !set[v.Name] {
					names = append(names, v.Name)
				}
				set[v.Name] = true
			}
			sort.Strings(names)
			d := data("env", out)
			sum := fmt.Sprintf("%s has %d variable(s)", in.App, len(vars))
			if len(names) > 0 {
				sum += ": " + strings.Join(names, ", ")
			}
			sum += "."
			next := []string{"Values are never shown. To add or change one: set_env, then restart_app."}
			if len(in.Names) > 0 {
				checked := map[string]bool{}
				var missing []string
				for _, n := range in.Names {
					n = strings.TrimSpace(n)
					if n == "" || checked[n] {
						continue
					}
					checked[n] = set[n]
					if !set[n] {
						missing = append(missing, n)
					}
				}
				d["set"] = checked
				d["missing"] = nonNil(missing)
				if len(missing) == 0 {
					sum += " All of the names you asked about are set."
				} else {
					sum += " Not set: " + strings.Join(missing, ", ") + "."
					next = []string{"Ask the user for the missing values (or make up a random secret where any value works), then set_env and restart_app."}
				}
			}
			return &result{Summary: sum, Data: d, Next: next}, nil
		})

	addTool(s, "get_traffic", "Get traffic",
		"Requests, server errors, page views, visitors and answer times of a web app over a window (24h per hour, 7d or 30d per day), from bitploy's proxy. "+
			"Use it for \"is my site working\", \"why is it slow\" and \"how many visitors\": a running app can still answer every request with an error. "+
			"totals: requests, errors (status 500 or more) and error_rate (percent of requests), client_errors (400-499), page_views (pages people opened), visitors (unique per day), "+
			"median_ms and p95_ms (answer times of people's requests, estimates; null without answers), slow (a second or longer). previous holds the same totals of the period before. "+
			"Also points (the series), the top pages, referrers, countries and not_found (paths answered 404). Workers have no traffic.",
		readOnly(), false,
		enums(map[string][]string{"window": {"24h", "7d", "30d"}}),
		func(ctx context.Context, a app.Actor, in trafficIn) (*result, error) {
			w := in.Window
			if w == "" {
				w = "24h"
			}
			t, err := svc.GetTraffic(ctx, a, in.App, w)
			if err != nil {
				return nil, err
			}
			tt := t.Totals
			sum := fmt.Sprintf("%s over %s: %d request(s), %d server error(s) (%.1f%%), %d page view(s), %d visitor(s).",
				in.App, w, tt.Requests, tt.Errors, tt.ErrorRate, tt.PageViews, tt.Visitors)
			if tt.MedianMS != nil && tt.P95MS != nil {
				sum += fmt.Sprintf(" Answer time: median %d ms, 95%% within %d ms.", *tt.MedianMS, *tt.P95MS)
			}
			var next []string
			switch {
			case tt.Requests == 0:
				next = []string{"No requests in this window. If the user expected visitors, check the address with get_app."}
			case tt.ErrorRate >= 5:
				next = []string{"Many requests fail with a server error. Call get_logs with app=" + in.App + " and kind=runtime to see why."}
			case tt.P95MS != nil && *tt.P95MS >= 2000:
				next = []string{"Some answers are slow. Look at memory and CPU with get_metrics, and at slow queries or calls in the app's code."}
			default:
				next = []string{"Tell the user the numbers that answer their question; not_found lists paths that answered 404."}
			}
			t.Points = nonNil(t.Points)
			return &result{Summary: sum, Data: toMap(t), Next: next}, nil
		})

	addTool(s, "set_backup_schedule", "Set automatic backups",
		"Changes when bitploy backs up an app by itself and how long it keeps those backups. Apps with something to back up start with daily backups kept 14 days. "+
			"schedule off, daily or weekly (Sunday), at the app's own time between 02:00 and 06:00 UTC (time_utc); keep_mode last keeps the newest keep backups, days keeps them keep days. "+
			"Retention applies to automatic and before-restore backups; manual ones (create_backup) stay until the user deletes them. Fields left out stay as they are. "+
			"Only turn backups off when the user asks: once set here, bitploy never changes the schedule by itself. Backups count towards the storage allowance (whoami). "+
			"Valkey, Redis and Meilisearch get no backups (backups_not_needed). list_backups shows the settings and next_at.",
		idempotent(), true,
		enums(map[string][]string{"schedule": {"off", "daily", "weekly"}, "keep_mode": {"last", "days"}}),
		func(ctx context.Context, a app.Actor, in backupScheduleIn) (*result, error) {
			var body app.BackupSettingsInput
			if in.Schedule != "" {
				body.Schedule = &in.Schedule
			}
			if in.KeepMode != "" {
				body.KeepMode = &in.KeepMode
			}
			body.Keep = in.Keep
			st, err := svc.UpdateBackupSettings(ctx, a, in.App, body)
			if err != nil {
				return nil, err
			}
			next := []string{"Tell the user when the backups run. They download and restore them in the dashboard (Storage tab of the app)."}
			if st.Schedule == "off" {
				next = []string{"Tell the user bitploy no longer backs up this app by itself; create_backup makes one when they ask."}
			}
			return &result{Summary: in.App + ": " + scheduleLine(st), Data: data("settings", st), Next: next}, nil
		})

	addTool(s, "update_catalog_app", "Update a one-click app",
		"Updates an app made with deploy_catalog_app to the catalog's newer version (catalog_update on the app, from get_app or list_apps): bitploy replaces its Dockerfile with the catalog's and deploys. "+
			"Data in /data, variables and domains stay. Take a create_backup first: a new version can change its data in a way an older one cannot read. "+
			"Only when the user asks. Errors: not_catalog_app, catalog_up_to_date, git_source_linked. Databases from add_service are not upgraded by bitploy.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in appIn) (*result, error) {
			d, err := svc.UpdateCatalogApp(ctx, a, in.App)
			if err != nil {
				return nil, err
			}
			return deploymentResult("Queued the update deployment", d), nil
		})
}
