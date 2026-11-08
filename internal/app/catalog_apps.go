package app

// One-click apps: the Go side of internal/catalog. (catalog.go in this package
// is about Dockerfile templates for stacks; this file is about ready-made apps.)

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/catalog"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/servicetpl"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// CatalogEnv is an environment variable a catalog app sets. The catalog
// lists no value: a generated secret is random and saved hidden, a fixed
// value is filled in at create time.
type CatalogEnv struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Generated   bool   `json:"generated" doc:"True when bitploy makes up a random secret value for it"`
}

// CatalogApp is one entry of the one-click catalog.
type CatalogApp struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Tagline     string       `json:"tagline"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	Version     string       `json:"version"`
	Website     string       `json:"website"`
	Port        int          `json:"port"`
	MemoryMB    int          `json:"memory_mb"`
	Volume      bool         `json:"volume"`
	VolumeMB    int          `json:"volume_mb"`
	HealthPath  string       `json:"health_path"`
	Env         []CatalogEnv `json:"env"`
	FirstSteps  []string     `json:"first_steps" doc:"What to do once the app is up. {url} stands for the app's address."`
	Dockerfile  string       `json:"dockerfile"`
	// Services are the database servers deployed next to the app, in its
	// project (each from a service template, see GET /service-templates).
	Services []CatalogService `json:"services" doc:"Services (like PostgreSQL) deployed next to the app in the same project; empty for a single-container app"`
}

// CatalogService is a service a catalog app brings along.
type CatalogService struct {
	Template string `json:"template" doc:"The service template id, like postgres"`
	Name     string `json:"name" doc:"Its short name in the project"`
	Image    string `json:"image" doc:"The image with its pinned version"`
	MemoryMB int64  `json:"memory_mb"`
	VolumeMB int64  `json:"volume_mb"`
}

// CatalogDeployInput: every field is optional.
type CatalogDeployInput struct {
	Name     string `json:"name,omitempty"`
	Slug     string `json:"slug,omitempty"`
	Domain   string `json:"domain,omitempty" doc:"The domain of the address, one of app_domains in whoami. Empty: the default domain."`
	MemoryMB *int64 `json:"memory_mb,omitempty"`
	VolumeMB *int64 `json:"volume_mb,omitempty"`
	// Project is the project (id or name) to add the app and its services
	// to; empty: a new project named after the app.
	Project string `json:"project,omitempty" doc:"The project to add the app (and its services) to, an id from GET /projects. Empty: a new project named after the app."`
	// Alias is the app's short name in the project; default: the subdomain.
	Alias string `json:"alias,omitempty" doc:"The app's short name in the project (a DNS label). Default: the subdomain, with -2, -3 ... when that is taken."`
}

// CatalogDeployResult is what deploying a catalog app returns.
type CatalogDeployResult struct {
	App        App        `json:"app"`
	Deployment Deployment `json:"deployment"`
	FirstSteps []string   `json:"first_steps" doc:"What to do once the app is up, with the app's address filled in"`
}

func catalogView(e catalog.Entry) CatalogApp {
	env := make([]CatalogEnv, 0, len(e.Env))
	for _, ev := range e.Env {
		env = append(env, CatalogEnv{Name: ev.Name, Description: ev.Description, Generated: ev.Generate == "secret"})
	}
	svcs := make([]CatalogService, 0, len(e.Services))
	for _, sv := range e.Services {
		cs := CatalogService{Template: sv.Template, Name: sv.Name}
		if t, ok := servicetpl.Get(sv.Template); ok {
			cs.Image, cs.MemoryMB, cs.VolumeMB = t.ImageRef(), t.MemoryMB, t.VolumeMB
		}
		svcs = append(svcs, cs)
	}
	return CatalogApp{
		ID: e.ID, Name: e.Name, Tagline: e.Tagline, Description: e.Description, Category: e.Category,
		Version: e.Version, Website: e.Website, Port: e.Port, MemoryMB: e.MemoryMB, Volume: e.Volume, VolumeMB: e.VolumeMB,
		HealthPath: e.HealthPath, Env: env, FirstSteps: e.FirstSteps, Dockerfile: e.Dockerfile, Services: svcs,
	}
}

func catalogUnknown(id string) *apperr.Error {
	return apperr.NotFound("catalog_unknown", "There is no one-click app "+quote(id)+".",
		"Use one of these ids: "+strings.Join(catalog.IDs(), ", ")+".").WithNext("list_catalog")
}

// ListCatalog returns every one-click app. Any signed-in caller may read it.
func (s *Service) ListCatalog(ctx context.Context, a Actor) []CatalogApp {
	all := catalog.All()
	out := make([]CatalogApp, len(all))
	for i, e := range all {
		out[i] = catalogView(e)
	}
	return out
}

// GetCatalogApp returns one one-click app, or 404 catalog_unknown.
func (s *Service) GetCatalogApp(ctx context.Context, a Actor, id string) (CatalogApp, error) {
	e, ok := catalog.Get(id)
	if !ok {
		return CatalogApp{}, catalogUnknown(id)
	}
	return catalogView(e), nil
}

// DeployCatalogApp creates an app from a catalog entry and deploys its
// Dockerfile. The app goes through CreateApp (quotas, slug rules), so a refusal
// there is the caller's answer. With in.Project the app and the entry's
// services are added to that project; without it a multi-service entry gets a
// new project named after the app. A service whose name is taken in the
// project gets the next free one (db-2), and the app's references follow it.
// When anything fails before the deployment is queued, everything this call
// made is deleted again (a project it made goes as a whole; a project that was
// given keeps its other apps).
func (s *Service) DeployCatalogApp(ctx context.Context, a Actor, id string, in CatalogDeployInput) (res CatalogDeployResult, err error) {
	if err := RequireDeploy(a); err != nil {
		return res, err
	}
	e, ok := catalog.Get(id)
	if !ok {
		return res, catalogUnknown(id)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = e.Name
	}
	mem := int64(e.MemoryMB)
	if in.MemoryMB != nil {
		mem = *in.MemoryMB
	}
	port := int64(e.Port)
	create := CreateAppInput{
		Name: name, Type: "web", Port: &port, Volume: e.Volume, MemoryMB: &mem, BuildMode: "dockerfile", Domain: in.Domain, Alias: in.Alias,
	}
	if e.Volume {
		vol := int64(e.VolumeMB)
		if in.VolumeMB != nil {
			vol = *in.VolumeMB
		}
		create.VolumeMB = &vol
	}
	if strings.TrimSpace(in.Alias) != "" {
		if _, ae := normalizeAlias(in.Alias); ae != nil {
			return res, ae
		}
	}

	// The project: the one given (the same check as create_app), a new one
	// for a multi-service entry, or none (CreateApp makes the app's own).
	var made *Project // a project this call created, removed on failure
	projectID := ""
	if ref := strings.TrimSpace(in.Project); ref != "" {
		// A project shared with the caller works too: the app is its owner's
		// (createApp).
		p, perr := s.loadProject(ctx, a, ref, projectUse)
		if perr != nil {
			return res, perr
		}
		projectID = p.ID
	} else if len(e.Services) > 0 {
		p, perr := s.CreateProject(ctx, a, CreateProjectInput{Name: name})
		if perr != nil {
			return res, perr
		}
		made, projectID = &p, p.ID
	}
	create.Project = projectID

	var created App
	var services []App // service apps this call added
	queued := false
	defer func() {
		if err == nil || queued {
			return
		}
		// Do not leave anything half-made behind.
		bg := context.WithoutCancel(ctx)
		if made != nil {
			if derr := s.DeleteProject(bg, a, made.ID, made.Name, true); derr != nil {
				s.Log.Error("catalog: remove the project after a failed deploy", "project", made.ID, "err", derr)
			}
			return
		}
		if created.ID != "" {
			if derr := s.deleteApp(bg, a, created.ID, created.Slug, false); derr != nil {
				s.Log.Error("catalog: remove the app after a failed deploy", "app", created.Slug, "err", derr)
			}
		}
		for _, sv := range services {
			if derr := s.deleteApp(bg, a, sv.ID, sv.Slug, false); derr != nil {
				s.Log.Error("catalog: remove a service after a failed deploy", "app", sv.Slug, "err", derr)
			}
		}
	}()

	if slug := strings.ToLower(strings.TrimSpace(in.Slug)); slug != "" {
		create.Slug = slug
		created, err = s.CreateApp(ctx, a, create)
	} else {
		// The id first, then the id with four random letters.
		for i := 0; i < 20; i++ {
			create.Slug = e.ID
			if i > 0 {
				create.Slug = e.ID + "-" + strings.ToLower(ids.Base62(4))
			}
			created, err = s.CreateApp(ctx, a, create)
			if ae := apperr.As(err); err == nil || (ae.Code != "slug_taken" && ae.Code != "slug_reserved") {
				break
			}
		}
	}
	if err != nil {
		return res, err
	}

	url := created.URL
	if e.HealthPath != "" && e.HealthPath != created.HealthPath {
		if _, err = s.UpdateApp(ctx, a, created.Slug, UpdateAppInput{HealthPath: &e.HealthPath}); err != nil {
			return res, err
		}
	}
	// The services, under the entry's names or the next free ones.
	renamed := map[string]string{}
	for _, sv := range e.Services {
		alias, aerr := freeAlias(ctx, s.Q, created.Project.ID, sv.Name, "", nil)
		if aerr != nil {
			err = apperr.Internal(aerr)
			return res, err
		}
		var svc App
		if svc, err = s.AddService(ctx, a, created.Project.ID, AddServiceInput{Template: sv.Template, Name: alias}); err != nil {
			return res, err
		}
		services = append(services, svc)
		if svc.Alias != sv.Name {
			renamed[sv.Name] = svc.Alias
		}
	}
	// A generated secret is hidden: nobody needs to read it.
	set, hidden := map[string]string{}, map[string]bool{}
	for _, ev := range e.Env {
		if ev.Generate == "secret" {
			set[ev.Name], hidden[ev.Name] = ids.Base62(32), true
		} else {
			set[ev.Name] = renameRefs(catalog.Fill(ev.Value, url), renamed)
		}
	}
	if len(set) > 0 {
		if _, err = s.ChangeEnv(ctx, a, created.Slug, EnvChange{Set: set, Hidden: hidden}); err != nil {
			return res, err
		}
	}
	if err = s.Q.SetAppCatalog(ctx, db.SetAppCatalogParams{CatalogID: e.ID, CatalogVersion: e.Version, UpdatedAt: s.now(), ID: created.ID}); err != nil {
		err = apperr.Internal(err)
		return res, err
	}
	var dep Deployment
	dep, err = s.deployFiles(ctx, a, created.Slug, []FileInput{{Path: "Dockerfile", Content: e.Dockerfile}}, e.Version)
	if err != nil {
		return res, err
	}
	// From here on the app stays: its deployment is queued.
	queued = true
	meta := map[string]any{"catalog": e.ID, "version": e.Version, "app": created.Slug, "deployment": dep.ID, "project": created.Project.ID}
	if len(services) > 0 {
		aliases := make([]string, len(services))
		for i, sv := range services {
			aliases[i] = sv.Alias
		}
		meta["services"] = aliases
	}
	s.Audit(ctx, a, "catalog.deploy", "app", created.ID, meta)
	steps := make([]string, len(e.FirstSteps))
	for i, st := range e.FirstSteps {
		steps[i] = catalog.Fill(st, url)
	}
	final, gerr := s.GetApp(ctx, a, created.Slug)
	if gerr != nil {
		final = created
	}
	return CatalogDeployResult{App: final, Deployment: dep, FirstSteps: steps}, nil
}

// renameRefs points ${{old.VAR}} at ${{new.VAR}} for each old -> new.
func renameRefs(v string, renamed map[string]string) string {
	if len(renamed) == 0 || !strings.Contains(v, "${{") {
		return v
	}
	return refRe.ReplaceAllStringFunc(v, func(m string) string {
		sm := refRe.FindStringSubmatch(m)
		if to, ok := renamed[sm[1]]; ok {
			return varRef{to, sm[2]}.String()
		}
		return m
	})
}

// catalogNewer says whether the catalog version cat is newer than the
// version the app runs. Versions are compared part by part on dots and dashes,
// numerically where both parts are numbers (1.10 is newer than 1.9, 26.11.0
// than 26.9.1); a version that cannot be compared counts as newer when it
// differs.
func catalogNewer(cat, running string) bool {
	if cat == running {
		return false
	}
	split := func(v string) []string {
		return strings.FieldsFunc(strings.TrimPrefix(v, "v"), func(r rune) bool { return r == '.' || r == '-' })
	}
	a, b := split(cat), split(running)
	for i := 0; i < len(a) && i < len(b); i++ {
		x, xerr := strconv.Atoi(a[i])
		y, yerr := strconv.Atoi(b[i])
		if xerr != nil || yerr != nil {
			if a[i] != b[i] {
				return true
			}
			continue
		}
		if x != y {
			return x > y
		}
	}
	return len(a) > len(b)
}

// catalogUpdateFor is the App's catalog_update: the catalog's version when it
// is newer than the one the app runs and the app can be updated (it is not
// linked to a repository), else nil.
func catalogUpdateFor(r db.App) *string {
	if r.CatalogID == "" || isGitLinked(r) {
		return nil
	}
	e, ok := catalog.Get(r.CatalogID)
	if !ok || !catalogNewer(e.Version, r.CatalogVersion) {
		return nil
	}
	v := e.Version
	return &v
}

// UpdateCatalogApp replaces the app's Dockerfile with the catalog's current
// one and queues a deployment. The app's catalog_version changes when that
// deployment goes live, so a failed or cancelled update can be tried again.
func (s *Service) UpdateCatalogApp(ctx context.Context, a Actor, ref string) (Deployment, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return Deployment{}, err
	}
	if row.CatalogID == "" {
		return Deployment{}, apperr.Conflict("not_catalog_app", "This app was not made from a one-click app, so there is no catalog version to update to.",
			"Edit its Dockerfile instead, or deploy new files.")
	}
	if err := gitLinkedErr(row); err != nil {
		return Deployment{}, err
	}
	e, ok := catalog.Get(row.CatalogID)
	if !ok {
		return Deployment{}, catalogUnknown(row.CatalogID)
	}
	if !catalogNewer(e.Version, row.CatalogVersion) {
		return Deployment{}, apperr.Conflict("catalog_up_to_date", "This app already runs "+e.Name+" "+row.CatalogVersion+"; the catalog has "+e.Version+".",
			"Nothing to update.")
	}
	var dep Deployment
	snap, ok, err := s.sourceSnapshot(ctx, row)
	switch {
	case err != nil:
		return Deployment{}, err
	case ok:
		// Keep any other files of the source; only the Dockerfile changes.
		dep, err = s.deployWithDockerfile(ctx, a, row, snap, e.Dockerfile, e.Version)
	default:
		// No stored source (pruned): the catalog Dockerfile is the whole source.
		dep, err = s.deployFiles(ctx, a, row.Slug, []FileInput{{Path: path.Clean(row.DockerfilePath), Content: e.Dockerfile}}, e.Version)
	}
	if err != nil {
		return Deployment{}, err
	}
	s.Audit(ctx, a, "catalog.update", "app", row.ID, map[string]any{"catalog": e.ID, "from": row.CatalogVersion, "to": e.Version, "app": row.Slug, "deployment": dep.ID})
	return dep, nil
}
