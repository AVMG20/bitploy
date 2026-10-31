package app

// Backups of an app's /data folder: make one, upload one, download, restore,
// delete and the per-app settings for automatic backups. The jobs themselves
// (archive, restore) are in backups_job.go, the schedule in backups_sched.go,
// the tar rules in backups_tar.go.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Backup kinds.
const (
	BackupManual        = "manual"
	BackupScheduled     = "scheduled"
	BackupUploaded      = "uploaded"
	BackupBeforeRestore = "before_restore"
)

// Backup statuses.
const (
	backupRunning = "running"
	backupReady   = "ready"
	backupFailed  = "failed"
)

// Defaults and bounds of the retention setting.
const (
	defaultBackupKeep = 7
	maxBackupKeepLast = 100
	maxBackupKeepDays = 365
)

// volumeStore reads and replaces the contents of a data volume. The Docker
// client does it for real; tests use a fake.
type volumeStore interface {
	ArchiveVolume(ctx context.Context, volume string) (io.ReadCloser, error)
	ReplaceVolume(ctx context.Context, volume string, archive io.Reader, fixOwner bool) error
}

// backupState is the in-memory part of the backup feature on Service.
type backupState struct {
	mu      sync.Mutex
	locks   map[string]*sync.Mutex    // appID -> held while a backup, upload or restore runs
	restore map[string]*BackupRestore // appID -> latest restore
	vol     volumeStore               // tests replace it; nil means Docker
	wg      sync.WaitGroup            // running jobs (tests wait for them)
}

// backupLock is the "one backup or restore per app at a time" lock. Use TryLock.
func (s *Service) backupLock(appID string) *sync.Mutex {
	b := &s.backup
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.locks == nil {
		b.locks = map[string]*sync.Mutex{}
	}
	l := b.locks[appID]
	if l == nil {
		l = &sync.Mutex{}
		b.locks[appID] = l
	}
	return l
}

func (s *Service) backupBusy(appID string) bool {
	l := s.backupLock(appID)
	if l.TryLock() {
		l.Unlock()
		return false
	}
	return true
}

func backupBusyErr() *apperr.Error {
	return apperr.Conflict("backup_busy", "A backup or restore of this app is already running.",
		"Wait until it has finished, then try again.")
}

// volumes returns what reads and writes the app volumes.
func (s *Service) volumes() (volumeStore, error) {
	if s.backup.vol != nil {
		return s.backup.vol, nil
	}
	if s.Docker == nil {
		return nil, apperr.Unavailable("docker_unavailable", "bitploy cannot reach Docker right now.", "Try again in a minute. If it keeps happening, tell the bitploy admin.")
	}
	return s.Docker, nil
}

func (s *Service) backupDir(appID string) string { return s.Cfg.Path("backups", appID) }

func (s *Service) backupPath(appID, id string) string {
	return filepath.Join(s.backupDir(appID), id+".tar.gz")
}

func backupView(b db.AppBackup) Backup {
	return Backup{ID: b.ID, Kind: b.Kind, Status: b.Status, SizeBytes: b.SizeBytes, Error: b.Error, CreatedAt: ts(b.CreatedAt)}
}

func backupNotFound() *apperr.Error {
	return apperr.NotFound("backup_not_found", "There is no such backup for this app.", "List the backups of the app to see what exists.")
}

func volumeOffErr() *apperr.Error {
	return apperr.Conflict("volume_off", "The /data folder is turned off for this app, so there is nothing to back up or restore into.",
		"Turn /data on first (update_app with volume=true, or the Storage tab in the dashboard).")
}

// backupApp loads the app and, for changes, its active owner.
func (s *Service) backupApp(ctx context.Context, a Actor, ref string, write bool) (db.App, db.User, error) {
	row, err := s.LoadApp(ctx, a, ref, write)
	if err != nil {
		return db.App{}, db.User{}, err
	}
	owner, err := s.Q.GetUser(ctx, row.UserID)
	if err != nil {
		return db.App{}, db.User{}, apperr.Internal(err)
	}
	if write && owner.Status != "active" {
		return db.App{}, db.User{}, suspendedErr()
	}
	return row, owner, nil
}

// backupRoom is how many bytes a new backup may take before the owner's
// storage allowance is full; -1 means no limit.
func (s *Service) backupRoom(ctx context.Context, u db.User) (int64, error) {
	if u.QuotaVolumeMb <= 0 {
		return -1, nil
	}
	taken, err := s.storageTaken(ctx, u.ID, "")
	if err != nil {
		return 0, err
	}
	if room := (u.QuotaVolumeMb - taken) * mib; room > 0 {
		return room, nil
	}
	return 0, nil
}

func (s *Service) noRoomErr(u db.User) *apperr.Error {
	return apperr.Forbidden("storage_quota_exceeded",
		fmt.Sprintf("There is no storage left for a backup: your apps already take your %d MB.", u.QuotaVolumeMb),
		"Delete an old backup or an app you no longer need, lower the /data limit of an app, or ask a bitploy admin for more storage.")
}

func (s *Service) checkBackupDisk() error {
	if ok, err := s.DiskHasRoom(); err == nil && !ok {
		return apperr.Unavailable("server_disk_full", "The server is almost out of disk space, so no backup can be made.",
			"Tell the bitploy admin that the server disk is full. Your apps are not affected.")
	}
	return nil
}

// backupGo runs a job in the background; it ends with the job.
func (s *Service) backupGo(fn func()) {
	s.backup.wg.Add(1)
	go func() {
		defer s.backup.wg.Done()
		fn()
	}()
}

// ---- list and settings ----

func (s *Service) ListBackups(ctx context.Context, a Actor, ref string) (BackupList, error) {
	row, _, err := s.backupApp(ctx, a, ref, false)
	if err != nil {
		return BackupList{}, err
	}
	rows, err := s.Q.ListBackupsByApp(ctx, row.ID)
	if err != nil {
		return BackupList{}, apperr.Internal(err)
	}
	out := BackupList{Items: make([]Backup, 0, len(rows)), Settings: s.backupSettings(ctx, row), Busy: s.backupBusy(row.ID)}
	for _, r := range rows {
		out.Items = append(out.Items, backupView(r))
		out.TotalBytes += r.SizeBytes
	}
	s.backup.mu.Lock()
	if r := s.backup.restore[row.ID]; r != nil {
		c := *r
		out.Restore = &c
	}
	s.backup.mu.Unlock()
	return out, nil
}

func (s *Service) backupSettings(ctx context.Context, row db.App) BackupSettings {
	st := BackupSettings{Schedule: row.BackupSchedule, KeepMode: row.BackupKeepMode, Keep: row.BackupKeep}
	if row.BackupSchedule != "off" {
		last, _ := s.Q.LatestScheduledBackupAt(ctx, row.ID)
		next := nextBackupAt(row.BackupSchedule, last, s.Now())
		st.NextAt = &next
	}
	return st
}

// UpdateBackupSettings changes the automatic backups of an app.
func (s *Service) UpdateBackupSettings(ctx context.Context, a Actor, ref string, in BackupSettingsInput) (BackupSettings, error) {
	row, _, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return BackupSettings{}, err
	}
	schedule, mode, keep := row.BackupSchedule, row.BackupKeepMode, row.BackupKeep
	if in.Schedule != nil {
		schedule = strings.ToLower(strings.TrimSpace(*in.Schedule))
		if schedule != "off" && schedule != "daily" && schedule != "weekly" {
			return BackupSettings{}, apperr.BadRequest("backup_schedule_invalid", "The schedule "+quote(*in.Schedule)+" is not known.", "Use off, daily or weekly.")
		}
	}
	if in.KeepMode != nil {
		mode = strings.ToLower(strings.TrimSpace(*in.KeepMode))
		if mode != "last" && mode != "days" {
			return BackupSettings{}, apperr.BadRequest("backup_keep_invalid", "The keep mode "+quote(*in.KeepMode)+" is not known.", "Use last (keep the newest backups) or days (keep backups for some days).")
		}
	}
	if in.Keep != nil {
		keep = *in.Keep
	}
	if max := int64(maxBackupKeepLast); (mode == "last" && (keep < 1 || keep > max)) || (mode == "days" && (keep < 1 || keep > maxBackupKeepDays)) {
		if mode == "days" {
			max = maxBackupKeepDays
		}
		return BackupSettings{}, apperr.BadRequest("backup_keep_invalid", fmt.Sprintf("%d is not a valid number to keep.", keep), fmt.Sprintf("Use a number from 1 to %d.", max))
	}
	if err := s.Q.UpdateAppBackupSettings(ctx, db.UpdateAppBackupSettingsParams{
		BackupSchedule: schedule, BackupKeepMode: mode, BackupKeep: keep, UpdatedAt: s.now(), ID: row.ID,
	}); err != nil {
		return BackupSettings{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "app.backup_settings", "app", row.ID, map[string]any{"schedule": schedule, "keep_mode": mode, "keep": keep})
	row.BackupSchedule, row.BackupKeepMode, row.BackupKeep = schedule, mode, keep
	return s.backupSettings(ctx, row), nil
}

// ---- back up now ----

// CreateBackup starts a manual backup and returns at once; the backup shows up
// as "running" in the list until it is done.
func (s *Service) CreateBackup(ctx context.Context, a Actor, ref string) (Backup, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return Backup{}, err
	}
	if row.VolumeEnabled == 0 {
		return Backup{}, volumeOffErr()
	}
	if _, err := s.volumes(); err != nil {
		return Backup{}, err
	}
	if err := s.checkBackupDisk(); err != nil {
		return Backup{}, err
	}
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		return Backup{}, apperr.Internal(err)
	}
	if room == 0 {
		return Backup{}, s.noRoomErr(owner)
	}
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return Backup{}, backupBusyErr()
	}
	b, err := s.insertBackup(ctx, row.ID, BackupManual)
	if err != nil {
		lock.Unlock()
		return Backup{}, err
	}
	s.Audit(ctx, a, "backup.create", "app", row.ID, map[string]any{"backup": b.ID, "kind": BackupManual})
	s.backupGo(func() {
		defer lock.Unlock()
		ctx, cancel := backgroundCtx()
		defer cancel()
		if err := s.finishBackup(ctx, row, b.ID, room); err != nil {
			s.Log.Warn("backup failed", "app", row.Slug, "err", err)
		}
	})
	return backupView(b), nil
}

func backgroundCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 3*time.Hour)
}

func (s *Service) insertBackup(ctx context.Context, appID, kind string) (db.AppBackup, error) {
	b := db.AppBackup{ID: ids.New(ids.Backup), AppID: appID, Kind: kind, Status: backupRunning, CreatedAt: s.now()}
	if err := s.Q.InsertBackup(ctx, db.InsertBackupParams{
		ID: b.ID, AppID: b.AppID, Kind: b.Kind, Status: b.Status, CreatedAt: b.CreatedAt,
	}); err != nil {
		return db.AppBackup{}, apperr.Internal(err)
	}
	return b, nil
}

// ---- download ----

// OpenBackup opens a finished backup for download and gives the file name to
// offer. Only the owner may download: the files are the user's data.
func (s *Service) OpenBackup(ctx context.Context, a Actor, ref, id string) (*os.File, string, error) {
	row, _, err := s.backupApp(ctx, a, ref, false)
	if err != nil {
		return nil, "", err
	}
	if row.UserID != a.UserID && a.Channel != ChannelSystem {
		return nil, "", appNotFound(ref)
	}
	b, err := s.Q.GetBackup(ctx, db.GetBackupParams{ID: id, AppID: row.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", backupNotFound()
	}
	if err != nil {
		return nil, "", apperr.Internal(err)
	}
	if b.Status != backupReady {
		return nil, "", apperr.Conflict("backup_not_ready", "This backup is not finished, so it cannot be downloaded.", "Wait until it is ready.")
	}
	f, err := os.Open(s.backupPath(row.ID, b.ID))
	if err != nil {
		return nil, "", apperr.NotFound("backup_file_missing", "The file of this backup is gone from the server.", "Delete this backup and make a new one.")
	}
	s.Audit(ctx, a, "backup.download", "app", row.ID, map[string]any{"backup": b.ID})
	name := fmt.Sprintf("%s-%s-%s.tar.gz", row.Slug, ts(b.CreatedAt).Format("20060102-150405"), strings.ReplaceAll(b.Kind, "_", "-"))
	return f, name, nil
}

// ---- upload ----

var (
	errUploadTooLarge = errors.New("upload too large")
	errBackupNoRoom   = errors.New("no room for the backup")
)

type readErr struct{ err error }

func (e *readErr) Error() string { return e.err.Error() }
func (e *readErr) Unwrap() error { return e.err }

// capReader fails with errUploadTooLarge when more than left bytes come in,
// and marks failures of the source as readErr.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if int64(len(p)) > c.left+1 {
		p = p[:c.left+1]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	if c.left < 0 {
		return 0, errUploadTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) {
		err = &readErr{err}
	}
	return n, err
}

// limitWriter fails with errBackupNoRoom instead of writing past left bytes
// (a negative left means no limit).
type limitWriter struct {
	w    io.Writer
	left int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.left >= 0 {
		if int64(len(p)) > l.left {
			return 0, errBackupNoRoom
		}
		l.left -= int64(len(p))
	}
	return l.w.Write(p)
}

// UploadBackup stores a .tar.gz sent by the user as an "uploaded" backup. It
// checks every entry of the archive and writes a clean copy. contentLength is
// the size announced by the client, or -1.
func (s *Service) UploadBackup(ctx context.Context, a Actor, ref string, body io.Reader, contentLength int64) (Backup, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return Backup{}, err
	}
	if row.VolumeEnabled == 0 {
		return Backup{}, volumeOffErr()
	}
	if err := s.checkBackupDisk(); err != nil {
		return Backup{}, err
	}
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		return Backup{}, apperr.Internal(err)
	}
	if room == 0 {
		return Backup{}, s.noRoomErr(owner)
	}
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return Backup{}, backupBusyErr()
	}
	defer lock.Unlock()

	// Neither the file nor what is in it may be bigger than the /data limit.
	volBytes := row.VolumeMb * mib
	limit, roomBound := volBytes, false
	if room >= 0 && room < limit {
		limit, roomBound = room, true
	}
	tooBig := func() error {
		if roomBound {
			return s.noRoomErr(owner)
		}
		return apperr.New(413, "backup_too_large",
			fmt.Sprintf("This backup is larger than the %d MB /data limit of this app.", row.VolumeMb),
			"Upload a smaller backup, or raise the /data limit of the app first.")
	}
	if contentLength > limit {
		return Backup{}, tooBig()
	}

	if err := os.MkdirAll(s.backupDir(row.ID), 0o750); err != nil {
		return Backup{}, apperr.Internal(err)
	}
	id := ids.New(ids.Backup)
	final := s.backupPath(row.ID, id)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return Backup{}, apperr.Internal(err)
	}
	stats, werr := func() (tarStats, error) {
		defer f.Close()
		gzr, err := gzip.NewReader(&capReader{r: body, left: limit})
		if err != nil {
			return tarStats{}, err
		}
		gzw := gzip.NewWriter(&limitWriter{w: f, left: limit})
		tw := tar.NewWriter(gzw)
		stats, err := sanitizeTar(tw, tar.NewReader(gzr), tarLimits{MaxBytes: volBytes})
		if err == nil {
			err = tw.Close()
		}
		if err == nil {
			err = gzw.Close()
		}
		if err == nil {
			err = f.Sync()
		}
		return stats, err
	}()
	if werr == nil && stats.Entries == 0 {
		werr = problem("The archive is empty.")
	}
	var size int64
	if werr == nil {
		if fi, err := os.Stat(tmp); err == nil {
			size = fi.Size()
		}
		werr = os.Rename(tmp, final)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return Backup{}, s.uploadErr(werr, tooBig)
	}
	b := db.AppBackup{ID: id, AppID: row.ID, Kind: BackupUploaded, Status: backupReady, SizeBytes: size, CreatedAt: s.now()}
	if err := s.Q.InsertBackup(ctx, db.InsertBackupParams{
		ID: b.ID, AppID: b.AppID, Kind: b.Kind, Status: b.Status, SizeBytes: b.SizeBytes, CreatedAt: b.CreatedAt,
		FinishedAt: sql.NullInt64{Int64: b.CreatedAt, Valid: true},
	}); err != nil {
		_ = os.Remove(final)
		return Backup{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "backup.upload", "app", row.ID, map[string]any{"backup": b.ID, "bytes": size})
	return backupView(b), nil
}

func (s *Service) uploadErr(err error, tooBig func() error) error {
	var tp *tarProblem
	var re *readErr
	const hint = "Upload a .tar.gz of the contents of the /data folder, for example one you downloaded here."
	switch {
	case errors.As(err, &re):
		return apperr.BadRequest("upload_interrupted", "The upload was cut off before it finished.", "Try again.")
	case errors.As(err, &tp):
		return apperr.Unprocessable("backup_invalid", tp.msg, hint)
	case errors.Is(err, errTarTooBig), errors.Is(err, errUploadTooLarge), errors.Is(err, errBackupNoRoom):
		return tooBig()
	case isCorrupt(err):
		return apperr.Unprocessable("backup_invalid", "The file is damaged or is not a .tar.gz archive.", hint)
	}
	return apperr.Internal(err)
}
