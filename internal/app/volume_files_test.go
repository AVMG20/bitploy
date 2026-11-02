package app

import (
	"bytes"
	"context"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

// fsVol is a fakeVol that also keeps files in a tree: path -> content, and a
// folder is a path with a nil content. The empty path is /data.
type fsVol struct {
	*fakeVol
	fmu   sync.Mutex
	tree  map[string][]byte
	calls []string
	hold  chan struct{} // PutFile waits for it to be closed
}

func newFsVol() *fsVol {
	return &fsVol{fakeVol: &fakeVol{}, tree: map[string][]byte{
		"":             nil,
		"config.json":  []byte(`{"a":1}`),
		"data":         nil,
		"data/app.db":  []byte("sqlite"),
		"data/z.txt":   []byte("z"),
		"data/Archive": nil,
	}}
}

func (f *fsVol) kind(p string) string {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	return f.kindLocked(p)
}

func (f *fsVol) kindLocked(p string) string {
	b, ok := f.tree[p]
	switch {
	case !ok:
		return ""
	case b == nil:
		return "dir"
	}
	return "file"
}

func (f *fsVol) ListFiles(_ context.Context, _, dir string, max int) (dockerx.DirListing, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.calls = append(f.calls, "list:"+dir)
	if f.kindLocked(dir) == "" {
		return dockerx.DirListing{}, dockerx.ErrNotFound
	}
	if f.kindLocked(dir) == "file" {
		return dockerx.DirListing{}, dockerx.ErrNotDir
	}
	var l dockerx.DirListing
	for p, b := range f.tree {
		if p == "" {
			continue
		}
		parent, name := "", p
		if i := strings.LastIndex(p, "/"); i >= 0 {
			parent, name = p[:i], p[i+1:]
		}
		if parent != dir {
			continue
		}
		e := dockerx.FileEntry{Name: name, Kind: "dir", Mtime: time.Unix(1700000000, 0)}
		if b != nil {
			e.Kind, e.Size = "file", int64(len(b))
		}
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if (a.Kind == "dir") != (b.Kind == "dir") {
			return a.Kind == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	if len(l.Entries) > max {
		l.Entries, l.Truncated = l.Entries[:max], true
	}
	return l, nil
}

func (f *fsVol) OpenFile(_ context.Context, _, rel string) (io.ReadCloser, int64, time.Time, error) {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.calls = append(f.calls, "open:"+rel)
	switch f.kindLocked(rel) {
	case "":
		return nil, 0, time.Time{}, dockerx.ErrNotFound
	case "dir":
		return nil, 0, time.Time{}, dockerx.ErrWrongKind
	}
	b := f.tree[rel]
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), time.Unix(1700000000, 0), nil
}

func (f *fsVol) PutFile(_ context.Context, _, dir, name string, size int64, body io.Reader, overwrite bool) error {
	if f.hold != nil {
		<-f.hold
	}
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.calls = append(f.calls, "put:"+dir+"|"+name)
	switch f.kindLocked(dir) {
	case "":
		return dockerx.ErrNotFound
	case "file":
		return dockerx.ErrNotDir
	}
	p := name
	if dir != "" {
		p = dir + "/" + name
	}
	if k := f.kindLocked(p); k != "" && !overwrite {
		return dockerx.ErrExists
	} else if k == "dir" {
		return dockerx.ErrWrongKind
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return io.ErrUnexpectedEOF
	}
	f.tree[p] = append([]byte{}, b...)
	return nil
}

func (f *fsVol) MakeDir(_ context.Context, _, dir, name string) error {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.calls = append(f.calls, "mkdir:"+dir+"|"+name)
	if f.kindLocked(dir) != "dir" {
		return dockerx.ErrNotFound
	}
	p := name
	if dir != "" {
		p = dir + "/" + name
	}
	if f.kindLocked(p) != "" {
		return dockerx.ErrExists
	}
	f.tree[p] = nil
	return nil
}

func (f *fsVol) RemoveFile(_ context.Context, _, dir, name string) error {
	f.fmu.Lock()
	defer f.fmu.Unlock()
	f.calls = append(f.calls, "rm:"+dir+"|"+name)
	p := name
	if dir != "" {
		p = dir + "/" + name
	}
	if f.kindLocked(p) == "" {
		return dockerx.ErrNotFound
	}
	for k := range f.tree {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(f.tree, k)
		}
	}
	return nil
}

func newFilesEnv(t *testing.T) (*bkEnv, *fsVol) {
	t.Helper()
	e := newBkEnv(t)
	fv := newFsVol()
	e.s.backup.vol = fv
	return e, fv
}

func TestCleanFilePath(t *testing.T) {
	ok := map[string]string{
		"":          "",
		"/":         "",
		".":         "",
		"a":         "a",
		"/a/b":      "a/b",
		"a//b/":     "a/b",
		"./a/./b":   "a/b",
		"a b/ü.txt": "a b/ü.txt",
		"a\\b":      "a\\b",
		"/data/a":   "data/a",
	}
	for in, want := range ok {
		got, err := cleanFilePath(in)
		if err != nil || got != want {
			t.Errorf("cleanFilePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"..", "../a", "a/..", "a/../b", "/../etc", "a\x00b", "a\nb", "a\tb", "a\x7fb", "\xff\xfe",
		strings.Repeat("a", 256), strings.Repeat("a/", 3000)}
	for _, in := range bad {
		if got, err := cleanFilePath(in); err == nil || depCode(err) != "path_invalid" {
			t.Errorf("cleanFilePath(%q) = %q, %v; want path_invalid", in, got, err)
		}
	}
}

func TestValidFileName(t *testing.T) {
	for _, n := range []string{"a", "data.json", "my file.txt", ".env", "ü", strings.Repeat("a", 255)} {
		if err := validFileName(n); err != nil {
			t.Errorf("validFileName(%q) = %v", n, err)
		}
	}
	for n, code := range map[string]string{
		"": "name_missing", ".": "name_invalid", "..": "name_invalid", "a/b": "name_invalid", "/": "name_invalid",
		"a\x00": "name_invalid", "a\n": "name_invalid", strings.Repeat("a", 256): "name_invalid", "\xff": "name_invalid",
	} {
		if err := validFileName(n); depCode(err) != code {
			t.Errorf("validFileName(%q) = %v; want %s", n, err, code)
		}
	}
}

func TestFilesList(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	l, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "/data/")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != "data" || len(l.Items) != 3 || l.Items[0].Name != "Archive" || l.Items[0].Type != "dir" ||
		l.Items[1].Name != "app.db" || l.Items[1].Type != "file" || l.Items[1].Size != 6 || l.Truncated {
		t.Fatalf("list = %+v", l)
	}
	if l.UploadMaxBytes != maxFileUpload {
		t.Errorf("upload max = %d", l.UploadMaxBytes)
	}
	root, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "")
	if err != nil || root.Path != "" || len(root.Items) != 2 {
		t.Fatalf("root = %+v %v", root, err)
	}
	// What reaches the store is the cleaned path.
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "//data/./"); err != nil {
		t.Fatal(err)
	}
	if last := fv.calls[len(fv.calls)-1]; last != "list:data" {
		t.Errorf("store got %q", last)
	}
	n := len(fv.calls)
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "../x"); depCode(err) != "path_invalid" || len(fv.calls) != n {
		t.Errorf("dotdot: %v (store calls %d)", err, len(fv.calls)-n)
	}
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "nope"); depCode(err) != "file_not_found" {
		t.Errorf("missing: %v", err)
	}
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "config.json"); depCode(err) != "not_a_folder" {
		t.Errorf("file: %v", err)
	}
}

func TestFilesUploadLimitFollowsFreeRoom(t *testing.T) {
	e, _ := newFilesEnv(t)
	ctx := context.Background()
	// 100 MB limit, 98 MB measured: 2 MB are left.
	e.exec(t, `INSERT INTO app_disk (app_id, image_bytes, volume_bytes, measured_at, source_bytes) VALUES (?, 0, ?, 1, 0)`, e.app.ID, int64(98<<20))
	l, err := e.s.ListFiles(ctx, e.user, e.app.Slug, "")
	if err != nil || l.UploadMaxBytes != 2<<20 {
		t.Fatalf("limit = %d, %v", l.UploadMaxBytes, err)
	}
	big := bytes.NewReader(make([]byte, 3<<20))
	if _, err := e.s.UploadFile(ctx, e.user, e.app.Slug, "", "big.bin", false, big, int64(big.Len())); depCode(err) != "volume_full" {
		t.Errorf("announced too big: %v", err)
	}
	// Announces nothing (-1) but sends too much.
	if _, err := e.s.UploadFile(ctx, e.user, e.app.Slug, "", "big.bin", false, bytes.NewReader(make([]byte, 3<<20)), -1); depCode(err) != "volume_full" {
		t.Errorf("sent too much: %v", err)
	}
	if _, err := e.s.UploadFile(ctx, e.user, e.app.Slug, "", "ok.bin", false, bytes.NewReader(make([]byte, 1<<20)), 1<<20); err != nil {
		t.Errorf("fits: %v", err)
	}
	// Full volume.
	e.exec(t, `UPDATE app_disk SET volume_bytes = ? WHERE app_id = ?`, int64(100<<20), e.app.ID)
	if _, err := e.s.UploadFile(ctx, e.user, e.app.Slug, "", "x", false, strings.NewReader("x"), 1); depCode(err) != "volume_full" {
		t.Errorf("full: %v", err)
	}
}

func TestFilesUploadMaxSize(t *testing.T) {
	e, _ := newFilesEnv(t)
	e.exec(t, `UPDATE apps SET volume_mb = 1000 WHERE id = ?`, e.app.ID)
	over := int64(maxFileUpload + 1)
	if _, err := e.s.UploadFile(context.Background(), e.user, e.app.Slug, "", "x", false, strings.NewReader("x"), over); depCode(err) != "file_too_large" {
		t.Errorf("announced: %v", err)
	}
	r := io.LimitReader(fileZeros{}, over)
	if _, err := e.s.UploadFile(context.Background(), e.user, e.app.Slug, "", "x", false, r, -1); depCode(err) != "file_too_large" {
		t.Errorf("streamed: %v", err)
	}
}

type fileZeros struct{}

func (fileZeros) Read(p []byte) (int, error) { return len(p), nil }

func TestFilesUploadOverwrite(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	put := func(dir, name, body string, ow bool) error {
		_, err := e.s.UploadFile(ctx, e.user, e.app.Slug, dir, name, ow, strings.NewReader(body), int64(len(body)))
		return err
	}
	if err := put("data", "new.json", "[1]", false); err != nil {
		t.Fatal(err)
	}
	if string(fv.tree["data/new.json"]) != "[1]" {
		t.Errorf("tree = %v", fv.tree)
	}
	if err := put("data", "new.json", "[2]", false); depCode(err) != "file_exists" {
		t.Errorf("no overwrite: %v", err)
	}
	if string(fv.tree["data/new.json"]) != "[1]" {
		t.Error("file replaced without overwrite")
	}
	if err := put("data", "new.json", "[2]", true); err != nil {
		t.Fatal(err)
	}
	if string(fv.tree["data/new.json"]) != "[2]" {
		t.Error("file not replaced")
	}
	if err := put("nope", "a", "x", false); depCode(err) != "file_not_found" {
		t.Errorf("missing folder: %v", err)
	}
	for _, name := range []string{"", "..", "a/b", "x\n"} {
		if err := put("data", name, "x", false); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if err := put("../etc", "a", "x", false); depCode(err) != "path_invalid" {
		t.Errorf("dotdot: %v", err)
	}
	for _, c := range fv.calls {
		if strings.Contains(c, "..") {
			t.Errorf("store got %q", c)
		}
	}
	if err := put("", "root.txt", "r", false); err != nil || string(fv.tree["root.txt"]) != "r" {
		t.Errorf("root upload: %v", err)
	}
}

func TestFilesDownload(t *testing.T) {
	e, _ := newFilesEnv(t)
	ctx := context.Background()
	d, err := e.s.OpenFile(ctx, e.user, e.app.Slug, "/data/app.db")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(d.Body)
	d.Body.Close()
	if string(b) != "sqlite" || d.Name != "app.db" || d.Size != 6 {
		t.Errorf("download = %q %+v", b, d)
	}
	if _, err := e.s.OpenFile(ctx, e.user, e.app.Slug, "data"); depCode(err) != "not_a_file" {
		t.Errorf("folder: %v", err)
	}
	if _, err := e.s.OpenFile(ctx, e.user, e.app.Slug, ""); depCode(err) != "not_a_file" {
		t.Errorf("root: %v", err)
	}
	if _, err := e.s.OpenFile(ctx, e.user, e.app.Slug, "nope"); depCode(err) != "file_not_found" {
		t.Errorf("missing: %v", err)
	}
	if _, err := e.s.OpenFile(ctx, e.user, e.app.Slug, "data/../../etc/passwd"); depCode(err) != "path_invalid" {
		t.Errorf("dotdot: %v", err)
	}
}

func TestFilesFolderAndDelete(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	if _, err := e.s.MakeFolder(ctx, e.user, e.app.Slug, "data", "new"); err != nil {
		t.Fatal(err)
	}
	if fv.kind("data/new") != "dir" {
		t.Error("folder not made")
	}
	if _, err := e.s.MakeFolder(ctx, e.user, e.app.Slug, "data", "new"); depCode(err) != "file_exists" {
		t.Errorf("twice: %v", err)
	}
	if _, err := e.s.MakeFolder(ctx, e.user, e.app.Slug, "data", "a/b"); depCode(err) != "name_invalid" {
		t.Errorf("slash: %v", err)
	}
	if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, "data/app.db"); err != nil {
		t.Fatal(err)
	}
	if fv.kind("data/app.db") != "" {
		t.Error("file not deleted")
	}
	if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, "/data/"); err != nil {
		t.Fatal(err)
	}
	if fv.kind("data") != "" || fv.kind("data/z.txt") != "" || fv.kind("config.json") != "file" {
		t.Errorf("tree after folder delete: %v", fv.tree)
	}
	if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, "data"); depCode(err) != "file_not_found" {
		t.Errorf("twice: %v", err)
	}
	// /data itself is never deleted, however it is written.
	n := len(fv.calls)
	for _, p := range []string{"", "/", ".", "./", "a/..", "/data/../"} {
		if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, p); err == nil {
			t.Errorf("delete %q accepted", p)
		}
	}
	for _, c := range fv.calls[n:] {
		if strings.HasPrefix(c, "rm:") {
			t.Errorf("store got %q", c)
		}
	}
	if fv.kind("") != "dir" {
		t.Error("/data is gone")
	}
}

func TestFilesAuthAndState(t *testing.T) {
	e, _ := newFilesEnv(t)
	ctx := context.Background()
	slug := e.app.Slug

	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.ListFiles(ctx, ro, slug, ""); err != nil {
		t.Errorf("read token list: %v", err)
	}
	if _, err := e.s.OpenFile(ctx, ro, slug, "config.json"); depCode(err) != "token_read_only" {
		t.Errorf("read token download: %v", err)
	}
	if _, err := e.s.UploadFile(ctx, ro, slug, "", "x", false, strings.NewReader("x"), 1); depCode(err) != "token_read_only" {
		t.Errorf("read token upload: %v", err)
	}
	if _, err := e.s.MakeFolder(ctx, ro, slug, "", "x"); depCode(err) != "token_read_only" {
		t.Errorf("read token mkdir: %v", err)
	}
	if err := e.s.DeleteFile(ctx, ro, slug, "config.json"); depCode(err) != "token_read_only" {
		t.Errorf("read token delete: %v", err)
	}

	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.ListFiles(ctx, other, slug, ""); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	// An admin session sees apps of others, but not their files.
	admin := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	admin.Scope, admin.Channel = ScopeSession, ChannelDashboard
	if _, err := e.s.ListFiles(ctx, admin, slug, ""); depCode(err) != "app_not_found" {
		t.Errorf("admin: %v", err)
	}

	// Suspended owner: no changes.
	e.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, e.user.UserID)
	if err := e.s.DeleteFile(ctx, e.user, slug, "config.json"); depCode(err) != "account_suspended" {
		t.Errorf("suspended: %v", err)
	}
	e.exec(t, `UPDATE users SET status = 'active' WHERE id = ?`, e.user.UserID)

	// A plain fakeVol cannot browse files.
	e.s.backup.vol = e.vol
	if _, err := e.s.ListFiles(ctx, e.user, slug, ""); depCode(err) != "docker_unavailable" {
		t.Errorf("no file store: %v", err)
	}
}

func TestFilesVolumeOffAndNotReady(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	fv.noVolume = true
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, ""); depCode(err) != "volume_not_ready" {
		t.Errorf("no volume yet: %v", err)
	}
	fv.noVolume = false
	e.exec(t, `UPDATE apps SET volume_enabled = 0 WHERE id = ?`, e.app.ID)
	if _, err := e.s.ListFiles(ctx, e.user, e.app.Slug, ""); depCode(err) != "volume_off" {
		t.Errorf("volume off: %v", err)
	}
	if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, "config.json"); depCode(err) != "volume_off" {
		t.Errorf("volume off delete: %v", err)
	}
}

func TestFilesChangesWaitForBackupsAndDeploys(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	slug := e.app.Slug

	// A backup or restore runs: no change, and the reason is plain.
	lock := e.s.backupLock(e.app.ID)
	lock.Lock()
	if err := e.s.DeleteFile(ctx, e.user, slug, "config.json"); depCode(err) != "files_busy" {
		t.Errorf("backup running: %v", err)
	}
	if _, err := e.s.UploadFile(ctx, e.user, slug, "", "x", false, strings.NewReader("x"), 1); depCode(err) != "files_busy" {
		t.Errorf("backup running, upload: %v", err)
	}
	lock.Unlock()

	// A deployment is about to swap containers.
	e.s.deployRT.mu.Lock()
	e.s.deployRT.init()
	e.s.deployRT.busy[e.app.ID] = "dep_x"
	e.s.deployRT.mu.Unlock()
	if _, err := e.s.MakeFolder(ctx, e.user, slug, "", "x"); depCode(err) != "app_busy" {
		t.Errorf("deploying: %v", err)
	}
	// Reading is fine meanwhile.
	if _, err := e.s.ListFiles(ctx, e.user, slug, ""); err != nil {
		t.Errorf("list while deploying: %v", err)
	}
	if fv.kind("x") != "" {
		t.Error("folder made while deploying")
	}
}

func TestFilesRefusedWhileRestoring(t *testing.T) {
	e, _ := newFilesEnv(t)
	e.s.backup.mu.Lock()
	e.s.backup.restore = map[string]*BackupRestore{e.app.ID: {State: "running"}}
	e.s.backup.mu.Unlock()
	if _, err := e.s.ListFiles(context.Background(), e.user, e.app.Slug, ""); depCode(err) != "backup_busy" {
		t.Errorf("restoring: %v", err)
	}
}

func TestFilesBackupLockedWhileUploading(t *testing.T) {
	e, fv := newFilesEnv(t)
	fv.hold = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.s.UploadFile(context.Background(), e.user, e.app.Slug, "", "slow.bin", false, strings.NewReader("abc"), 3)
		done <- err
	}()
	// The upload is inside the store now: a backup can not start.
	deadline := time.Now().Add(5 * time.Second)
	for !e.s.backupBusy(e.app.ID) {
		if time.Now().After(deadline) {
			t.Fatal("upload never took the lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug); depCode(err) != "backup_busy" {
		t.Errorf("backup during upload: %v", err)
	}
	close(fv.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.s.backupBusy(e.app.ID) {
		t.Error("lock still held")
	}
}
