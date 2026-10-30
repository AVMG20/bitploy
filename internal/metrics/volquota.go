package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// Volume quota: apps.volume_mb is the size limit of each app's /data volume
// (users.quota_volume_mb is the total the user hands out to their apps).
// EnforceVolumeQuota measures the volumes (the same measurement the
// nightly job does, so app_disk stays current) and then:
//   - over the limit: stops the app, sets status failed with a plain message,
//     and writes an audit row;
//   - at 80% or more: puts a warning in the status message of a running app.
const (
	volumeWarnFraction = 0.8
	// msgVolumeWarn starts every warning, so it can be recognised and cleared again.
	msgVolumeWarn = "Warning: the /data volume is almost full"
	// msgVolumeOver starts the failed message.
	msgVolumeOver = "The /data volume is over its"

	volumeQuotaEvery = time.Hour
)

func quotaText(mb int64) string {
	if mb%1024 == 0 {
		return fmt.Sprintf("%d GB", mb/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

func sizeText(b int64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// VolumeOverMessage is the status message of an app stopped for exceeding its volume limit.
func VolumeOverMessage(limitMB, usedBytes int64) string {
	return fmt.Sprintf("%s %s limit (it holds %s), so bitploy stopped the app. "+
		"Delete files you no longer need from /data (from inside the app), or give the app a bigger storage limit (Settings in the dashboard, or update_app volume_mb), then start the app again.",
		msgVolumeOver, quotaText(limitMB), sizeText(usedBytes))
}

// VolumeWarnMessage is the warning for a volume at 80% of its limit or more.
func VolumeWarnMessage(limitMB, usedBytes int64) string {
	return fmt.Sprintf("%s: it holds %s of its %s limit. At 100%% bitploy stops the app. "+
		"Delete files you no longer need from /data, or give the app a bigger storage limit (Settings in the dashboard, or update_app volume_mb).",
		msgVolumeWarn, sizeText(usedBytes), quotaText(limitMB))
}

func (m *Manager) runVolumeQuota(ctx context.Context) {
	m.logErr("volume quota", m.EnforceVolumeQuota(ctx))
	t := time.NewTicker(volumeQuotaEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.logErr("volume quota", m.EnforceVolumeQuota(ctx))
		}
	}
}

// EnforceVolumeQuota measures all app volumes and applies the volume quota.
func (m *Manager) EnforceVolumeQuota(ctx context.Context) error {
	if m.Docker == nil {
		return nil
	}
	if err := m.MeasureDisk(ctx); err != nil {
		return err
	}
	rows, err := m.Store.Q.OpsListVolumeUsage(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.VolumeMb <= 0 {
			continue
		}
		limit := r.VolumeMb << 20
		switch {
		case r.VolumeBytes > limit:
			if r.DesiredState == "running" {
				m.stopForVolume(ctx, r)
			}
		case float64(r.VolumeBytes) >= volumeWarnFraction*float64(limit):
			if r.Status == "running" && (r.StatusMessage == "" || strings.HasPrefix(r.StatusMessage, msgVolumeWarn)) {
				m.setVolumeStatus(ctx, r, "running", VolumeWarnMessage(r.VolumeMb, r.VolumeBytes))
			}
		default:
			if strings.HasPrefix(r.StatusMessage, msgVolumeWarn) {
				m.setVolumeStatus(ctx, r, r.Status, "")
			}
		}
	}
	return nil
}

func (m *Manager) setVolumeStatus(ctx context.Context, r db.OpsListVolumeUsageRow, status, msg string) {
	if err := m.Store.Q.SetAppStatus(ctx, db.SetAppStatusParams{
		Status: status, StatusMessage: msg, UpdatedAt: m.now().Unix(), ID: r.ID,
	}); err != nil {
		m.Log.Error("setting volume status failed", "app", r.ID, "err", err)
	}
}

// stopForVolume stops an app whose volume is over its limit. desired_state
// changes first, so the container's die event is not read as a crash.
func (m *Manager) stopForVolume(ctx context.Context, r db.OpsListVolumeUsageRow) {
	if err := m.Store.Q.OpsSetAppDesiredState(ctx, db.OpsSetAppDesiredStateParams{
		DesiredState: "stopped", UpdatedAt: m.now().Unix(), ID: r.ID,
	}); err != nil {
		m.Log.Error("stopping app over volume quota failed", "app", r.ID, "err", err)
		return
	}
	m.setVolumeStatus(ctx, r, "failed", VolumeOverMessage(r.VolumeMb, r.VolumeBytes))
	if cs, err := m.Docker.ManagedContainers(ctx, false); err == nil {
		for _, c := range cs {
			if c.AppID == r.ID {
				_ = m.Docker.StopContainerGrace(ctx, c.Name, 10)
			}
		}
	}
	m.audit(ctx, "app.volume_quota_exceeded", "app", r.ID, map[string]any{
		"slug": r.Slug, "volume_bytes": r.VolumeBytes, "limit_mb": r.VolumeMb,
	})
	if m.SyncRoute != nil {
		if err := m.SyncRoute(ctx, r.ID); err != nil {
			m.Log.Warn("route sync after volume quota stop failed", "app", r.ID, "err", err)
		}
	}
}
