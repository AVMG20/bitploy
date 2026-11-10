package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// fakeVol stands in for Docker: the "volume" is a map of path to content.
type fakeVol struct {
	mu         sync.Mutex
	files      map[string]string
	extra      []tent // entries added to every archive (links, special files)
	noVolume   bool   // the volume does not exist yet
	archiveErr error
	gate       chan struct{} // Open waits for it to be closed (the app is paused then)
	prepared   int           // PrepareArchive calls
	opened     int           // Open calls
	closed     int           // Close calls
	events     []string      // "prepare", "open", "close" in order
	replaceN   int
	failAt     map[int]bool // ReplaceVolume calls (1-based) that fail
	fixOwner   []bool
}

func (f *fakeVol) HasVolume(_ context.Context, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.noVolume, nil
}

func (f *fakeVol) PrepareArchive(_ context.Context, _ string) (volumeArchive, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noVolume {
		return nil, dockerx.ErrNoVolume
	}
	f.prepared++
	f.events = append(f.events, "prepare")
	return &fakeArchive{f}, nil
}

type fakeArchive struct{ f *fakeVol }

func (a *fakeArchive) Open(ctx context.Context) (io.ReadCloser, error) {
	f := a.f
	f.mu.Lock()
	f.opened++
	f.events = append(f.events, "open")
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.archiveErr != nil {
		return nil, f.archiveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o1777})
	for name, body := range f.files {
		_ = tw.WriteHeader(&tar.Header{Name: "./" + name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)), Uid: 1000, Gid: 1000})
		_, _ = tw.Write([]byte(body))
	}
	for _, e := range f.extra {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Linkname: e.link, Size: int64(len(e.body))}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		_ = tw.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	return io.NopCloser(&buf), nil
}

func (a *fakeArchive) Close() error {
	a.f.mu.Lock()
	defer a.f.mu.Unlock()
	a.f.closed++
	a.f.events = append(a.f.events, "close")
	return nil
}

func (f *fakeVol) ReplaceVolume(_ context.Context, _ string, archive io.Reader, fixOwner bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaceN++
	f.fixOwner = append(f.fixOwner, fixOwner)
	if f.failAt[f.replaceN] {
		return errors.New("Error response from daemon: no space left on device")
	}
	files := map[string]string{}
	tr := tar.NewReader(archive)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			files[h.Name] = string(b)
		}
	}
	f.files = files
	return nil
}

func (f *fakeVol) snapshot() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.files {
		out[k] = v
	}
	return out
}

type bkEnv struct {
	*depEnv
	vol   *fakeVol
	app   App
	clock time.Time
}

func newBkEnv(t *testing.T) *bkEnv {
	t.Helper()
	e := &bkEnv{depEnv: depTestSvc(t), vol: &fakeVol{files: map[string]string{"a.txt": "one", "d/b.txt": "two"}}}
	e.clock = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	e.s.Now = func() time.Time { return e.clock }
	e.s.backup.vol = e.vol
	e.s.ops.diskHasRoom = func() (bool, error) { return true, nil }
	e.app = e.mustApp(t, CreateAppInput{Slug: "bk-app", Volume: true, VolumeMB: ptr(int64(100))})
	e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_seed' WHERE id = ?`, e.app.ID)
	// The tests below start from automatic backups that are off, as if the
	// user turned them off; TestDefaultBackups covers the default.
	e.exec(t, `UPDATE apps SET backup_schedule = 'off', backup_keep_mode = 'last', backup_keep = 7, backup_default_at = 0 WHERE id = ?`, e.app.ID)
	return e
}

func (e *bkEnv) wait() { e.s.backup.wg.Wait() }

// runDue runs the automatic backups that are due and waits for them to end.
func (e *bkEnv) runDue(ctx context.Context) {
	e.s.runDueBackups(ctx)
	e.wait()
}

func (e *bkEnv) list(t *testing.T) BackupList {
	t.Helper()
	l, err := e.s.ListBackups(context.Background(), e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (e *bkEnv) makeBackup(t *testing.T) Backup {
	t.Helper()
	b, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	e.wait()
	for _, it := range e.list(t).Items {
		if it.ID == b.ID {
			if it.Status != "ready" {
				t.Fatalf("backup %+v", it)
			}
			return it
		}
	}
	t.Fatal("backup not in the list")
	return Backup{}
}

// readBackup lists the regular files of a stored backup.
func (e *bkEnv) readBackup(t *testing.T, b Backup) map[string]string {
	t.Helper()
	f, err := os.Open(e.s.backupPath(e.app.ID, b.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		if h.Typeflag == tar.TypeReg {
			out[h.Name] = string(body)
		} else {
			out[h.Name] = ""
		}
	}
}

func makeTarGz(t *testing.T, ents ...tent) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(buildTar(t, ents...))
	_ = gz.Close()
	return buf.Bytes()
}

func TestBackupCreate(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b, err := e.s.CreateBackup(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "running" || b.Kind != "manual" || !strings.HasPrefix(b.ID, "bak_") {
		t.Errorf("created %+v", b)
	}
	e.wait()
	l := e.list(t)
	if len(l.Items) != 1 || l.Items[0].Status != "ready" || l.Items[0].SizeBytes == 0 || l.Busy {
		t.Fatalf("list = %+v", l)
	}
	if got := e.readBackup(t, l.Items[0]); !reflect.DeepEqual(got, map[string]string{"a.txt": "one", "d/b.txt": "two"}) {
		t.Errorf("archive holds %v", got)
	}
	if l.TotalBytes != l.Items[0].SizeBytes || l.Settings.Schedule != "off" || l.Settings.NextAt != nil {
		t.Errorf("list = %+v", l)
	}
	if entries, _ := filepath.Glob(filepath.Join(e.s.backupDir(e.app.ID), "*.tmp")); len(entries) != 0 {
		t.Errorf("temp files left: %v", entries)
	}
}

func TestBackupCreateRules(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.CreateBackup(ctx, other, e.app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.CreateBackup(ctx, ro, e.app.Slug); depCode(err) != "token_read_only" {
		t.Errorf("read-only token: %v", err)
	}
	if _, err := e.s.ListBackups(ctx, ro, e.app.Slug); err != nil {
		t.Errorf("read-only token may list: %v", err)
	}
	if _, err := e.s.ListBackups(ctx, other, e.app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user lists: %v", err)
	}
	e.exec(t, `UPDATE apps SET volume_enabled = 0 WHERE id = ?`, e.app.ID)
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); depCode(err) != "volume_off" {
		t.Errorf("volume off: %v", err)
	}
	e.exec(t, `UPDATE apps SET volume_enabled = 1 WHERE id = ?`, e.app.ID)
	e.s.ops.diskHasRoom = func() (bool, error) { return false, nil }
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); depCode(err) != "server_disk_full" {
		t.Errorf("disk full: %v", err)
	}
}

func TestBackupOnlyOneAtATime(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.vol.gate = make(chan struct{})
	b, err := e.s.CreateBackup(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); depCode(err) != "backup_busy" {
		t.Errorf("second backup: %v", err)
	}
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(makeTarGz(t, tent{name: "a", body: "x"})), -1); depCode(err) != "backup_busy" {
		t.Errorf("upload during backup: %v", err)
	}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "backup_not_ready" {
		t.Errorf("restore of a running backup: %v", err)
	}
	if err := e.s.DeleteBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "backup_busy" {
		t.Errorf("delete during backup: %v", err)
	}
	if err := e.s.DeleteApp(ctx, e.user, e.app.Slug, e.app.Slug); depCode(err) != "backup_busy" {
		t.Errorf("delete app during backup: %v", err)
	}
	if !e.list(t).Busy {
		t.Error("list does not say busy")
	}
	close(e.vol.gate)
	e.wait()
	if l := e.list(t); l.Busy || l.Items[0].Status != "ready" {
		t.Errorf("after: %+v", l)
	}
}

func TestBackupFailureIsRecorded(t *testing.T) {
	e := newBkEnv(t)
	e.vol.archiveErr = errors.New("Error response from daemon: boom")
	b, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	e.wait()
	got := e.list(t).Items[0]
	if got.ID != b.ID || got.Status != "failed" || !strings.Contains(got.Error, "boom") || got.SizeBytes != 0 {
		t.Errorf("backup = %+v", got)
	}
	if _, err := os.Stat(e.s.backupPath(e.app.ID, b.ID)); err == nil {
		t.Error("file of a failed backup exists")
	}
	if _, _, err := e.s.OpenBackup(context.Background(), e.user, e.app.Slug, b.ID); depCode(err) != "backup_not_ready" {
		t.Errorf("download of a failed backup: %v", err)
	}
	// A failed backup can be deleted like any other, and a new one works.
	if err := e.s.DeleteBackup(context.Background(), e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	e.vol.archiveErr = nil
	e.makeBackup(t)
}

func TestBackupStorageAllowance(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	// The 100 MB /data limit is reserved; an allowance of exactly that leaves nothing.
	e.exec(t, `UPDATE users SET quota_volume_mb = 100 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug); depCode(err) != "storage_quota_exceeded" {
		t.Fatalf("no room: %v", err)
	}
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(makeTarGz(t, tent{name: "a", body: "x"})), -1); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("upload without room: %v", err)
	}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, "bak_x"); depCode(err) != "backup_not_found" {
		t.Errorf("restore of nothing: %v", err)
	}

	// 1 MB of room left: a small backup fits, an incompressible 3 MB one does not.
	e.exec(t, `UPDATE users SET quota_volume_mb = 102 WHERE id = ?`, e.user.UserID)
	e.makeBackup(t)
	big := make([]byte, 3<<20)
	_, _ = rand.Read(big)
	e.vol.files = map[string]string{"big.bin": string(big)}
	b, err := e.s.CreateBackup(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	e.wait()
	var failed Backup
	for _, it := range e.list(t).Items {
		if it.ID == b.ID {
			failed = it
		}
	}
	if failed.Status != "failed" || !strings.Contains(failed.Error, "storage") {
		t.Errorf("big backup = %+v", failed)
	}
	if entries, _ := filepath.Glob(filepath.Join(e.s.backupDir(e.app.ID), "*")); len(entries) != 1 {
		t.Errorf("files after a refused backup: %v", entries)
	}
}

func TestBackupsCountAsStorage(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	e.exec(t, `UPDATE app_backups SET size_bytes = ? WHERE id = ?`, 3<<20, b.ID)

	app, err := e.s.GetApp(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if app.BackupsMB != 3 {
		t.Errorf("app.BackupsMB = %v", app.BackupsMB)
	}
	u, err := e.s.Usage(ctx, e.user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if u.BackupsMB != 3 {
		t.Errorf("usage.BackupsMB = %v", u.BackupsMB)
	}
	taken, err := e.s.storageTaken(ctx, e.user.UserID, "")
	if err != nil || taken != 103 { // 100 MB reserved for /data + 3 MB of backups
		t.Errorf("storageTaken = %d, %v", taken, err)
	}
	// Turning /data of another app on has to fit next to them.
	e.exec(t, `UPDATE users SET quota_volume_mb = 150 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: "second", Volume: true, VolumeMB: ptr(int64(100))}); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("second volume: %v", err)
	}
	e.exec(t, `UPDATE users SET quota_volume_mb = 203 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: "third", Volume: true, VolumeMB: ptr(int64(100))}); err != nil {
		t.Errorf("third app, 203 MB: %v", err)
	}
}

func TestBackupUpload(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	good := makeTarGz(t,
		tent{name: "./", typ: tar.TypeDir},
		tent{name: "./x.txt", body: "hello"},
		tent{name: "sub/y.txt", body: "there"},
	)
	b, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(good), int64(len(good)))
	if err != nil {
		t.Fatal(err)
	}
	if b.Kind != "uploaded" || b.Status != "ready" || b.SizeBytes == 0 {
		t.Errorf("uploaded %+v", b)
	}
	if got := e.readBackup(t, b); !reflect.DeepEqual(got, map[string]string{"x.txt": "hello", "sub/y.txt": "there"}) {
		t.Errorf("stored archive holds %v", got)
	}

	bad := map[string]struct {
		body []byte
		code string
	}{
		"absolute path":  {makeTarGz(t, tent{name: "/etc/cron.d/x", body: "x"}), "backup_invalid"},
		"dotdot":         {makeTarGz(t, tent{name: "../x", body: "x"}), "backup_invalid"},
		"escaping link":  {makeTarGz(t, tent{name: "l", typ: tar.TypeSymlink, link: "../../etc"}), "backup_invalid"},
		"device":         {makeTarGz(t, tent{name: "null", typ: tar.TypeChar}), "backup_invalid"},
		"empty archive":  {makeTarGz(t), "backup_invalid"},
		"not gzip":       {buildTar(t, tent{name: "a", body: "x"}), "backup_invalid"},
		"plain text":     {[]byte("this is not an archive"), "backup_invalid"},
		"empty body":     {nil, "backup_invalid"},
		"truncated gzip": {good[:len(good)/2], "backup_invalid"},
	}
	for name, c := range bad {
		before := e.list(t)
		_, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(c.body), -1)
		if depCode(err) != c.code {
			t.Errorf("%s: %v", name, err)
		}
		if after := e.list(t); len(after.Items) != len(before.Items) {
			t.Errorf("%s: a backup was stored", name)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(e.s.backupDir(e.app.ID), "*")); len(entries) != 1 {
		t.Errorf("files left after refused uploads: %v", entries)
	}

	// The files in it may not add up to more than the /data limit (100 MB).
	var huge bytes.Buffer
	gz := gzip.NewWriter(&huge)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "zeros", Typeflag: tar.TypeReg, Mode: 0o644, Size: 101 << 20})
	_, _ = io.CopyN(tw, zeroReader{}, 101<<20)
	_ = tw.Close()
	_ = gz.Close()
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(huge.Bytes()), int64(huge.Len())); depCode(err) != "backup_too_large" {
		t.Errorf("bomb: %v", err)
	}
	// The same for the size announced by the client.
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(good), 101<<20); depCode(err) != "backup_too_large" {
		t.Errorf("announced size: %v", err)
	}
	// A cut-off connection is not the user's archive being bad.
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, io.MultiReader(bytes.NewReader(good[:20]), failReader{}), -1); depCode(err) != "upload_interrupted" {
		t.Errorf("interrupted: %v", err)
	}
	// Less room than the limit: the allowance is what counts.
	e.exec(t, `UPDATE users SET quota_volume_mb = 101 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(huge.Bytes()), -1); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("over allowance: %v", err)
	}
	e.exec(t, `UPDATE apps SET volume_enabled = 0 WHERE id = ?`, e.app.ID)
	if _, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(good), -1); depCode(err) != "volume_off" {
		t.Errorf("volume off: %v", err)
	}
}

func TestBackupDownload(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	f, name, err := e.s.OpenBackup(ctx, e.user, e.app.Slug, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !strings.HasPrefix(name, "bk-app-20261029-120000-manual") || !strings.HasSuffix(name, ".tar.gz") {
		t.Errorf("name = %q", name)
	}
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, _, err := e.s.OpenBackup(ctx, other, e.app.Slug, b.ID); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	// An admin may look at the list of any app but not at its files.
	admin := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	admin.Scope, admin.Channel = ScopeSession, ChannelDashboard
	if _, err := e.s.ListBackups(ctx, admin, e.app.Slug); err != nil {
		t.Errorf("admin list: %v", err)
	}
	if _, _, err := e.s.OpenBackup(ctx, admin, e.app.Slug, b.ID); depCode(err) != "app_not_found" {
		t.Errorf("admin download: %v", err)
	}
	if _, _, err := e.s.OpenBackup(ctx, e.user, e.app.Slug, "bak_nope"); depCode(err) != "backup_not_found" {
		t.Errorf("unknown backup: %v", err)
	}
	if err := os.Remove(e.s.backupPath(e.app.ID, b.ID)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.OpenBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "backup_file_missing" {
		t.Errorf("missing file: %v", err)
	}
}

func (e *bkEnv) waitRestore(t *testing.T) BackupRestore {
	t.Helper()
	e.wait()
	r := e.list(t).Restore
	if r == nil {
		t.Fatal("no restore state")
	}
	return *r
}

func TestBackupRestore(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t) // a.txt, d/b.txt
	e.vol.files = map[string]string{"a.txt": "changed", "new.txt": "new"}

	st, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "running" || st.BackupID != b.ID {
		t.Errorf("state = %+v", st)
	}
	r := e.waitRestore(t)
	if r.State != "done" || r.Error != "" || r.FinishedAt == nil {
		t.Fatalf("restore = %+v", r)
	}
	if got := e.vol.snapshot(); !reflect.DeepEqual(got, map[string]string{"a.txt": "one", "d/b.txt": "two"}) {
		t.Errorf("volume holds %v", got)
	}
	if !reflect.DeepEqual(e.vol.fixOwner, []bool{false}) {
		t.Errorf("fixOwner = %v: owners of our own backups must be kept", e.vol.fixOwner)
	}
	// The data from before the restore is kept as a backup of its own.
	var safe Backup
	for _, it := range e.list(t).Items {
		if it.Kind == "before_restore" {
			safe = it
		}
	}
	if safe.Status != "ready" {
		t.Fatalf("no before-restore backup: %+v", e.list(t).Items)
	}
	if got := e.readBackup(t, safe); !reflect.DeepEqual(got, map[string]string{"a.txt": "changed", "new.txt": "new"}) {
		t.Errorf("before-restore backup holds %v", got)
	}

	// An uploaded archive gets its owners fixed; the app lock is free again.
	up, err := e.s.UploadBackup(ctx, e.user, e.app.Slug, bytes.NewReader(makeTarGz(t, tent{name: "u.txt", body: "up"})), -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, up.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.waitRestore(t); r.State != "done" {
		t.Fatalf("restore of upload = %+v", r)
	}
	if got := e.vol.snapshot(); !reflect.DeepEqual(got, map[string]string{"u.txt": "up"}) {
		t.Errorf("volume holds %v", got)
	}
	if !reflect.DeepEqual(e.vol.fixOwner, []bool{false, true}) {
		t.Errorf("fixOwner = %v", e.vol.fixOwner)
	}
}

func TestBackupRestoreRefusals(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, "bak_nope"); depCode(err) != "backup_not_found" {
		t.Errorf("unknown: %v", err)
	}
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.RestoreBackup(ctx, other, e.app.Slug, b.ID); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES ('dep_q', ?, 9, 'queued', 'files', 'api', 1)`, e.app.ID)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "app_busy" {
		t.Errorf("queued deployment: %v", err)
	}
	e.exec(t, `DELETE FROM deployments WHERE id = 'dep_q'`)
	if err := os.Remove(e.s.backupPath(e.app.ID, b.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "backup_file_missing" {
		t.Errorf("missing file: %v", err)
	}
	if e.vol.replaceN != 0 {
		t.Error("the volume was touched")
	}
}

func TestBackupRestoreFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("old data is put back", func(t *testing.T) {
		e := newBkEnv(t)
		b := e.makeBackup(t)
		e.vol.files = map[string]string{"now.txt": "now"}
		e.vol.failAt = map[int]bool{1: true}
		if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
			t.Fatal(err)
		}
		r := e.waitRestore(t)
		if r.State != "failed" || !strings.Contains(r.Error, "old data was put back") || strings.Contains(r.Error, "daemon") {
			t.Errorf("restore = %+v", r)
		}
		if got := e.vol.snapshot(); !reflect.DeepEqual(got, map[string]string{"now.txt": "now"}) {
			t.Errorf("volume holds %v", got)
		}
		if e.vol.replaceN != 2 {
			t.Errorf("replace calls = %d", e.vol.replaceN)
		}
	})

	t.Run("old data cannot be put back", func(t *testing.T) {
		e := newBkEnv(t)
		b := e.makeBackup(t)
		e.vol.failAt = map[int]bool{1: true, 2: true}
		if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
			t.Fatal(err)
		}
		if r := e.waitRestore(t); r.State != "failed" || !strings.Contains(r.Error, "before restore") {
			t.Errorf("restore = %+v", r)
		}
	})

	t.Run("safety backup fails first", func(t *testing.T) {
		e := newBkEnv(t)
		b := e.makeBackup(t)
		e.vol.archiveErr = errors.New("boom")
		if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
			t.Fatal(err)
		}
		r := e.waitRestore(t)
		if r.State != "failed" || !strings.Contains(r.Error, "nothing was restored") {
			t.Errorf("restore = %+v", r)
		}
		if e.vol.replaceN != 0 {
			t.Error("the volume was touched")
		}
	})

	t.Run("damaged backup file", func(t *testing.T) {
		e := newBkEnv(t)
		b := e.makeBackup(t)
		if err := os.WriteFile(e.s.backupPath(e.app.ID, b.ID), []byte("garbage"), 0o640); err != nil {
			t.Fatal(err)
		}
		e.vol.files = map[string]string{"keep.txt": "keep"}
		if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
			t.Fatal(err)
		}
		if r := e.waitRestore(t); r.State != "failed" {
			t.Errorf("restore = %+v", r)
		}
		if got := e.vol.snapshot(); !reflect.DeepEqual(got, map[string]string{"keep.txt": "keep"}) {
			t.Errorf("volume holds %v", got)
		}
	})
}

func TestBackupDelete(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if err := e.s.DeleteBackup(ctx, other, e.app.Slug, b.ID); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	if err := e.s.DeleteBackup(ctx, e.user, e.app.Slug, "bak_nope"); depCode(err) != "backup_not_found" {
		t.Errorf("unknown: %v", err)
	}
	if err := e.s.DeleteBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.s.backupPath(e.app.ID, b.ID)); err == nil {
		t.Error("file still there")
	}
	if l := e.list(t); len(l.Items) != 0 {
		t.Errorf("list = %+v", l)
	}
	// Turning /data off deletes the backups, so it needs a confirmation.
	b = e.makeBackup(t)
	off := false
	if _, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off}); depCode(err) != "volume_has_data" {
		t.Fatalf("turning /data off with a backup: %v", err)
	}
	if l := e.list(t); len(l.Items) != 1 {
		t.Errorf("after the refusal: %+v", l)
	}
	if row, _ := e.s.Q.GetApp(ctx, e.app.ID); row.VolumeEnabled == 0 {
		t.Error("the refusal turned /data off anyway")
	}
	yes := true
	if _, err := e.s.UpdateApp(ctx, e.user, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes}); err != nil {
		t.Fatal(err)
	}
	if l := e.list(t); len(l.Items) != 0 {
		t.Errorf("after turning /data off: %+v", l)
	}
	if _, err := os.Stat(e.s.backupPath(e.app.ID, b.ID)); err == nil {
		t.Error("backup file still there")
	}
}

func TestBackupsGoWithTheApp(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	if err := e.s.DeleteApp(ctx, e.user, e.app.Slug, e.app.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.s.backupDir(e.app.ID)); !os.IsNotExist(err) {
		t.Errorf("backup folder still there: %v", err)
	}
	if _, err := e.s.Q.GetBackup(ctx, db.GetBackupParams{ID: b.ID, AppID: e.app.ID}); err == nil {
		t.Error("backup row still there")
	}
}

func TestBackupSettings(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	l := e.list(t)
	if l.Settings.Schedule != "off" || l.Settings.KeepMode != "last" || l.Settings.Keep != 7 {
		t.Errorf("defaults = %+v", l.Settings)
	}
	st, err := e.s.UpdateBackupSettings(ctx, e.user, e.app.Slug, BackupSettingsInput{Schedule: ptr("Daily")})
	if err != nil || st.Schedule != "daily" || st.Keep != 7 || st.NextAt == nil {
		t.Fatalf("daily: %+v, %v", st, err)
	}
	if want := latestSlot("daily", backupMinute(e.app.ID), e.clock); !st.NextAt.Equal(want) { // never ran: due at once
		t.Errorf("next = %v, want %v", st.NextAt, want)
	}
	if st.TimeUTC != backupClock(backupMinute(e.app.ID)) {
		t.Errorf("time_utc = %q", st.TimeUTC)
	}
	st, err = e.s.UpdateBackupSettings(ctx, e.user, e.app.Slug, BackupSettingsInput{KeepMode: ptr("days"), Keep: ptr(int64(30))})
	if err != nil || st.KeepMode != "days" || st.Keep != 30 || st.Schedule != "daily" {
		t.Fatalf("days: %+v, %v", st, err)
	}
	for name, in := range map[string]BackupSettingsInput{
		"schedule":    {Schedule: ptr("hourly")},
		"mode":        {KeepMode: ptr("forever")},
		"zero":        {Keep: ptr(int64(0))},
		"too many":    {Keep: ptr(int64(400))},
		"mode switch": {KeepMode: ptr("last"), Keep: ptr(int64(101))},
	} {
		if _, err := e.s.UpdateBackupSettings(ctx, e.user, e.app.Slug, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if st, err := e.s.UpdateBackupSettings(ctx, e.user, e.app.Slug, BackupSettingsInput{Schedule: ptr("off")}); err != nil || st.NextAt != nil {
		t.Errorf("off: %+v, %v", st, err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.UpdateBackupSettings(ctx, ro, e.app.Slug, BackupSettingsInput{Schedule: ptr("daily")}); depCode(err) != "token_read_only" {
		t.Errorf("read-only: %v", err)
	}
}

func (e *bkEnv) backupsOfKind(t *testing.T, kind string) []Backup {
	t.Helper()
	var out []Backup
	for _, it := range e.list(t).Items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}
	return out
}

func TestScheduledBackups(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily', backup_keep_mode = 'last', backup_keep = 2 WHERE id = ?`, e.app.ID)

	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 1 || got[0].Status != "ready" {
		t.Fatalf("first run: %+v", got)
	}
	// Not due again until the app's next backup time.
	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 1 {
		t.Fatalf("second run made another: %+v", got)
	}
	manual := e.makeBackup(t)
	for day := 1; day <= 3; day++ {
		e.clock = e.clock.Add(24 * time.Hour)
		e.runDue(ctx)
	}
	got := e.backupsOfKind(t, "scheduled")
	if len(got) != 2 {
		t.Fatalf("after 3 more days (keep 2): %+v", got)
	}
	if !got[0].CreatedAt.After(got[1].CreatedAt) || got[0].CreatedAt.Before(e.clock.Add(-24*time.Hour)) {
		t.Errorf("kept the wrong ones: %+v", got)
	}
	// Retention leaves manual backups alone.
	if m := e.backupsOfKind(t, "manual"); len(m) != 1 || m[0].ID != manual.ID {
		t.Errorf("manual backups = %+v", m)
	}
	for _, it := range got {
		if _, err := os.Stat(e.s.backupPath(e.app.ID, it.ID)); err != nil {
			t.Errorf("file of a kept backup: %v", err)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(e.s.backupDir(e.app.ID), "*.tar.gz")); len(entries) != 3 {
		t.Errorf("files on disk = %v", entries)
	}
}

func TestScheduledBackupsSkipAndReport(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily' WHERE id = ?`, e.app.ID)

	// No room: skipped, with the reason, and not retried every ten minutes.
	e.exec(t, `UPDATE users SET quota_volume_mb = 100 WHERE id = ?`, e.user.UserID)
	e.runDue(ctx)
	e.runDue(ctx)
	got := e.backupsOfKind(t, "scheduled")
	if len(got) != 1 || got[0].Status != "failed" || !strings.Contains(got[0].Error, "no storage left") {
		t.Fatalf("skipped run: %+v", got)
	}
	// The next night tries again.
	e.exec(t, `UPDATE users SET quota_volume_mb = 25600 WHERE id = ?`, e.user.UserID)
	e.clock = e.clock.Add(24 * time.Hour)
	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 2 || got[0].Status != "ready" {
		t.Fatalf("next night: %+v", got)
	}

	// No /data, no deployment or a suspended owner: nothing happens, nothing is reported.
	for _, q := range []string{
		`UPDATE apps SET volume_enabled = 0 WHERE id = ?`,
		`UPDATE apps SET volume_enabled = 1, current_deployment_id = NULL WHERE id = ?`,
	} {
		e.exec(t, q, e.app.ID)
		e.clock = e.clock.Add(24 * time.Hour)
		e.runDue(ctx)
		if got := e.backupsOfKind(t, "scheduled"); len(got) != 2 {
			t.Fatalf("%s: %+v", q, got)
		}
	}
	e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_seed' WHERE id = ?`, e.app.ID)
	e.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, e.user.UserID)
	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 2 {
		t.Fatalf("suspended: %+v", got)
	}
	e.exec(t, `UPDATE users SET status = 'active' WHERE id = ?`, e.user.UserID)

	// A volume that does not exist yet is not an error worth showing.
	e.vol.noVolume = true
	e.clock = e.clock.Add(24 * time.Hour)
	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 2 {
		t.Fatalf("no volume: %+v", got)
	}
	// An error is shown, in plain words.
	e.vol.noVolume = false
	e.vol.archiveErr = errors.New("Error response from daemon: boom")
	e.clock = e.clock.Add(24 * time.Hour)
	e.runDue(ctx)
	if got := e.backupsOfKind(t, "scheduled"); len(got) != 3 || got[0].Status != "failed" || !strings.Contains(got[0].Error, "boom") {
		t.Fatalf("daemon error: %+v", got)
	}
}

// A scheduled backup without room deletes the app's oldest automatic backups
// first, but never the newest one, nor manual backups.
func TestScheduledBackupMakesRoom(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily' WHERE id = ?`, e.app.ID)
	var auto []string
	for i := 3; i >= 1; i-- { // 3, 2 and 1 days old, 1 MB each
		b := e.makeBackup(t)
		e.exec(t, `UPDATE app_backups SET kind = 'scheduled', size_bytes = ?, created_at = ? WHERE id = ?`, 1<<20, e.clock.Unix()-int64(i)*86400, b.ID)
		auto = append(auto, b.ID)
	}
	manual := e.makeBackup(t)
	e.exec(t, `UPDATE app_backups SET size_bytes = ?, created_at = ? WHERE id = ?`, 1<<20, e.clock.Unix()-10*86400, manual.ID)
	// 100 MB reserved for /data + 4 MB of backups: no room left.
	e.exec(t, `UPDATE users SET quota_volume_mb = 104 WHERE id = ?`, e.user.UserID)

	e.runDue(ctx)
	got := e.backupsOfKind(t, "scheduled")
	if len(got) != 3 || got[0].Status != "ready" {
		t.Fatalf("scheduled after the run: %+v", got)
	}
	left := map[string]bool{}
	for _, it := range got {
		left[it.ID] = true
	}
	if left[auto[0]] || !left[auto[1]] || !left[auto[2]] {
		t.Errorf("deleted the wrong ones: %v (made %v)", left, auto)
	}
	if m := e.backupsOfKind(t, "manual"); len(m) != 1 {
		t.Errorf("manual backups = %+v", m)
	}

	// Only manual backups and the newest automatic one left: skipped, nothing deleted.
	e.exec(t, `DELETE FROM app_backups WHERE id IN (?, ?)`, got[1].ID, got[2].ID)
	e.exec(t, `UPDATE app_backups SET size_bytes = ? WHERE id = ?`, 4<<20, manual.ID)
	e.clock = e.clock.Add(24 * time.Hour)
	e.runDue(ctx)
	got = e.backupsOfKind(t, "scheduled")
	if len(got) != 2 || got[0].Status != "failed" || !strings.Contains(got[0].Error, "no storage left") || got[1].Status != "ready" {
		t.Fatalf("no room even after pruning: %+v", got)
	}
}

// Automatic backups do not stop a build; manual ones do.
func TestStorageRoomIgnoresScheduledBackups(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	e.exec(t, `UPDATE app_backups SET kind = 'scheduled', size_bytes = ? WHERE id = ?`, 5<<20, b.ID)
	e.exec(t, `UPDATE users SET quota_volume_mb = 103 WHERE id = ?`, e.user.UserID)
	u, err := e.s.Q.GetUser(ctx, e.user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.checkStorageRoom(ctx, u); err != nil {
		t.Errorf("automatic backups blocked a build: %v", err)
	}
	if taken, _ := e.s.storageTaken(ctx, e.user.UserID, ""); taken != 105 {
		t.Errorf("storageTaken = %d, want the automatic backup counted for backups", taken)
	}
	e.exec(t, `UPDATE app_backups SET kind = 'manual' WHERE id = ?`, b.ID)
	if err := e.s.checkStorageRoom(ctx, u); depCode(err) != "storage_full" {
		t.Errorf("manual backups: %v", err)
	}
}

func TestRetentionByDays(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.exec(t, `UPDATE apps SET backup_keep_mode = 'days', backup_keep = 3 WHERE id = ?`, e.app.ID)
	var ids []string
	for i := 0; i < 5; i++ {
		b := e.makeBackup(t)
		age := int64(i) * 2 * 86400 // 0, 2, 4, 6, 8 days old
		e.exec(t, `UPDATE app_backups SET kind = 'scheduled', created_at = ? WHERE id = ?`, e.clock.Unix()-age, b.ID)
		ids = append(ids, b.ID)
	}
	e.exec(t, `UPDATE app_backups SET kind = 'manual' WHERE id = ?`, ids[4]) // old, but manual
	e.exec(t, `INSERT INTO app_backups (id, app_id, kind, status, error, created_at) VALUES ('bak_old', ?, 'scheduled', 'failed', 'x', ?)`, e.app.ID, e.clock.Unix()-30*86400)
	e.runDue(ctx)
	var left []string
	for _, it := range e.list(t).Items {
		left = append(left, it.ID)
	}
	want := map[string]bool{ids[0]: true, ids[1]: true, ids[4]: true}
	if len(left) != len(want) {
		t.Fatalf("left %v", left)
	}
	for _, id := range left {
		if !want[id] {
			t.Errorf("%s was kept", id)
		}
	}
}

func TestStaleBackupsAreCleaned(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.exec(t, `INSERT INTO app_backups (id, app_id, kind, status, created_at) VALUES ('bak_stuck', ?, 'manual', 'running', 1)`, e.app.ID)
	if err := os.MkdirAll(e.s.backupDir(e.app.ID), 0o750); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(e.s.backupDir(e.app.ID), "bak_stuck.tar.gz.tmp")
	if err := os.WriteFile(tmp, []byte("half"), 0o640); err != nil {
		t.Fatal(err)
	}
	e.s.recoverAfterCrash(ctx)
	l := e.list(t)
	if len(l.Items) != 1 || l.Items[0].Status != "failed" || !strings.Contains(l.Items[0].Error, "restarted") {
		t.Errorf("list = %+v", l)
	}
	if _, err := os.Stat(tmp); err == nil {
		t.Error("temp file still there")
	}
}

// multiVolumes turns the test app into one with two services, each with its
// own volume.
func (e *bkEnv) multiVolumes(t *testing.T) {
	t.Helper()
	e.makeMulti(t, e.app.ID, "web", "db")
	e.exec(t, `DELETE FROM app_volumes WHERE app_id = ?`, e.app.ID)
	for _, v := range []db.InsertAppVolumeParams{
		{AppID: e.app.ID, Name: "uploads", VolumeName: "bp-v-up", SizeMb: 50, Service: "web", MountPath: "/uploads", CreatedAt: 1},
		{AppID: e.app.ID, Name: "pgdata", VolumeName: "bp-v-pg", SizeMb: 50, Service: "db", MountPath: "/var/lib/postgresql/data", CreatedAt: 2},
	} {
		if err := e.s.Q.InsertAppVolume(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
}

// A manual backup without a volume covers every volume of the app, like the
// scheduled one; volume= picks one.
func TestBackupEveryVolume(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	e.multiVolumes(t)

	bs, err := e.s.CreateBackupAll(ctx, e.user, e.app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 || bs[0].Volume != "pgdata" || bs[1].Volume != "uploads" {
		t.Fatalf("backups = %+v", bs)
	}
	e.wait()
	manual := e.backupsOfKind(t, "manual")
	if len(manual) != 2 {
		t.Fatalf("manual backups = %+v", manual)
	}
	for _, it := range manual {
		if it.Status != "ready" {
			t.Errorf("backup %+v", it)
		}
	}

	one, err := e.s.CreateBackup(ctx, e.user, e.app.Slug, "pgdata")
	if err != nil || one.Volume != "pgdata" {
		t.Fatalf("one volume: %+v %v", one, err)
	}
	e.wait()
	if _, err := e.s.CreateBackup(ctx, e.user, e.app.Slug, "nope"); depCode(err) != "volume_not_found" {
		t.Errorf("unknown volume: %v", err)
	}

	e.exec(t, `UPDATE apps SET backup_schedule = 'daily', backup_keep_mode = 'last', backup_keep = 5 WHERE id = ?`, e.app.ID)
	e.runDue(ctx)
	got := e.backupsOfKind(t, "scheduled")
	ready := map[string]bool{}
	for _, it := range got {
		ready[it.Volume] = it.Status == "ready"
	}
	if len(got) != 2 || !ready["uploads"] || !ready["pgdata"] {
		t.Errorf("scheduled = %+v", got)
	}
}

// A single app has one volume: CreateBackupAll makes one backup of /data.
func TestBackupAllSingle(t *testing.T) {
	e := newBkEnv(t)
	bs, err := e.s.CreateBackupAll(context.Background(), e.user, e.app.Slug)
	e.wait()
	if err != nil || len(bs) != 1 || bs[0].Volume != "" {
		t.Fatalf("single app: %+v %v", bs, err)
	}
}
