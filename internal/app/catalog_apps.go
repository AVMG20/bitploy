package app

// One-click apps: the Go side of internal/catalog. (catalog.go in this package
// is about Dockerfile templates for stacks; this file is about ready-made apps.)

import (
	"context"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/catalog"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// CatalogEnv is an environment variable a catalog app sets. Values are never
// returned: a generated secret is random and stored encrypted, a fixed value
// is part of the app's Dockerfile or filled in at create time.
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
}

// CatalogDeployInput: every field is optional.
type CatalogDeployInput struct {
	Name     string `json:"name,omitempty"`
	Slug     string `json:"slug,omitempty"`
	MemoryMB *int64 `json:"memory_mb,omitempty"`
	VolumeMB *int64 `json:"volume_mb,omitempty"`
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
	return CatalogApp{
		ID: e.ID, Name: e.Name, Tagline: e.Tagline, Description: e.Description, Category: e.Category,
		Version: e.Version, Website: e.Website, Port: e.Port, MemoryMB: e.MemoryMB, Volume: e.Volume, VolumeMB: e.VolumeMB,
		HealthPath: e.HealthPath, Env: env, FirstSteps: e.FirstSteps, Dockerfile: e.Dockerfile,
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
// there is the caller's answer. When anything fails after the app was created,
// the app is deleted again: nothing half-made stays.
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
		Name: name, Type: "web", Port: &port, Volume: e.Volume, MemoryMB: &mem, BuildMode: "dockerfile",
	}
	if e.Volume {
		vol := int64(e.VolumeMB)
		if in.VolumeMB != nil {
			vol = *in.VolumeMB
		}
		create.VolumeMB = &vol
	}

	var created App
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
	defer func() {
		if err != nil { // do not leave a half-made app behind
			if derr := s.DeleteApp(context.WithoutCancel(ctx), a, created.Slug, created.Slug); derr != nil {
				s.Log.Error("catalog: remove the app after a failed deploy", "app", created.Slug, "err", derr)
			}
		}
	}()

	url := created.URL
	if e.HealthPath != "" && e.HealthPath != created.HealthPath {
		if _, err = s.UpdateApp(ctx, a, created.Slug, UpdateAppInput{HealthPath: &e.HealthPath}); err != nil {
			return res, err
		}
	}
	set := map[string]string{}
	for _, ev := range e.Env {
		if ev.Generate == "secret" {
			set[ev.Name] = ids.Base62(32)
		} else {
			set[ev.Name] = catalog.Fill(ev.Value, url)
		}
	}
	if len(set) > 0 {
		if _, err = s.SetEnv(ctx, a, created.Slug, set, nil); err != nil {
			return res, err
		}
	}
	if err = s.Q.SetAppCatalog(ctx, db.SetAppCatalogParams{CatalogID: e.ID, CatalogVersion: e.Version, UpdatedAt: s.now(), ID: created.ID}); err != nil {
		err = apperr.Internal(err)
		return res, err
	}
	var dep Deployment
	dep, err = s.DeployFiles(ctx, a, created.Slug, []FileInput{{Path: "Dockerfile", Content: e.Dockerfile}})
	if err != nil {
		return res, err
	}
	s.Audit(ctx, a, "catalog.deploy", "app", created.ID, map[string]any{"catalog": e.ID, "version": e.Version, "app": created.Slug, "deployment": dep.ID})
	final, gerr := s.GetApp(ctx, a, created.Slug)
	if gerr != nil {
		err = gerr
		return res, err
	}
	steps := make([]string, len(e.FirstSteps))
	for i, st := range e.FirstSteps {
		steps[i] = catalog.Fill(st, url)
	}
	return CatalogDeployResult{App: final, Deployment: dep, FirstSteps: steps}, nil
}

// UpdateCatalogApp replaces the app's Dockerfile with the catalog's current
// one and queues a deployment. The new version is recorded when the
// deployment is queued: the stored source already is the new Dockerfile, so a
// retry of a failed deployment builds the new version too.
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
	if e.Version == row.CatalogVersion {
		return Deployment{}, apperr.Conflict("catalog_up_to_date", "This app already runs "+e.Name+" "+e.Version+", the version in the catalog.",
			"Nothing to update.")
	}
	dep, err := s.deployWithDockerfile(ctx, a, row, e.Dockerfile)
	if err != nil {
		return Deployment{}, err
	}
	if err := s.Q.SetAppCatalog(ctx, db.SetAppCatalogParams{CatalogID: e.ID, CatalogVersion: e.Version, UpdatedAt: s.now(), ID: row.ID}); err != nil {
		return Deployment{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "catalog.update", "app", row.ID, map[string]any{"catalog": e.ID, "from": row.CatalogVersion, "to": e.Version, "app": row.Slug, "deployment": dep.ID})
	s.publishApp(ctx, row.ID)
	return dep, nil
}
