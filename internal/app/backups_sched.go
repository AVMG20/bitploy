package app

// Automatic backups and their retention.
//
// Schedule: "daily" runs at 03:00 UTC every day, "weekly" at 03:00 UTC on
// Sunday. A backup is due when the newest automatic attempt of the app is older
// than the latest such moment, so a missed one (bitploy was down, the setting
// was just switched on) is made at the next check. The check runs every ten
// minutes.
//
// Retention: either keep the newest N, or keep N days. It applies to automatic
// and before-restore backups, each kind on its own, and the newest one of a kind
// is always kept. Manual and uploaded backups stay until the user deletes them.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	backupCheckEvery = 10 * time.Minute
	backupHour       = 3 // UTC
	pruneFailedAfter = 14 * 24 * time.Hour
)

// latestSlot is the most recent moment at or before now at which a backup of
// this schedule is due.
func latestSlot(schedule string, now time.Time) time.Time {
	t := now.UTC()
	slot := time.Date(t.Year(), t.Month(), t.Day(), backupHour, 0, 0, 0, time.UTC)
	switch schedule {
	case "weekly":
		slot = slot.AddDate(0, 0, -int(slot.Weekday())) // back to Sunday
		if slot.After(t) {
			slot = slot.AddDate(0, 0, -7)
		}
	default:
		if slot.After(t) {
			slot = slot.AddDate(0, 0, -1)
		}
	}
	return slot
}

func slotPeriod(schedule string) int {
	if schedule == "weekly" {
		return 7
	}
	return 1
}

// backupDue reports whether an automatic backup is due: lastAttempt is the unix
// time of the newest automatic attempt (0 for none).
func backupDue(schedule string, lastAttempt int64, now time.Time) bool {
	if schedule != "daily" && schedule != "weekly" {
		return false
	}
	return lastAttempt < latestSlot(schedule, now).Unix()
}

// nextBackupAt is when the next automatic backup happens; a time that has
// passed means it is due and will run at the next check.
func nextBackupAt(schedule string, lastAttempt int64, now time.Time) time.Time {
	slot := latestSlot(schedule, now)
	if lastAttempt < slot.Unix() {
		return slot
	}
	return slot.AddDate(0, 0, slotPeriod(schedule))
}

type retainable struct {
	ID        string
	CreatedAt int64
}

// backupsToPrune says which of items (all of one kind, finished) retention
// removes: mode "last" keeps the newest keep, mode "days" keeps those younger
// than keep days. The newest is never removed.
func backupsToPrune(items []retainable, mode string, keep int64, now time.Time) []string {
	if len(items) <= 1 || keep < 1 {
		return nil
	}
	sorted := append([]retainable(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt != sorted[j].CreatedAt {
			return sorted[i].CreatedAt > sorted[j].CreatedAt
		}
		return sorted[i].ID > sorted[j].ID
	})
	var out []string
	switch mode {
	case "days":
		cutoff := now.Add(-time.Duration(keep) * 24 * time.Hour).Unix()
		for _, it := range sorted[1:] {
			if it.CreatedAt < cutoff {
				out = append(out, it.ID)
			}
		}
	default:
		for int64(len(sorted)) > keep {
			out = append(out, sorted[len(sorted)-1].ID)
			sorted = sorted[:len(sorted)-1]
		}
	}
	return out
}

// pruneBackups applies retention to one app unless a backup or restore is
// running there.
func (s *Service) pruneBackups(ctx context.Context, app db.App) {
	l := s.backupLock(app.ID)
	if !l.TryLock() {
		return
	}
	defer l.Unlock()
	s.pruneLocked(ctx, app)
}

// pruneLocked is pruneBackups for callers that hold the backup lock.
func (s *Service) pruneLocked(ctx context.Context, app db.App) {
	rows, err := s.Q.ListBackupsByApp(ctx, app.ID)
	if err != nil {
		return
	}
	// The settings may have changed since the caller read the app.
	if fresh, err := s.Q.GetApp(ctx, app.ID); err == nil {
		app = fresh
	}
	now := s.Now()
	kinds := map[string][]retainable{}
	for _, r := range rows {
		switch {
		case r.Status == backupFailed:
			if ts(r.CreatedAt).Before(now.Add(-pruneFailedAfter)) {
				s.dropBackup(ctx, app, r.ID)
			}
		case r.Status == backupReady && (r.Kind == BackupScheduled || r.Kind == BackupBeforeRestore):
			kinds[r.Kind] = append(kinds[r.Kind], retainable{ID: r.ID, CreatedAt: r.CreatedAt})
		}
	}
	for _, items := range kinds {
		for _, id := range backupsToPrune(items, app.BackupKeepMode, app.BackupKeep, now) {
			s.dropBackup(ctx, app, id)
		}
	}
}

func (s *Service) dropBackup(ctx context.Context, app db.App, id string) {
	if err := s.removeBackup(ctx, app.ID, id); err != nil {
		s.Log.Warn("removing old backup failed", "app", app.Slug, "backup", id, "err", err)
		return
	}
	s.Log.Info("old backup removed", "app", app.Slug, "backup", id)
}

// runDueBackups starts the automatic backups that are due and applies
// retention to every app. The backups run in goroutines of their own, at most
// backupConcurrency at a time, so a slow one does not hold up the others or
// the next check.
func (s *Service) runDueBackups(ctx context.Context) {
	apps, err := s.Q.ListAllApps(ctx)
	if err != nil {
		s.Log.Warn("listing apps for backups failed", "err", err)
		return
	}
	for _, app := range apps {
		if ctx.Err() != nil {
			return
		}
		s.pruneBackups(ctx, app)
		if app.BackupSchedule == "off" || app.VolumeEnabled == 0 || !app.CurrentDeploymentID.Valid {
			continue
		}
		last, err := s.Q.LatestScheduledBackupAt(ctx, app.ID)
		if err != nil || !backupDue(app.BackupSchedule, last, s.Now()) {
			continue
		}
		s.startScheduledBackup(ctx, app)
	}
}

// startScheduledBackup queues one automatic backup. The app's backup lock is
// taken here and held until the job is done, so the next check does not queue
// the same app twice while it waits for a free slot.
func (s *Service) startScheduledBackup(ctx context.Context, app db.App) {
	owner, err := s.Q.GetUser(ctx, app.UserID)
	if err != nil || owner.Status != "active" {
		return
	}
	if _, err := s.volumes(); err != nil {
		return // no Docker right now; try again at the next check
	}
	l := s.backupLock(app.ID)
	if !l.TryLock() {
		return
	}
	s.backupGo(func() {
		defer l.Unlock()
		sem := s.backupSem()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return
		}
		jctx, cancel := s.backupCtx()
		defer cancel()
		s.runScheduledBackup(jctx, app, owner)
	})
}

func (s *Service) backupSem() chan struct{} {
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	if s.backup.sem == nil {
		s.backup.sem = make(chan struct{}, backupConcurrency)
	}
	return s.backup.sem
}

// runScheduledBackup makes one automatic backup; the caller holds the backup
// lock. A backup that cannot be made is recorded as failed, with the reason, so
// the user can see it; that also counts as this period's attempt, unless it was
// cut short (bitploy stopped, or a deployment was in the way): those are tried
// again at the next check.
func (s *Service) runScheduledBackup(ctx context.Context, app db.App, owner db.User) {
	// What the loop read may be minutes old.
	fresh, err := s.Q.GetApp(ctx, app.ID)
	if err != nil || fresh.VolumeEnabled == 0 || fresh.BackupSchedule == "off" {
		return
	}
	app = fresh
	b, err := s.insertBackup(ctx, app.ID, BackupScheduled)
	if err != nil {
		return
	}
	fail := func(msg string) {
		_ = s.Q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
			Status: backupFailed, Error: msg, FinishedAt: nullInt(s.now(), true), ID: b.ID,
		})
	}
	if err := s.checkBackupDisk(); err != nil {
		fail("The server is almost out of disk space.")
		return
	}
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		fail("Something went wrong on the server.")
		return
	}
	if room == 0 {
		fail("Skipped: there is no storage left for a backup. Delete an old backup or lower the /data limit of an app.")
		return
	}
	if err := s.finishBackup(ctx, app, owner, b.ID, room, false); err != nil {
		if errors.Is(err, dockerx.ErrNoVolume) || errors.Is(err, errDeployBusy) {
			_ = s.Q.DeleteBackup(context.WithoutCancel(ctx), b.ID) // not started: nothing to report, tried again at the next check
			return
		}
		s.Log.Warn("automatic backup failed", "app", app.Slug, "err", err)
		return
	}
	s.pruneLocked(ctx, app)
}

// runBackups is the background loop of automatic backups. It also repairs what
// a crash left behind.
func (s *Service) runBackups(ctx context.Context) {
	s.recoverAfterCrash(ctx)
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.runDueBackups(ctx)
			timer.Reset(backupCheckEvery)
		}
	}
}

// recoverAfterCrash repairs what a stop of bitploy in the middle of a backup or
// restore left behind: backups that were running are marked failed (and do not
// count as an automatic attempt), half-written files and helper containers are
// removed, frozen apps thawed, and interrupted restores finished.
func (s *Service) recoverAfterCrash(ctx context.Context) {
	if err := s.Q.FailRunningBackups(ctx, db.FailRunningBackupsParams{
		Error: "bitploy was restarted while this backup was being made.", FinishedAt: nullInt(s.now(), true),
	}); err != nil {
		s.Log.Warn("cleaning stale backups failed", "err", err)
	}
	dirs, _ := filepath.Glob(s.Cfg.Path("backups", "app_*"))
	for _, d := range dirs {
		tmps, _ := filepath.Glob(filepath.Join(d, "*.tmp"))
		for _, t := range tmps {
			if strings.HasSuffix(t, ".tar.gz.tmp") {
				_ = os.Remove(t)
			}
		}
	}
	if s.Docker != nil && s.backup.vol == nil {
		removed, resumed, err := s.Docker.CleanupHelpers(ctx)
		if err != nil {
			s.Log.Warn("cleaning up backup helpers failed", "err", err)
		} else if removed+resumed > 0 {
			s.Log.Warn("repaired after an interrupted backup", "helpers_removed", removed, "apps_resumed", resumed)
		}
	}
	s.recoverRestores(ctx)
}
