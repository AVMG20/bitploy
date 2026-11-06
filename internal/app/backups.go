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
	"github.com/AVMG20/bitploy/internal/dockerx"
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

// Bounds of the retention setting.
const (
	maxBackupKeepLast = 100
	maxBackupKeepDays = 365
)

// volumeStore reads and replaces the contents of a data volume. The Docker
// client does it for real (dockerVolumes); tests use a fake.
type volumeStore interface {
	HasVolume(ctx context.Context, volume string) (bool, error)
	// PrepareArchive does the slow setup of reading a volume, so that an app is
	// only paused for the reading itself.
	PrepareArchive(ctx context.Context, volume string) (volumeArchive, error)
	ReplaceVolume(ctx context.Context, volume string, archive io.Reader, fixOwner bool) error
}

type volumeArchive interface {
	Open(ctx context.Context) (io.ReadCloser, error)
	Close() error
}

type dockerVolumes struct{ c *dockerx.Client }

func (d dockerVolumes) HasVolume(ctx context.Context, volume string) (bool, error) {
	return d.c.VolumeExists(ctx, volume)
}

func (d dockerVolumes) PrepareArchive(ctx context.Context, volume string) (volumeArchive, error) {
	return d.c.PrepareArchive(ctx, volume)
}

func (d dockerVolumes) ReplaceVolume(ctx context.Context, volume string, archive io.Reader, fixOwner bool) error {
	return d.c.ReplaceVolume(ctx, volume, archive, fixOwner)
}

// backupConcurrency is how many automatic backups run at the same time.
const backupConcurrency = 2

// backupState is the in-memory part of the backup feature on Service.
type backupState struct {
	mu       sync.Mutex
	locks    map[string]*sync.Mutex    // appID -> held while a backup, upload or restore runs
	restore  map[string]*BackupRestore // appID -> latest restore
	base     context.Context           // ends when the service shuts down; nil until RunBackground
	vol      volumeStore               // tests replace it; nil means Docker
	run      serviceRunner             // tests replace it; nil means Docker (database dumps)
	wg       sync.WaitGroup            // running jobs
	commitMu sync.Mutex                // one backup at a time checks the storage allowance and lands
	sem      chan struct{}             // limits automatic backups that run at once
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

// forgetBackupState drops what is kept in memory for a deleted app.
func (s *Service) forgetBackupState(appID string) {
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	delete(s.backup.locks, appID)
	delete(s.backup.restore, appID)
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
	return dockerVolumes{s.Docker}, nil
}

func (s *Service) backupDir(appID string) string { return s.Cfg.Path("backups", appID) }

func (s *Service) backupPath(appID, id string) string {
	return filepath.Join(s.backupDir(appID), id+".tar.gz")
}

func backupView(b db.AppBackup) Backup {
	format := b.Format
	if format == "" {
		format = formatVolume
	}
	return Backup{ID: b.ID, Kind: b.Kind, Format: format, Tool: b.Tool, Status: b.Status, SizeBytes: b.SizeBytes, Error: b.Error, CreatedAt: ts(b.CreatedAt)}
}

func backupNotFound() *apperr.Error {
	return apperr.NotFound("backup_not_found", "There is no such backup for this app.", "List the backups of the app to see what exists.")
}

func volumeOffErr() *apperr.Error {
	return apperr.Conflict("volume_off", "Saved files (/data) are turned off for this app, so there is nothing to back up or restore into.",
		"Turn them on with update_app volume=true, or in the dashboard under Storage.")
}

func volumeNotReadyErr() *apperr.Error {
	return apperr.Conflict("volume_not_ready", "This app has no /data folder yet.",
		"Restart or redeploy the app once so that /data is created, then try again.")
}

// backupApp loads the app and its owner. Everything that changes something, and
// everything that hands out the files, needs the deploy scope or a session.
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

// backupGo runs a job in the background; RunBackground waits for it at shutdown.
func (s *Service) backupGo(fn func()) {
	s.backup.wg.Add(1)
	go func() {
		defer s.backup.wg.Done()
		fn()
	}()
}

// backupCtx is the context of a background job: it ends at shutdown, or after
// three hours.
func (s *Service) backupCtx() (context.Context, context.CancelFunc) {
	s.backup.mu.Lock()
	base := s.backup.base
	s.backup.mu.Unlock()
	if base == nil {
		base = context.Background()
	}
	return context.WithTimeout(base, 3*time.Hour)
}

// commitBackup is the last step of every backup and upload: under one lock for
// all of them it checks that the finished file still fits the owner's storage
// allowance (several backups of one user may have been written at the same
// time), moves it into place and records it. Nothing is left behind on failure.
func (s *Service) commitBackup(ctx context.Context, owner db.User, tmp, final string, size int64, record func() error) error {
	s.backup.commitMu.Lock()
	defer s.backup.commitMu.Unlock()
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		return err
	}
	if room >= 0 && size > room {
		return errBackupNoRoom
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if err := record(); err != nil {
		_ = os.Remove(final)
		return err
	}
	return nil
}

// ---- list and settings ----

func (s *Service) ListBackups(ctx context.Context, a Actor, ref string) (BackupList, error) {
	row, owner, err := s.backupApp(ctx, a, ref, false)
	if err != nil {
		return BackupList{}, err
	}
	rows, err := s.Q.ListBackupsByApp(ctx, row.ID)
	if err != nil {
		return BackupList{}, apperr.Internal(err)
	}
	out := BackupList{Items: make([]Backup, 0, len(rows)), Settings: s.backupSettings(ctx, row), Busy: s.backupBusy(row.ID)}
	for _, r := range rows {
		v := backupView(r)
		if isMulti(row) {
			v.Volume = r.Volume
		}
		out.Items = append(out.Items, v)
		out.TotalBytes += r.SizeBytes
	}
	if eng := dumpEngineOf(row); eng != nil {
		out.DumpTool = eng.tool
	}
	out.UploadMaxBytes, _ = s.uploadLimit(ctx, row, owner)
	out.Volumes = []BackupVolume{}
	if _, vols, err := s.appSpecs(ctx, row); err == nil {
		for i, v := range vols {
			r := row
			r.VolumeMb = v.SizeMB
			limit, _ := s.uploadLimit(ctx, r, owner)
			if i == 0 {
				out.UploadMaxBytes = limit
			}
			out.Volumes = append(out.Volumes, BackupVolume{Name: v.Name, Service: v.Service, MountPath: v.MountPath, SizeMB: v.SizeMB, UploadMaxBytes: limit})
		}
	}
	s.backup.mu.Lock()
	if r := s.backup.restore[row.ID]; r != nil {
		c := *r
		out.Restore = &c
	}
	s.backup.mu.Unlock()
	return out, nil
}

// uploadLimit is the biggest backup file that may be uploaded: no more than
// the /data limit of the app and no more than the owner's free storage.
// roomBound says it is the free storage that sets the limit.
func (s *Service) uploadLimit(ctx context.Context, row db.App, owner db.User) (limit int64, roomBound bool) {
	limit = row.VolumeMb * mib
	if room, err := s.backupRoom(ctx, owner); err == nil && room >= 0 && room < limit {
		return room, true
	}
	return limit, false
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
// as "running" in the list until it is done. An optional volume name picks
// the volume of an app with several; the default is the app's first volume.
func (s *Service) CreateBackup(ctx context.Context, a Actor, ref string, volume ...string) (Backup, error) {
	out, err := s.createBackups(ctx, a, ref, firstOpt(volume), false)
	if err != nil {
		return Backup{}, err
	}
	return out[0], nil
}

// CreateBackupAll starts a manual backup of every volume a service of the
// app mounts (the one /data volume of a single app), one after the other.
// Volumes that do not exist yet (their service never started) are left out.
func (s *Service) CreateBackupAll(ctx context.Context, a Actor, ref string) ([]Backup, error) {
	return s.createBackups(ctx, a, ref, "", true)
}

func (s *Service) createBackups(ctx context.Context, a Actor, ref, volume string, all bool) ([]Backup, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return nil, err
	}
	var vols []volumeSpec
	if all {
		_, specs, err := s.appSpecs(ctx, row)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		for _, v := range specs {
			if v.Service != "" { // a volume kept from an earlier compose file: nothing writes to it
				vols = append(vols, v)
			}
		}
		if len(vols) == 0 {
			return nil, volumeOffErr()
		}
	} else {
		vol, ok, err := s.pickVolume(ctx, row, volume)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, volumeOffErr()
		}
		vols = []volumeSpec{vol}
	}
	vs, err := s.volumes()
	if err != nil {
		return nil, err
	}
	ready := vols[:0:0]
	for _, vol := range vols {
		has, err := vs.HasVolume(ctx, vol.VolumeName)
		if err != nil {
			return nil, apperr.Unavailable("docker_unavailable", "bitploy could not read the files in /data.", "Try again in a minute.")
		}
		if has {
			ready = append(ready, vol)
		}
	}
	if len(ready) == 0 {
		return nil, volumeNotReadyErr()
	}
	if s.appBusy(row.ID) {
		return nil, busyErr()
	}
	if err := s.checkBackupDisk(); err != nil {
		return nil, err
	}
	room, err := s.backupRoom(ctx, owner)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if room == 0 {
		return nil, s.noRoomErr(owner)
	}
	lock := s.backupLock(row.ID)
	if !lock.TryLock() {
		return nil, backupBusyErr()
	}
	rows := make([]db.AppBackup, 0, len(ready))
	for _, vol := range ready {
		b, err := s.insertBackup(ctx, row.ID, BackupManual, vol.Name, s.backupFormatFor(ctx, row, vol.Name))
		if err != nil {
			for _, r := range rows {
				_ = s.Q.DeleteBackup(context.WithoutCancel(ctx), r.ID)
			}
			lock.Unlock()
			return nil, err
		}
		rows = append(rows, b)
		meta := map[string]any{"backup": b.ID, "kind": BackupManual, "format": b.Format}
		if isMulti(row) {
			meta["volume"] = b.Volume
		}
		s.Audit(ctx, a, "backup.create", "app", row.ID, meta)
	}
	s.backupGo(func() {
		defer lock.Unlock()
		ctx, cancel := s.backupCtx()
		defer cancel()
		for i, b := range rows {
			left := room
			if i > 0 {
				// The backups before this one took storage.
				var err error
				if left, err = s.backupRoom(ctx, owner); err != nil || left == 0 {
					_ = s.Q.FinishBackup(context.WithoutCancel(ctx), db.FinishBackupParams{
						Status: backupFailed, Error: "Skipped: there is no storage left for a backup. Delete an old backup or lower the /data limit of an app.",
						FinishedAt: nullInt(s.now(), true), ID: b.ID,
					})
					s.publishBackup(ctx, row.ID)
					continue
				}
			}
			if err := s.finishBackup(ctx, row, owner, b.ID, left, false); err != nil {
				s.Log.Warn("backup failed", "app", row.Slug, "volume", b.Volume, "err", err)
			}
		}
	})
	out := make([]Backup, len(rows))
	for i, b := range rows {
		out[i] = backupView(b)
		if isMulti(row) {
			out[i].Volume = b.Volume
		}
	}
	return out, nil
}

// insertBackup records a backup that is about to be made, in status
// running; format is formatVolume or formatDump.
func (s *Service) insertBackup(ctx context.Context, appID, kind, volume, format string) (db.AppBackup, error) {
	if volume == "" {
		volume = singleVolume
	}
	b := db.AppBackup{ID: ids.New(ids.Backup), AppID: appID, Kind: kind, Status: backupRunning, CreatedAt: s.now(), Volume: volume, Format: format}
	if err := s.Q.InsertBackup(ctx, db.InsertBackupParams{
		ID: b.ID, AppID: b.AppID, Kind: b.Kind, Status: b.Status, CreatedAt: b.CreatedAt, Volume: b.Volume, Format: b.Format,
	}); err != nil {
		return db.AppBackup{}, apperr.Internal(err)
	}
	s.publishBackup(ctx, appID)
	return b, nil
}

// ---- download ----

// OpenBackup opens a finished backup for download and gives the file name to
// offer. The files are the user's data: only the owner may download, and a
// read-only token may not.
func (s *Service) OpenBackup(ctx context.Context, a Actor, ref, id string) (*os.File, string, error) {
	row, _, err := s.backupApp(ctx, a, ref, true)
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
	f, err := os.Open(s.backupFile(b))
	if err != nil {
		return nil, "", apperr.NotFound("backup_file_missing", "The file of this backup is gone from the server.", "Delete this backup and make a new one.")
	}
	s.Audit(ctx, a, "backup.download", "app", row.ID, map[string]any{"backup": b.ID})
	ext := ".tar.gz"
	if b.Format == formatDump {
		ext = ".sql.gz"
	}
	name := fmt.Sprintf("%s-%s-%s%s", row.Slug, ts(b.CreatedAt).Format("20060102-150405"), strings.ReplaceAll(b.Kind, "_", "-"), ext)
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
// the size announced by the client, or -1. An optional volume name says which
// volume of an app with several the backup is for (default: the first).
func (s *Service) UploadBackup(ctx context.Context, a Actor, ref string, body io.Reader, contentLength int64, volume ...string) (Backup, error) {
	row, owner, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return Backup{}, err
	}
	vol, ok, err := s.pickVolume(ctx, row, firstOpt(volume))
	if err != nil {
		return Backup{}, err
	}
	if !ok {
		return Backup{}, volumeOffErr()
	}
	row.VolumeMb = vol.SizeMB // the limit of the volume the backup is for
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
	limit, roomBound := s.uploadLimit(ctx, row, owner)
	problems := uploadProblems{
		unpacked: apperr.New(413, "backup_too_large",
			fmt.Sprintf("The files in this backup add up to more than the %d MB /data limit of this app.", row.VolumeMb),
			"Upload a smaller backup, or raise the /data limit of the app first."),
	}
	if roomBound {
		problems.file = s.noRoomErr(owner)
	} else {
		problems.file = apperr.New(413, "backup_too_large",
			fmt.Sprintf("This backup file is larger than the %d MB /data limit of this app.", row.VolumeMb),
			"Upload a smaller backup, or raise the /data limit of the app first.")
	}
	if contentLength > limit {
		return Backup{}, problems.file
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
		stats, err := sanitizeTar(tw, tar.NewReader(gzr), tarLimits{MaxBytes: volBytes, Ctx: ctx})
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
		now := s.now()
		werr = s.commitBackup(ctx, owner, tmp, final, size, func() error {
			return s.Q.InsertBackup(ctx, db.InsertBackupParams{
				ID: id, AppID: row.ID, Kind: BackupUploaded, Status: backupReady, SizeBytes: size, CreatedAt: now,
				FinishedAt: sql.NullInt64{Int64: now, Valid: true}, Volume: vol.Name, Format: formatVolume,
			})
		})
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return Backup{}, s.uploadErr(werr, problems)
	}
	s.Audit(ctx, a, "backup.upload", "app", row.ID, map[string]any{"backup": id, "bytes": size})
	s.publishBackup(ctx, row.ID)
	out := Backup{ID: id, Kind: BackupUploaded, Format: formatVolume, Status: backupReady, SizeBytes: size, CreatedAt: ts(s.now())}
	if isMulti(row) {
		out.Volume = vol.Name
	}
	return out, nil
}

// uploadProblems are the answers for an upload that is too big: the unpacked
// files over the /data limit, or the file over the limit or the free storage.
type uploadProblems struct{ unpacked, file error }

func (s *Service) uploadErr(err error, p uploadProblems) error {
	var tp *tarProblem
	var re *readErr
	const hint = "Upload a .tar.gz of the contents of the /data folder, for example one you downloaded here."
	switch {
	case errors.As(err, &re):
		return apperr.BadRequest("upload_interrupted", "The upload was cut off before it finished.", "Try again.")
	case errors.As(err, &tp):
		return apperr.Unprocessable("backup_invalid", tp.msg, hint)
	case errors.Is(err, errTarTooBig):
		return p.unpacked
	case errors.Is(err, errUploadTooLarge), errors.Is(err, errBackupNoRoom):
		return p.file
	case errors.Is(err, context.Canceled):
		return apperr.BadRequest("upload_interrupted", "The upload was cut off before it finished.", "Try again.")
	case isCorrupt(err):
		return apperr.Unprocessable("backup_invalid", "The file is damaged or is not a .tar.gz archive.", hint)
	}
	return apperr.Internal(err)
}

// DismissRestore forgets a finished restore, so its result is not shown again.
func (s *Service) DismissRestore(ctx context.Context, a Actor, ref string) error {
	row, _, err := s.backupApp(ctx, a, ref, true)
	if err != nil {
		return err
	}
	defer s.publishBackup(ctx, row.ID)
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	if r := s.backup.restore[row.ID]; r != nil {
		if r.State == "running" {
			return apperr.Conflict("backup_busy", "The restore is still running.", "Wait until it has finished.")
		}
		delete(s.backup.restore, row.ID)
	}
	return nil
}
