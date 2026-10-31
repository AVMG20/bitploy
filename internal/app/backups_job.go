package app

// The work behind backups: reading the volume into a .tar.gz, restoring one,
// deleting. Callers hold the app's backup lock (backupLock).

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// finishBackup makes the archive for the (running) row backupID and records
// the outcome on the row. It returns the error, already recorded.
func (s *Service) finishBackup(ctx context.Context, app db.App, backupID string, room int64) error {
	size, err := s.makeBackup(ctx, app, backupID, room)
	st, msg := backupReady, ""
	if err != nil {
		st, msg, size = backupFailed, backupErrText(err), 0
	}
	if uerr := s.Q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
		Status: st, SizeBytes: size, Error: msg, FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: backupID,
	}); uerr != nil {
		s.Log.Error("recording backup result failed", "backup", backupID, "err", uerr)
	}
	return err
}

// backupErrText is the plain-words reason shown for a failed backup.
func backupErrText(err error) string {
	var tp *tarProblem
	switch {
	case errors.Is(err, dockerx.ErrNoVolume):
		return "This app has no data yet. Start the app once, then try again."
	case errors.Is(err, errBackupNoRoom):
		return "There was not enough storage left. Delete an old backup or lower the /data limit of an app."
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "The backup took too long and was stopped."
	case errors.As(err, &tp):
		return tp.msg
	}
	msg := apperr.Redact(dockerx.DescribeError(err))
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300]) + "..."
	}
	return "The data folder could not be read: " + msg
}

// makeBackup writes <backups>/<app>/<id>.tar.gz from the app's volume and
// returns its size. A running app is paused while its files are being read, so
// the copy is consistent; it is always resumed.
func (s *Service) makeBackup(ctx context.Context, app db.App, id string, room int64) (int64, error) {
	vs, aerr := s.volumes()
	if aerr != nil {
		return 0, aerr
	}
	if err := os.MkdirAll(s.backupDir(app.ID), 0o750); err != nil {
		return 0, err
	}
	final := s.backupPath(app.ID, id)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	size, err := func() (int64, error) {
		defer f.Close()
		resume := s.pauseApp(ctx, app)
		defer resume()
		rc, err := vs.ArchiveVolume(ctx, appVolumeName(app))
		if err != nil {
			return 0, err
		}
		defer rc.Close()
		gz := gzip.NewWriter(&limitWriter{w: f, left: room})
		tw := tar.NewWriter(gz)
		if _, err := sanitizeTar(tw, tar.NewReader(rc), tarLimits{SkipSpecial: true}); err != nil {
			return 0, err
		}
		resume() // everything has been read; do not hold the app for the gzip tail
		if err := tw.Close(); err != nil {
			return 0, err
		}
		if err := gz.Close(); err != nil {
			return 0, err
		}
		if err := f.Sync(); err != nil {
			return 0, err
		}
		fi, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}()
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return size, nil
}

// pauseApp freezes the running containers of the app and returns the func that
// thaws them (safe to call more than once).
func (s *Service) pauseApp(ctx context.Context, app db.App) func() {
	if s.Docker == nil || s.backup.vol != nil {
		return func() {}
	}
	cs, err := s.Docker.ListAppContainers(ctx, app.ID)
	if err != nil {
		s.Log.Warn("backup: listing containers failed", "app", app.Slug, "err", err)
		return func() {}
	}
	var paused []string
	for _, c := range cs {
		if string(c.State) != "running" {
			continue
		}
		name := dockerx.SummaryName(c)
		if err := s.Docker.PauseContainer(ctx, name); err != nil {
			s.Log.Warn("backup: pausing failed", "app", app.Slug, "container", name, "err", err)
			continue
		}
		paused = append(paused, name)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			bg := context.WithoutCancel(ctx)
			for _, name := range paused {
				if err := s.Docker.UnpauseContainer(bg, name); err != nil {
					s.Log.Error("backup: resuming failed", "app", app.Slug, "container", name, "err", err)
				}
			}
		})
	}
}

// ---- restore ----

// RestoreBackup puts a backup back into /data. A backup of the current data
// is made first, the app is stopped while the files are swapped and started
// again afterwards if it was running. It returns at once; the progress is in
// the list (BackupList.Restore).
func (s *Service) RestoreBackup(ctx context.Context, a Actor, ref, id string) (BackupRestore, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return BackupRestore{}, err
	}
	b, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: id, AppID: row.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return BackupRestore{}, backupNotFound()
	}
	if err != nil {
		return BackupRestore{}, apperr.Internal(err)
	}
	if b.Status != backupReady {
		return BackupRestore{}, apperr.Conflict("backup_not_ready", "This backup is not finished or failed, so it cannot be restored.", "Pick a backup that is ready.")
	}
	if row.VolumeEnabled == 0 {
		return BackupRestore{}, volumeOffErr()
	}
	if _, err := os.Stat(s.backupPath(row.ID, b.ID)); err != nil {
		return BackupRestore{}, apperr.NotFound("backup_file_missing", "The file of this backup is gone from the server.", "Delete this backup and pick another one.")
	}
	if _, err := s.volumes(); err != nil {
		return BackupRestore{}, err
	}
	if !s.settled(row.ID) {
		return BackupRestore{}, busyErr()
	}
	if n, err := s.Q.CountPendingDeploymentsByApp(ctx, row.ID); err == nil && n > 0 {
		return BackupRestore{}, busyErr()
	}
	if err := s.checkBackupDisk(); err != nil {
		return BackupRestore{}, err
	}
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		return BackupRestore{}, apperr.Internal(err)
	}
	if room == 0 {
		e := s.noRoomErr(owner)
		e.Message = "There is no storage left to first make a backup of the current data, so nothing was restored."
		return BackupRestore{}, e
	}
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return BackupRestore{}, backupBusyErr()
	}
	st := &BackupRestore{BackupID: b.ID, State: "running", StartedAt: s.Now()}
	s.setRestore(row.ID, st)
	started := *st // the job below updates st
	s.Audit(ctx, a, "backup.restore", "app", row.ID, map[string]any{"backup": b.ID})
	s.backupGo(func() {
		defer lock.Unlock()
		ctx, cancel := backgroundCtx()
		defer cancel()
		err := s.runRestore(ctx, row, b, room)
		fin := s.Now()
		s.backup.mu.Lock()
		st.FinishedAt = &fin
		if err != nil {
			st.State, st.Error = "failed", err.Error()
		} else {
			st.State = "done"
		}
		s.backup.mu.Unlock()
		if err != nil {
			s.Log.Warn("restore failed", "app", row.Slug, "err", err)
			s.Audit(ctx, System, "backup.restore_failed", "app", row.ID, map[string]any{"backup": b.ID, "error": err.Error()})
		}
	})
	return started, nil
}

func (s *Service) setRestore(appID string, r *BackupRestore) {
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	if s.backup.restore == nil {
		s.backup.restore = map[string]*BackupRestore{}
	}
	s.backup.restore[appID] = r
}

// runRestore does the restore under the app lock. The error is in plain words.
func (s *Service) runRestore(ctx context.Context, row db.App, b db.AppBackup, room int64) (rerr error) {
	vs, err := s.volumes()
	if err != nil {
		return err
	}
	al := s.appLock(row.ID)
	al.Lock()
	defer al.Unlock()
	app, err := s.Q.GetApp(ctx, row.ID)
	if err != nil {
		return errors.New("The app no longer exists.")
	}
	wasRunning := s.Docker != nil && s.backup.vol == nil && app.DesiredState == "running" && app.CurrentDeploymentID.Valid
	if wasRunning {
		s.stopForRestoreLocked(ctx, app)
	}
	if wasRunning {
		defer func() {
			msg := s.startAfterRestoreLocked(ctx, row.ID)
			switch {
			case msg == "":
			case rerr == nil:
				rerr = errors.New("The backup was restored, but the app did not start again: " + msg)
			default:
				rerr = errors.New(rerr.Error() + " The app did not start again: " + msg)
			}
		}()
	}

	// 1. A backup of what is there now, so a restore can be undone.
	safe, err := s.insertBackup(ctx, app.ID, BackupBeforeRestore)
	if err != nil {
		return errors.New("Something went wrong on the server. Nothing was restored.")
	}
	if err := s.finishBackup(ctx, app, safe.ID, room); err != nil {
		if errors.Is(err, dockerx.ErrNoVolume) {
			return errors.New("This app has no data folder yet, so there is nothing to restore into. Start the app once and try again.")
		}
		return errors.New("The current data could not be backed up first, so nothing was restored. " + backupErrText(err))
	}

	// 2. Swap the files.
	if err := s.replaceFrom(ctx, vs, app, b); err != nil {
		msg := capFirst(restoreErrText(err)) + "."
		if rerr := s.replaceFrom(ctx, vs, app, db.AppBackup{ID: safe.ID, AppID: app.ID, Kind: BackupBeforeRestore}); rerr != nil {
			return errors.New(msg + " Putting the old data back failed too: use the backup marked \"before restore\" to recover it.")
		}
		return errors.New(msg + " The old data was put back.")
	}
	s.pruneLocked(ctx, app)
	return nil
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[:1])) + string(r[1:])
}

func restoreErrText(err error) string {
	var tp *tarProblem
	if errors.As(err, &tp) {
		return strings.TrimSuffix(tp.msg, ".")
	}
	return strings.TrimSuffix(apperr.Redact(dockerx.DescribeError(err)), ".")
}

// replaceFrom empties the volume and unpacks backup b into it. The archive is
// checked again on the way in.
func (s *Service) replaceFrom(ctx context.Context, vs volumeStore, app db.App, b db.AppBackup) error {
	f, err := os.Open(s.backupPath(app.ID, b.ID))
	if err != nil {
		return errors.New("the file of the backup is gone")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return problem("the backup file is damaged")
	}
	defer gz.Close()
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		tw := tar.NewWriter(pw)
		_, err := sanitizeTar(tw, tar.NewReader(gz), tarLimits{})
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	err = vs.ReplaceVolume(ctx, appVolumeName(app), pr, b.Kind == BackupUploaded)
	// The file and the gzip reader are closed on return: let the copier stop first.
	_ = pr.Close()
	<-done
	return err
}

// stopForRestoreLocked takes the app offline like stop_app does, so that no
// container writes to /data and the crash detection leaves it alone.
func (s *Service) stopForRestoreLocked(ctx context.Context, app db.App) {
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "stopped", Status: "stopped", StatusMessage: "Restoring a backup.", UpdatedAt: s.now(), ID: app.ID,
	}); err != nil {
		s.Log.Warn("restore: marking app stopped failed", "app", app.Slug, "err", err)
	}
	if err := s.syncRouteLocked(ctx, app.ID); err != nil {
		s.Log.Warn("restore: removing route failed", "app", app.Slug, "err", err)
	}
	if cs, err := s.Docker.ListAppContainers(ctx, app.ID); err == nil {
		for _, c := range cs {
			if err := s.Docker.StopContainerGrace(ctx, dockerx.SummaryName(c), 10); err != nil {
				s.Log.Warn("restore: stopping container failed", "app", app.Slug, "err", err)
			}
		}
	}
}

// startAfterRestoreLocked starts the app again. It returns a plain-words
// reason when that fails (the app is then marked failed and the reconciler
// keeps trying).
func (s *Service) startAfterRestoreLocked(ctx context.Context, appID string) string {
	ctx = context.WithoutCancel(ctx)
	fresh, err := s.Q.GetApp(ctx, appID)
	if err != nil {
		return ""
	}
	if ae := s.startCurrentLocked(ctx, fresh); ae != nil {
		_ = s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
			DesiredState: "running", Status: "failed", StatusMessage: apperr.Redact(ae.Message), UpdatedAt: s.now(), ID: appID,
		})
		return ae.Message
	}
	_ = s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "running", Status: "running", StatusMessage: "", UpdatedAt: s.now(), ID: appID,
	})
	_ = s.syncRouteLocked(ctx, appID)
	s.deployRT.mu.Lock()
	delete(s.deployRT.restore, appID)
	s.deployRT.mu.Unlock()
	return ""
}

// ---- delete ----

// DeleteBackup removes a backup and its file.
func (s *Service) DeleteBackup(ctx context.Context, a Actor, ref, id string) error {
	row, _, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return err
	}
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return backupBusyErr()
	}
	defer lock.Unlock()
	b, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: id, AppID: row.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return backupNotFound()
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.removeBackup(ctx, row.ID, b.ID); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "backup.delete", "app", row.ID, map[string]any{"backup": b.ID, "kind": b.Kind})
	return nil
}

func (s *Service) removeBackup(ctx context.Context, appID, id string) error {
	if err := os.Remove(s.backupPath(appID, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.Q.DeleteBackup(ctx, id)
}

// removeAppBackups deletes every backup of an app (files; the rows go with the
// app). The caller holds the backup lock.
func (s *Service) removeAppBackups(appID string) error {
	return os.RemoveAll(s.backupDir(appID))
}
