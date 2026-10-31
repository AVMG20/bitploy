package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// waitEvent waits until the fake volume has seen event.
func (e *bkEnv) waitEvent(t *testing.T, event string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.vol.mu.Lock()
		for _, ev := range e.vol.events {
			if ev == event {
				e.vol.mu.Unlock()
				return
			}
		}
		e.vol.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the volume never saw %q", event)
}

// smallApp makes an app whose /data limit is below what the API allows, so that
// the limits are easy to reach.
func (e *bkEnv) smallApp(t *testing.T, slug string, mb int64) App {
	t.Helper()
	a := e.mustApp(t, CreateAppInput{Slug: slug, Volume: true, VolumeMB: ptr(int64(100))})
	e.exec(t, `UPDATE apps SET volume_mb = ?, current_deployment_id = 'dep_seed' WHERE id = ?`, mb, a.ID)
	return a
}

func (e *bkEnv) backupRow(t *testing.T, id string) (status, errText string, interrupted int64) {
	t.Helper()
	row := e.s.Store.DB.QueryRow(`SELECT status, error, interrupted FROM app_backups WHERE id = ?`, id)
	if err := row.Scan(&status, &errText, &interrupted); err != nil {
		t.Fatal(err)
	}
	return
}

func TestBackupDownloadNeedsDeployScope(t *testing.T) {
	e := newBkEnv(t)
	b := e.makeBackup(t)
	ro := e.user
	ro.Scope = ScopeRead
	if _, _, err := e.s.OpenBackup(context.Background(), ro, e.app.Slug, b.ID); depCode(err) != "token_read_only" {
		t.Errorf("read-only token: %v", err)
	}
	f, _, err := e.s.OpenBackup(context.Background(), e.user, e.app.Slug, b.ID)
	if err != nil {
		t.Fatalf("owner: %v", err)
	}
	f.Close()
}

func TestBackupHelperIsMadeBeforeTheAppIsPaused(t *testing.T) {
	e := newBkEnv(t)
	e.makeBackup(t)
	if got := strings.Join(e.vol.events, " "); got != "prepare open close" {
		t.Errorf("events = %q", got)
	}
}

func TestBackupNoVolumeYetIsRefusedAtRequestTime(t *testing.T) {
	e := newBkEnv(t)
	e.vol.noVolume = true
	if _, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug); depCode(err) != "volume_not_ready" {
		t.Errorf("create: %v", err)
	}
	e.vol.noVolume = false
	good := e.makeBackup(t) // the restore of an existing backup needs the volume too
	e.vol.noVolume = true
	if _, err := e.s.RestoreBackup(context.Background(), e.user, e.app.Slug, good.ID); depCode(err) != "volume_not_ready" {
		t.Errorf("restore: %v", err)
	}
	if n := len(e.list(t).Items); n != 1 {
		t.Errorf("a refused backup left %d rows", n)
	}
}

func TestBackupStoppedByShutdownIsNotAnAttempt(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	base, stop := context.WithCancel(context.Background())
	e.s.backup.base = base
	e.vol.gate = make(chan struct{}) // the app is "paused" here
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily' WHERE id = ?`, e.app.ID)

	e.s.runDueBackups(ctx)
	e.waitEvent(t, "open")
	stop()
	e.wait()
	got := e.backupsOfKind(t, "scheduled")
	if len(got) != 1 || got[0].Status != "failed" {
		t.Fatalf("scheduled = %+v", got)
	}
	if strings.Contains(got[0].Error, "too long") || !strings.Contains(got[0].Error, "stopped") {
		t.Errorf("message = %q", got[0].Error)
	}
	if _, _, interrupted := e.backupRow(t, got[0].ID); interrupted != 1 {
		t.Error("not marked as interrupted")
	}
	if e.vol.closed != e.vol.prepared {
		t.Errorf("helper not removed: %d prepared, %d closed", e.vol.prepared, e.vol.closed)
	}
	// The slot was not used up: the next start tries again.
	e.s.backup.base = nil
	e.vol.gate = nil
	e.runDue(ctx)
	got = e.backupsOfKind(t, "scheduled") // same second: the order of the two is by id
	if len(got) != 2 || (got[0].Status == "ready") == (got[1].Status == "ready") {
		t.Fatalf("after the restart: %+v", got)
	}
}

func TestBackupsRunAtMostTwoAtATime(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.vol.gate = make(chan struct{})
	for _, slug := range []string{"bk-two", "bk-three", "bk-four"} {
		a := e.smallApp(t, slug, 100)
		e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_seed' WHERE id = ?`, a.ID)
	}
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily'`)
	e.s.runDueBackups(ctx) // returns at once, the jobs run on their own
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.vol.mu.Lock()
		n := e.vol.opened
		e.vol.mu.Unlock()
		if n >= backupConcurrency {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a third one would have shown up by now
	e.vol.mu.Lock()
	if e.vol.opened != backupConcurrency {
		t.Errorf("%d backups read at once, want %d", e.vol.opened, backupConcurrency)
	}
	e.vol.mu.Unlock()
	close(e.vol.gate)
	e.wait()
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM app_backups WHERE kind = 'scheduled' AND status = 'ready'`).Scan(&n); err != nil || n != 4 {
		t.Errorf("%d scheduled backups ready, %v", n, err)
	}
}

func TestBackupWaitsForTheAppLockAndSkipsDeploys(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	// A deployment that is building: no backup is started.
	e.s.tryBusy(e.app.ID, "dep_x", func() {})
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); depCode(err) != "app_busy" {
		t.Errorf("during a deploy: %v", err)
	}
	e.s.clearBusy(e.app.ID, "dep_x")

	// A deploy swapping containers holds the app lock: the backup does not read until it is done.
	al := e.s.appLock(e.app.ID)
	al.Lock()
	b, err := e.s.CreateBackup(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	e.waitEvent(t, "prepare")
	time.Sleep(50 * time.Millisecond)
	e.vol.mu.Lock()
	opened := e.vol.opened
	e.vol.mu.Unlock()
	if opened != 0 {
		t.Error("the volume was read while the app lock was held")
	}
	// ...and when a deploy has started meanwhile, the backup gives way.
	e.s.tryBusy(e.app.ID, "dep_y", func() {})
	al.Unlock()
	e.wait()
	status, msg, interrupted := e.backupRow(t, b.ID)
	if status != "failed" || !strings.Contains(msg, "deployed") || interrupted != 1 {
		t.Errorf("row = %s %q %d", status, msg, interrupted)
	}
}

func TestBackupQuotaIsCheckedWhenTheBackupLands(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	two := e.smallApp(t, "bk-two", 1)
	// 100 MB + 1 MB reserved for /data; 1 MB left. Each backup is about 0.7 MB.
	e.exec(t, `UPDATE users SET quota_volume_mb = 102 WHERE id = ?`, e.user.UserID)
	junk := make([]byte, 700<<10)
	_, _ = rand.Read(junk)
	e.vol.files = map[string]string{"junk": string(junk)}
	e.vol.gate = make(chan struct{})
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateBackup(ctx, e.user, two.Slug); err != nil {
		t.Fatalf("both pass the check at the start: %v", err)
	}
	close(e.vol.gate)
	e.wait()
	var ready, failed int
	for _, id := range []string{e.app.ID, two.ID} {
		rows, err := e.s.Q.ListBackupsByApp(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			switch r.Status {
			case "ready":
				ready++
			case "failed":
				failed++
				if !strings.Contains(r.Error, "not enough storage") {
					t.Errorf("error = %q", r.Error)
				}
			}
		}
	}
	if ready != 1 || failed != 1 {
		t.Errorf("%d ready, %d failed: the allowance was overshot", ready, failed)
	}
	if m, _ := e.s.storageTaken(ctx, e.user.UserID, ""); m > 102 {
		t.Errorf("storage taken = %d MB of 102", m)
	}
	for _, id := range []string{e.app.ID, two.ID} {
		if tmps, _ := os.ReadDir(e.s.backupDir(id)); func() bool {
			for _, f := range tmps {
				if strings.HasSuffix(f.Name(), ".tmp") {
					return true
				}
			}
			return false
		}() {
			t.Error("temp file left behind")
		}
	}
}

func TestBackupUploadErrorsNameTheRightLimit(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	small := e.smallApp(t, "bk-small", 1)
	// 3 MB of zeros packs to a few KB: the file is small, what is in it is not.
	big := makeTarGz(t, tent{name: "zeros", body: strings.Repeat("\x00", 3<<20)})
	_, err := e.s.UploadBackup(ctx, e.user, small.Slug, bytes.NewReader(big), -1)
	if depCode(err) != "backup_too_large" || !strings.Contains(err.Error(), "files in this backup") {
		t.Errorf("unpacked too big: %v", err)
	}
	// A file that is bigger than the limit itself.
	junk := make([]byte, 2<<20)
	_, _ = rand.Read(junk)
	file := makeTarGz(t, tent{name: "junk", body: string(junk)})
	if _, err := e.s.UploadBackup(ctx, e.user, small.Slug, bytes.NewReader(file), int64(len(file))); depCode(err) != "backup_too_large" || !strings.Contains(err.Error(), "backup file is larger") {
		t.Errorf("file too big: %v", err)
	}
	// Not the limit of the app but the free storage: it says so.
	five := e.smallApp(t, "bk-five", 5)
	e.exec(t, `UPDATE users SET quota_volume_mb = 107 WHERE id = ?`, e.user.UserID) // 100 + 1 + 5 reserved, 1 MB left
	if _, err := e.s.UploadBackup(ctx, e.user, five.Slug, bytes.NewReader(file), -1); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("free storage blamed on something else: %v", err)
	}
}

func TestBackupRestoreDryRunKeepsTheVolumeSafe(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	// An uploaded archive that goes bad: a link whose target is not in it.
	up, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(makeTarGz(t, tent{name: "f", body: "x"})), -1)
	if err != nil {
		t.Fatal(err)
	}
	// Damage the end of the file: the start is fine, so only a full read finds it.
	path := e.s.backupPath(e.app.ID, up.ID)
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, data[:len(data)-12], 0o640); err != nil {
		t.Fatal(err)
	}
	before := e.vol.snapshot()
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, up.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitRestore(t)
	if r.State != "failed" || !strings.Contains(r.Error, "nothing was changed") {
		t.Errorf("restore = %+v", r)
	}
	if e.vol.replaceN != 0 || !reflect.DeepEqual(e.vol.snapshot(), before) {
		t.Error("the volume was touched")
	}
	if got := e.backupsOfKind(t, "before_restore"); len(got) != 0 {
		t.Errorf("a safety backup was made for nothing: %+v", got)
	}
	var marker string
	_ = e.s.Store.DB.QueryRow(`SELECT restore_backup_id FROM apps WHERE id = ?`, e.app.ID).Scan(&marker)
	if marker != "" {
		t.Error("restore marker left behind")
	}
}

func TestBackupRestoreFromTrustedArchive(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	// What a real app volume holds: an absolute link, a hard link to a file that is left out.
	e.vol.extra = []tent{
		{name: "./current", typ: tar.TypeSymlink, link: "/data/releases/1"},
		{name: "./cache/.wh.x", body: "x"},
		{name: "./cache/alias", typ: tar.TypeLink, link: "cache/.wh.x"},
		{name: "./pipe", typ: tar.TypeFifo},
	}
	b := e.makeBackup(t)
	got := e.readBackup(t, b)
	if _, ok := got["current"]; !ok {
		t.Errorf("the link is missing from the backup: %v", got)
	}
	if _, ok := got["cache/alias"]; ok {
		t.Errorf("the link to a left-out file was kept: %v", got)
	}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.waitRestore(t); r.State != "done" {
		t.Fatalf("restore of a backup with an absolute link = %+v", r)
	}
	// The same link in an upload is refused.
	_, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(makeTarGz(t, tent{name: "current", typ: tar.TypeSymlink, link: "/data/releases/1"})), -1)
	if depCode(err) != "backup_invalid" {
		t.Errorf("upload with an absolute link: %v", err)
	}
}

func TestBackupRestoreBothFailLeavesTheAppStopped(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	e.vol.failAt = map[int]bool{1: true, 2: true}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitRestore(t)
	if r.State != "failed" || !strings.Contains(r.Error, "left stopped") {
		t.Errorf("restore = %+v", r)
	}
	var desired, msg string
	_ = e.s.Store.DB.QueryRow(`SELECT desired_state, status_message FROM apps WHERE id = ?`, e.app.ID).Scan(&desired, &msg)
	if desired != "stopped" || !strings.Contains(msg, "before restore") {
		t.Errorf("app = %q %q", desired, msg)
	}
	var marker string
	_ = e.s.Store.DB.QueryRow(`SELECT restore_backup_id FROM apps WHERE id = ?`, e.app.ID).Scan(&marker)
	if marker != "" {
		t.Error("restore marker left behind")
	}
}

func TestInterruptedRestoreIsRecoveredAtStartup(t *testing.T) {
	ctx := context.Background()
	rec := func(t *testing.T, e *bkEnv, backupID, safeID string) BackupRestore {
		t.Helper()
		e.s.setRestoreMarker(ctx, e.app.ID, backupID, safeID, false)
		e.s.recoverAfterCrash(ctx)
		l := e.list(t)
		if l.Restore == nil {
			t.Fatal("no restore state after the recovery")
		}
		var marker string
		_ = e.s.Store.DB.QueryRow(`SELECT restore_backup_id FROM apps WHERE id = ?`, e.app.ID).Scan(&marker)
		if marker != "" {
			t.Error("marker not cleared")
		}
		return *l.Restore
	}

	t.Run("after the old data was saved", func(t *testing.T) {
		e := newBkEnv(t)
		safe := e.makeBackup(t)
		e.vol.files = map[string]string{"half": "restored"} // a restore that died in the middle
		r := rec(t, e, "bak_other", safe.ID)
		if r.State != "failed" || !strings.Contains(r.Error, "old data was put back") {
			t.Errorf("restore = %+v", r)
		}
		if got := e.vol.snapshot(); !reflect.DeepEqual(got, map[string]string{"a.txt": "one", "d/b.txt": "two"}) {
			t.Errorf("volume holds %v", got)
		}
	})
	t.Run("before the volume was touched", func(t *testing.T) {
		e := newBkEnv(t)
		r := rec(t, e, "bak_other", "")
		if !strings.Contains(r.Error, "Nothing was changed") || e.vol.replaceN != 0 {
			t.Errorf("restore = %+v, replaced %d", r, e.vol.replaceN)
		}
	})
	t.Run("old data is gone too", func(t *testing.T) {
		e := newBkEnv(t)
		r := rec(t, e, "bak_other", "bak_missing")
		if !strings.Contains(r.Error, "left stopped") {
			t.Errorf("restore = %+v", r)
		}
		var desired string
		_ = e.s.Store.DB.QueryRow(`SELECT desired_state FROM apps WHERE id = ?`, e.app.ID).Scan(&desired)
		if desired != "stopped" {
			t.Errorf("desired state = %q", desired)
		}
	})
}

func TestRestoreStateCanBeDismissed(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	e.vol.failAt = map[int]bool{1: true}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	e.waitRestore(t)
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if err := e.s.DismissRestore(ctx, other, e.app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	if err := e.s.DismissRestore(ctx, e.user, e.app.Slug); err != nil {
		t.Fatal(err)
	}
	if l := e.list(t); l.Restore != nil {
		t.Errorf("restore state is back: %+v", l.Restore)
	}
	if err := e.s.DismissRestore(ctx, e.user, e.app.Slug); err != nil {
		t.Errorf("dismissing nothing: %v", err)
	}
}

func TestRestoreStateOfARunningRestoreCannotBeDismissed(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	e.vol.mu.Lock()
	e.vol.events = nil
	e.vol.gate = make(chan struct{})
	e.vol.mu.Unlock()
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	e.waitEvent(t, "open") // the safety backup is being read
	if err := e.s.DismissRestore(ctx, e.user, e.app.Slug); depCode(err) != "backup_busy" {
		t.Errorf("dismiss while running: %v", err)
	}
	close(e.vol.gate)
	e.waitRestore(t)
}

func TestDeletingAnAppForgetsItsBackupState(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	e.waitRestore(t)
	if err := e.s.DeleteApp(ctx, e.user, e.app.Slug, e.app.Slug); err != nil {
		t.Fatal(err)
	}
	e.s.backup.mu.Lock()
	defer e.s.backup.mu.Unlock()
	if len(e.s.backup.locks) != 0 || len(e.s.backup.restore) != 0 {
		t.Errorf("state left: %d locks, %d restores", len(e.s.backup.locks), len(e.s.backup.restore))
	}
	if _, err := os.Stat(e.s.backupDir(e.app.ID)); err == nil {
		t.Error("backup files left")
	}
}
