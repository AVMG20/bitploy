package app

import (
	"context"
	"os"
	"testing"
)

func TestTurningDataOffPersists(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	fv.fmu.Lock()
	fv.tree = map[string][]byte{"": nil} // an empty /data
	fv.fmu.Unlock()
	off, on := false, true

	// Nothing in /data and no backups: off at once, and it stays off.
	got, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off})
	if err != nil || got.Volume {
		t.Fatalf("off: %+v %v", got, err)
	}
	for range 2 { // a fresh read, as after a page refresh
		again, err := e.s.GetApp(ctx, e.user, e.app.Slug)
		if err != nil || again.Volume {
			t.Fatalf("after reading again: %+v %v", again, err)
		}
	}
	// Off again is not an error and not a deletion.
	if _, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off}); err != nil {
		t.Errorf("off twice: %v", err)
	}
	got, err = e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &on})
	if err != nil || !got.Volume {
		t.Fatalf("on: %+v %v", got, err)
	}
}

func TestTurningDataOffNeedsConfirmation(t *testing.T) {
	ctx := context.Background()
	off, yes, no := false, true, false

	t.Run("files in /data", func(t *testing.T) {
		e, _ := newFilesEnv(t) // the fake /data holds files; the hourly measurement knows nothing
		_, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off})
		if depCode(err) != "volume_has_data" {
			t.Fatalf("without delete_data: %v", err)
		}
		if _, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &no}); depCode(err) != "volume_has_data" {
			t.Errorf("delete_data=false: %v", err)
		}
		if got, _ := e.s.GetApp(ctx, e.user, e.app.Slug); !got.Volume {
			t.Fatal("the refusal turned /data off")
		}
		if got, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes}); err != nil || got.Volume {
			t.Fatalf("with delete_data: %+v %v", got, err)
		}
	})

	t.Run("backups only", func(t *testing.T) {
		e, fv := newFilesEnv(t)
		fv.fmu.Lock()
		fv.tree = map[string][]byte{"": nil}
		fv.fmu.Unlock()
		e.makeBackup(t)
		e.makeBackup(t)
		_, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off})
		if depCode(err) != "volume_has_data" {
			t.Fatalf("with backups: %v", err)
		}
		if got, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes}); err != nil || got.Volume || got.BackupsMB != 0 {
			t.Fatalf("with delete_data: %+v %v", got, err)
		}
		if l := e.list(t); len(l.Items) != 0 {
			t.Errorf("backups left: %+v", l.Items)
		}
		if _, err := os.Stat(e.s.backupDir(e.app.ID)); !os.IsNotExist(err) {
			t.Errorf("backup folder still there: %v", err)
		}
		if n, _ := e.s.Q.SumBackupBytesByUser(ctx, e.user.UserID); n != 0 {
			t.Errorf("backup bytes still counted: %d", n)
		}
	})

	t.Run("a volume that does not exist yet is empty", func(t *testing.T) {
		e, fv := newFilesEnv(t)
		fv.noVolume = true
		if got, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off}); err != nil || got.Volume {
			t.Fatalf("no volume yet: %+v %v", got, err)
		}
	})

	t.Run("the stale measurement does not decide", func(t *testing.T) {
		e, fv := newFilesEnv(t)
		fv.fmu.Lock()
		fv.tree = map[string][]byte{"": nil}
		fv.fmu.Unlock()
		e.exec(t, `INSERT INTO app_disk (app_id, image_bytes, volume_bytes, measured_at) VALUES (?, 0, ?, 1)`, e.app.ID, 50<<20)
		if got, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off}); err != nil || got.Volume {
			t.Fatalf("emptied since the measurement: %+v %v", got, err)
		}
	})
}

func TestTurningDataOffWaitsForBackups(t *testing.T) {
	e, _ := newFilesEnv(t)
	ctx := context.Background()
	off, yes := false, true
	l := e.s.backupLock(e.app.ID)
	l.Lock() // a backup or restore is running
	_, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes})
	l.Unlock()
	if depCode(err) != "backup_busy" {
		t.Fatalf("while a backup runs: %v", err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, e.app.Slug); !got.Volume {
		t.Error("/data was turned off while a backup runs")
	}
	if _, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes}); err != nil {
		t.Errorf("afterwards: %v", err)
	}
}
