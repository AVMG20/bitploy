package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// fakeDB stands in for a database server reached through docker exec: data
// is what a dump prints (without its frame), set by a replay.
type fakeDB struct {
	mu       sync.Mutex
	eng      *dumpEngine
	running  bool
	data     string
	scripts  []string // every script run, in order
	stdin    []string // what each restore read
	failDump string   // stderr of a failing dump
	cutDump  bool     // the dump ends early without an error
	failRepl []bool   // restore calls (in order) that fail
	replN    int
	snaps    int // redis snapshots
}

const pgDumpTrailer = "--\n-- PostgreSQL database cluster dump complete\n--\n"

// dumpOf frames data like pg_dumpall does: the roles first, the current role
// among them, then the databases.
func pgDumpOf(data string) string {
	return "--\n-- PostgreSQL database cluster dump\n--\n\\restrict k\nCREATE ROLE app;\nALTER ROLE app WITH SUPERUSER LOGIN;\nCREATE ROLE reader;\n\\unrestrict k\n\\connect template1\n" +
		data + "\n" + pgDumpTrailer
}

func (f *fakeDB) Running(context.Context, db.App) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, nil
}

func (f *fakeDB) Exec(_ context.Context, _ db.App, script string, stdin io.Reader, stdout io.Writer) (string, error) {
	f.mu.Lock()
	f.scripts = append(f.scripts, script)
	eng := f.eng
	f.mu.Unlock()
	if stdout == nil {
		stdout = io.Discard
	}
	switch {
	case strings.Contains(script, "BGSAVE"):
		f.mu.Lock()
		f.snaps++
		f.mu.Unlock()
		return "", nil
	case script == eng.version:
		_, _ = io.WriteString(stdout, "pg_dumpall (PostgreSQL) 18.1 (Debian 18.1-1.pgdg13+2)\n")
		return "", nil
	case script == eng.dump:
		f.mu.Lock()
		data, fail, cut := f.data, f.failDump, f.cutDump
		f.mu.Unlock()
		if fail != "" {
			return fail, &dockerx.ExecError{Code: 1, Tail: fail}
		}
		out := pgDumpOf(data)
		if cut {
			out = out[:len(out)-len(pgDumpTrailer)]
		}
		_, err := io.WriteString(stdout, out)
		return "", err
	case script == eng.clean:
		f.mu.Lock()
		f.data = ""
		f.mu.Unlock()
		_, _ = io.WriteString(stdout, "app\n")
		return "", nil
	case script == eng.restore:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stdin = append(f.stdin, string(b))
		n := f.replN
		f.replN++
		if n < len(f.failRepl) && f.failRepl[n] {
			tail := "SET\npsql:<stdin>:12: ERROR:  relation \"users\" already exists\n"
			return tail, &dockerx.ExecError{Code: 3, Tail: tail}
		}
		s := string(b)
		i := strings.Index(s, "\\connect template1\n")
		j := strings.Index(s, "\n"+pgDumpTrailer)
		if i >= 0 && j > i {
			f.data = s[i+len("\\connect template1\n") : j]
		}
		return "", nil
	}
	return "", errors.New("unknown script")
}

func (f *fakeDB) set(data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = data
}

func (f *fakeDB) get() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data
}

// newDumpEnv is a backup test environment whose app is a PostgreSQL service.
func newDumpEnv(t *testing.T) (*bkEnv, *fakeDB) {
	t.Helper()
	e := newBkEnv(t)
	e.exec(t, `UPDATE apps SET template = 'postgres', template_version = '18.6' WHERE id = ?`, e.app.ID)
	if _, err := e.s.SetEnv(context.Background(), e.user, e.app.Slug, map[string]string{
		"POSTGRES_USER": "app", "POSTGRES_PASSWORD": "S3cretPassw0rdS3cretPassw0rdXyZq",
	}, nil); err != nil {
		t.Fatal(err)
	}
	f := &fakeDB{eng: &pgEngine, running: true, data: "CREATE TABLE users (id int);\nCOPY users FROM stdin;\n1\n\\."}
	e.s.backup.run = f
	return e, f
}

func (e *bkEnv) readDump(t *testing.T, b Backup) string {
	t.Helper()
	raw, err := os.ReadFile(e.s.dumpPath(e.app.ID, b.ID))
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestDumpBackupOfARunningDatabase(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	if b.Format != "dump" || !strings.HasPrefix(b.Tool, "pg_dumpall (PostgreSQL) 18.1") {
		t.Fatalf("backup = %+v", b)
	}
	if got := e.readDump(t, b); got != pgDumpOf(f.get()) {
		t.Errorf("dump file holds %q", got)
	}
	if _, err := os.Stat(e.s.backupPath(e.app.ID, b.ID)); !os.IsNotExist(err) {
		t.Error("a dump left a .tar.gz behind")
	}
	if e.vol.prepared != 0 {
		t.Error("a dump read the volume")
	}
	// Passwords are read inside the container, never put on a command line.
	for _, s := range f.scripts {
		if strings.Contains(s, "S3cretPassw0rd") {
			t.Errorf("script holds the password: %s", s)
		}
	}
	l := e.list(t)
	if l.DumpTool != "pg_dumpall" {
		t.Errorf("dump_tool = %q", l.DumpTool)
	}
	file, name, err := e.s.OpenBackup(ctx, e.user, e.app.Slug, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if !strings.HasSuffix(name, "-manual.sql.gz") {
		t.Errorf("download name = %q", name)
	}
	// Deleting removes the dump file.
	if err := e.s.DeleteBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.s.dumpPath(e.app.ID, b.ID)); !os.IsNotExist(err) {
		t.Error("the dump file is still there")
	}
}

func TestDumpBackupOfAStoppedDatabaseCopiesTheFiles(t *testing.T) {
	e, f := newDumpEnv(t)
	f.running = false
	b := e.makeBackup(t)
	if b.Format != "volume" || b.Tool != "" || e.vol.prepared != 1 {
		t.Fatalf("backup = %+v, prepared %d", b, e.vol.prepared)
	}
	if len(f.scripts) != 0 {
		t.Errorf("scripts ran on a stopped database: %d", len(f.scripts))
	}
}

func TestDumpBackupFailures(t *testing.T) {
	t.Run("the tool fails", func(t *testing.T) {
		e, f := newDumpEnv(t)
		f.failDump = "pg_dumpall: error: connection to server at \"127.0.0.1\", port 5432 failed: FATAL:  password authentication failed for user \"app\"\n"
		if _, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug); err != nil {
			t.Fatal(err)
		}
		e.wait()
		it := e.list(t).Items[0]
		if it.Status != "failed" || !strings.Contains(it.Error, "The dump with pg_dumpall failed: pg_dumpall: error") || !strings.Contains(it.Error, "password authentication failed") {
			t.Errorf("backup = %+v", it)
		}
		if m, _ := filepath.Glob(filepath.Join(e.s.backupDir(e.app.ID), "*")); len(m) != 0 {
			t.Errorf("files left: %v", m)
		}
	})
	t.Run("the dump is cut short", func(t *testing.T) {
		e, f := newDumpEnv(t)
		f.cutDump = true
		if _, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug); err != nil {
			t.Fatal(err)
		}
		e.wait()
		if it := e.list(t).Items[0]; it.Status != "failed" || !strings.Contains(it.Error, "stopped before it was complete") {
			t.Errorf("backup = %+v", it)
		}
	})
	t.Run("no room", func(t *testing.T) {
		e, f := newDumpEnv(t)
		e.exec(t, `UPDATE users SET quota_volume_mb = 101 WHERE id = ?`, e.user.UserID)
		// The app reserves 100 MB of 101: 1 MB is left, the dump needs more
		// (random-ish data does not shrink much under gzip).
		var sb strings.Builder
		for i := 0; sb.Len() < 3<<20; i++ {
			sb.WriteString(strings.Repeat(string(rune('a'+i%26)), 1+i%7))
			sb.WriteString(strconv.Itoa(i * 7919))
		}
		f.set(sb.String())
		if _, err := e.s.CreateBackup(context.Background(), e.user, e.app.Slug); err != nil {
			t.Fatal(err)
		}
		e.wait()
		if it := e.list(t).Items[0]; it.Status != "failed" || !strings.Contains(it.Error, "not enough storage") {
			t.Errorf("backup = %+v", it)
		}
	})
}

func TestDumpRestore(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	before := f.get()
	f.set("CREATE TABLE users (id int);\nCREATE TABLE later (id int);")

	st, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.AppRestarts {
		t.Error("a dump restore does not restart the database")
	}
	r := e.waitRestore(t)
	if r.State != "done" || r.Error != "" {
		t.Fatalf("restore = %+v", r)
	}
	if got := f.get(); got != before {
		t.Errorf("database holds %q, want %q", got, before)
	}
	// The role the restore logs in as is not created again (psql would stop
	// at the error); the other roles and everything after \connect stay.
	in := f.stdin[0]
	if strings.Contains(in, "CREATE ROLE app;") || !strings.Contains(in, "ALTER ROLE app WITH") || !strings.Contains(in, "CREATE ROLE reader;") {
		t.Errorf("replayed %q", in)
	}
	// The databases from before are kept as a dump of their own.
	var safe Backup
	for _, it := range e.list(t).Items {
		if it.Kind == "before_restore" {
			safe = it
		}
	}
	if safe.Status != "ready" || safe.Format != "dump" || !strings.Contains(e.readDump(t, safe), "CREATE TABLE later") {
		t.Fatalf("before-restore backup = %+v", safe)
	}
	if e.vol.replaceN != 0 {
		t.Error("a dump restore replaced the volume")
	}
	var marker string
	_ = e.s.Store.DB.QueryRow(`SELECT restore_backup_id FROM apps WHERE id = ?`, e.app.ID).Scan(&marker)
	if marker != "" {
		t.Error("restore marker left behind")
	}
}

func TestDumpRestoreFailurePutsTheOldDatabasesBack(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	now := "CREATE TABLE now (id int);"
	f.set(now)
	f.failRepl = []bool{true}
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	r := e.waitRestore(t)
	if r.State != "failed" || !strings.Contains(r.Error, `relation "users" already exists`) || !strings.Contains(r.Error, "put back") {
		t.Fatalf("restore = %+v", r)
	}
	if got := f.get(); got != now {
		t.Errorf("database holds %q, want what was there before", got)
	}

	// Both fail: the message says what to do.
	f.failRepl = []bool{true, true, true, true}
	f.replN = 0
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.waitRestore(t); r.State != "failed" || !strings.Contains(r.Error, `restore the backup marked "before restore"`) {
		t.Fatalf("restore = %+v", r)
	}
}

func TestDumpRestoreRefusals(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	b := e.makeBackup(t)
	f.running = false
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "database_not_running" {
		t.Errorf("stopped database: %v", err)
	}
	f.running = true
	// A damaged file is found before anything is dropped.
	if err := os.WriteFile(e.s.dumpPath(e.app.ID, b.ID), []byte("not gzip"), 0o640); err != nil {
		t.Fatal(err)
	}
	n := len(f.scripts)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.waitRestore(t); r.State != "failed" || !strings.Contains(r.Error, "nothing was changed") {
		t.Errorf("restore = %+v", r)
	}
	if len(f.scripts) != n {
		t.Errorf("scripts ran for a damaged dump: %v", f.scripts[n:])
	}
	// A dump on an app that is no database (cannot happen through the API,
	// but the row says dump).
	e.exec(t, `UPDATE apps SET template = '' WHERE id = ?`, e.app.ID)
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); depCode(err) != "dump_not_restorable" {
		t.Errorf("not a database: %v", err)
	}
}

func TestScheduledBackupOfADatabaseIsADump(t *testing.T) {
	e, _ := newDumpEnv(t)
	e.exec(t, `UPDATE apps SET backup_schedule = 'daily' WHERE id = ?`, e.app.ID)
	e.runDue(context.Background())
	got := e.backupsOfKind(t, "scheduled")
	if len(got) != 1 || got[0].Status != "ready" || got[0].Format != "dump" {
		t.Fatalf("scheduled = %+v", got)
	}
}

func TestInterruptedDumpRestoreIsRecovered(t *testing.T) {
	e, f := newDumpEnv(t)
	ctx := context.Background()
	safe := e.makeBackup(t)
	want := f.get()
	f.set("half restored")
	e.s.setRestoreMarker(ctx, e.app.ID, "bak_other", safe.ID, false)
	e.s.recoverAfterCrash(ctx)
	l := e.list(t)
	if l.Restore == nil || !strings.Contains(l.Restore.Error, "databases from before were put back") {
		t.Fatalf("restore = %+v", l.Restore)
	}
	if got := f.get(); got != want {
		t.Errorf("database holds %q", got)
	}
	var desired string
	_ = e.s.Store.DB.QueryRow(`SELECT desired_state FROM apps WHERE id = ?`, e.app.ID).Scan(&desired)
	if desired == "stopped" {
		t.Error("the database was stopped")
	}
}

func TestRedisGetsASnapshotBeforeItsFilesAreCopied(t *testing.T) {
	e := newBkEnv(t)
	e.exec(t, `UPDATE apps SET template = 'valkey' WHERE id = ?`, e.app.ID)
	f := &fakeDB{eng: &pgEngine, running: true}
	e.s.backup.run = f
	b := e.makeBackup(t)
	if b.Format != "volume" || f.snaps != 1 || e.vol.prepared != 1 {
		t.Fatalf("backup = %+v, snapshots %d, prepared %d", b, f.snaps, e.vol.prepared)
	}
	if s := f.scripts[0]; !strings.Contains(s, `REDISCLI_AUTH="$VALKEY_PASSWORD"`) || !strings.Contains(s, "valkey-cli") {
		t.Errorf("script = %s", s)
	}
	if l := e.list(t); l.DumpTool != "" {
		t.Errorf("dump_tool = %q", l.DumpTool)
	}
}

func TestSkipOwnRole(t *testing.T) {
	in := "SET x = 1;\nCREATE ROLE app;\nCREATE ROLE \"App-1\";\nCREATE ROLE apps;\n\\connect template1\nCREATE ROLE app;\nlast line without newline"
	read := func(role string) string {
		b, err := io.ReadAll(skipOwnRole(strings.NewReader(in), role))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, want := read("app"), "SET x = 1;\nCREATE ROLE \"App-1\";\nCREATE ROLE apps;\n\\connect template1\nCREATE ROLE app;\nlast line without newline"; got != want {
		t.Errorf("app: %q", got)
	}
	if got := read("App-1"); strings.Contains(got, `"App-1"`) || !strings.Contains(got, "CREATE ROLE app;\nCREATE ROLE apps") {
		t.Errorf("App-1: %q", got)
	}
	if got := read(""); got != in {
		t.Errorf("no role: %q", got)
	}
}

func TestDumpScriptsKeepPasswordsOffTheCommandLine(t *testing.T) {
	for _, eng := range []*dumpEngine{&pgEngine, &mariadbEngine, &mysqlEngineV} {
		for _, s := range []string{eng.dump, eng.clean, eng.restore} {
			// The tools get the password from the environment or a private
			// option file, never as an argument.
			if strings.Contains(s, "-p\"$") || strings.Contains(s, "--password") || strings.Contains(s, "-a \"$") {
				t.Errorf("%s: password on the command line: %s", eng.tool, s)
			}
		}
	}
	if !strings.Contains(mariadbEngine.dump, "mariadb-dump --defaults-extra-file=") || !strings.Contains(mysqlEngineV.dump, "mysqldump --defaults-extra-file=") {
		t.Error("dump tools must read the option file first")
	}
	for _, want := range []string{"--single-transaction", "--routines", "--events", "--triggers"} {
		if !strings.Contains(mariadbEngine.dump, want) || !strings.Contains(mysqlEngineV.dump, want) {
			t.Errorf("mysql dumps miss %s", want)
		}
	}
}

func TestToolTail(t *testing.T) {
	tail := "mysqldump: [Warning] Using a password on the command line interface can be insecure.\n" +
		"a\nb\nc\nd\ne\nERROR 1045 (28000): Access denied for user 'root'@'127.0.0.1' (using password: YES)\n"
	got := toolTail(tail)
	if strings.Contains(got, "Using a password") || strings.HasPrefix(got, "a ") || !strings.HasSuffix(got, "(using password: YES)") {
		t.Errorf("tail = %q", got)
	}
}
