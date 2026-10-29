package app

// OWNER: deploy agent. The reconciler: compares desired state in the database
// with real containers, networks, volumes, images and route files, and
// repairs drift. Runs on start and every 60 s. A crash or reboot must never
// leave an app half-deployed.

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/store/db"
)

const maxRestoreTries = 3

// RunReconciler repairs drift on startup and every 60 s until ctx ends.
func (s *Service) RunReconciler(ctx context.Context) {
	s.reconcileSafe(ctx)
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcileSafe(ctx)
		}
	}
}

func (s *Service) reconcileSafe(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.Log.Error("reconciler panic", "panic", r)
		}
	}()
	s.Reconcile(ctx)
}

// Reconcile runs one pass.
func (s *Service) Reconcile(ctx context.Context) {
	s.reconcileDeployments(ctx)
	if s.Docker != nil {
		s.reconcileDocker(ctx)
	}
	s.reconcileRoutes(ctx)
	s.cleanBuildDirs()
	s.cleanupMisc(ctx)
}

// reconcileDeployments fails deployments stuck in a non-terminal state with
// no pipeline running in this process (bitploy crashed or restarted), and
// removes what they left behind.
func (s *Service) reconcileDeployments(ctx context.Context) {
	rows, err := s.Q.ListInterruptibleDeployments(ctx)
	if err != nil {
		return
	}
	for _, d := range rows {
		if s.activeDeployment(d.ID) {
			continue
		}
		n, err := s.Q.InterruptDeployment(ctx, db.InterruptDeploymentParams{
			ErrorCode:    nullStr("interrupted"),
			ErrorMessage: nullStr("bitploy restarted while this deployment was running, so it was stopped."),
			ErrorHint:    nullStr("Deploy again. Your previous version, if any, kept running."),
			ErrorNext:    nullStr("deploy"),
			FinishedAt:   sql.NullInt64{Int64: s.now(), Valid: true},
			ID:           d.ID, State: d.State,
		})
		if err != nil || n == 0 {
			continue
		}
		s.Log.Warn("deployment interrupted", "deployment", d.ID, "state", d.State)
		app, aerr := s.Q.GetApp(ctx, d.AppID)
		isCurrent := aerr == nil && app.CurrentDeploymentID.Valid && app.CurrentDeploymentID.String == d.ID
		if s.Docker != nil && aerr == nil && !isCurrent {
			name := dockerx.ContainerName(app.Slug, d.Seq)
			if !s.isDraining(name) {
				_ = s.Docker.RemoveContainer(ctx, name)
			}
			if d.SourceKind != "rollback" {
				_ = s.Docker.RemoveImage(ctx, dockerx.ImageName(app.Slug, d.Seq))
			}
		}
		_ = os.RemoveAll(s.Cfg.BuildDir(d.ID))
		if aerr == nil {
			if !app.CurrentDeploymentID.Valid && app.DesiredState == "running" {
				_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "failed", StatusMessage: "The first deployment was interrupted by a bitploy restart.", UpdatedAt: s.now(), ID: app.ID})
			}
			s.setCandidate(app.ID, "")
			_ = s.SyncRoute(ctx, app.ID)
			s.Audit(ctx, System, "deployment.interrupted", "deployment", d.ID, map[string]any{"app": app.Slug, "state": d.State})
		}
		s.NotifyDeployment(d.ID)
	}
}

func (s *Service) reconcileDocker(ctx context.Context) {
	dk := s.Docker
	apps, err := s.Q.ListAllApps(ctx)
	if err != nil {
		return
	}
	byID := make(map[string]db.App, len(apps))
	currentName := map[string]string{}
	for _, a := range apps {
		byID[a.ID] = a
		if a.CurrentDeploymentID.Valid {
			if d, err := s.Q.GetDeployment(ctx, a.CurrentDeploymentID.String); err == nil {
				currentName[a.ID] = dockerx.ContainerName(a.Slug, d.Seq)
			}
		}
	}
	// Containers: remove those without an app, or that are not the current
	// one. The decision is re-checked under the app lock with fresh state, so
	// a swap that happens while we list can never lose its new container.
	if cs, err := dk.ListManagedContainers(ctx); err == nil {
		for _, c := range cs {
			name := dockerx.SummaryName(c)
			appID := c.Labels[dockerx.LabelAppID]
			if appID == "" || ctx.Err() != nil {
				continue // not an app container (for example the test Traefik)
			}
			if _, ok := byID[appID]; !ok {
				if _, err := s.Q.GetApp(ctx, appID); err == nil {
					continue // created after our snapshot
				}
				s.Log.Warn("removing container of a deleted app", "container", name)
				_ = dk.StopContainerGrace(ctx, name, 5)
				_ = dk.RemoveContainer(ctx, name)
				continue
			}
			s.removeIfStale(ctx, name, appID, c.Labels[dockerx.LabelDeployment])
		}
	}

	// Networks of deleted apps.
	if ns, err := dk.ListManagedNetworks(ctx); err == nil {
		for _, n := range ns {
			if n.Labels[dockerx.LabelAppID] == "" {
				continue
			}
			if _, ok := byID[n.Labels[dockerx.LabelAppID]]; !ok {
				_ = dk.DisconnectNetwork(ctx, n.Name, s.Cfg.TraefikContainer)
				if err := dk.RemoveNetwork(ctx, n.Name); err != nil {
					s.Log.Warn("remove orphan network", "network", n.Name, "err", err)
				}
			}
		}
	}
	// Volumes and images of deleted apps.
	if vs, err := dk.ListManagedVolumes(ctx); err == nil {
		for _, v := range vs {
			id := v.Labels[dockerx.LabelAppID]
			if _, ok := byID[id]; id != "" && !ok {
				_ = dk.RemoveVolume(ctx, v.Name)
			}
		}
	}
	if ims, err := dk.ListManagedImages(ctx); err == nil {
		for _, im := range ims {
			if _, ok := byID[im.AppID]; im.AppID != "" && !ok {
				for _, t := range im.Tags {
					_ = dk.RemoveImage(ctx, t)
				}
			}
		}
	}

	// Images that no deployment knows (a build that finished after bitploy
	// was killed) are removed.
	for _, app := range apps {
		if ctx.Err() != nil {
			return
		}
		s.removeOrphanImages(ctx, app.ID)
	}

	// Apps: bring containers in line with the desired state.
	for _, app := range apps {
		if ctx.Err() != nil {
			return
		}
		if s.appBusy(app.ID) || !app.CurrentDeploymentID.Valid {
			continue
		}
		name := currentName[app.ID]
		if name == "" {
			continue
		}
		info, ierr := dk.InspectContainer(ctx, name)
		missing := dockerx.IsNotFound(ierr)
		switch app.DesiredState {
		case "stopped":
			if ierr == nil && info.State != nil && (info.State.Running || info.State.Restarting) {
				_ = dk.StopContainerGrace(ctx, name, 10)
			}
		case "running":
			if app.Status == "crashed" {
				continue // crash-loop detection stopped it on purpose
			}
			dead := ierr == nil && info.State != nil && info.State.Dead
			if missing || dead {
				s.restoreApp(ctx, app, dead)
			}
		}
	}
}

// restoreApp recreates a missing container of an app that should run.
func (s *Service) restoreApp(ctx context.Context, app db.App, dead bool) {
	r := &s.deployRT
	r.mu.Lock()
	r.init()
	tries := r.restore[app.ID]
	r.mu.Unlock()
	if tries >= maxRestoreTries {
		return
	}
	l := s.appLock(app.ID)
	if !l.TryLock() {
		return
	}
	defer l.Unlock()
	fresh, err := s.Q.GetApp(ctx, app.ID)
	if err != nil || fresh.DesiredState != "running" || s.appBusy(app.ID) {
		return
	}
	s.Log.Warn("restoring missing container", "app", app.Slug)
	if ae := s.startCurrentLocked(ctx, fresh); ae != nil {
		r.mu.Lock()
		r.restore[app.ID]++
		n := r.restore[app.ID]
		r.mu.Unlock()
		s.Log.Error("restore failed", "app", app.Slug, "try", n, "err", ae)
		if n >= maxRestoreTries {
			_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{
				Status: "failed", StatusMessage: apperr.Redact("The container disappeared and could not be restored: " + ae.Message),
				UpdatedAt: s.now(), ID: app.ID,
			})
		}
		return
	}
	r.mu.Lock()
	delete(r.restore, app.ID)
	r.mu.Unlock()
	if fresh.Status != "running" {
		_ = s.Q.SetAppStatus(ctx, db.SetAppStatusParams{Status: "running", StatusMessage: "", UpdatedAt: s.now(), ID: app.ID})
	}
	s.Audit(ctx, System, "app.restore", "app", app.ID, map[string]any{"slug": app.Slug})
}

// reconcileRoutes makes the route files match the database.
func (s *Service) reconcileRoutes(ctx context.Context) {
	apps, err := s.Q.ListAllApps(ctx)
	if err != nil {
		return
	}
	known := make(map[string]bool, len(apps))
	for _, a := range apps {
		known[a.Slug] = true
	}
	if files, err := s.Routes.List(); err == nil {
		for _, slug := range files {
			if !known[slug] {
				s.Log.Warn("removing route file of an unknown app", "slug", slug)
				_ = s.Routes.Remove(slug)
			}
		}
	}
	for _, a := range apps {
		l := s.appLock(a.ID)
		if !l.TryLock() {
			continue
		}
		func() {
			defer l.Unlock()
			fresh, err := s.Q.GetApp(ctx, a.ID)
			if err != nil {
				return
			}
			want := s.desiredRoute(ctx, fresh)
			have, _ := s.Routes.Read(fresh.Slug)
			if want.Upstream == "" && want.Candidate == "" {
				if have != nil {
					_ = s.Routes.Remove(fresh.Slug)
				}
				return
			}
			b, err := s.Routes.Render(want)
			if err != nil {
				return
			}
			if !bytes.Equal(b, have) {
				s.Log.Info("rewriting route file", "slug", fresh.Slug)
				_ = s.Routes.Write(want)
			}
		}()
	}
}

// cleanBuildDirs removes build contexts nobody is using.
func (s *Service) cleanBuildDirs() {
	entries, err := os.ReadDir(s.Cfg.BuildsDir())
	if err != nil {
		return
	}
	active := s.activeDeploymentIDs()
	for _, e := range entries {
		if !active[e.Name()] {
			_ = os.RemoveAll(s.Cfg.BuildDir(e.Name()))
		}
	}
}

// removeIfStale removes a container that is not its app's current one and
// belongs to no running pipeline.
func (s *Service) removeIfStale(ctx context.Context, name, appID, depID string) {
	l := s.appLock(appID)
	if !l.TryLock() {
		return
	}
	defer l.Unlock()
	app, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return
	}
	if s.activeDeployment(depID) || s.isDraining(name) {
		return
	}
	if app.CurrentDeploymentID.Valid {
		if d, err := s.Q.GetDeployment(ctx, app.CurrentDeploymentID.String); err == nil && dockerx.ContainerName(app.Slug, d.Seq) == name {
			return
		}
	}
	s.Log.Warn("removing container that is not current", "container", name, "app", app.Slug)
	_ = s.Docker.StopContainerGrace(ctx, name, 5)
	_ = s.Docker.RemoveContainer(ctx, name)
}

// removeOrphanImages removes images of an app that no deployment row refers to.
func (s *Service) removeOrphanImages(ctx context.Context, appID string) {
	if s.appBusy(appID) {
		return
	}
	l := s.appLock(appID)
	if !l.TryLock() {
		return
	}
	defer l.Unlock()
	if s.appBusy(appID) {
		return
	}
	tags, err := s.Docker.ListAppImages(ctx, appID)
	if err != nil || len(tags) == 0 {
		return
	}
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, appID)
	if err != nil {
		return
	}
	known := map[string]bool{}
	for _, d := range deps {
		if d.Image.Valid {
			known[d.Image.String] = true
		}
	}
	for _, t := range tags {
		if !known[t] {
			s.Log.Warn("removing image that no deployment knows", "image", t)
			_ = s.Docker.RemoveImage(ctx, t)
		}
	}
}
