package app

// OWNER: deploy agent. Retention: images (last 3 per app), build logs (last
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
	keepImages = 3
	keepLogs   = 20
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

	// Source snapshots: keep the current deployment's and the newest files/upload one.
	keepSrc := map[string]bool{}
	if appErr == nil && app.CurrentDeploymentID.Valid {
		keepSrc[app.CurrentDeploymentID.String] = true
	}
	for _, d := range deps {
		if (d.SourceKind == "files" || d.SourceKind == "upload") && d.State != "cancelled" {
			keepSrc[d.ID] = true
			break
		}
	}
	for _, d := range deps {
		if !keepSrc[d.ID] {
			_ = os.Remove(s.sourceZip(d.ID))
		}
	}

	// Images: the newest 3 that ran, the current one and anything in flight stay.
	if s.Docker == nil {
		return
	}
	keep := map[string]bool{}
	ran := 0
	for _, d := range deps {
		if !d.Image.Valid || d.Image.String == "" {
			continue
		}
		switch d.State {
		case "running", "superseded":
			if !keep[d.Image.String] && ran < keepImages {
				keep[d.Image.String] = true
				ran++
			}
		case "queued", "fetching", "validating", "building", "starting", "health_check", "swapping":
			keep[d.Image.String] = true
		}
	}
	if appErr == nil && app.CurrentDeploymentID.Valid {
		for _, d := range deps {
			if d.ID == app.CurrentDeploymentID.String && d.Image.Valid {
				keep[d.Image.String] = true
			}
		}
	}
	tried := map[string]bool{}
	for _, d := range deps {
		img := d.Image.String
		if !d.Image.Valid || img == "" || keep[img] || tried[img] || len(tried) >= 50 {
			continue
		}
		tried[img] = true
		if err := s.Docker.RemoveImage(ctx, img); err != nil && !strings.Contains(err.Error(), "in use") && !strings.Contains(err.Error(), "conflict") {
			s.Log.Debug("remove old image", "image", img, "err", err)
		}
	}
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
