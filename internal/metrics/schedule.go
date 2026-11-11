package metrics

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
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
	m.rollupJob(ctx)
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		wait := time.Until(NextNightly(m.now()))
		select {
		case <-ctx.Done():
			return
		case <-hourly.C:
			m.rollupJob(ctx)
		case <-time.After(wait):
			m.Nightly(ctx)
		}
	}
}

// rollupJob is one scheduled Rollup, logged and reported.
func (m *Manager) rollupJob(ctx context.Context) {
	start := m.now()
	err := m.Rollup(ctx)
	m.logErr("rollup", err)
	m.jobRan(JobRollup, start, err, "")
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
	// Each step says in a few words what it did, for the admin's Jobs page.
	steps := []struct {
		name, job string
		fn        func(context.Context) (string, error)
	}{
		{"rollup", JobRollup, func(ctx context.Context) (string, error) { return "", m.Rollup(ctx) }},
		{"prune metrics", JobPruneMetrics, m.pruneMetrics},
		{"measure disk", JobMeasureDisk, m.measureDisk},
		{"docker prune", JobDockerPrune, m.pruneDocker},
		{"database backup", JobBackupDB, m.backupDB},
		{"prune audit log", JobPruneAudit, func(ctx context.Context) (string, error) { return "", m.PruneAudit(ctx) }},
		{"delete old uploads", JobOldUploads, m.deleteOldUploads},
	}
	for _, s := range steps {
		if ctx.Err() != nil {
			return
		}
		start := m.now()
		summary, err := s.fn(ctx)
		if err != nil {
			m.Log.Error("nightly step failed", "step", s.name, "err", err)
		}
		m.jobRan(s.job, start, err, summary)
	}
	m.Log.Info("nightly job finished")
}

// Rollup condenses raw samples into 5-minute buckets for every app service
// and for the server itself (host_samples). Only
// complete buckets are written, and a bucket is never touched again, so the
// job is safe to run as often as you like.
func (m *Manager) Rollup(ctx context.Context) error {
	q := m.Store.Q
	apps, err := q.OpsListSampleApps(ctx)
	if err != nil {
		return err
	}
	end := BucketStart(m.now().Unix())
	for _, as := range apps {
		appID, service := as.AppID, as.Service
		last, err := q.OpsLastRollupTS(ctx, db.OpsLastRollupTSParams{AppID: appID, Service: service})
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
		rows, err := q.OpsListSamplesBefore(ctx, db.OpsListSamplesBeforeParams{AppID: appID, Service: service, Ts: from, Ts_2: end})
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
					AppID: appID, Service: service, Ts: r.TS, Cpu: r.CPU, MemBytes: r.Mem, RxBytes: r.Rx, TxBytes: r.Tx,
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
	return m.rollupHost(ctx)
}

// PruneMetrics deletes raw samples older than 24 h and rollups older than 30
// days, of the apps and of the server itself.
func (m *Manager) PruneMetrics(ctx context.Context) error {
	_, err := m.pruneMetrics(ctx)
	return err
}

// pruneMetrics is PruneMetrics; it also says how many old samples went.
func (m *Manager) pruneMetrics(ctx context.Context) (string, error) {
	now := m.now()
	q := m.Store.Q
	var deleted int64
	for _, del := range []func(context.Context, int64) (int64, error){q.OpsDeleteSamplesBefore, q.OpsDeleteHostSamplesBefore} {
		n, err := del(ctx, now.Add(-rawRetention).Unix())
		if err != nil {
			return "", err
		}
		deleted += n
	}
	for _, del := range []func(context.Context, int64) (int64, error){q.OpsDeleteRollupsBefore, q.OpsDeleteHostRollupsBefore} {
		n, err := del(ctx, now.Add(-rollupRetention).Unix())
		if err != nil {
			return "", err
		}
		deleted += n
	}
	// Rows of deleted apps (these tables have no foreign key).
	for _, del := range []func(context.Context) error{
		m.Store.Q.OpsDeleteOrphanSamples, m.Store.Q.OpsDeleteOrphanRollups, m.Store.Q.OpsDeleteOrphanAppDisk,
	} {
		if err := del(ctx); err != nil {
			return "", err
		}
	}
	if err := m.Store.Q.OpsDeleteOrphanUsage(ctx, now.Add(-usageRetention).Format("2006-01-02")); err != nil {
		return "", err
	}
	return countText(deleted, "old sample", "deleted"), nil
}

// MeasureDisk records per app in app_disk what counts towards the storage
// allowance: its images, its stored source snapshots and its volume.
func (m *Manager) MeasureDisk(ctx context.Context) error {
	_, err := m.measureDisk(ctx)
	return err
}

// measureDisk is MeasureDisk; it also says how many apps it measured.
func (m *Manager) measureDisk(ctx context.Context) (string, error) {
	if m.Docker == nil {
		return "skipped: Docker is unavailable", nil
	}
	du, err := m.Docker.DiskUsage(ctx, true)
	if err != nil {
		return "", err
	}
	images := imageBytesByApp(du.Images)
	sources, err := m.sourceBytesByApp(ctx)
	if err != nil {
		return "", err
	}
	volumes := map[string]int64{}
	for _, v := range du.Volumes {
		if v.AppID != "" {
			volumes[v.AppID] += v.Size
		}
	}
	apps, err := m.Store.Q.OpsListAppVolumes(ctx)
	if err != nil {
		return "", err
	}
	appVols, err := m.Store.Q.ListAllAppVolumes(ctx)
	if err != nil {
		return "", err
	}
	sizeByName := map[string]int64{}
	for _, v := range du.Volumes {
		sizeByName[v.Name] = v.Size
	}
	now := m.now().Unix()
	err = m.Store.Tx(ctx, func(q *db.Queries) error {
		// Every volume of an app on its own, for the per-volume limits.
		for _, v := range appVols {
			if err := q.SetAppVolumeUsed(ctx, db.SetAppVolumeUsedParams{UsedBytes: sizeByName[v.VolumeName], VolumeName: v.VolumeName}); err != nil {
				return err
			}
		}
		for _, a := range apps {
			vol := volumes[a.ID]
			if vol == 0 {
				// Fall back to the volume name when the label is missing.
				name := a.VolumeName
				if name == "" {
					name = "bp-" + a.Slug + "-data"
				}
				for _, v := range du.Volumes {
					if v.Name == name {
						vol = v.Size
					}
				}
			}
			if err := q.OpsUpsertAppDisk(ctx, db.OpsUpsertAppDiskParams{
				AppID: a.ID, ImageBytes: images[a.ID], SourceBytes: sources[a.ID], VolumeBytes: vol, MeasuredAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return countText(int64(len(apps)), "app", "measured"), nil
}

// imageBytesByApp is the disk an app's images take: its largest image (the
// current one) in full, base image included, plus what each older kept
// version adds on top (Size - SharedSize). Layers shared between versions
// count once, so a rollback copy of an unchanged base costs nothing.
func imageBytesByApp(images []dockerx.DiskImage) map[string]int64 {
	byApp := map[string][]dockerx.DiskImage{}
	for _, im := range images {
		if im.AppID != "" {
			byApp[im.AppID] = append(byApp[im.AppID], im)
		}
	}
	out := map[string]int64{}
	for id, ims := range byApp {
		largest := 0
		for i, im := range ims {
			if im.Size > ims[largest].Size {
				largest = i
			}
		}
		total := ims[largest].Size
		for i, im := range ims {
			if i == largest {
				continue
			}
			if im.SharedSize < 0 {
				total += im.Size // not computed: count it in full
			} else if own := im.Size - im.SharedSize; own > 0 {
				total += own
			}
		}
		out[id] = total
	}
	return out
}

// sourceBytesByApp adds up the stored source snapshots (sources/<deployment
// id>.zip, see app.Service.sourceZip) per app.
func (m *Manager) sourceBytesByApp(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	if m.DataDir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(filepath.Join(m.DataDir, "sources"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	sizes := map[string]int64{}
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".zip"); ok && e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				sizes[id] = info.Size()
			}
		}
	}
	if len(sizes) == 0 {
		return out, nil
	}
	deps, err := m.Store.Q.OpsListDeploymentApps(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range deps {
		out[d.AppID] += sizes[d.ID]
	}
	return out, nil
}

// PruneDocker removes dangling bitploy images and trims the build cache.
// It does nothing in dev mode, where the Docker daemon is the developer's own.
func (m *Manager) PruneDocker(ctx context.Context) error {
	_, err := m.pruneDocker(ctx)
	return err
}

// pruneDocker is PruneDocker; it also says how much it freed.
func (m *Manager) pruneDocker(ctx context.Context) (string, error) {
	switch {
	case m.Docker == nil:
		return "skipped: Docker is unavailable", nil
	case m.Dev:
		return "skipped in dev mode", nil
	}
	images, err := m.Docker.PruneDanglingImages(ctx)
	if err != nil {
		return "", fmt.Errorf("image prune: %w", err)
	}
	freed := int64(images)
	if m.BuildxPrune != nil {
		cache, err := m.BuildxPrune(ctx, m.BuildCacheKeepBytes)
		if err != nil {
			return "", fmt.Errorf("builder prune: %w", err)
		}
		freed += max(cache, 0)
	}
	m.Log.Info("docker pruned", "images_reclaimed_bytes", images, "build_cache_cap_bytes", m.BuildCacheKeepBytes)
	if freed == 0 {
		return "nothing to free", nil
	}
	return "freed " + sizeText(freed), nil
}

// BackupDir is where VACUUM INTO writes the database copies.
func (m *Manager) BackupDir() string { return filepath.Join(m.DataDir, "backups") }

// BackupDB writes a consistent copy of the database with VACUUM INTO and
// keeps the newest seven copies. The restic backup script picks them up.
func (m *Manager) BackupDB(ctx context.Context) error {
	_, err := m.backupDB(ctx)
	return err
}

// backupDB is BackupDB; it also says how big the copy is.
func (m *Manager) backupDB(ctx context.Context) (string, error) {
	if m.DataDir == "" {
		return "skipped: no data directory", nil
	}
	dir := m.BackupDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := "bitploy-" + m.now().UTC().Format("20060102T150405Z") + ".db"
	path := filepath.Join(dir, name)
	if _, err := m.Store.DB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return "", fmt.Errorf("vacuum into %s: %w", path, err)
	}
	summary := "saved a copy"
	if info, err := os.Stat(path); err == nil {
		summary = "saved a " + sizeText(info.Size()) + " copy"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
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
	return summary, nil
}

// PruneAudit deletes audit rows older than 90 days.
func (m *Manager) PruneAudit(ctx context.Context) error {
	return m.Store.Q.DeleteOldAudit(ctx, m.now().Add(-auditRetention).Unix())
}

// DeleteOldUploads removes upload rows and zip files older than 24 h.
func (m *Manager) DeleteOldUploads(ctx context.Context) error {
	_, err := m.deleteOldUploads(ctx)
	return err
}

// deleteOldUploads is DeleteOldUploads; it also says how many uploads went.
func (m *Manager) deleteOldUploads(ctx context.Context) (string, error) {
	cutoff := m.now().Add(-uploadRetention)
	ids, err := m.Store.Q.OpsListUploadsBefore(ctx, cutoff.Unix())
	if err != nil {
		return "", err
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
			return "", err
		}
	}
	summary := countText(int64(len(ids)), "upload", "deleted")
	// Files whose row is already gone.
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return summary, nil
		}
		return "", err
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
	return summary, nil
}
