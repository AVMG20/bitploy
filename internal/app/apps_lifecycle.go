package app

// OWNER: deploy agent. Restart, start, stop.

import (
	"context"
	"database/sql"
	"os"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// RestartApp recreates the current containers from their images, so new env
// vars and resource limits apply. An optional service name restarts only that
// service of a running app; without it every service restarts.
func (s *Service) RestartApp(ctx context.Context, a Actor, ref string, service ...string) (App, error) {
	return s.bringUp(ctx, a, ref, "app.restart", true, firstOpt(service))
}

// StartApp starts a stopped app (a no-op when it already runs).
func (s *Service) StartApp(ctx context.Context, a Actor, ref string) (App, error) {
	return s.bringUp(ctx, a, ref, "app.start", false, "")
}

func (s *Service) bringUp(ctx context.Context, a Actor, ref, action string, force bool, service string) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	if _, err := s.activeUser(ctx, row.UserID); err != nil {
		return App{}, err
	}
	if !s.settled(row.ID) {
		return App{}, busyErr()
	}
	l := s.appLock(row.ID)
	l.Lock()
	defer l.Unlock()
	row, err = s.Q.GetApp(ctx, row.ID)
	if err != nil {
		return App{}, appNotFound(ref)
	}
	if !row.CurrentDeploymentID.Valid {
		return App{}, apperr.Conflict("app_not_deployed", "This app has not been deployed yet, so there is nothing to "+map[bool]string{true: "restart", false: "start"}[force]+".",
			"Deploy it first with deploy_files, or upload a zip and call deploy. If a deploy was started before and failed, call deploy with only app= to retry without resending files.").WithNext("deploy_files")
	}
	sp, _, err := s.pickService(ctx, row, service)
	if err != nil {
		return App{}, err
	}
	if !force && row.DesiredState == "running" && s.currentRunning(ctx, row) {
		return s.appView(ctx, a, row), nil
	}
	if err := s.checkStartQuota(ctx, row); err != nil { // start_app of a stopped app
		return App{}, err
	}
	var only []string
	if sp != nil && isMulti(row) && row.DesiredState == "running" {
		only = []string{sp.Name} // a stopped app starts as a whole
	}
	if ae := s.startCurrentLocked(ctx, row, only...); ae != nil {
		return App{}, ae
	}
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "running", Status: "running", StatusMessage: "", UpdatedAt: s.now(), ID: row.ID,
	}); err != nil {
		return App{}, apperr.Internal(err)
	}
	// startCurrentLocked synced the route while a stopped app was still marked
	// stopped (no upstream); sync again now that it is running.
	if err := s.syncRouteLocked(ctx, row.ID); err != nil {
		return App{}, err
	}
	s.deployRT.mu.Lock()
	delete(s.deployRT.restore, row.ID)
	s.deployRT.mu.Unlock()
	meta := map[string]any{"slug": row.Slug}
	if len(only) > 0 {
		meta["service"] = only[0]
	}
	s.Audit(ctx, a, action, "app", row.ID, meta)
	fresh, _ := s.Q.GetApp(ctx, row.ID)
	return s.appView(ctx, a, fresh), nil
}

// currentRunning reports whether every container of the current deployment
// runs.
func (s *Service) currentRunning(ctx context.Context, row db.App) bool {
	if s.Docker == nil || !row.CurrentDeploymentID.Valid {
		return false
	}
	if !isMulti(row) {
		d, err := s.Q.GetDeployment(ctx, row.CurrentDeploymentID.String)
		if err != nil || !d.ContainerName.Valid {
			return false
		}
		c, err := s.Docker.InspectContainer(ctx, d.ContainerName.String)
		return err == nil && c.State != nil && c.State.Running
	}
	cur := s.currentContainers(ctx, row)
	if len(cur) == 0 {
		return false
	}
	for _, name := range cur {
		c, err := s.Docker.InspectContainer(ctx, name)
		if err != nil || c.State == nil || !c.State.Running {
			return false
		}
	}
	return true
}

// StopApp: owners and admins may stop an app.
func (s *Service) StopApp(ctx context.Context, a Actor, ref string) (App, error) {
	if err := RequireDeploy(a); err != nil {
		return App{}, err
	}
	row, err := s.LoadApp(ctx, a, ref, false) // admins may stop any app
	if err != nil {
		return App{}, err
	}
	if err := s.stopApp(ctx, a, row); err != nil {
		return App{}, err
	}
	fresh, _ := s.Q.GetApp(ctx, row.ID)
	return s.appView(ctx, a, fresh), nil
}

func (s *Service) stopApp(ctx context.Context, a Actor, row db.App) error {
	s.cancelAllDeployments(ctx, row.ID)
	now := s.now()
	l := s.appLock(row.ID)
	l.Lock()
	defer l.Unlock()
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "stopped", Status: "stopped", StatusMessage: "", UpdatedAt: now, ID: row.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	// Take the route away first so the catch-all answers "This app is stopped".
	// If that fails, still stop the containers: the reconciler rewrites routes.
	if err := s.syncRouteLocked(ctx, row.ID); err != nil {
		s.Log.Warn("remove route of stopped app", "app", row.Slug, "err", err)
	}
	if s.Docker != nil {
		bg := context.WithoutCancel(ctx)
		if cs, err := s.Docker.ListAppContainers(bg, row.ID); err == nil {
			for _, c := range cs {
				if err := s.Docker.StopContainerGrace(bg, dockerx.SummaryName(c), 10); err != nil {
					s.Log.Warn("stop container", "app", row.Slug, "err", err)
				}
			}
		}
	}
	s.Audit(ctx, a, "app.stop", "app", row.ID, map[string]any{"slug": row.Slug})
	return nil
}

// StopAllForUser stops every app of a user (used when suspending).
func (s *Service) StopAllForUser(ctx context.Context, userID string) error {
	rows, err := s.Q.ListAppsByUser(ctx, userID)
	if err != nil {
		return apperr.Internal(err)
	}
	var first error
	for _, r := range rows {
		if r.DesiredState == "stopped" {
			s.cancelAllDeployments(ctx, r.ID) // a queued deploy would start it again
			continue
		}
		if err := s.stopApp(ctx, System, r); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// cancelAllDeployments cancels an app's queued deployments and its running
// pipeline. Queued ones go first: when the running pipeline exits it wakes the
// dispatcher, which would otherwise claim the next queued deployment.
func (s *Service) cancelAllDeployments(ctx context.Context, appID string) {
	cancelQueued := func() {
		ids, err := s.Q.CancelQueuedDeployments(ctx, db.CancelQueuedDeploymentsParams{FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, AppID: appID})
		if err != nil {
			return
		}
		for _, id := range ids {
			_ = os.Remove(s.sourceZip(id))
			s.NotifyDeployment(id)
		}
	}
	cancelQueued()
	s.cancelInflight(appID, 30*time.Second)
	cancelQueued() // anything queued while we waited
}
