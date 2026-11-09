package mcp

// Projects and service templates (docs/compose-plan.md, "Phase 4b").

import (
	"context"
	"fmt"
	"strings"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
)

type createProjectIn struct {
	Name string `json:"name" jsonschema:"The project's name, 1-80 characters, like Shop or Family photos."`
}

type addServiceIn struct {
	Project   string   `json:"project" jsonschema:"The project's id (prj_...) from list_projects or get_app (project.id), or its name."`
	Template  string   `json:"template" jsonschema:"A template id from list_service_templates, like postgres, mysql, mariadb, valkey (Redis-compatible: use it when the code needs Redis), meilisearch or typesense (search boxes; typesense for facets and large data sets), clickhouse (analytics: events, logs, metrics queried in bulk; not for ordinary app data), garage (S3-compatible file storage when the code needs S3 for uploads) or mailpit (a test inbox that catches outgoing email; its web inbox on port 8025 is private: open it with set_public_port while the user reads the mail)."`
	Name      string   `json:"name,omitempty" jsonschema:"The service's short name in the project: its host name for the other apps and the name in ${{<name>.VAR}}. 1-40 characters of a-z, 0-9 and -. Leave out to use the template id (postgres, or postgres-2 when that is taken)."`
	ConnectTo []string `json:"connect_to,omitempty" jsonschema:"Slugs of apps in the same project that should use the service, like [\"shop\"] (the slug, not the short name: list_projects shows both). Each gets the template's main variable (DATABASE_URL for postgres, REDIS_URL for valkey, S3_ENDPOINT for garage, SMTP_URL for mailpit) as a reference; set the template's other variables the same way with set_env, like S3_ACCESS_KEY_ID=${{garage.S3_ACCESS_KEY_ID}} and restarts once the service runs. Fails with connect_to_var_set when the app already has that variable."`
	MemoryMB  *int64   `json:"memory_mb,omitempty" jsonschema:"Memory limit in MB. Leave out for the template's (list_service_templates)."`
	VolumeMB  *int64   `json:"volume_mb,omitempty" jsonschema:"Storage limit of the service's data in MB. Leave out for the template's."`
}

type projectIn struct {
	Project string `json:"project" jsonschema:"The project's id (prj_...) or its name."`
}

type renameProjectIn struct {
	Project string `json:"project" jsonschema:"The project's id (prj_...) or its current name."`
	Name    string `json:"name" jsonschema:"The new name, 1-80 characters, like Shop or Family photos."`
}

type deleteProjectIn struct {
	Project  string `json:"project" jsonschema:"The project's id (prj_...) or its name."`
	Confirm  string `json:"confirm" jsonschema:"The project's name typed out again, exactly as get_project shows it. Ask the user before deleting."`
	WithApps bool   `json:"with_apps,omitempty" jsonschema:"true: also delete the project's apps with their data and backups. Default false: a project with apps is refused (project_not_empty lists them)."`
}

// projectBrief is a project as list_projects shows it.
type projectBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Role is owner or member; Owner names the owner of a shared project.
	Role  string           `json:"role"`
	Owner string           `json:"owner,omitempty"`
	Apps  []map[string]any `json:"apps"`
}

func briefProject(p app.Project) projectBrief {
	b := projectBrief{ID: p.ID, Name: p.Name, Role: p.Role, Apps: []map[string]any{}}
	if p.Shared {
		b.Owner = ownerName(p)
	}
	for _, ap := range p.Apps {
		m := map[string]any{"slug": ap.Slug, "name": ap.Name, "alias": ap.Alias, "status": ap.Status, "internal_host": ap.InternalHost, "references": ap.References}
		if ap.URL != "" {
			m["url"] = ap.URL
		}
		if ap.Template != nil {
			m["template"] = *ap.Template
			m["connection_vars"] = ap.ConnectionVars
		}
		b.Apps = append(b.Apps, m)
	}
	return b
}

func ownerName(p app.Project) string {
	if p.Owner.Name != "" {
		return p.Owner.Name + " (" + p.Owner.Email + ")"
	}
	return p.Owner.Email
}

func projectLine(p app.Project) string {
	label := fmt.Sprintf("%s (%s)", p.Name, p.ID)
	if p.Shared {
		label = fmt.Sprintf("%s (%s, shared by %s)", p.Name, p.ID, ownerName(p))
	}
	names := make([]string, len(p.Apps))
	for i, ap := range p.Apps {
		var about []string
		if ap.Template != nil {
			about = append(about, *ap.Template)
		}
		if ap.Slug != ap.Alias {
			about = append(about, "slug "+ap.Slug)
		}
		names[i] = ap.Alias
		if len(about) > 0 {
			names[i] += " (" + strings.Join(about, ", ") + ")"
		}
	}
	if len(names) == 0 {
		return label + ": no apps"
	}
	return label + ": " + strings.Join(names, ", ")
}

func (s *srv) registerProjectTools() {
	svc := s.svc

	addTool(s, "list_projects", "List projects",
		"Lists the user's projects with their apps, and the projects other users shared with them (role member, owner says whose: their apps can be deployed and changed like the user's own, but only the owner renames or deletes the project). Every app is in one project. The apps of a project reach each other at their short name (alias, like postgres:5432) on a private network, and can use each other's variables as ${{<alias>.VAR}} in set_env, like DATABASE_URL=${{postgres.DATABASE_URL}}.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			ps, err := svc.ListProjects(ctx, a)
			if err != nil {
				return nil, err
			}
			rows := make([]projectBrief, len(ps))
			lines := make([]string, len(ps))
			for i, p := range ps {
				rows[i] = briefProject(p)
				lines[i] = projectLine(p)
			}
			return &result{
				Summary: fmt.Sprintf("%d project(s). %s", len(ps), strings.Join(lines, "; ")),
				Data:    data("items", rows),
				Next:    []string{"To add a database to a project, call add_service with the project id. To put a new app in a project, call create_app with project=<id>."},
			}, nil
		})

	addTool(s, "get_project", "Get a project",
		"One project with its apps in full (as get_app returns them): alias, internal_host, template, connection_vars and references of each.",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, in projectIn) (*result, error) {
			p, err := svc.GetProject(ctx, a, in.Project)
			if err != nil {
				return nil, err
			}
			return &result{Summary: projectLine(p) + ".", Data: data("project", p)}, nil
		})

	addTool(s, "create_project", "Create a project",
		"Makes an empty project to group apps that belong together, like a shop and its database. Not needed for a single app: create_app makes a project named after the app. An empty project is kept.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in createProjectIn) (*result, error) {
			p, err := svc.CreateProject(ctx, a, app.CreateProjectInput{Name: in.Name})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("Created project %s (%s).", p.Name, p.ID),
				Data:    data("project", p),
				Next:    []string{"Create apps in it with create_app project=" + p.ID + ", or add a database with add_service project=" + p.ID + "."},
			}, nil
		})

	addTool(s, "rename_project", "Rename a project",
		"Gives a project another name (1-80 characters). Its apps, their addresses and the aliases they use for each other stay as they are.",
		idempotent(), true, nil,
		func(ctx context.Context, a app.Actor, in renameProjectIn) (*result, error) {
			p, err := svc.UpdateProject(ctx, a, in.Project, app.UpdateProjectInput{Name: &in.Name})
			if err != nil {
				return nil, err
			}
			return &result{
				Summary: fmt.Sprintf("The project %s is now called %s.", p.ID, p.Name),
				Data:    data("project", p),
				Next:    []string{"Tell the user the new name. delete_project needs this name as confirm from now on."},
			}, nil
		})

	addTool(s, "delete_project", "Delete a project",
		"Permanently deletes a project. Needs confirm equal to the project's name (get_project). A project with apps is refused with project_not_empty, which lists them; "+
			"with_apps=true deletes those apps too, with their /data, databases and backups. Cannot be undone. Ask the user first and name the apps that would go; "+
			"only set with_apps=true when they said those apps may be deleted.",
		destructive(), true, nil,
		func(ctx context.Context, a app.Actor, in deleteProjectIn) (*result, error) {
			if strings.TrimSpace(in.Confirm) == "" {
				return nil, apperr.BadRequest("confirm_required", "delete_project needs confirm, the project's name typed out again.",
					"Ask the user to confirm, then call delete_project again with confirm set to the project's name.").WithNext("delete_project")
			}
			p, err := svc.GetProject(ctx, a, in.Project)
			if err != nil {
				return nil, err
			}
			if err := svc.DeleteProject(ctx, a, p.ID, in.Confirm, in.WithApps); err != nil {
				return nil, err
			}
			sum := "Deleted the project " + p.Name + "."
			next := []string{"Tell the user the project is gone."}
			if in.WithApps && len(p.Apps) > 0 {
				slugs := make([]string, len(p.Apps))
				for i, ap := range p.Apps {
					slugs[i] = ap.Slug
				}
				sum = fmt.Sprintf("Deleted the project %s and its %d app(s): %s.", p.Name, len(slugs), strings.Join(slugs, ", "))
				next = []string{"Tell the user the project, its apps and their data are gone. If a code folder has a .bitploy file for one of those apps, delete that file too."}
			}
			return &result{Summary: sum, Data: data("deleted", p.ID), Next: next}, nil
		})

	addTool(s, "list_service_templates", "List service templates",
		"Databases, caches, search engines, S3 file storage and a test mail inbox that add_service can put in a project: id, image with its pinned version, port, memory, storage and connection_vars (the variables other apps can reference, main_var first). Most apps need none: they keep their data as SQLite in /data. Use one only when the code needs PostgreSQL, MySQL, MariaDB, ClickHouse, Redis (Valkey), Meilisearch, Typesense, S3 storage (Garage) or a test inbox for outgoing email (Mailpit).",
		readOnly(), false, nil,
		func(ctx context.Context, a app.Actor, _ emptyIn) (*result, error) {
			ts := svc.ListServiceTemplates(ctx, a)
			ids := make([]string, len(ts))
			for i, t := range ts {
				ids[i] = t.ID + " (" + t.Image + ")"
			}
			return &result{
				Summary: fmt.Sprintf("%d service templates: %s.", len(ts), strings.Join(ids, ", ")),
				Data:    data("items", ts),
				Next:    []string{"Call add_service with project, template and connect_to=[<app slug>] so the app gets the connection variable."},
			}, nil
		})

	addTool(s, "add_service", "Add a database or other service",
		"Adds a service from a template (list_service_templates) to a project as an app of its own and deploys it: private (no web address), data in its own volume, passwords generated and stored encrypted. Each app in connect_to gets the template's main variable as a reference (like DATABASE_URL=${{postgres.DATABASE_URL}}) and restarts once the service runs. Other apps of the project reach it at <name>:<port>. Returns the new app; follow its deployment with get_deployment app=<slug>.",
		write(), true, nil,
		func(ctx context.Context, a app.Actor, in addServiceIn) (*result, error) {
			ap, err := svc.AddService(ctx, a, in.Project, app.AddServiceInput{
				Template: in.Template, Name: in.Name, ConnectTo: in.ConnectTo, MemoryMB: in.MemoryMB, VolumeMB: in.VolumeMB,
			})
			if err != nil {
				return nil, err
			}
			tpl := ""
			if ap.Template != nil {
				tpl = *ap.Template
			}
			next := []string{"Follow the deployment with get_deployment app=" + ap.Slug + " wait_seconds=30."}
			if len(in.ConnectTo) > 0 {
				var live, idle []string
				for _, slug := range in.ConnectTo {
					if c, err := svc.GetApp(ctx, a, slug); err == nil && c.Current == nil {
						idle = append(idle, c.Slug)
					} else {
						live = append(live, slug)
					}
				}
				next = append(next, fmt.Sprintf("Connected: %s, with %s=${{%s.%s}}. The code must read %s from the environment; if it reads other names, set them the same way with set_env (%s).",
					strings.Join(in.ConnectTo, ", "), ap.MainVar, ap.Alias, ap.MainVar, ap.MainVar, strings.Join(ap.ConnectionVars, ", ")))
				if len(live) > 0 {
					next = append(next, "Restarted by bitploy once "+ap.Alias+" runs: "+strings.Join(live, ", ")+".")
				}
				if len(idle) > 0 {
					next = append(next, "Nothing runs yet in "+strings.Join(idle, ", ")+", so bitploy does not restart it: deploy it (deploy with only app= rebuilds code sent before; else send the code). A deployment waits up to 5 minutes for "+ap.Alias+" to run.")
				}
			} else {
				next = append(next, fmt.Sprintf("To use it from an app of the project: set_env app=<slug> set={\"%s\":\"${{%s.%s}}\"}, then restart_app. Other variables: %s.",
					ap.MainVar, ap.Alias, ap.MainVar, strings.Join(ap.ConnectionVars, ", ")))
			}
			return &result{
				Summary: fmt.Sprintf("Added %s (%s %s) to project %s as %s, reachable at %s; deploying.", ap.Slug, tpl, ap.TemplateVersion, ap.Project.Name, ap.Alias, ap.InternalHost),
				Data:    data("app", ap),
				Next:    next,
			}, nil
		})
}
