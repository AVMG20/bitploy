package metrics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arno/bitploy/internal/store/db"
)

const (
	rawRetention    = 24 * time.Hour
	rollupRetention = 30 * 24 * time.Hour
	auditRetention  = 90 * 24 * time.Hour
	uploadRetention = 24 * time.Hour
	usageRetention  = 45 * 24 * time.Hour
	backupsToKeep   = 7

	// nightlyHour is the UTC hour the nightly job starts (03:30).
	nightlyHour, nightlyMinute = 3, 30
)

// runSchedule runs the hourly rollup and the nightly job.
func (m *Manager) runSchedule(ctx context.Context) {
	m.logErr("rollup", m.Rollup(ctx))
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		wait := time.Until(NextNightly(m.now()))
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			m.logErr("rollup", m.Rollup(ctx))
		case <-time.After(wait):
			m.Nightly(ctx)
		}
	}
}

// NextNightly is the next 03:30 UTC after t.
func NextNightly(t time.Time) time.Time {
	t = t.UTC()
	next := time.Date(t.Year(), t.Month(), t.Day(), nightlyHour, nightlyMinute, 0, 0, time.UTC)
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (m *Manager) logErr(job string, err error) {
	if err != nil {
		m.Log.Error("background job failed", "job", job, "err", err)
	}
}

// Nightly runs every housekeeping step; one failing step never stops the rest.
func (m *Manager) Nightly(ctx context.Context) {
	m.Log.Info("nightly job started")
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"rollup", m.Rollup},
		{"prune metrics", m.PruneMetrics},
		{"measure disk", m.MeasureDisk},
		{"docker prune", m.PruneDocker},
		{"database backup", m.BackupDB},
		{"prune audit log", m.PruneAudit},
		{"delete old uploads", m.DeleteOldUploads},
	}
	for _, s := range steps {
		if ctx.Err() != nil {
			return
		}
		if err := s.fn(ctx); err != nil {
			m.Log.Error("nightly step failed", "step", s.name, "err", err)
		}
	}
	m.Log.Info("nightly job finished")
}

// Rollup condenses raw samples into 5-minute buckets for every app. Only
// complete buckets are written, and a bucket is never touched again, so the
// job is safe to run as often as you like.
func (m *Manager) Rollup(ctx context.Context) error {
	q := m.Store.Q
	apps, err := q.OpsListSampleApps(ctx)
	if err != nil {
		return err
	}
	end := BucketStart(m.now().Unix())
	for _, appID := range apps {
		last, err := q.OpsLastRollupTS(ctx, appID)
		if err != nil {
			return err
		}
		from := int64(0)
		if last > 0 {
			from = last + BucketSeconds
		}
		if from >= end {
			continue
		}
		rows, err := q.OpsListSamplesBefore(ctx, db.OpsListSamplesBeforeParams{AppID: appID, Ts: from, Ts_2: end})
		if err != nil {
			return err
		}
		samples := make([]Sample, len(rows))
		for i, r := range rows {
			samples[i] = Sample{TS: r.Ts, CPU: r.Cpu, Mem: r.MemBytes, Rx: r.RxBytes, Tx: r.TxBytes}
		}
		rolled := Rollup(samples)
		if len(rolled) == 0 {
			continue
		}
		err = m.Store.Tx(ctx, func(q *db.Queries) error {
			for _, r := range rolled {
				if err := q.OpsUpsertRollup(ctx, db.OpsUpsertRollupParams{
					AppID: appID, Ts: r.TS, Cpu: r.CPU, MemBytes: r.Mem, RxBytes: r.Rx, TxBytes: r.Tx,
					CpuMax: r.CPUMax, MemMax: r.MemMax,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// PruneMetrics deletes raw samples older than 24 h and rollups older than 30 days.
func (m *Manager) PruneMetrics(ctx context.Context) error {
	now := m.now()
	if _, err := m.Store.Q.OpsDeleteSamplesBefore(ctx, now.Add(-rawRetention).Unix()); err != nil {
		return err
	}
	if _, err := m.Store.Q.OpsDeleteRollupsBefore(ctx, now.Add(-rollupRetention).Unix()); err != nil {
		return err
	}
	// Rows of deleted apps (these tables have no foreign key).
	for _, del := range []func(context.Context) error{
		m.Store.Q.OpsDeleteOrphanSamples, m.Store.Q.OpsDeleteOrphanRollups, m.Store.Q.OpsDeleteOrphanAppDisk,
	} {
		if err := del(ctx); err != nil {
			return err
		}
	}
	return m.Store.Q.OpsDeleteOrphanUsage(ctx, now.Add(-usageRetention).Format("2006-01-02"))
}

// MeasureDisk records image and volume sizes per app in app_disk. An app's
// image size is its largest image (its current one); volumes are attributed
// by label.
func (m *Manager) MeasureDisk(ctx context.Context) error {
	if m.Docker == nil {
		return nil
	}
	du, err := m.Docker.DiskUsage(ctx, true)
	if err != nil {
		return err
	}
	images := map[string]int64{}
	for _, im := range du.Images {
		if im.AppID != "" && im.Size > images[im.AppID] {
			images[im.AppID] = im.Size
		}
	}
	volumes := map[string]int64{}
	for _, v := range du.Volumes {
		if v.AppID != "" {
			volumes[v.AppID] += v.Size
		}
	}
	apps, err := m.Store.Q.OpsListAppVolumes(ctx)
	if err != nil {
		return err
	}
	now := m.now().Unix()
	return m.Store.Tx(ctx, func(q *db.Queries) error {
		for _, a := range apps {
			vol := volumes[a.ID]
			if vol == 0 {
				// Fall back to the volume name when the label is missing.
				for _, v := range du.Volumes {
					if v.Name == "bp-"+a.Slug+"-data" {
						vol = v.Size
					}
				}
			}
			if err := q.OpsUpsertAppDisk(ctx, db.OpsUpsertAppDiskParams{
				AppID: a.ID, ImageBytes: images[a.ID], VolumeBytes: vol, MeasuredAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneDocker removes dangling bitploy images and trims the build cache.
// It does nothing in dev mode, where the Docker daemon is the developer's own.
func (m *Manager) PruneDocker(ctx context.Context) error {
	if m.Docker == nil || m.Dev {
		return nil
	}
	images, err := m.Docker.PruneDanglingImages(ctx)
	if err != nil {
		return fmt.Errorf("image prune: %w", err)
	}
	cache, err := m.Docker.PruneBuildCache(ctx, m.BuildCacheKeepBytes)
	if err != nil {
		return fmt.Errorf("builder prune: %w", err)
	}
	m.Log.Info("docker pruned", "images_reclaimed_bytes", images, "build_cache_reclaimed_bytes", cache)
	return nil
}

// BackupDir is where VACUUM INTO writes the database copies.
func (m *Manager) BackupDir() string { return filepath.Join(m.DataDir, "backups") }

// BackupDB writes a consistent copy of the database with VACUUM INTO and
// keeps the newest seven copies. The restic backup script picks them up.
func (m *Manager) BackupDB(ctx context.Context) error {
	if m.DataDir == "" {
		return nil
	}
	dir := m.BackupDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	name := "bitploy-" + m.now().UTC().Format("20060102T150405Z") + ".db"
	path := filepath.Join(dir, name)
	if _, err := m.Store.DB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("vacuum into %s: %w", path, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, "bitploy-") && strings.HasSuffix(n, ".db") {
			files = append(files, n)
		}
	}
	sort.Strings(files) // timestamps sort lexically
	for len(files) > backupsToKeep {
		_ = os.Remove(filepath.Join(dir, files[0]))
		files = files[1:]
	}
	return nil
}

// PruneAudit deletes audit rows older than 90 days.
func (m *Manager) PruneAudit(ctx context.Context) error {
	return m.Store.Q.DeleteOldAudit(ctx, m.now().Add(-auditRetention).Unix())
}

// DeleteOldUploads removes upload rows and zip files older than 24 h.
func (m *Manager) DeleteOldUploads(ctx context.Context) error {
	cutoff := m.now().Add(-uploadRetention)
	ids, err := m.Store.Q.OpsListUploadsBefore(ctx, cutoff.Unix())
	if err != nil {
		return err
	}
	dir := filepath.Join(m.DataDir, "uploads")
	for _, id := range ids {
		if strings.ContainsAny(id, `/\`) || id == "" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, id+".zip")); err != nil && !os.IsNotExist(err) {
			m.Log.Warn("deleting upload file failed", "upload", id, "err", err)
		}
		if err := m.Store.Q.OpsDeleteUpload(ctx, id); err != nil {
			return err
		}
	}
	// Files whose row is already gone.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}
