package app

// Service templates in projects: add_service creates a database (or cache,
// or search engine) as an app of its own in a project, from a pinned
// template (internal/servicetpl), and connects apps to it through a
// reference variable such as DATABASE_URL=${{postgres.DATABASE_URL}}.

import (
	"context"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/servicetpl"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// referenceWait is how long a deployment waits for the apps its variables
// refer to while they are being deployed themselves (a database added in the
// same minute).
var referenceWait = 5 * time.Minute

func templateOf(r db.App) (servicetpl.Template, bool) {
	if r.Template == "" {
		return servicetpl.Template{}, false
	}
	return servicetpl.Get(r.Template)
}

func templateView(t servicetpl.Template) ServiceTemplate {
	return ServiceTemplate{
		ID: t.ID, Name: t.Name, Category: t.Category, Description: t.Description, Image: t.ImageRef(), Version: t.Version,
		Port: t.Port, MemoryMB: t.MemoryMB, CPUMillis: t.CPUMillis, VolumeMB: t.VolumeMB,
		ConnectionVars: t.ConnectionNames(), MainVar: t.MainVar,
	}
}

// ListServiceTemplates returns the templates add_service offers.
func (s *Service) ListServiceTemplates(ctx context.Context, a Actor) []ServiceTemplate {
	all := servicetpl.Listed()
	out := make([]ServiceTemplate, len(all))
	for i, t := range all {
		out[i] = templateView(t)
	}
	return out
}

func templateUnknown(id string) *apperr.Error {
	names := []string{}
	for _, t := range servicetpl.Listed() {
		names = append(names, t.ID)
	}
	return apperr.NotFound("service_template_unknown", "There is no service template "+quote(id)+".",
		"Use one of: "+strings.Join(names, ", ")+".").WithNext("list_service_templates")
}

// AddService creates an app from a service template in a project, sets its
// generated passwords, and deploys it. Each app in connectTo (slugs in the
// same project) gets <main_var>=${{<alias>.<main_var>}} and is restarted
// once the service is live. When anything fails before the deployment is
// queued, the new app is deleted again and the connected apps keep their
// variables as they were.
func (s *Service) AddService(ctx context.Context, a Actor, projectRef string, in AddServiceInput) (res App, err error) {
	p, err := s.loadProject(ctx, a, projectRef, true)
	if err != nil {
		return res, err
	}
	tpl, ok := servicetpl.Get(in.Template)
	if !ok {
		return res, templateUnknown(in.Template)
	}

	var alias string
	if strings.TrimSpace(in.Name) != "" {
		al, ae := normalizeAlias(in.Name)
		if ae != nil {
			return res, ae
		}
		if err := checkHostNames(ctx, s.Q, p.ID, "", al, nil); err != nil {
			return res, apperr.As(err)
		}
		alias = al
	} else if alias, err = freeAlias(ctx, s.Q, p.ID, tpl.ID, "", nil); err != nil {
		return res, apperr.Internal(err)
	}
	ref := "${{" + alias + "." + tpl.MainVar + "}}"

	// connect_to: apps of this project that do not set the variable yet.
	var connect []db.App
	seen := map[string]bool{}
	for _, slug := range in.ConnectTo {
		row, err := s.LoadApp(ctx, a, slug, true)
		if err != nil {
			return res, err
		}
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		if row.ProjectID != p.ID {
			return res, apperr.BadRequest("connect_to_other_project", "The app "+quote(row.Slug)+" is in another project.",
				"Only apps of the same project can connect; move it first with update_app project="+p.ID+".").WithNext("update_app")
		}
		vars, ae := s.sharedVars(ctx, row.ID)
		if ae != nil {
			return res, ae
		}
		if _, has := vars[tpl.MainVar]; has {
			return res, apperr.Conflict("connect_to_var_set", "The app "+row.Slug+" already has a variable "+tpl.MainVar+".",
				"Remove it with set_env unset=["+tpl.MainVar+"] and add the service again, or set "+tpl.MainVar+"="+ref+" yourself after adding it.").
				WithNext("set_env").WithDetail("app", row.Slug).WithDetail("variable", tpl.MainVar)
		}
		connect = append(connect, row)
	}

	name := tpl.Name
	if alias != tpl.ID {
		name = alias
	}
	mem, cpu, vol := tpl.MemoryMB, tpl.CPUMillis, tpl.VolumeMB
	if in.MemoryMB != nil {
		mem = *in.MemoryMB
	}
	if in.VolumeMB != nil {
		vol = *in.VolumeMB
	}
	create := CreateAppInput{
		Name: name, Type: "worker", Volume: true, VolumeMB: &vol, MemoryMB: &mem, CPUMillis: &cpu,
		BuildMode: "dockerfile", Project: p.ID, Alias: alias,
	}
	var created App
	for i := 0; i < 20; i++ {
		create.Slug = trimAlias(alias) + "-" + strings.ToLower(ids.Base62(4))
		created, err = s.CreateApp(ctx, a, create)
		if ae := apperr.As(err); err == nil || (ae.Code != "slug_taken" && ae.Code != "slug_reserved") {
			break
		}
	}
	if err != nil {
		return res, err
	}
	queued := false
	var connected []db.App
	defer func() {
		if err == nil || queued {
			return
		}
		bg := context.WithoutCancel(ctx)
		for _, c := range connected {
			if _, uerr := s.SetEnv(bg, a, c.ID, nil, []string{tpl.MainVar}); uerr != nil {
				s.Log.Error("add_service: undo connect_to", "app", c.Slug, "err", uerr)
			}
		}
		if derr := s.deleteApp(bg, a, created.Slug, created.Slug, false); derr != nil {
			s.Log.Error("add_service: remove the app after a failed deploy", "app", created.Slug, "err", derr)
		}
	}()

	set := map[string]string{}
	for _, e := range tpl.Env {
		if e.Generate == "secret" {
			set[e.Name] = ids.Base62(32)
		} else {
			set[e.Name] = e.Value
		}
	}
	if len(set) > 0 {
		if _, err = s.SetEnv(ctx, a, created.ID, set, nil); err != nil {
			return res, err
		}
	}
	if err = s.Q.SetAppTemplate(ctx, db.SetAppTemplateParams{Template: tpl.ID, TemplateVersion: tpl.Version, UpdatedAt: s.now(), ID: created.ID}); err != nil {
		err = apperr.Internal(err)
		return res, err
	}
	for _, c := range connect {
		if _, err = s.SetEnv(ctx, a, c.ID, map[string]string{tpl.MainVar: ref}, nil); err != nil {
			return res, err
		}
		connected = append(connected, c)
	}
	var dep Deployment
	dep, err = s.deployFiles(ctx, a, created.ID, []FileInput{{Path: "Dockerfile", Content: tpl.Dockerfile()}}, "")
	if err != nil {
		return res, err
	}
	queued = true
	now := s.now()
	bg := context.WithoutCancel(ctx) // the deployment is queued: a client that went away must not lose the restarts
	for _, c := range connected {
		// Stored, so a bitploy restart before the service is live does not
		// lose the restart (reconcileRestarts).
		if ierr := s.Q.InsertDeploymentRestart(bg, db.InsertDeploymentRestartParams{DeploymentID: dep.ID, AppID: c.ID, CreatedAt: now}); ierr != nil {
			s.Log.Warn("add_service: store the restart of a connected app", "app", c.Slug, "err", ierr)
		}
	}
	slugs := make([]string, len(connected))
	for i, c := range connected {
		slugs[i] = c.Slug
	}
	s.Audit(ctx, a, "service.add", "app", created.ID, map[string]any{
		"project": p.ID, "template": tpl.ID, "version": tpl.Version, "alias": alias, "app": created.Slug, "deployment": dep.ID, "connect_to": slugs,
	})
	final, gerr := s.GetApp(ctx, a, created.ID)
	if gerr != nil {
		final = created
	}
	return final, nil
}

// runAfterLive restarts the apps that were connected to a service when its
// deployment went live, so they start with the new variable. The list is in
// the database (deployment_restarts); taking it deletes it, so each app is
// restarted once even when the reconciler looks at the same time.
func (s *Service) runAfterLive(ctx context.Context, depID string) {
	appIDs, err := s.Q.TakeDeploymentRestarts(ctx, depID)
	if err != nil {
		s.Log.Warn("read the restarts after a deployment", "deployment", depID, "err", err)
		return
	}
	for _, id := range appIDs {
		row, err := s.Q.GetApp(ctx, id)
		if err != nil {
			continue
		}
		s.restartIfRunning(ctx, row.UserID, row.ID)
	}
}

// forgetAfterLive drops the restarts of a deployment that did not go live.
func (s *Service) forgetAfterLive(ctx context.Context, depID string) {
	if _, err := s.Q.TakeDeploymentRestarts(ctx, depID); err != nil {
		s.Log.Warn("drop the restarts of a failed deployment", "deployment", depID, "err", err)
	}
}

// reconcileRestarts finishes the restarts that a bitploy restart (or a
// deployment that finished before they were stored) left behind: done for a
// deployment that went live, dropped for one that failed.
func (s *Service) reconcileRestarts(ctx context.Context) {
	rows, err := s.Q.ListSettledDeploymentRestarts(ctx)
	if err != nil {
		return
	}
	for _, r := range rows {
		if ctx.Err() != nil || s.activeDeployment(r.DeploymentID) {
			continue // its pipeline is finishing and does it
		}
		live := r.State == "running" || r.State == "superseded"
		if !live {
			// bitploy stopped between the swap and the deployment's last step:
			// the reconciler marked it failed, but it is what the app runs.
			if d, err := s.Q.GetDeployment(ctx, r.DeploymentID); err == nil {
				if ap, err := s.Q.GetApp(ctx, d.AppID); err == nil && ap.CurrentDeploymentID.Valid && ap.CurrentDeploymentID.String == d.ID {
					live = true
				}
			}
		}
		if !live {
			s.forgetAfterLive(ctx, r.DeploymentID)
			continue
		}
		if s.Docker == nil {
			continue // nothing can restart now; kept for the next pass
		}
		s.runAfterLive(ctx, r.DeploymentID)
	}
}

// waitForReferences waits (up to referenceWait) while a service-template app
// this app's variables refer to is being deployed, so a web app added
// together with its database starts after the database runs. (Only template
// apps: they never refer back, so two apps cannot wait for each other.)
func (p *pipeline) waitForReferences(ctx context.Context) *apperr.Error {
	s := p.s
	aliases := s.appReferences(ctx, p.app.ID)
	if len(aliases) == 0 || p.app.ProjectID == "" {
		return nil
	}
	var targets []db.App
	for _, al := range aliases {
		if row, err := s.Q.GetAppByProjectAlias(ctx, db.GetAppByProjectAliasParams{ProjectID: p.app.ProjectID, Alias: al}); err == nil && row.ID != p.app.ID && row.Template != "" {
			targets = append(targets, row)
		}
	}
	deadline := time.Now().Add(referenceWait)
	logged := map[string]bool{}
	for {
		var waiting []string
		for _, t := range targets {
			if n, err := s.Q.CountPendingDeploymentsByApp(ctx, t.ID); err == nil && n > 0 {
				waiting = append(waiting, t.Alias)
			}
		}
		if len(waiting) == 0 {
			return nil
		}
		for _, w := range waiting {
			if !logged[w] {
				logged[w] = true
				p.logf("Waiting for %s to finish deploying (this app's variables refer to it).", w)
			}
		}
		if time.Now().After(deadline) {
			p.logf("Still deploying after %s: %s. Starting anyway.", referenceWait, strings.Join(waiting, ", "))
			return nil
		}
		select {
		case <-ctx.Done():
			return errCancelled
		case <-time.After(2 * time.Second):
		}
	}
}
