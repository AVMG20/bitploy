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
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// errDeployBusy: a deployment of the app is about to swap containers, so the
// volume must not be touched now.
var errDeployBusy = errors.New("a deployment is running")

// stopping reports whether the service is shutting down.
func (s *Service) stopping() bool {
	s.backup.mu.Lock()
	base := s.backup.base
	s.backup.mu.Unlock()
	return base != nil && base.Err() != nil
}

// finishBackup makes the archive for the (running) row backupID and records
// the outcome on the row. appLocked says that the caller holds the app lock
// already (a restore does). It returns the error, already recorded.
func (s *Service) finishBackup(ctx context.Context, app db.App, owner db.User, backupID string, room int64, appLocked bool) error {
	defer s.publishBackup(ctx, app.ID)
	_, err := s.makeBackup(ctx, app, owner, backupID, room, appLocked)
	if err == nil {
		s.notifyBackup(ctx, app.ID, backupID)
		return nil
	}
	// A stop of bitploy is not the backup's fault and does not use up the
	// slot of an automatic backup.
	interrupted := errors.Is(err, context.Canceled) && s.stopping() || errors.Is(err, errDeployBusy)
	if uerr := s.Q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
		Status: backupFailed, Error: backupErrText(err), Interrupted: b2i(interrupted),
		FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: backupID,
	}); uerr != nil {
		s.Log.Error("recording backup result failed", "backup", backupID, "err", uerr)
	}
	if !errors.Is(err, dockerx.ErrNoVolume) { // no data yet: an automatic backup is tried again later
		s.notifyBackup(ctx, app.ID, backupID)
	}
	return err
}

// backupErrText is the plain-words reason shown for a failed backup.
func backupErrText(err error) string {
	var tp *tarProblem
	var de *dumpError
	switch {
	case errors.As(err, &de):
		return strings.TrimSpace(de.msg)
	case errors.Is(err, dockerx.ErrNoVolume):
		return "This app has no data yet. Start the app once, then try again."
	case errors.Is(err, errBackupNoRoom):
		return "There was not enough storage left. Delete an old backup or lower the /data limit of an app."
	case errors.Is(err, errDeployBusy):
		return "A new version of the app was being deployed. Try again when it is done."
	case errors.Is(err, context.DeadlineExceeded):
		return "The backup took too long and was stopped."
	case errors.Is(err, context.Canceled):
		return "bitploy was stopped while this backup was being made."
	case errors.As(err, &tp):
		return tp.msg
	}
	msg := apperr.Redact(dockerx.DescribeError(err))
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300]) + "..."
	}
	return "The files in /data could not be read: " + msg
}

// makeBackup writes <backups>/<app>/<id>.tar.gz from the app's volume and
// records the row as ready. A running app is paused while its files are being
// read, so the copy is consistent; it is always resumed. The helper container
// (and the image it needs) is made before the pause: that is the slow part.
func (s *Service) makeBackup(ctx context.Context, app db.App, owner db.User, id string, room int64, appLocked bool) (int64, error) {
	volName := singleVolume
	if b, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: id, AppID: app.ID}); err == nil {
		if b.Format == formatDump {
			return s.makeDump(ctx, app, owner, b, room, appLocked)
		}
		volName = b.Volume
	}
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
	vol, ok := s.volumeOf(ctx, app, volName)
	if !ok {
		_ = os.Remove(tmp)
		return 0, dockerx.ErrNoVolume
	}
	size, err := func() (int64, error) {
		defer f.Close()
		arch, err := vs.PrepareArchive(ctx, vol.VolumeName)
		if err != nil {
			return 0, err
		}
		defer arch.Close()
		if !appLocked {
			// The same lock a deploy takes to swap containers and the
			// reconciler to restart them: nothing else touches the app while
			// it is paused.
			al := s.appLock(app.ID)
			al.Lock()
			defer al.Unlock()
			if s.appBusy(app.ID) {
				return 0, errDeployBusy
			}
		}
		resume := s.pauseApp(ctx, app)
		defer resume()
		rc, err := arch.Open(ctx)
		if err != nil {
			return 0, err
		}
		defer rc.Close()
		gz := gzip.NewWriter(&limitWriter{w: f, left: room})
		tw := tar.NewWriter(gz)
		if _, err := sanitizeTar(tw, tar.NewReader(rc), tarLimits{SkipSpecial: true, Trusted: true, Ctx: ctx}); err != nil {
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
		err = s.commitBackup(ctx, owner, tmp, final, size, func() error {
			return s.Q.FinishBackup(ctx, db.FinishBackupParams{
				Status: backupReady, SizeBytes: size, FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: id,
			})
		})
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return size, nil
}

// pauseApp freezes the running containers of the app and returns the func that
// thaws them (safe to call more than once). Thawing never depends on ctx: a
// cancelled backup must still unfreeze the app. If bitploy dies in between,
// the next start thaws everything (dockerx.CleanupHelpers).
func (s *Service) pauseApp(ctx context.Context, app db.App) func() {
	if s.Docker == nil || s.backup.vol != nil {
		return func() {}
	}
	var paused []string
	var once sync.Once
	resume := func() {
		once.Do(func() {
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			for _, name := range paused {
				if err := s.Docker.UnpauseContainer(bg, name); err != nil {
					s.Log.Error("backup: resuming failed", "app", app.Slug, "container", name, "err", err)
				}
			}
		})
	}
	cs, err := s.Docker.ListAppContainers(ctx, app.ID)
	if err != nil {
		s.Log.Warn("backup: listing containers failed", "app", app.Slug, "err", err)
		return resume
	}
	for _, c := range cs {
		if string(c.State) != "running" {
			continue
		}
		name := dockerx.SummaryName(c)
		// Counted before the call: a pause that failed half-way is thawed too.
		paused = append(paused, name)
		if err := s.Docker.PauseContainer(ctx, name); err != nil {
			s.Log.Warn("backup: pausing failed", "app", app.Slug, "container", name, "err", err)
		}
	}
	return resume
}

// ---- restore ----

// restartsApp says whether a restore stops the app and starts it again.
func (s *Service) restartsApp(app db.App) bool {
	return s.Docker != nil && s.backup.vol == nil && app.DesiredState == "running" && app.CurrentDeploymentID.Valid
}

// RestoreBackup puts a backup back into /data. A backup of the current data
// is made first, the app is stopped while the files are swapped and started
// again afterwards if it was running. It returns at once; the progress is in
// the list (BackupList.Restore).
func (s *Service) RestoreBackup(ctx context.Context, a Actor, ref, id string) (BackupRestore, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return BackupRestore{}, err
	}
	// A restore replaces the live data: the owner's call, like deleting it.
	if err := s.requireAppOwner(ctx, a, row, "restore backups"); err != nil {
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
	dump := b.Format == formatDump
	if dump && dumpEngineOf(row) == nil {
		return BackupRestore{}, apperr.Conflict("dump_not_restorable", "This backup is a database dump, and this app is no database it can be replayed into.",
			"Restore a backup of the app's files instead.")
	}
	vol, ok, err := s.pickVolume(ctx, row, b.Volume)
	if err != nil {
		return BackupRestore{}, err
	}
	if !ok {
		return BackupRestore{}, volumeOffErr()
	}
	if _, err := os.Stat(s.backupFile(b)); err != nil {
		return BackupRestore{}, apperr.NotFound("backup_file_missing", "The file of this backup is gone from the server.", "Delete this backup and pick another one.")
	}
	vs, err := s.volumes()
	if err != nil {
		return BackupRestore{}, err
	}
	if has, err := vs.HasVolume(ctx, vol.VolumeName); err != nil {
		return BackupRestore{}, apperr.Unavailable("docker_unavailable", "bitploy could not read the files in /data.", "Try again in a minute.")
	} else if !has {
		return BackupRestore{}, volumeNotReadyErr()
	}
	if !s.settled(row.ID) {
		return BackupRestore{}, busyErr()
	}
	if n, err := s.Q.CountPendingDeploymentsByApp(ctx, row.ID); err == nil && n > 0 {
		return BackupRestore{}, busyErr()
	}
	if dump && !s.dbRunning(ctx, row) {
		return BackupRestore{}, apperr.Conflict("database_not_running", "The database is not running, so the dump cannot be replayed into it.",
			"Start the app and wait until it runs, then restore again. A dump is replayed by the database itself.")
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
	st := &BackupRestore{BackupID: b.ID, State: "running", StartedAt: s.Now(), AppRestarts: !dump && s.restartsApp(row)}
	s.setRestore(row.ID, st)
	started := *st // the job below updates st
	s.Audit(ctx, a, "backup.restore", "app", row.ID, map[string]any{"backup": b.ID, "format": backupView(b).Format})
	s.backupGo(func() {
		defer lock.Unlock()
		ctx, cancel := s.backupCtx()
		defer cancel()
		// Database helpers that read the volume finish first: one that closes its
		// last connection would remove a -wal file that is restored meanwhile.
		s.waitDBReads(ctx, row.ID)
		var err error
		if dump {
			err = s.runDumpRestore(ctx, row, owner, b, room)
		} else {
			err = s.runRestore(ctx, row, owner, b, room, st)
		}
		s.finishRestoreState(row.ID, st, err)
		if err != nil {
			s.Log.Warn("restore failed", "app", row.Slug, "err", err)
			s.Audit(context.WithoutCancel(ctx), System, "backup.restore_failed", "app", row.ID, map[string]any{"backup": b.ID, "error": err.Error()})
		}
	})
	return started, nil
}

func (s *Service) setRestore(appID string, r *BackupRestore) {
	defer s.publishBackup(context.Background(), appID) // runs after the unlock below
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	if s.backup.restore == nil {
		s.backup.restore = map[string]*BackupRestore{}
	}
	s.backup.restore[appID] = r
}

func (s *Service) finishRestoreState(appID string, st *BackupRestore, err error) {
	defer func() {
		s.publishBackup(context.Background(), appID)
		s.publishApp(context.Background(), appID)
	}()
	fin := s.Now()
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	st.FinishedAt = &fin
	if err != nil {
		st.State, st.Error = "failed", err.Error()
	} else {
		st.State = "done"
	}
}

// restoreLimits are the tar rules for putting backup b back: what bitploy made
// itself from the volume is trusted, an uploaded file is not.
func (s *Service) restoreLimits(ctx context.Context, app db.App, b db.AppBackup) tarLimits {
	if b.Kind == BackupUploaded {
		vol, _ := s.volumeOf(ctx, app, b.Volume)
		return tarLimits{MaxBytes: vol.SizeMB * mib, Ctx: ctx}
	}
	return tarLimits{Trusted: true, Ctx: ctx}
}

// checkBackupFile reads the whole backup once without writing anything, so a
// damaged or invalid file is found before the volume is emptied.
func (s *Service) checkBackupFile(ctx context.Context, app db.App, b db.AppBackup) error {
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
	_, err = sanitizeTar(nil, tar.NewReader(gz), s.restoreLimits(ctx, app, b))
	if errors.Is(err, errTarTooBig) {
		return problem("the files in the backup are bigger than the /data limit of the app")
	}
	if err != nil {
		return err
	}
	// The tar reader stops at the end-of-archive marker. Read the rest of the
	// gzip stream too, so a file cut off after it (or with a bad checksum) is
	// found here and not halfway through the restore. Only this member is
	// checked: data after it is not read by the restore either.
	gz.Multistream(false)
	if _, err := io.Copy(io.Discard, gz); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return problem("the backup file is damaged")
	}
	return nil
}

// setRestoreMarker records on the app that a restore is under way, so that a
// restart of bitploy in the middle of it is noticed (recoverRestores).
func (s *Service) setRestoreMarker(ctx context.Context, appID, backupID, safeID string, resume bool) {
	if err := s.Q.SetRestoreMarker(context.WithoutCancel(ctx), db.SetRestoreMarkerParams{
		RestoreBackupID: backupID, RestoreSafeID: safeID, RestoreResume: b2i(resume), ID: appID,
	}); err != nil {
		s.Log.Error("restore: saving restore state failed", "app", appID, "err", err)
	}
}

// runRestore does the restore. The error is in plain words. Its steps, with
// what a crash after each one leaves (recoverRestores handles those):
//
//  0. the backup file is read once without writing (nothing to undo)
//  1. the app is stopped, the marker is written
//  2. a before-restore backup of the current data is made; the marker gets its id
//  3. the volume is emptied and the backup unpacked; a failure puts the
//     before-restore backup back
//  4. the marker is cleared and the app started again
//
// When the old data could not be put back either, the app stays stopped.
func (s *Service) runRestore(ctx context.Context, row db.App, owner db.User, b db.AppBackup, room int64, st *BackupRestore) (rerr error) {
	vs, err := s.volumes()
	if err != nil {
		return err
	}
	if err := s.checkBackupFile(ctx, row, b); err != nil {
		if ctx.Err() != nil {
			return errors.New("The restore was stopped. Nothing was changed.")
		}
		return errors.New("This backup cannot be restored, so nothing was changed: " + restoreErrText(err) + ".")
	}
	al := s.appLock(row.ID)
	al.Lock()
	defer al.Unlock()
	app, err := s.Q.GetApp(ctx, row.ID)
	if err != nil {
		return errors.New("The app no longer exists.")
	}
	if s.appBusy(app.ID) {
		return errors.New("A new version of the app is being deployed, so nothing was restored. Try again when it is done.")
	}
	wasRunning := s.restartsApp(app)
	s.backup.mu.Lock()
	st.AppRestarts = wasRunning
	s.backup.mu.Unlock()

	keepStopped := false
	s.setRestoreMarker(ctx, app.ID, b.ID, "", wasRunning)
	defer func() {
		if s.stopping() {
			return // the marker stays: the next start finishes the job
		}
		s.setRestoreMarker(ctx, app.ID, "", "", false)
		if !wasRunning || keepStopped {
			return
		}
		msg := s.startAfterRestoreLocked(ctx, row.ID)
		switch {
		case msg == "":
		case rerr == nil:
			rerr = errors.New("The backup was restored, but the app did not start again: " + msg)
		default:
			rerr = errors.New(rerr.Error() + " The app did not start again: " + msg)
		}
	}()

	if wasRunning {
		if err := s.stopForRestoreLocked(ctx, app); err != nil {
			return errors.New("The app could not be stopped, so nothing was restored.")
		}
	}

	// A backup of what is there now, so a restore can be undone.
	safe, err := s.insertBackup(ctx, app.ID, BackupBeforeRestore, b.Volume, formatVolume)
	if err != nil {
		return errors.New("A server error stopped the restore before it began. Nothing was changed.")
	}
	if err := s.finishBackup(ctx, app, owner, safe.ID, room, true); err != nil {
		if errors.Is(err, dockerx.ErrNoVolume) {
			return errors.New("This app has no /data folder yet, so there is nothing to restore into. Start the app once and try again.")
		}
		return errors.New("The current data could not be backed up first, so nothing was restored. " + backupErrText(err))
	}
	s.setRestoreMarker(ctx, app.ID, b.ID, safe.ID, wasRunning)

	// Swap the files.
	if err := s.replaceFrom(ctx, vs, app, b); err != nil {
		if s.stopping() {
			return errors.New("bitploy was stopped during the restore. It will finish putting the old data back when it starts again.")
		}
		msg := capFirst(restoreErrText(err)) + "."
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Hour)
		defer cancel()
		safeRow := db.AppBackup{ID: safe.ID, AppID: app.ID, Kind: BackupBeforeRestore}
		if ferr := s.replaceFrom(fctx, vs, app, safeRow); ferr != nil {
			keepStopped = true
			s.keepStoppedLocked(ctx, app.ID, "The restore failed and the old data could not be put back, so the app was left stopped. Restore the backup marked \"before restore\" from the Storage tab, then start the app.")
			return errors.New(msg + " Putting the old data back failed too, so the app was left stopped: restore the backup marked \"before restore\", then start the app.")
		}
		return errors.New(msg + " The old data was put back.")
	}
	s.pruneLocked(ctx, app)
	return nil
}

// keepStoppedLocked leaves the app stopped, with a message on its page.
func (s *Service) keepStoppedLocked(ctx context.Context, appID, msg string) {
	if err := s.Q.SetAppDesiredState(context.WithoutCancel(ctx), db.SetAppDesiredStateParams{
		DesiredState: "stopped", Status: "stopped", StatusMessage: msg, UpdatedAt: s.now(), ID: appID,
	}); err != nil {
		s.Log.Warn("restore: marking app stopped failed", "app", appID, "err", err)
	}
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
		_, err := sanitizeTar(tw, tar.NewReader(gz), s.restoreLimits(ctx, app, b))
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	vol, ok := s.volumeOf(ctx, app, b.Volume)
	if !ok {
		_ = pr.CloseWithError(dockerx.ErrNoVolume)
		<-done
		return dockerx.ErrNoVolume
	}
	err = vs.ReplaceVolume(ctx, vol.VolumeName, pr, b.Kind == BackupUploaded)
	// The file and the gzip reader are closed on return: let the copier stop first.
	_ = pr.Close()
	<-done
	return err
}

// stopForRestoreLocked takes the app offline like stop_app does, so that no
// container writes to /data and the crash detection leaves it alone. It fails
// when a container could not be stopped: the volume must not be emptied under
// a running app.
func (s *Service) stopForRestoreLocked(ctx context.Context, app db.App) error {
	defer s.publishApp(ctx, app.ID)
	if err := s.Q.SetAppDesiredState(ctx, db.SetAppDesiredStateParams{
		DesiredState: "stopped", Status: "stopped", StatusMessage: "Restoring a backup.", UpdatedAt: s.now(), ID: app.ID,
	}); err != nil {
		return err
	}
	if err := s.syncRouteLocked(ctx, app.ID); err != nil {
		s.Log.Warn("restore: removing route failed", "app", app.Slug, "err", err)
	}
	cs, err := s.Docker.ListAppContainers(ctx, app.ID)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := s.Docker.StopContainerGrace(ctx, dockerx.SummaryName(c), 10); err != nil {
			s.Log.Warn("restore: stopping container failed", "app", app.Slug, "err", err)
			return err
		}
	}
	return nil
}

// startAfterRestoreLocked starts the app again. It returns a plain-words
// reason when that fails (the app is then marked failed and the reconciler
// keeps trying).
func (s *Service) startAfterRestoreLocked(ctx context.Context, appID string) string {
	ctx = context.WithoutCancel(ctx)
	defer s.publishApp(ctx, appID)
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

// ---- recovery after a crash or restart ----

// recoverRestores finishes restores that bitploy's stop interrupted. A restore
// that had not touched the volume yet only needs the app started again; one
// that had is undone from its before-restore backup. When even that is not
// possible the app stays stopped, with a message on its page.
func (s *Service) recoverRestores(ctx context.Context) {
	apps, err := s.Q.ListRestoringApps(ctx)
	if err != nil {
		s.Log.Warn("looking for interrupted restores failed", "err", err)
		return
	}
	for _, app := range apps {
		s.recoverRestore(ctx, app)
	}
}

func (s *Service) recoverRestore(ctx context.Context, app db.App) {
	lock := s.backupLock(app.ID)
	lock.Lock()
	defer lock.Unlock()
	al := s.appLock(app.ID)
	al.Lock()
	defer al.Unlock()

	st := &BackupRestore{BackupID: app.RestoreBackupID, State: "failed", StartedAt: s.Now(), AppRestarts: app.RestoreResume != 0}
	fin := s.Now()
	st.FinishedAt = &fin
	keepStopped := false
	switch {
	case app.RestoreSafeID == "":
		st.Error = "bitploy was restarted before the restore began. Nothing was changed."
	case s.restoreSafeIsDump(ctx, app):
		// A dump restore never stopped the database: replay the dump from
		// before. If that fails, the database keeps running as it is, so the
		// user can restore the before-restore backup.
		if err := s.undoRestore(ctx, app); err != nil {
			st.Error = "bitploy was restarted in the middle of the restore and the databases from before could not be put back. Restore the backup marked \"before restore\" from the Storage tab."
			s.Log.Error("recovering an interrupted restore failed", "app", app.Slug, "err", err)
		} else {
			st.Error = "bitploy was restarted in the middle of the restore. The databases from before were put back."
		}
	default:
		if err := s.undoRestore(ctx, app); err != nil {
			keepStopped = true
			st.Error = "bitploy was restarted in the middle of the restore and the old data could not be put back, so the app was left stopped. Restore the backup marked \"before restore\" from the Storage tab, then start the app."
			s.Log.Error("recovering an interrupted restore failed", "app", app.Slug, "err", err)
		} else {
			st.Error = "bitploy was restarted in the middle of the restore. The old data was put back."
		}
	}
	s.setRestore(app.ID, st)
	s.setRestoreMarker(ctx, app.ID, "", "", false)
	if keepStopped {
		s.keepStoppedLocked(ctx, app.ID, st.Error)
		return
	}
	if app.RestoreResume != 0 {
		if msg := s.startAfterRestoreLocked(ctx, app.ID); msg != "" {
			s.backup.mu.Lock()
			st.Error += " The app did not start again: " + msg
			s.backup.mu.Unlock()
		}
	}
	s.Audit(ctx, System, "backup.restore_recovered", "app", app.ID, map[string]any{"backup": app.RestoreBackupID, "undone": app.RestoreSafeID != ""})
}

// undoRestore puts the before-restore backup of an interrupted restore back.
func (s *Service) undoRestore(ctx context.Context, app db.App) error {
	vs, err := s.volumes()
	if err != nil {
		return err
	}
	safe, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: app.RestoreSafeID, AppID: app.ID})
	if err != nil || safe.Status != backupReady {
		return errors.New("the before-restore backup is not usable")
	}
	if safe.Format == formatDump {
		eng := dumpEngineOf(app)
		if eng == nil {
			return errors.New("the app is no database")
		}
		return s.replayDump(ctx, app, eng, safe)
	}
	return s.replaceFrom(ctx, vs, app, safe)
}

// restoreSafeIsDump reports whether the interrupted restore of app had made
// a dump as its before-restore backup.
func (s *Service) restoreSafeIsDump(ctx context.Context, app db.App) bool {
	if app.RestoreSafeID == "" {
		return false
	}
	safe, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: app.RestoreSafeID, AppID: app.ID})
	return err == nil && safe.Format == formatDump
}

// ---- delete ----

// DeleteBackup removes a backup and its file.
func (s *Service) DeleteBackup(ctx context.Context, a Actor, ref, id string) error {
	row, _, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return err
	}
	if err := s.requireAppOwner(ctx, a, row, "delete backups"); err != nil {
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
	s.publishBackup(ctx, row.ID)
	return nil
}

func (s *Service) removeBackup(ctx context.Context, appID, id string) error {
	for _, p := range []string{s.backupPath(appID, id), s.dumpPath(appID, id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.Q.DeleteBackup(ctx, id)
}

// removeAppBackups deletes every backup file of an app (the rows go with the
// app). The caller holds the backup lock.
func (s *Service) removeAppBackups(appID string) error {
	return os.RemoveAll(s.backupDir(appID))
}
