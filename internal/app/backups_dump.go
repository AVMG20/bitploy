package app

// Backups of database services as logical dumps. A PostgreSQL, MariaDB or
// MySQL app from a service template is backed up with the database's own
// dump tool, run inside its container while it runs (docker exec, as the
// container's user); the output is gzipped into <backups>/<app>/<id>.sql.gz.
// A restore empties the server and replays the dump with the database's
// client, after a dump of what is there now (the before-restore backup).
//
// Passwords never appear on a command line: the scripts below read them
// from the container's own variables (POSTGRES_PASSWORD and so on) and hand
// them to the tools through the environment (PGPASSWORD) or a private
// option file (mktemp, mode 0600, removed on exit).
//
// A database that is not running is backed up as a copy of its files: a
// stopped database's files are consistent. Valkey and Redis get a BGSAVE
// before their files are copied.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/servicetpl"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Backup formats (app_backups.format).
const (
	formatVolume = "volume" // a .tar.gz of the files in the volume
	formatDump   = "dump"   // a gzipped logical dump of a database service
)

// dumpEngine is how one kind of database is dumped and restored. The
// scripts run with sh -c in the database's container.
type dumpEngine struct {
	id   string // servicetpl.Engine*
	tool string // the dump tool, as shown: pg_dumpall, mariadb-dump, mysqldump
	// version prints the version of the dump tool.
	version string
	// dump writes the dump to stdout.
	dump string
	// clean drops every database (and, for PostgreSQL, every role but the
	// one it logs in as, whose name it prints).
	clean string
	// restore replays a dump read from stdin.
	restore string
	// complete are the texts one of which the end of a finished dump holds.
	complete []string
}

// pgLogin connects the PostgreSQL tools over TCP as the template's user,
// like the healthcheck; the password goes through the environment.
const pgLogin = `export PGPASSWORD="$POSTGRES_PASSWORD"
export PGHOST=127.0.0.1 PGUSER="$POSTGRES_USER"
`

var pgEngine = dumpEngine{
	id: servicetpl.EnginePostgres, tool: "pg_dumpall",
	version: `pg_dumpall --version`,
	dump:    pgLogin + `exec pg_dumpall -w`,
	// Connected to template1: every other database is dropped (WITH FORCE
	// ends the sessions of the apps that use it) and postgres is made again,
	// empty. Then every role except the one logged in goes; what such a
	// role still owns in template1 is handed over first.
	clean: pgLogin + `psql -X -q -w -v ON_ERROR_STOP=1 -d template1 <<'SQL'
SELECT format('DROP DATABASE %I WITH (FORCE)', datname) FROM pg_database WHERE datname NOT IN ('template0', 'template1') \gexec
CREATE DATABASE postgres TEMPLATE template0;
SELECT format('REASSIGN OWNED BY %1$I TO CURRENT_USER; DROP OWNED BY %1$I; DROP ROLE %1$I', rolname) FROM pg_roles WHERE rolname <> current_user AND rolname !~ '^pg_' \gexec
SQL
exec psql -X -q -w -t -A -d template1 -c 'SELECT current_user'`,
	restore:  pgLogin + `exec psql -X -q -w -v ON_ERROR_STOP=1 -d postgres`,
	complete: []string{"PostgreSQL database cluster dump complete"},
}

// mysqlScript wraps a MariaDB or MySQL script: it logs in as root through
// an option file only the container's user can read, removed on exit.
func mysqlScript(passVar, body string) string {
	return `set -e
f=$(mktemp)
t=$(mktemp)
trap 'rm -f "$f" "$t"' EXIT
printf '[client]\nuser=root\npassword="%s"\nhost=127.0.0.1\n' "$` + passVar + `" > "$f"
` + body
}

// userDBs selects the databases a dump holds: all but MySQL's own.
const userDBs = `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')`

func mysqlEngine(id, dumpBin, client, passVar, extra string) dumpEngine {
	return dumpEngine{
		id: id, tool: dumpBin,
		version: dumpBin + ` --version`,
		// Every database but the system ones, in one transaction (one
		// consistent moment for InnoDB tables), with routines, events and
		// triggers. Each one is dropped and made again on restore.
		dump: mysqlScript(passVar, client+` --defaults-extra-file="$f" -N -B -r -e "`+userDBs+` ORDER BY schema_name" > "$t"
if [ ! -s "$t" ]; then echo '-- bitploy: no databases to dump'; exit 0; fi
set -f
IFS='
'
`+dumpBin+` --defaults-extra-file="$f" --single-transaction --routines --events --triggers --hex-blob --add-drop-database`+extra+` --databases $(cat "$t")`),
		clean: mysqlScript(passVar, client+` --defaults-extra-file="$f" -N -B -r -e "SELECT CONCAT('DROP DATABASE ', CHAR(96 USING utf8mb4), REPLACE(schema_name, CHAR(96 USING utf8mb4), REPEAT(CHAR(96 USING utf8mb4), 2)), CHAR(96 USING utf8mb4), ';') FROM (`+userDBs+`) d" > "$t"
`+client+` --defaults-extra-file="$f" < "$t"`),
		restore:  mysqlScript(passVar, client+` --defaults-extra-file="$f" --binary-mode`),
		complete: []string{"-- Dump completed", "-- bitploy: no databases to dump"},
	}
}

var (
	mariadbEngine = mysqlEngine(servicetpl.EngineMariaDB, "mariadb-dump", "mariadb", "MARIADB_ROOT_PASSWORD", "")
	mysqlEngineV  = mysqlEngine(servicetpl.EngineMySQL, "mysqldump", "mysql", "MYSQL_ROOT_PASSWORD", " --set-gtid-purged=OFF")
)

// redisSnapshot makes Valkey or Redis write a fresh snapshot (dump.rdb)
// and waits until it is on disk, at most 5 minutes. The data itself is in
// the append-only file, which the paused copy holds as it is.
func redisSnapshot(cli, passVar string) string {
	return `export REDISCLI_AUTH="$` + passVar + `"
before=$(` + cli + ` LASTSAVE)
sleep 1
` + cli + ` BGSAVE SCHEDULE > /dev/null || true
i=0
while [ "$(` + cli + ` LASTSAVE)" = "$before" ]; do
  i=$((i + 1))
  if [ "$i" -ge 300 ]; then echo 'the snapshot did not finish within 5 minutes' >&2; exit 1; fi
  sleep 1
done
` + cli + ` INFO persistence | grep -q 'rdb_last_bgsave_status:ok' || { echo 'the snapshot failed' >&2; exit 1; }`
}

// dumpEngineOf is how the app's databases are dumped, or nil when the app
// is not a database service.
func dumpEngineOf(app db.App) *dumpEngine {
	if isMulti(app) {
		return nil
	}
	t, ok := templateOf(app)
	if !ok {
		return nil
	}
	switch t.BackupEngine() {
	case servicetpl.EnginePostgres:
		return &pgEngine
	case servicetpl.EngineMariaDB:
		return &mariadbEngine
	case servicetpl.EngineMySQL:
		return &mysqlEngineV
	}
	return nil
}

// ---- running commands in the app's container ----

// serviceRunner runs scripts in the container of a (single-service) app.
// Docker does it for real (dockerRunner); tests use a fake.
type serviceRunner interface {
	// Running reports whether the app's current container runs.
	Running(ctx context.Context, app db.App) (bool, error)
	// Exec runs sh -c script there, as the container's user, and returns the
	// end of stderr. A non-zero exit is a *dockerx.ExecError.
	Exec(ctx context.Context, app db.App, script string, stdin io.Reader, stdout io.Writer) (string, error)
}

type dockerRunner struct{ s *Service }

func (d dockerRunner) container(ctx context.Context, app db.App) string {
	return d.s.currentContainers(ctx, app)[singleService]
}

func (d dockerRunner) Running(ctx context.Context, app db.App) (bool, error) {
	name := d.container(ctx, app)
	if name == "" {
		return false, nil
	}
	return d.s.Docker.ContainerRunning(ctx, name)
}

func (d dockerRunner) Exec(ctx context.Context, app db.App, script string, stdin io.Reader, stdout io.Writer) (string, error) {
	name := d.container(ctx, app)
	if name == "" {
		return "", errors.New("the app has no container")
	}
	user := ""
	if t, ok := templateOf(app); ok {
		user = t.User
	}
	return d.s.Docker.Exec(ctx, name, []string{"sh", "-c", script}, dockerx.ExecOptions{User: user, Stdin: stdin, Stdout: stdout})
}

// runner returns what runs commands in app containers, or nil without Docker.
func (s *Service) runner() serviceRunner {
	if s.backup.run != nil {
		return s.backup.run
	}
	if s.Docker == nil {
		return nil
	}
	return dockerRunner{s}
}

// dbRunning reports whether the app's container runs, so that a dump can be
// made or replayed. Errors count as not running.
func (s *Service) dbRunning(ctx context.Context, app db.App) bool {
	r := s.runner()
	if r == nil {
		return false
	}
	ok, err := r.Running(ctx, app)
	return err == nil && ok
}

// backupFormatFor is the format a new backup of the volume gets: a dump for
// a database service that runs, else a copy of the files.
func (s *Service) backupFormatFor(ctx context.Context, app db.App, volume string) string {
	if dumpEngineOf(app) != nil && (volume == "" || volume == singleVolume) && s.dbRunning(ctx, app) {
		return formatDump
	}
	return formatVolume
}

// ---- files ----

func (s *Service) dumpPath(appID, id string) string {
	return filepath.Join(s.backupDir(appID), id+".sql.gz")
}

// backupFile is where the file of backup b is.
func (s *Service) backupFile(b db.AppBackup) string {
	if b.Format == formatDump {
		return s.dumpPath(b.AppID, b.ID)
	}
	return s.backupPath(b.AppID, b.ID)
}

// ---- errors ----

// dumpError is a dump or replay that failed, with the reason in plain words.
type dumpError struct{ msg string }

func (e *dumpError) Error() string { return e.msg }

// toolTail is the end of what a database tool wrote to stderr, cut to its
// last lines, for an error message.
func toolTail(tail string) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(tail, "\r\n", "\n")), "\n")
	keep := lines[:0:0]
	for _, l := range lines {
		l = strings.TrimSpace(l)
		// Noise the tools print on every run.
		if l == "" || strings.Contains(l, "Using a password on the command line") || strings.HasPrefix(l, "mysqldump: [Warning]") && strings.Contains(l, "deprecated") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 5 {
		keep = keep[len(keep)-5:]
	}
	out := apperr.Redact(strings.Join(keep, " | "))
	if r := []rune(out); len(r) > 600 {
		out = "..." + string(r[len(r)-600:])
	}
	return out
}

// execFailure turns an error of Exec into a dumpError that starts with what.
func execFailure(what string, tail string, err error) error {
	if errors.Is(err, errBackupNoRoom) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ee *dockerx.ExecError
	if errors.As(err, &ee) {
		if t := toolTail(ee.Tail); t != "" {
			return &dumpError{what + ": " + t}
		}
		return &dumpError{fmt.Sprintf("%s (exit code %d)", what, ee.Code)}
	}
	msg := apperr.Redact(dockerx.DescribeError(err))
	if t := toolTail(tail); t != "" {
		msg += ": " + t
	}
	return &dumpError{what + ": " + msg}
}

// ---- dump ----

// makeDump writes the dump of backup b and records the row as ready. The
// database keeps running; nothing is paused.
func (s *Service) makeDump(ctx context.Context, app db.App, owner db.User, b db.AppBackup, room int64, appLocked bool) (int64, error) {
	eng := dumpEngineOf(app)
	run := s.runner()
	if eng == nil || run == nil {
		return 0, &dumpError{"This app is not a database bitploy can dump."}
	}
	if !appLocked && s.appBusy(app.ID) {
		return 0, errDeployBusy
	}
	if !s.dbRunning(ctx, app) {
		return 0, &dumpError{"The database is not running, so it could not be dumped. Start the app, or back up while it is stopped to copy its files."}
	}
	if err := os.MkdirAll(s.backupDir(app.ID), 0o750); err != nil {
		return 0, err
	}
	var ver bytes.Buffer
	if _, err := run.Exec(ctx, app, eng.version, nil, &ver); err == nil {
		tool := strings.TrimSpace(strings.SplitN(ver.String(), "\n", 2)[0])
		if r := []rune(tool); len(r) > 100 {
			tool = string(r[:100])
		}
		if tool != "" {
			_ = s.Q.SetBackupTool(ctx, db.SetBackupToolParams{Tool: tool, ID: b.ID})
		}
	}
	final := s.dumpPath(app.ID, b.ID)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	size, err := func() (int64, error) {
		defer f.Close()
		gz := gzip.NewWriter(&limitWriter{w: f, left: room})
		end := &dockerx.TailBuffer{Max: 512}
		tail, err := run.Exec(ctx, app, eng.dump, nil, io.MultiWriter(gz, end))
		if err != nil {
			return 0, execFailure("The dump with "+eng.tool+" failed", tail, err)
		}
		if !dumpComplete(eng, end.String()) {
			return 0, &dumpError{"The dump with " + eng.tool + " stopped before it was complete." + tailSuffix(tail)}
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
				Status: backupReady, SizeBytes: size, FinishedAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: b.ID,
			})
		})
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return size, nil
}

func tailSuffix(tail string) string {
	if t := toolTail(tail); t != "" {
		return " " + t
	}
	return ""
}

func dumpComplete(eng *dumpEngine, end string) bool {
	for _, c := range eng.complete {
		if strings.Contains(end, c) {
			return true
		}
	}
	return false
}

// snapshotBeforeCopy asks Valkey or Redis to write a snapshot before its
// files are copied. A failure is logged, not fatal: the append-only file
// holds the data either way.
func (s *Service) snapshotBeforeCopy(ctx context.Context, app db.App) {
	t, ok := templateOf(app)
	if !ok || t.BackupEngine() != servicetpl.EngineRedis || isMulti(app) {
		return
	}
	run := s.runner()
	if run == nil || !s.dbRunning(ctx, app) {
		return
	}
	cli := "valkey-cli"
	if t.ID == "redis" {
		cli = "redis-cli"
	}
	sctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	if tail, err := run.Exec(sctx, app, redisSnapshot(cli, t.PasswordVar()), nil, nil); err != nil {
		s.Log.Warn("backup: snapshot before the copy failed", "app", app.Slug, "err", err, "stderr", toolTail(tail))
	}
}

// ---- restore ----

// checkDumpFile reads the whole dump once, so a damaged file is found
// before anything is dropped.
func (s *Service) checkDumpFile(ctx context.Context, eng *dumpEngine, b db.AppBackup) error {
	f, err := os.Open(s.backupFile(b))
	if err != nil {
		return errors.New("the file of the backup is gone")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return problem("the backup file is damaged")
	}
	defer gz.Close()
	gz.Multistream(false) // the replay reads one member too
	end := &dockerx.TailBuffer{Max: 512}
	if _, err := io.Copy(end, &ctxReader{ctx: ctx, r: gz}); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return problem("the backup file is damaged")
	}
	if !dumpComplete(eng, end.String()) {
		return problem("the dump in the backup is not complete")
	}
	return nil
}

// replayDump empties the database server and replays the dump of b.
func (s *Service) replayDump(ctx context.Context, app db.App, eng *dumpEngine, b db.AppBackup) error {
	run := s.runner()
	if run == nil {
		return &dumpError{"bitploy cannot reach Docker right now"}
	}
	f, err := os.Open(s.backupFile(b))
	if err != nil {
		return &dumpError{"the file of the backup is gone"}
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return &dumpError{"the backup file is damaged"}
	}
	defer gz.Close()
	gz.Multistream(false)
	var who bytes.Buffer
	if tail, err := run.Exec(ctx, app, eng.clean, nil, &who); err != nil {
		return execFailure("emptying the database failed", tail, err)
	}
	var in io.Reader = gz
	if eng.id == servicetpl.EnginePostgres {
		in = skipOwnRole(gz, lastLine(who.String()))
	}
	client := "psql"
	switch eng.id {
	case servicetpl.EngineMariaDB:
		client = "mariadb"
	case servicetpl.EngineMySQL:
		client = "mysql"
	}
	if tail, err := run.Exec(ctx, app, eng.restore, in, nil); err != nil {
		var re *readErr
		if errors.As(err, &re) || isCorrupt(err) {
			return &dumpError{"the backup file is damaged"}
		}
		return execFailure(client+" stopped at an error", tail, err)
	}
	return nil
}

// lastLine is the last line of s that is not empty, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// skipOwnRole drops "CREATE ROLE <role>;" for the role the restore logs in
// as from the roles part of a pg_dumpall dump (everything before its first
// \connect): that role exists, and psql would stop at the error. The rest
// of the dump passes through unchanged.
func skipOwnRole(r io.Reader, role string) io.Reader {
	if role == "" {
		return r
	}
	skip := map[string]bool{
		"CREATE ROLE " + role + ";":                                  true,
		`CREATE ROLE "` + strings.ReplaceAll(role, `"`, `""`) + `";`: true,
	}
	pr, pw := io.Pipe()
	go func() {
		br := bufio.NewReaderSize(r, 64<<10)
		for {
			line, err := br.ReadString('\n')
			if strings.HasPrefix(line, `\connect`) {
				if _, werr := io.WriteString(pw, line); werr != nil {
					pw.CloseWithError(werr)
					return
				}
				_, err = io.Copy(pw, br)
				pw.CloseWithError(err)
				return
			}
			if !skip[strings.TrimRight(line, "\r\n")] && line != "" {
				if _, werr := io.WriteString(pw, line); werr != nil {
					pw.CloseWithError(werr)
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				pw.CloseWithError(err)
				return
			}
		}
	}()
	return pr
}

// runDumpRestore restores the dump b into the running database. Its steps,
// with what a crash after each one leaves (recoverRestore handles those):
//
//  0. the dump file is read once (nothing to undo)
//  1. the marker is written; a dump of the current databases is made (the
//     before-restore backup) and the marker gets its id
//  2. the server is emptied and the dump replayed; a failure replays the
//     before-restore dump
//  3. the marker is cleared
//
// The database keeps running: a dump can only be replayed into a running
// server. The apps that use it lose their connections when the databases
// are dropped and connect again.
func (s *Service) runDumpRestore(ctx context.Context, row db.App, owner db.User, b db.AppBackup, room int64) (rerr error) {
	eng := dumpEngineOf(row)
	if eng == nil {
		return errors.New("This app is not a database, so a dump cannot be restored into it. Nothing was changed.")
	}
	if err := s.checkDumpFile(ctx, eng, b); err != nil {
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
	if !s.dbRunning(ctx, app) {
		return errors.New("The database is not running, so nothing was restored. Start the app, then try again.")
	}
	s.setRestoreMarker(ctx, app.ID, b.ID, "", false)
	defer func() {
		if !s.stopping() {
			s.setRestoreMarker(ctx, app.ID, "", "", false)
		}
	}()

	safe, err := s.insertBackup(ctx, app.ID, BackupBeforeRestore, b.Volume, formatDump)
	if err != nil {
		return errors.New("A server error stopped the restore before it began. Nothing was changed.")
	}
	if err := s.finishBackup(ctx, app, owner, safe.ID, room, true); err != nil {
		return errors.New("The current databases could not be dumped first, so nothing was restored. " + backupErrText(err))
	}
	s.setRestoreMarker(ctx, app.ID, b.ID, safe.ID, false)

	if err := s.replayDump(ctx, app, eng, b); err != nil {
		if s.stopping() {
			return errors.New("bitploy was stopped during the restore. It puts the databases from before back when it starts again.")
		}
		msg := "The dump could not be restored: " + strings.TrimSuffix(restoreErrText(err), ".") + "."
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Hour)
		defer cancel()
		safeRow := safe
		safeRow.Format = formatDump
		if ferr := s.replayDump(fctx, app, eng, safeRow); ferr != nil {
			s.Log.Error("restore: putting the databases back failed", "app", app.Slug, "err", ferr)
			return errors.New(msg + " Putting the databases from before back failed too: restore the backup marked \"before restore\".")
		}
		return errors.New(msg + " The databases from before were put back.")
	}
	s.pruneLocked(ctx, app)
	return nil
}
