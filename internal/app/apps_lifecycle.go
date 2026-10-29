package app

// OWNER: deploy agent. Restart, start, stop.

import (
	"context"
	"database/sql"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/dockerx"
	"github.com/arno/bitploy/internal/store/db"
)

// RestartApp recreates the current container from its image, so new env vars
// and resource limits apply.
func (s *Service) RestartApp(ctx context.Context, a Actor, ref string) (App, error) {
	return s.bringUp(ctx, a, ref, "app.restart", true)
}

// StartApp starts a stopped app (a no-op when it already runs).
func (s *Service) StartApp(ctx context.Context, a Actor, ref string) (App, error) {
	return s.bringUp(ctx, a, ref, "app.start", false)
}

func (s *Service) bringUp(ctx context.Context, a Actor, ref, action string, force bool) (App, error) {
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
			"Deploy it first with deploy_files, or upload a zip and call deploy.").WithNext("deploy_files")
	}
	if !force && row.DesiredState == "running" && s.currentRunning(ctx, row) {
		return s.appView(ctx, a, row), nil
	}
	if ae := s.startCurrentLocked(ctx, row); ae != nil {
		return App{}, ae
	}
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "running", Status: "running", StatusMessage: "", UpdatedAt: s.now(), ID: row.ID,
	}); err != nil {
		return App{}, apperr.Internal(err)
	}
	s.deployRT.mu.Lock()
	delete(s.deployRT.restore, row.ID)
	s.deployRT.mu.Unlock()
	s.Audit(ctx, a, action, "app", row.ID, map[string]any{"slug": row.Slug})
	fresh, _ := s.Q.GetApp(ctx, row.ID)
	return s.appView(ctx, a, fresh), nil
}

// currentRunning reports whether the current deployment's container runs.
func (s *Service) currentRunning(ctx context.Context, row db.App) bool {
	if s.Docker == nil || !row.CurrentDeploymentID.Valid {
		return false
	}
	d, err := s.Q.GetDeployment(ctx, row.CurrentDeploymentID.String)
	if err != nil || !d.ContainerName.Valid {
		return false
	}
	c, err := s.Docker.InspectContainer(ctx, d.ContainerName.String)
	return err == nil && c.State != nil && c.State.Running
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
	s.cancelInflight(row.ID, 30*time.Second)
	now := s.now()
	if ids, err := s.Q.CancelQueuedDeployments(ctx, db.CancelQueuedDeploymentsParams{FinishedAt: sql.NullInt64{Int64: now, Valid: true}, AppID: row.ID}); err == nil {
		for _, id := range ids {
			s.NotifyDeployment(id)
		}
	}
	l := s.appLock(row.ID)
	l.Lock()
	defer l.Unlock()
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "stopped", Status: "stopped", StatusMessage: "", UpdatedAt: now, ID: row.ID,
	}); err != nil {
		return apperr.Internal(err)
	}
	// Take the route away first so the catch-all answers "This app is stopped".
	if err := s.syncRouteLocked(ctx, row.ID); err != nil {
		return err
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
			continue
		}
		if err := s.stopApp(ctx, System, r); err != nil && first == nil {
			first = err
		}
	}
	return first
}
