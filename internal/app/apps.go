package app

// OWNER: deploy agent. Apps, deployments, sources (files, uploads), env vars,
// lifecycle, logs, the deploy worker and the reconciler. Signatures are a
// shared contract used by internal/api and internal/mcp; implement the
// bodies, keep the signatures. Split into more files freely
// (apps_*.go, deployments.go, worker.go, reconcile.go, …).

import (
	"context"
	"io"
	"time"

	"github.com/arno/bitploy/internal/apperr"
)

// ---- apps ----

func (s *Service) CreateApp(ctx context.Context, a Actor, in CreateAppInput) (App, error) {
	return App{}, apperr.NotImplemented("create app")
}

func (s *Service) ListApps(ctx context.Context, a Actor) ([]App, error) {
	return nil, apperr.NotImplemented("list apps")
}

// ListAllApps is admin-only: every app on the server.
func (s *Service) ListAllApps(ctx context.Context, a Actor) ([]App, error) {
	return nil, apperr.NotImplemented("list all apps")
}

func (s *Service) GetApp(ctx context.Context, a Actor, ref string) (App, error) {
	return App{}, apperr.NotImplemented("get app")
}

func (s *Service) UpdateApp(ctx context.Context, a Actor, ref string, in UpdateAppInput) (App, error) {
	return App{}, apperr.NotImplemented("update app")
}

// DeleteApp requires confirmSlug == the app's slug.
func (s *Service) DeleteApp(ctx context.Context, a Actor, ref, confirmSlug string) error {
	return apperr.NotImplemented("delete app")
}

// ---- sources & deployments ----

// DeployFiles stores inline files (2 MB, 200 files), runs the Dockerfile
// checks synchronously (so the agent gets errors at once), and queues a deployment.
func (s *Service) DeployFiles(ctx context.Context, a Actor, ref string, files []FileInput) (Deployment, error) {
	return Deployment{}, apperr.NotImplemented("deploy files")
}

// CreateUpload makes a one-time upload URL (15 min) and the exact curl command.
func (s *Service) CreateUpload(ctx context.Context, a Actor, ref string) (Upload, error) {
	return Upload{}, apperr.NotImplemented("uploads")
}

// ReceiveUpload stores the raw zip body for POST /u/{id} (no actor: the ID is
// the credential). Max 95 MB. Returns what to do next.
func (s *Service) ReceiveUpload(ctx context.Context, uploadID string, body io.Reader) (map[string]any, error) {
	return nil, apperr.NotImplemented("uploads")
}

// Deploy queues a deployment from an upload (uploadID != "") or the app's git source.
func (s *Service) Deploy(ctx context.Context, a Actor, ref, uploadID string) (Deployment, error) {
	return Deployment{}, apperr.NotImplemented("deploy")
}

// Redeploy queues a deployment that rebuilds the current deployment's source
// (git: latest commit; files/upload: the stored snapshot if still present).
func (s *Service) Redeploy(ctx context.Context, a Actor, ref string) (Deployment, error) {
	return Deployment{}, apperr.NotImplemented("redeploy")
}

// GetDeployment returns a deployment; with wait > 0 (max 50 s) it long-polls
// until the state changes to a terminal state or wait runs out.
func (s *Service) GetDeployment(ctx context.Context, a Actor, id string, wait time.Duration) (Deployment, error) {
	return Deployment{}, apperr.NotImplemented("get deployment")
}

func (s *Service) ListDeployments(ctx context.Context, a Actor, ref string, limit int, cursor string) (Page[Deployment], error) {
	return Page[Deployment]{}, apperr.NotImplemented("list deployments")
}

// Rollback reruns a previous deployment's image from `starting`.
func (s *Service) Rollback(ctx context.Context, a Actor, ref, deploymentID string) (Deployment, error) {
	return Deployment{}, apperr.NotImplemented("rollback")
}

// ---- env ----

func (s *Service) ListEnv(ctx context.Context, a Actor, ref string) ([]EnvVar, error) {
	return nil, apperr.NotImplemented("env")
}

// SetEnv sets and unsets env vars; they apply on the next deploy or restart.
func (s *Service) SetEnv(ctx context.Context, a Actor, ref string, set map[string]string, unset []string) ([]EnvVar, error) {
	return nil, apperr.NotImplemented("env")
}

// ---- lifecycle ----

func (s *Service) RestartApp(ctx context.Context, a Actor, ref string) (App, error) {
	return App{}, apperr.NotImplemented("restart")
}

func (s *Service) StartApp(ctx context.Context, a Actor, ref string) (App, error) {
	return App{}, apperr.NotImplemented("start")
}

// StopApp: owners and admins may stop an app.
func (s *Service) StopApp(ctx context.Context, a Actor, ref string) (App, error) {
	return App{}, apperr.NotImplemented("stop")
}

// ---- logs ----

// BuildLog returns the last opts.Tail lines of a deployment's build log.
func (s *Service) BuildLog(ctx context.Context, a Actor, deploymentID string, tail int) ([]LogLine, error) {
	return nil, apperr.NotImplemented("build log")
}

// FollowBuildLog streams the build log until the deployment is terminal or ctx ends.
func (s *Service) FollowBuildLog(ctx context.Context, a Actor, deploymentID string, fn func(LogLine) error) error {
	return apperr.NotImplemented("build log")
}

// Logs returns build (latest deployment) or runtime logs for an app, max 1000 lines.
func (s *Service) Logs(ctx context.Context, a Actor, ref string, opts LogOptions) ([]LogLine, error) {
	return nil, apperr.NotImplemented("logs")
}

// FollowRuntimeLogs streams runtime logs from the current container.
func (s *Service) FollowRuntimeLogs(ctx context.Context, a Actor, ref string, tail int, fn func(LogLine) error) error {
	return apperr.NotImplemented("logs")
}

// ---- routing & background ----

// SyncRoute rewrites an app's Traefik route file from the database: the
// current running container plus every active custom domain. Called after
// swaps, stops, starts, and by the domains code when a domain becomes active.
func (s *Service) SyncRoute(ctx context.Context, appID string) error {
	return apperr.NotImplemented("routes")
}

// RunWorker runs the deploy queue until ctx ends (max parallel builds from config).
func (s *Service) RunWorker(ctx context.Context) {}

// RunReconciler repairs drift on startup and every 60 s until ctx ends.
func (s *Service) RunReconciler(ctx context.Context) {}

// StopAllForUser stops every app of a user (used when suspending).
func (s *Service) StopAllForUser(ctx context.Context, userID string) error {
	return apperr.NotImplemented("stop all")
}
