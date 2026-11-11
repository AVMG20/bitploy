package app

// OWNER: deploy agent. Retention: images (last 3 versions per app unless
// the admin changed it on the disk page), build logs (last
// 20 deployments per app), source snapshots, old uploads.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	keepLogs = 20
)

// pruneApp applies the retention rules to one app. Safe to call at any time.
func (s *Service) pruneApp(ctx context.Context, appID string) {
	deps, err := s.Q.ListAllDeploymentsByApp(ctx, appID) // newest first
	if err != nil {
		return
	}
	app, appErr := s.Q.GetApp(ctx, appID)

	// Build logs: keep the newest 20 deployments.
	for i, d := range deps {
		if i >= keepLogs {
			_ = os.Remove(s.Cfg.BuildLog(d.ID))
		}
	}

	// Images: per service, the newest versions that ran (the admin's number,
	// 3 unless changed), the current one and anything
	// in flight stay. Worked out from the deployment rows, so the source rule
	// below holds without Docker too.
	imgs := s.deploymentImages(ctx, app, appErr == nil, deps)
	keepImages := s.rollbackVersions(ctx)
	keep := map[string]bool{}
	ran := map[string]int{}
	rollbackable := map[string]bool{} // deployments whose image is kept as a rollback target
	for _, e := range imgs {
		switch e.State {
		case "running", "superseded":
			if !keep[e.Image.String] && ran[e.Service] < keepImages {
				keep[e.Image.String] = true
				ran[e.Service]++
				rollbackable[e.DeploymentID] = true
			}
		case "queued", "fetching", "validating", "building", "starting", "health_check", "swapping":
			keep[e.Image.String] = true
		}
		if appErr == nil && app.CurrentDeploymentID.Valid && e.DeploymentID == app.CurrentDeploymentID.String {
			keep[e.Image.String] = true
		}
	}

	// Source snapshots: keep the live version's (for a rollback, the snapshot of
	// the version it rolled back to), the newest files/upload one, and the one
	// of every version whose image is kept, so what can be rolled back to can
	// also be rebuilt (redeploy after a rollback).
	keepSrc := map[string]bool{}
	if appErr == nil && app.CurrentDeploymentID.Valid {
		keepSrc[app.CurrentDeploymentID.String] = true
		if src := sourceDeployment(deps, app.CurrentDeploymentID.String); src != nil {
			keepSrc[src.ID] = true
		}
	}
	for _, d := range deps {
		if (d.SourceKind == "files" || d.SourceKind == "upload") && d.State != "cancelled" {
			keepSrc[d.ID] = true
			break
		}
	}
	for id := range rollbackable {
		if src := sourceDeployment(deps, id); src != nil {
			keepSrc[src.ID] = true
		}
	}
	for _, d := range deps {
		if !keepSrc[d.ID] {
			_ = os.Remove(s.sourceZip(d.ID))
		}
	}

	if s.Docker == nil {
		return
	}
	tried := map[string]bool{}
	for _, e := range imgs {
		img := e.Image.String
		if keep[img] || tried[img] || len(tried) >= 50 {
			continue
		}
		if appErr == nil && isMulti(app) && !strings.HasPrefix(img, "bitploy/") {
			continue // a registry image (postgres:16) may be used by other apps
		}
		tried[img] = true
		if err := s.Docker.RemoveImage(ctx, img); err != nil && !strings.Contains(err.Error(), "in use") && !strings.Contains(err.Error(), "conflict") {
			s.Log.Debug("remove old image", "image", img, "err", err)
		}
	}
}

// deploymentImages lists the image of every service of every deployment,
// newest deployment first. A single-layout app has one image per deployment
// row; deps is newest first.
func (s *Service) deploymentImages(ctx context.Context, app db.App, haveApp bool, deps []db.Deployment) []db.ListAppDeploymentImagesRow {
	if haveApp && isMulti(app) {
		rows, err := s.Q.ListAppDeploymentImages(ctx, app.ID)
		if err == nil {
			return rows
		}
		return nil
	}
	out := make([]db.ListAppDeploymentImagesRow, 0, len(deps))
	for _, d := range deps {
		if d.Image.Valid && d.Image.String != "" {
			out = append(out, db.ListAppDeploymentImagesRow{DeploymentID: d.ID, State: d.State, Service: singleService, Image: d.Image})
		}
	}
	return out
}

// cleanupMisc removes finished uploads, expired slug reservations and
// stray build directories. Called by the reconciler.
func (s *Service) cleanupMisc(ctx context.Context) {
	now := s.Now()
	_ = s.Q.DeleteExpiredSlugReservations(ctx, now.Unix())

	// Uploads: pending past expiry, or received/used more than 24 h ago.
	stale, err := s.Q.ListStaleUploads(ctx, db.ListStaleUploadsParams{
		Now: now.Unix(), Cutoff: nullInt(now.Add(-24*time.Hour).Unix(), true),
	})
	if err == nil {
		for _, u := range stale {
			_ = os.Remove(s.uploadPath(u.ID))
			_ = s.Q.DeleteUpload(ctx, u.ID)
		}
	}
	// Half-written upload parts older than an hour.
	if parts, err := filepath.Glob(filepath.Join(s.Cfg.UploadsDir(), "*.part-*")); err == nil {
		for _, p := range parts {
			if st, err := os.Stat(p); err == nil && now.Sub(st.ModTime()) > time.Hour {
				_ = os.Remove(p)
			}
		}
	}
}
