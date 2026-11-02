package app

// Turning /data off. The files and the backups of an app are its data, so this
// is only done when there is nothing in them or the caller confirms with
// delete_data (the dashboard asks the user to type the app name first).
// Static sites never have a volume (staticStorageErr).

import (
	"context"
	"errors"
	"fmt"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

func staticStorageErr() *apperr.Error {
	return apperr.Unprocessable("static_no_storage", "Static sites don't need storage, so /data cannot be turned on.",
		"A static site only serves its files. To keep files between deploys, deploy the app with your own Dockerfile.")
}

func staticSizeErr() *apperr.Error {
	return apperr.Unprocessable("static_fixed_size", fmt.Sprintf("A static site always has %d MB of memory and %d CPU millis, so its size cannot be changed.", staticMemoryMB, staticCPUMillis),
		"Static sites do not count towards your memory and CPU allowance. To choose a size, deploy the app with your own Dockerfile.")
}

func volumeHasDataErr(files bool, backups int) *apperr.Error {
	what := "files in /data"
	switch {
	case files && backups > 0:
		what = fmt.Sprintf("files in /data and %d %s", backups, plural(backups, "backup", "backups"))
	case backups > 0:
		what = fmt.Sprintf("%d %s of /data", backups, plural(backups, "backup", "backups"))
	}
	return apperr.Conflict("volume_has_data",
		"Turning /data off deletes the "+what+" for good, and nothing was changed.",
		"Ask the user first. If they agree, call update_app again with volume=false and delete_data=true.").
		WithNext("update_app").WithDetail("has_files", files).WithDetail("backups", backups)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// volumeEmpty reports whether /data holds nothing. A volume that does not exist
// yet is empty. The folder itself is listed (one entry is enough), not the
// hourly measurement, which can be hours old or missing. Without a way to list
// files (no Docker) the measurement is all there is.
func (s *Service) volumeEmpty(ctx context.Context, row db.App) (bool, error) {
	measuredEmpty := func() bool {
		d, err := s.Q.GetAppDisk(ctx, row.ID)
		return err != nil || d.VolumeBytes == 0
	}
	vs, err := s.volumes()
	if err != nil {
		return measuredEmpty(), nil
	}
	vol := appVolumeName(row)
	has, err := vs.HasVolume(ctx, vol)
	if err != nil {
		return false, apperr.Unavailable("docker_unavailable", "bitploy could not look at the data folder right now.", "Try again in a minute.")
	}
	if !has {
		return true, nil
	}
	fs, ok := vs.(fileStore)
	if !ok {
		return measuredEmpty(), nil
	}
	l, err := fs.ListFiles(ctx, vol, "", 1)
	if errors.Is(err, dockerx.ErrNoVolume) {
		return true, nil
	}
	if err != nil {
		return false, filesErr(err)
	}
	return len(l.Entries) == 0, nil
}

// checkVolumeOff runs before anything is changed. The caller holds the backup
// lock, so no backup or restore runs meanwhile.
func (s *Service) checkVolumeOff(ctx context.Context, row db.App, deleteData bool) error {
	if !s.settled(row.ID) {
		return busyErr()
	}
	if n, err := s.Q.CountPendingDeploymentsByApp(ctx, row.ID); err == nil && n > 0 {
		return busyErr()
	}
	s.backup.mu.Lock()
	r := s.backup.restore[row.ID]
	restoring := r != nil && r.State == "running"
	s.backup.mu.Unlock()
	if restoring {
		return backupBusyErr()
	}
	backups, err := s.Q.ListBackupsByApp(ctx, row.ID)
	if err != nil {
		return apperr.Internal(err)
	}
	empty, err := s.volumeEmpty(ctx, row)
	if err != nil {
		return err
	}
	if (!empty || len(backups) > 0) && !deleteData {
		return volumeHasDataErr(!empty, len(backups))
	}
	return nil
}

// dropVolume is the second half of turning /data off, after the database says
// volume_enabled = 0: the containers go (a container keeps its volume in use),
// then the Docker volume and all backups, then the app starts again without the
// volume. If the volume cannot be removed the setting is put back. The caller
// holds the backup lock.
func (s *Service) dropVolume(ctx context.Context, row db.App) error {
	al := s.appLock(row.ID)
	al.Lock()
	defer al.Unlock()
	bg := context.WithoutCancel(ctx)

	revert := func(ae *apperr.Error) error {
		if err := s.Q.SetAppVolumeEnabled(bg, db.SetAppVolumeEnabledParams{VolumeEnabled: 1, UpdatedAt: s.now(), ID: row.ID}); err != nil {
			s.Log.Error("put the volume setting back", "app", row.Slug, "err", err)
		}
		if fresh, err := s.Q.GetApp(bg, row.ID); err == nil && fresh.DesiredState == "running" && fresh.CurrentDeploymentID.Valid && s.Docker != nil {
			if rerr := s.startCurrentLocked(bg, fresh); rerr != nil {
				s.Log.Warn("start after a failed volume removal; the reconciler will retry", "app", row.Slug, "err", rerr)
			}
		}
		return ae
	}

	if s.Docker != nil {
		cs, err := s.Docker.ListAppContainers(bg, row.ID)
		if err != nil {
			return revert(apperr.Unavailable("volume_remove_failed", "bitploy could not stop the app to remove /data, so /data stays on.", "Try again in a minute.").WithCause(err))
		}
		for _, c := range cs {
			if err := s.Docker.RemoveContainer(bg, dockerx.SummaryName(c)); err != nil {
				return revert(apperr.Unavailable("volume_remove_failed", "bitploy could not stop the app to remove /data, so /data stays on.", "Try again in a minute.").WithCause(err))
			}
		}
		if err := s.Docker.RemoveVolume(bg, appVolumeName(row)); err != nil {
			s.Log.Error("remove volume", "app", row.Slug, "err", err)
			return revert(apperr.Unavailable("volume_remove_failed", "Docker could not remove the /data folder, so /data stays on.",
				"Try again in a minute. If it keeps failing, tell the bitploy admin to check the Docker volume "+quote(appVolumeName(row))+"."))
		}
	}
	// The backup files go with the volume. A failed file removal is only logged,
	// as when an app is deleted: the rows are what the storage allowance counts.
	if err := s.removeAppBackups(row.ID); err != nil {
		s.Log.Warn("removing backups failed", "app", row.Slug, "err", err)
	}
	if err := s.Q.DeleteBackupsByApp(bg, row.ID); err != nil {
		s.Log.Error("removing backup rows failed", "app", row.Slug, "err", err)
	}
	s.backup.mu.Lock()
	delete(s.backup.restore, row.ID)
	s.backup.mu.Unlock()
	s.metricsMgr().MeasureSoon()

	if fresh, err := s.Q.GetApp(bg, row.ID); err == nil && fresh.DesiredState == "running" && fresh.CurrentDeploymentID.Valid && s.Docker != nil {
		if rerr := s.startCurrentLocked(bg, fresh); rerr != nil {
			s.Log.Warn("start after turning /data off failed; the reconciler will retry", "app", row.Slug, "err", rerr)
		}
	}
	return nil
}
