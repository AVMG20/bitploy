package app

import (
	"context"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// ProjectPowerResult is what start all or stop all did to a project's apps.
type ProjectPowerResult struct {
	Project Project `json:"project"`
	// Changed are the apps that were started or stopped; Skipped the ones
	// that already were, or (for start) never deployed.
	Changed []string            `json:"changed" doc:"Slugs of the apps that were started or stopped"`
	Skipped []ProjectAppSkipped `json:"skipped" doc:"Apps left as they were, with why"`
	Failed  []ProjectAppFailure `json:"failed" doc:"Apps that could not be started or stopped, with the error; the others still were"`
}

// ProjectAppSkipped is an app start all or stop all left alone.
type ProjectAppSkipped struct {
	App    string `json:"app"`
	Reason string `json:"reason" enum:"already_running,already_stopped,not_deployed"`
}

// ProjectAppFailure is an app start all or stop all could not change.
type ProjectAppFailure struct {
	App     string `json:"app"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// isDataService reports whether the app is a database or other service
// made from a template (add_service): the apps that others connect to.
func isDataService(row db.App) bool { return row.Template != "" }

// StartProject starts every stopped app of a project: its databases and
// other services first, so the apps that use them find them running. Apps
// that never ran are left alone. One app that cannot start (quota, busy)
// does not stop the others; it is listed in failed.
func (s *Service) StartProject(ctx context.Context, a Actor, ref string) (ProjectPowerResult, error) {
	return s.powerProject(ctx, a, ref, true)
}

// StopProject stops every running app of a project: the apps first, then
// the databases and services they use.
func (s *Service) StopProject(ctx context.Context, a Actor, ref string) (ProjectPowerResult, error) {
	return s.powerProject(ctx, a, ref, false)
}

func (s *Service) powerProject(ctx context.Context, a Actor, ref string, start bool) (ProjectPowerResult, error) {
	p, err := s.loadProject(ctx, a, ref, projectUse)
	if err != nil {
		return ProjectPowerResult{}, err
	}
	rows, err := s.Q.ListAppsByProject(ctx, p.ID)
	if err != nil {
		return ProjectPowerResult{}, apperr.Internal(err)
	}
	// Starting: services first. Stopping: services last.
	var first, last []db.App
	for _, r := range rows {
		if isDataService(r) == start {
			first = append(first, r)
		} else {
			last = append(last, r)
		}
	}
	res := ProjectPowerResult{Changed: []string{}, Skipped: []ProjectAppSkipped{}, Failed: []ProjectAppFailure{}}
	for _, r := range append(first, last...) {
		switch {
		case start && !r.CurrentDeploymentID.Valid:
			res.Skipped = append(res.Skipped, ProjectAppSkipped{App: r.Slug, Reason: "not_deployed"})
			continue
		case start && r.DesiredState == "running":
			res.Skipped = append(res.Skipped, ProjectAppSkipped{App: r.Slug, Reason: "already_running"})
			continue
		case !start && r.DesiredState == "stopped":
			res.Skipped = append(res.Skipped, ProjectAppSkipped{App: r.Slug, Reason: "already_stopped"})
			continue
		}
		var err error
		if start {
			_, err = s.StartApp(ctx, a, r.ID)
		} else {
			_, err = s.StopApp(ctx, a, r.ID)
		}
		if err != nil {
			ae := apperr.As(err)
			res.Failed = append(res.Failed, ProjectAppFailure{App: r.Slug, Code: ae.Code, Message: apperr.Redact(ae.Message), Hint: apperr.Redact(ae.Hint)})
			continue
		}
		res.Changed = append(res.Changed, r.Slug)
	}
	action := map[bool]string{true: "project.start_all", false: "project.stop_all"}[start]
	s.Audit(ctx, a, action, "project", p.ID, map[string]any{
		"name": p.Name, "changed": len(res.Changed), "failed": len(res.Failed),
	})
	fresh, err := s.Q.GetProject(ctx, p.ID)
	if err != nil {
		return ProjectPowerResult{}, apperr.Internal(err)
	}
	if res.Project, err = s.projectView(ctx, a, fresh); err != nil {
		return ProjectPowerResult{}, err
	}
	return res, nil
}
