//go:build integration

package dockerx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/AVMG20/bitploy/internal/ids"

	_ "modernc.org/sqlite"
)

// Needs a Docker daemon and Go (it builds the helper binary for the daemon's
// architecture). Takes about 30 seconds, 15 of them for the timeout.
// Run: go test -tags=integration -count=1 -run DBHelper ./internal/dockerx/
func TestDBHelperIntegration(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx)
	if err != nil {
		t.Skip("no docker: ", err)
	}
	defer c.Close()
	c.Instance = "test-" + strings.ToLower(ids.Hex(5))

	// Without a Linux binary there is a clear error (not on Linux: the test binary itself is one).
	if runtime.GOOS != "linux" {
		if _, err := c.RunDBHelper(ctx, "bp-test-nope-data", []byte(`{}`)); !errors.Is(err, ErrNoVolume) {
			t.Errorf("missing volume: %v", err)
		}
	}

	// The helper binary, for the architecture of the daemon.
	info, err := c.API.Info(ctx, client.InfoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	goarch := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[info.Info.Architecture]
	if goarch == "" {
		t.Skip("unknown docker architecture ", info.Info.Architecture)
	}
	bin := filepath.Join(t.TempDir(), "bitploy-db-helper")
	build := exec.Command("go", "build", "-trimpath", "-o", bin, "../../cmd/bitploy")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the helper binary: %v\n%s", err, out)
	}
	c.DBHelperBin = bin
	notLinux := &Client{API: c.API, Instance: c.Instance, DBHelperBin: filepath.Join(t.TempDir(), "missing")}

	vol := "bp-test-db-" + strings.ToLower(ids.Hex(5)) + "-data"
	if _, err := c.EnsureVolume(ctx, vol, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.RemoveVolume(context.Background(), vol)
		_ = c.RemoveImage(context.Background(), DBHelperImage+":"+c.dbh.tag)
	})
	if _, err := notLinux.RunDBHelper(ctx, vol, []byte(`{}`)); !errors.Is(err, ErrNoDBHelper) {
		t.Errorf("no binary: %v", err)
	}

	// Databases made here and put in the volume: a WAL one owned by the app's
	// user (1000), a rollback-journal one owned by root, and one that is none.
	dir := t.TempDir()
	mk := func(name string, wal bool, stmts ...string) {
		db, err := sql.Open("sqlite", filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if wal {
			if _, err := db.Exec(`PRAGMA journal_mode = wal`); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range stmts {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("app.db", true, `CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT)`, `INSERT INTO notes (title, body) VALUES ('one', 'first')`)
	mk("wal.db", true, `CREATE TABLE t (a)`, `INSERT INTO t VALUES (1)`)
	mk("rootowned.db", false, `CREATE TABLE t (a)`, `INSERT INTO t VALUES (1)`)
	mk("big.db", false, `CREATE TABLE big (id INTEGER PRIMARY KEY, v TEXT)`,
		`WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < 100) INSERT INTO big SELECT i, hex(zeroblob(30000)) FROM c`)
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	var putIn func(folder, name string)
	put := func(name string) { putIn("", name) }
	putIn = func(folder, name string) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.PutVolumeFile(ctx, vol, folder, name, int64(len(b)), bytes.NewReader(b), true); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"app.db", "rootowned.db", "big.db", "plain.txt"} {
		put(n)
	}
	// A database of the app's user in a folder of root: the app itself could not
	// use it there (no -shm), so the helper, which becomes that user, can not either.
	if err := c.runHelper(ctx, vol, []string{"sh", "-c", "mkdir /data/locked && chmod 755 /data/locked"}); err != nil {
		t.Fatal(err)
	}
	putIn("locked", "wal.db")
	// /data belongs to the app's user, so does app.db; the other file stays root's.
	seed := `chown 1000:1000 /data /data/app.db /data/big.db /data/locked/wal.db && chmod 644 /data/locked/wal.db && chmod 755 /data && chmod 644 /data/app.db /data/big.db && chmod 666 /data/rootowned.db &&
		ln -s app.db /data/link.db && mkdir /data/d && ln -s /data /data/d/up && ln -s /etc/passwd /data/passwd.db`
	if err := c.runHelper(ctx, vol, []string{"sh", "-c", seed}); err != nil {
		t.Fatal(err)
	}

	run := func(req dbview.Request) dbview.Response {
		t.Helper()
		b, _ := json.Marshal(req)
		out, err := c.RunDBHelper(ctx, vol, b)
		if err != nil {
			t.Fatalf("%s %s: %v", req.Op, req.Path, err)
		}
		var resp dbview.Response
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("%s: bad answer (%d bytes): %v\n%.200s", req.Op, len(out), err, out)
		}
		return resp
	}

	// Reading, as the owner of the file.
	start := time.Now()
	r := run(dbview.Request{Op: dbview.OpInfo, Path: "app.db"})
	if r.Error != nil || !strings.Contains(string(r.Result), `"journal_mode":"wal"`) || !strings.Contains(string(r.Result), `"name":"notes"`) {
		t.Fatalf("info: %+v %s", r.Error, r.Result)
	}
	t.Logf("first call (builds the image): %v", time.Since(start))
	if r := run(dbview.Request{Op: dbview.OpRows, Path: "app.db", Table: "notes"}); r.Error != nil || !strings.Contains(string(r.Result), `"first"`) {
		t.Fatalf("rows: %+v", r.Error)
	}
	// Writing: the files stay the app's. No file of root appears next to the database.
	if r := run(dbview.Request{Op: dbview.OpInsert, Path: "app.db", Table: "notes", Values: map[string]json.RawMessage{"title": json.RawMessage(`"two"`)}}); r.Error != nil {
		t.Fatalf("insert: %+v", r.Error)
	}
	if r := run(dbview.Request{Op: dbview.OpSQL, Path: "app.db", Write: true, SQL: "UPDATE notes SET body = 'x' WHERE id = 2; SELECT count(*) FROM notes"}); r.Error != nil || !strings.Contains(string(r.Result), `"rows_affected":1`) {
		t.Fatalf("sql: %+v %s", r.Error, r.Result)
	}
	out := inspect(t, c, vol, `stat -c '%n %u:%g' /data/app.db*; find /data -user 0 -not -path /data/rootowned.db -not -path /data/plain.txt -not -path /data/link.db -not -path /data/passwd.db -not -path /data/d -not -path /data/d/up | head`)
	if !strings.Contains(out, "/data/app.db 1000:1000") {
		t.Errorf("app.db changed owner:\n%s", out)
	}
	if strings.Contains(out, "root") || strings.Count(out, "\n") > 3 {
		t.Errorf("the helper left files of root behind:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "/data/") && strings.Contains(line, " 0:0") && !strings.Contains(line, "rootowned") && !strings.Contains(line, "plain") {
			t.Errorf("file of root: %s", line)
		}
	}
	// A file of root is opened as root, and is still readable by everyone afterwards.
	if r := run(dbview.Request{Op: dbview.OpSQL, Path: "rootowned.db", Write: true, SQL: "INSERT INTO t VALUES (2)"}); r.Error != nil {
		t.Fatalf("root-owned: %+v", r.Error)
	}

	// The helper really is the owner of the file: as root it would have got in.
	if r := run(dbview.Request{Op: dbview.OpInfo, Path: "locked/wal.db"}); r.Error == nil || r.Error.Code != "db_unreadable" {
		t.Errorf("locked folder: %+v %s", r.Error, r.Result)
	}

	// Answers that are not results.
	for _, tc := range []struct{ path, code string }{
		{"plain.txt", "not_sqlite"}, {"nope.db", "db_not_found"}, {"link.db", "file_symlink"}, {"d/up/app.db", "file_symlink"},
		{"passwd.db", "file_symlink"}, {"d", "not_a_file"},
	} {
		if r := run(dbview.Request{Op: dbview.OpInfo, Path: tc.path}); r.Error == nil || r.Error.Code != tc.code {
			t.Errorf("%s: %+v, want %s", tc.path, r.Error, tc.code)
		}
	}

	// A big answer (a few MB, one line) comes through the container's log whole.
	r = run(dbview.Request{Op: dbview.OpRows, Path: "big.db", Table: "big", Limit: 100})
	if r.Error != nil || len(r.Result) < 3<<20 {
		t.Fatalf("big: %+v, %d bytes", r.Error, len(r.Result))
	}
	var page dbview.RowsResult
	if err := dbview.Decode(r.Result, &page); err != nil || len(page.Rows) != 100 {
		t.Errorf("big page: %v, %d rows", err, len(page.Rows))
	}

	// The image: built once, labelled, tagged with the hash of the binary.
	ref := DBHelperImage + ":" + c.dbh.tag
	im, err := c.API.ImageInspect(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if im.Config == nil || im.Config.Labels[LabelManaged] != "1" || im.Config.Labels[LabelDBHelper] != "1" || im.Config.Labels[LabelInstance] != c.Instance {
		t.Errorf("image labels = %+v", im.Config)
	}
	if ims, err := c.ListManagedImages(ctx); err != nil || len(ims) == 0 {
		t.Errorf("managed images = %v %v", ims, err)
	}
	t0 := time.Now()
	run(dbview.Request{Op: dbview.OpInfo, Path: "app.db"})
	if d := time.Since(t0); d > 10*time.Second {
		t.Errorf("a later call took %v: the image is rebuilt?", d)
	}
	if im2, _ := c.API.ImageInspect(ctx, ref); im2.ID != im.ID {
		t.Error("the image was rebuilt")
	}
	// An older helper image is removed when the binary changes.
	old := DBHelperImage + ":oldhash"
	if err := c.TagImage(ctx, ref, old); err != nil {
		t.Fatal(err)
	}
	c.removeOldDBHelperImages(ctx, ref)
	if _, err := c.API.ImageInspect(ctx, old); !IsNotFound(err) {
		t.Errorf("old helper image still there: %v", err)
	}
	if _, err := c.API.ImageInspect(ctx, ref); err != nil {
		t.Errorf("current helper image gone: %v", err)
	}

	// No helper container is left, and the helper had no network (see createFlags below).
	if out, _ := exec.Command("docker", "ps", "-a", "--filter", "label="+LabelDBHelper+"=1", "--format", "{{.Names}}").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("helper containers left: %s", out)
	}

	// A query that never ends is stopped by the helper itself after 15 seconds.
	start = time.Now()
	r = run(dbview.Request{Op: dbview.OpSQL, Path: "app.db", SQL: "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT count(*) FROM n"})
	if r.Error == nil || r.Error.Code != "db_timeout" {
		t.Errorf("endless query: %+v", r.Error)
	}
	if d := time.Since(start); d < 14*time.Second || d > 25*time.Second {
		t.Errorf("endless query stopped after %v", d)
	}

	// The container of a helper that does not answer is killed from here.
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(3 * time.Second); cancel() }()
	b, _ := json.Marshal(dbview.Request{Op: dbview.OpSQL, Path: "app.db", SQL: "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT count(*) FROM n"})
	if _, err := c.RunDBHelper(cctx, vol, b); err == nil {
		t.Error("a cancelled call succeeded")
	}
	time.Sleep(time.Second)
	if out, _ := exec.Command("docker", "ps", "-a", "--filter", "label="+LabelDBHelper+"=1", "--format", "{{.Names}}").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("helper container left after a cancel: %s", out)
	}
}
