package app

// The database viewer: for the SQLite files in /data, and for the database of
// a PostgreSQL, MariaDB or MySQL app (dbServerTarget). The tables, a page of
// rows, insert, update and delete of rows, and a SQL console. The access rules
// are those of the file browser (filesTarget): only the owner's own app, /data
// on and created, writes need the deploy scope and are refused while a backup
// restore runs. The work is done by dbview in a throwaway helper container
// (dockerx/dbhelper.go): SQLite runs there, never in bitploy itself and never
// in the app container, and the file is never copied out and back.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/servicetpl"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// dbRunner runs one request of the viewer against the volume of an app and
// returns the JSON response of dbview. The Docker client does it for real
// (dockerVolumes); tests use a fake. A volumeStore that is not a dbRunner
// cannot open databases.
type dbRunner interface {
	RunDB(ctx context.Context, volume string, request []byte) ([]byte, error)
}

func (d dockerVolumes) RunDB(ctx context.Context, volume string, request []byte) ([]byte, error) {
	return d.c.RunDBHelper(ctx, volume, request)
}

// dbServerRunner runs one request of the viewer for a database server in the
// network of the named container of the database app.
type dbServerRunner interface {
	RunDBServer(ctx context.Context, container string, request []byte) ([]byte, error)
}

func (d dockerVolumes) RunDBServer(ctx context.Context, container string, request []byte) ([]byte, error) {
	return d.c.RunDBServerHelper(ctx, container, request)
}

// The shapes of the viewer's answers are those of dbview.
type (
	DBInfo             = dbview.Info
	DBTable            = dbview.Table
	DBRows             = dbview.RowsResult
	DBRow              = dbview.Row
	DBInsertResult     = dbview.InsertResult
	DBUpdateResult     = dbview.UpdateResult
	DBDeleteResult     = dbview.DeleteResult
	DBUpdateManyResult = dbview.UpdateManyResult
	DBSQLResult        = dbview.SQLResult
	DBCellResult       = dbview.CellResult
)

// DBFilter is one condition of a rows request. A value is in the value shapes of
// the viewer: null, a number, a string, {"int": "..."}, {"real": "..."} or {"blob": "<base64>"}.
type DBFilter struct {
	Column string          `json:"column" doc:"A column of the table"`
	Op     string          `json:"op" enum:"=,!=,<,<=,>,>=,like,not like,is null,is not null"`
	Value  json.RawMessage `json:"value,omitempty" doc:"What to compare with; ignored for is null and is not null"`
}

// DBRowsQuery asks for a page of rows.
type DBRowsQuery struct {
	Volume  string     `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path    string     `json:"path" doc:"The database file, relative to /data, like app.db or db/main.sqlite"`
	Table   string     `json:"table" doc:"A table or view of the database"`
	Limit   int        `json:"limit,omitempty" doc:"Rows per page, 1 to 1000 (default 100)"`
	Offset  int        `json:"offset,omitempty" doc:"Rows to skip"`
	OrderBy string     `json:"order_by,omitempty" doc:"A column to sort by"`
	Desc    bool       `json:"desc,omitempty" doc:"Sort from high to low"`
	Filters []DBFilter `json:"filters,omitempty" doc:"Conditions, all of which must hold"`
	Search  string     `json:"search,omitempty" doc:"Free text, at most 200 characters, ANDed with the filters. Text columns contain it; number columns start with it when it is a number (12 finds 12, 120, 1234); blobs never match."`
}

// DBCellBody asks for one value of one row, whole.
type DBCellBody struct {
	Volume string                     `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string                     `json:"path" doc:"The database file, relative to /data"`
	Table  string                     `json:"table"`
	Key    map[string]json.RawMessage `json:"key" doc:"The key of the row as listed: {\"rowid\": 5}, or the primary key columns of a WITHOUT ROWID table"`
	Column string                     `json:"column" doc:"The column to read"`
}

// DBInsertBody adds a row.
type DBInsertBody struct {
	Volume string                     `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string                     `json:"path" doc:"The database file, relative to /data"`
	Table  string                     `json:"table"`
	Values map[string]json.RawMessage `json:"values,omitempty" doc:"Column name to value (value shapes as in rows). Columns left out get their default."`
}

// DBUpdateBody changes a row.
type DBUpdateBody struct {
	Volume string                     `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string                     `json:"path" doc:"The database file, relative to /data"`
	Table  string                     `json:"table"`
	Key    map[string]json.RawMessage `json:"key" doc:"The key of the row as listed: {\"rowid\": 5}, or the primary key columns of a WITHOUT ROWID table"`
	Values map[string]json.RawMessage `json:"values" doc:"The columns to change. A value that was listed cut off (truncated) can not be sent back."`
	Match  map[string]json.RawMessage `json:"match,omitempty" doc:"Optional: values the row must still have, as listed (column to value; leave out values that were cut off). When the key still exists but a value differs, nothing is changed and the answer is 409 row_changed."`
}

// DBUpdateManyBody sets the same values on many rows.
type DBUpdateManyBody struct {
	Volume string                       `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string                       `json:"path" doc:"The database file, relative to /data"`
	Table  string                       `json:"table"`
	Keys   []map[string]json.RawMessage `json:"keys" doc:"The keys of the rows to change (1 to 1000), as listed"`
	Match  []map[string]json.RawMessage `json:"match,omitempty" doc:"Optional: one set of values per key, in the same order, that the row must still have (see update). If one row changed, nothing is saved and the answer is 409 row_changed with details.index, the place of that row in keys. A key that is gone is skipped."`
	Values map[string]json.RawMessage   `json:"values" doc:"The columns to set on every row. A value that was listed cut off (truncated) can not be sent back."`
}

// DBDeleteBody removes rows.
type DBDeleteBody struct {
	Volume string                       `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string                       `json:"path" doc:"The database file, relative to /data"`
	Table  string                       `json:"table"`
	Keys   []map[string]json.RawMessage `json:"keys" doc:"The keys of the rows to remove (1 to 1000), as listed"`
	Match  []map[string]json.RawMessage `json:"match,omitempty" doc:"Optional: one set of values per key, in the same order, that the row must still have (see update). If one row changed, nothing is deleted and the answer is 409 row_changed. A key that is gone is skipped."`
}

// DBSQLBody runs SQL.
type DBSQLBody struct {
	Volume string `json:"volume,omitempty" doc:"For an app with several volumes (a compose app): the volume the database is in, like pgdata (default: the first). get_app lists the volumes."`
	Path   string `json:"path" doc:"The database file, relative to /data"`
	SQL    string `json:"sql" doc:"One or more statements separated by semicolons (at most 64 KB)"`
	Write  bool   `json:"write,omitempty" doc:"false (default): the database is opened read-only. true: the statements may change data (needs the deploy scope)."`
}

// ---- errors ----

// dbStatus is the HTTP status of the codes the helper answers with. A code that
// is not here is a mistake in the request.
var dbStatus = map[string]int{
	"not_sqlite": http.StatusUnprocessableEntity, "db_not_found": http.StatusNotFound, "table_not_found": http.StatusNotFound,
	"row_not_found": http.StatusNotFound, "db_busy": http.StatusConflict, "db_timeout": http.StatusUnprocessableEntity,
	"result_too_large": http.StatusUnprocessableEntity, "file_symlink": http.StatusUnprocessableEntity,
	"not_a_file": http.StatusUnprocessableEntity, "db_unreadable": http.StatusUnprocessableEntity,
	"request_too_large": http.StatusRequestEntityTooLarge, "db_failed": http.StatusServiceUnavailable,
	"row_changed": http.StatusConflict, "db_out_of_memory": http.StatusUnprocessableEntity,
}

// dbErr turns an error of dbview into an apperr.Error with the same code.
func dbErr(e *dbview.Error) *apperr.Error {
	status, ok := dbStatus[e.Code]
	if !ok {
		status = http.StatusBadRequest
	}
	hint := e.Hint
	if hint == "" {
		hint = "Check the message, fix the request and try again."
	}
	out := apperr.New(status, e.Code, e.Message, hint)
	out.Details = e.Details
	return out
}

func dbHelperUnavailable(msg string) *apperr.Error {
	return apperr.Unavailable("db_helper_unavailable", msg,
		"Try again in a minute. If it keeps happening, tell the bitploy admin: the helper that opens databases cannot run.")
}

// dbRunErr turns an error of running the helper container into plain words.
func dbRunErr(err error) error {
	switch {
	case errors.Is(err, dockerx.ErrNoDBHelper):
		return apperr.Unavailable("db_helper_unavailable", "bitploy has no program to open databases with on this server.",
			"Tell the bitploy admin. On a development machine that is not Linux, set BITPLOY_DB_HELPER_BIN to a bitploy binary built for Linux (make dev does this).").WithCause(err)
	case errors.Is(err, dockerx.ErrDBTimeout):
		return apperr.Unprocessable("db_timeout", "The database did not answer in time and the helper was stopped.",
			"Narrow the query down with a filter or a LIMIT, or look at fewer rows.")
	case errors.Is(err, dockerx.ErrDBOutOfMemory):
		return apperr.Unprocessable("db_out_of_memory", "The query needed more memory than the database helper may use (256 MB).",
			"Narrow the query down, or use LIMIT.")
	case errors.Is(err, dockerx.ErrNoVolume), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return filesErr(err)
	}
	msg := apperr.Redact(dockerx.DescribeError(err))
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300]) + "..."
	}
	return dbHelperUnavailable("bitploy could not run the database helper: " + msg).WithCause(err)
}

// ---- limits ----

// At most this many helper containers run at once for one app and for the
// server: each is up to 256 MB and a CPU, and its answer is held in memory.
const (
	dbMaxPerApp = 3
	dbMaxTotal  = 8
	// dbDrainWait is how long a restore waits for the reads that are running.
	dbDrainWait = 30 * time.Second
)

// dbSlotWait is how long a request waits for a free place before it answers
// db_busy (a variable for the tests).
var dbSlotWait = 2 * time.Second

// dbViewerState counts the helpers that run (dbMaxPerApp, dbMaxTotal) and, per
// app, the reads among them: a restore waits for those to end before it
// touches the files (a helper that closes the last connection to a database
// checkpoints and removes its -wal file).
type dbViewerState struct {
	mu      sync.Mutex
	total   int
	perApp  map[string]int
	readers map[string]int
	changed chan struct{} // closed (and replaced) whenever a place is freed
}

// acquire waits a little for a free place. It returns false when there is none.
func (v *dbViewerState) acquire(ctx context.Context, appID string) (release func(), ok bool) {
	timer := time.NewTimer(dbSlotWait)
	defer timer.Stop()
	for {
		v.mu.Lock()
		if v.total < dbMaxTotal && v.perApp[appID] < dbMaxPerApp {
			v.total++
			if v.perApp == nil {
				v.perApp = map[string]int{}
			}
			v.perApp[appID]++
			v.mu.Unlock()
			return func() { v.release(appID) }, true
		}
		if v.changed == nil {
			v.changed = make(chan struct{})
		}
		ch := v.changed
		v.mu.Unlock()
		select {
		case <-ch:
		case <-timer.C:
			return nil, false
		case <-ctx.Done():
			return nil, false
		}
	}
}

func (v *dbViewerState) release(appID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.total--
	if v.perApp[appID]--; v.perApp[appID] <= 0 {
		delete(v.perApp, appID)
	}
	if v.changed != nil {
		close(v.changed)
		v.changed = nil
	}
}

func dbTooBusyErr() *apperr.Error {
	return apperr.Conflict("db_busy", "Too many database requests are running right now.", "Wait a moment and try again.")
}

// startRead notes a read that runs, unless a restore has started: both are
// decided under the backup lock of the state, so a restore never misses a read.
func (s *Service) startRead(appID string) (end func(), err error) {
	s.backup.mu.Lock()
	defer s.backup.mu.Unlock()
	if r := s.backup.restore[appID]; r != nil && r.State == "running" {
		return nil, backupBusyErr()
	}
	v := &s.dbv
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.readers == nil {
		v.readers = map[string]int{}
	}
	v.readers[appID]++
	return func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.readers[appID]--; v.readers[appID] <= 0 {
			delete(v.readers, appID)
		}
	}, nil
}

// waitDBReads waits until no read of the app runs in a helper. A restore calls
// it once it is marked as running (new reads are refused from then on).
func (s *Service) waitDBReads(ctx context.Context, appID string) {
	deadline := time.Now().Add(dbDrainWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		s.dbv.mu.Lock()
		n := s.dbv.readers[appID]
		s.dbv.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---- the call ----

// dbWrite describes a call that may change the database, for the lock and the audit log.
type dbWrite struct {
	Table    string
	Kinds    []string     // the kinds of statement it holds
	Affected func() int64 // rows changed, once the call has worked
	Partial  bool         // an error may come after changes (a console request with several statements)
}

// dbCall does one request of the viewer on the database path of an app and
// decodes the result into out. A write (w is set) needs the deploy scope, and
// runs while it holds the backup lock of the app: no backup or restore starts
// meanwhile, and a database is not changed while a backup reads it. A
// deployment is not held up: the volume stays the same and SQLite's own file
// locks keep the app and the helper apart. A read does not take the lock but is
// counted, so that a restore can wait for it. At most dbMaxPerApp helpers run
// for an app and dbMaxTotal for the server; the rest waits a moment, then gets
// db_busy. Every write that reached the helper is audited, whatever came of it.
func (s *Service) dbCall(ctx context.Context, a Actor, ref, volume, path string, w *dbWrite, req dbview.Request, out any) error {
	app, _, err := s.backupApp(ctx, a, ref, w != nil)
	if err != nil {
		return err
	}
	var (
		row  db.App
		rel  string
		exec func(body []byte) ([]byte, error)
	)
	if eng := dumpEngineOf(app); eng != nil && s.mayUseApp(ctx, a, app) {
		row = app
		if exec, rel, err = s.dbServerTarget(ctx, app, eng.id, path, &req); err != nil {
			return err
		}
	} else {
		if rel, err = cleanFilePath(path); err != nil {
			return err
		}
		if rel == "" {
			return filePathErr("There is no path to a database.")
		}
		req.Path = rel
		var vol volumeSpec
		row, _, _, vol, err = s.filesTarget(ctx, a, ref, w != nil, volume) // "" is the first volume
		if err != nil {
			return err
		}
		runner, err := s.dbRunner()
		if err != nil {
			return err
		}
		exec = func(body []byte) ([]byte, error) { return runner.RunDB(ctx, vol.VolumeName, body) }
	}
	body, err := json.Marshal(req)
	if err != nil {
		return apperr.Internal(err)
	}
	if len(body) > dbview.MaxRequest {
		return apperr.New(http.StatusRequestEntityTooLarge, "request_too_large", "The values in this request are too big.", "Send fewer or smaller values.")
	}
	if w != nil {
		lock := s.backupLock(row.ID)
		if !lock.TryLock() {
			return filesBusyErr()
		}
		defer lock.Unlock()
	}
	release, ok := s.dbv.acquire(ctx, row.ID)
	if !ok {
		if ctx.Err() != nil {
			return filesErr(ctx.Err())
		}
		return dbTooBusyErr()
	}
	defer release()
	if w == nil {
		end, err := s.startRead(row.ID)
		if err != nil {
			return err
		}
		defer end()
	}

	raw, err := exec(body)
	if err != nil {
		// The helper may have been killed after it committed: nobody knows.
		s.dbAudit(ctx, a, row, rel, w, "unknown", "", nil)
		return dbRunErr(err)
	}
	var resp dbview.Response
	if err := json.Unmarshal(raw, &resp); err != nil || (resp.Error == nil && len(resp.Result) == 0) {
		s.dbAudit(ctx, a, row, rel, w, "unknown", "", nil)
		return dbHelperUnavailable("The database helper gave an answer bitploy could not read.").WithCause(err)
	}
	if resp.Error != nil {
		// A change that is too big to report was made all the same.
		outcome := "error"
		if resp.Error.Code == "result_too_large" {
			outcome = "ok"
		}
		s.dbAudit(ctx, a, row, rel, w, outcome, resp.Error.Code, resp.Error)
		return dbErr(resp.Error)
	}
	if err := dbview.Decode(resp.Result, out); err != nil {
		s.dbAudit(ctx, a, row, rel, w, "unknown", "", nil)
		return dbHelperUnavailable("The database helper gave an answer bitploy could not read.").WithCause(err)
	}
	s.dbAudit(ctx, a, row, rel, w, "ok", "", nil)
	return nil
}

func (s *Service) dbRunner() (dbRunner, error) {
	vs, err := s.volumes()
	if err != nil {
		return nil, err
	}
	runner, ok := vs.(dbRunner)
	if !ok {
		return nil, apperr.Unavailable("docker_unavailable", "bitploy cannot reach the files in /data.", "Try again in a minute.")
	}
	return runner, nil
}

// serverLogin is how the viewer logs in to each engine: the variables of the
// database app that hold the user, the password and the database it made.
// MariaDB and MySQL are opened as root, like their backups, so every database
// on the server can be looked at; PostgreSQL's user is a superuser already.
var serverLogin = map[string]struct{ user, userVar, passVar, dbVar string }{
	servicetpl.EnginePostgres: {userVar: "POSTGRES_USER", passVar: "POSTGRES_PASSWORD", dbVar: "POSTGRES_DB"},
	servicetpl.EngineMariaDB:  {user: "root", passVar: "MARIADB_ROOT_PASSWORD", dbVar: "MARIADB_DATABASE"},
	servicetpl.EngineMySQL:    {user: "root", passVar: "MYSQL_ROOT_PASSWORD", dbVar: "MYSQL_DATABASE"},
}

// dbServerTarget prepares a request for a PostgreSQL, MariaDB or MySQL app:
// the login from its variables, the database to open (path, else the one the
// app was made with), and a run that sends the request to a helper in the
// network of the database's container. It answers db_not_running when there
// is no container to reach.
func (s *Service) dbServerTarget(ctx context.Context, app db.App, engine, path string, req *dbview.Request) (func([]byte) ([]byte, error), string, error) {
	t, _ := templateOf(app)
	login, ok := serverLogin[engine]
	if !ok {
		return nil, "", apperr.BadRequest("not_a_database", "This app has no database the viewer can open.", "")
	}
	if !s.dbRunning(ctx, app) {
		return nil, "", apperr.Conflict("db_not_running", "The database is not running, so there is nothing to look at.",
			"Start the app (or wait until its deployment is live), then try again.")
	}
	env, ae := s.containerEnv(ctx, app, t.Port)
	if ae != nil {
		return nil, "", ae
	}
	vars := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	user := login.user
	if user == "" {
		user = vars[login.userVar]
	}
	database := strings.TrimSpace(path)
	if database == "" {
		database = vars[login.dbVar]
	}
	if database == "" || len(database) > 64 || strings.ContainsRune(database, 0) {
		return nil, "", apperr.BadRequest("db_not_found", "That is not the name of a database.", "Use the name of a database on this server, or leave it out for the one the app was made with.")
	}
	req.Path = database
	req.Server = &dbview.ServerConn{Engine: engine, Port: int(t.Port), User: user, Password: vars[login.passVar], Database: database}
	name := s.currentContainers(ctx, app)[singleService]
	vs, err := s.volumes()
	if err != nil {
		return nil, "", err
	}
	runner, ok := vs.(dbServerRunner)
	if !ok {
		return nil, "", apperr.Unavailable("docker_unavailable", "bitploy cannot reach the database.", "Try again in a minute.")
	}
	return func(body []byte) ([]byte, error) { return runner.RunDBServer(ctx, name, body) }, database, nil
}

// dbAudit writes the audit row of a write (db.write): the app, the path, the
// table, the kinds of statement, the rows affected and how it went (ok, error
// or unknown when the helper did not answer). Never values or SQL text.
func (s *Service) dbAudit(ctx context.Context, a Actor, row db.App, path string, w *dbWrite, outcome, code string, failed *dbview.Error) {
	if w == nil {
		return
	}
	var affected int64
	completed := -1
	switch {
	case outcome == "ok" && w.Affected != nil:
		affected = w.Affected()
	case failed != nil && w.Partial && failed.Details != nil:
		// Statements before the one that failed have been committed (each runs in
		// autocommit): they are changes too.
		var det struct {
			Results []dbview.StatementResult `json:"results"`
		}
		if raw, e := json.Marshal(failed.Details); e == nil && dbview.Decode(raw, &det) == nil {
			affected, completed = affectedOf(det.Results), len(det.Results)
		}
	}
	d := map[string]any{"app": row.Slug, "path": path, "statements": w.Kinds, "rows_affected": affected, "outcome": outcome}
	if w.Table != "" {
		d["table"] = w.Table
	}
	if code != "" {
		d["error"] = code
	}
	if completed >= 0 {
		d["completed"] = completed
	}
	// The caller may be gone: the change is audited all the same.
	s.Audit(context.WithoutCancel(ctx), a, "db.write", "app", row.ID, d)
	s.metricsMgr().MeasureSoon()
}

// ---- reading ----

// DBInfo lists the tables and views of a database in /data with their
// columns and indexes. An optional volume picks the volume of an app with
// several (default: the first).
func (s *Service) DBInfo(ctx context.Context, a Actor, ref, path string, volume ...string) (DBInfo, error) {
	var out DBInfo
	if err := s.dbCall(ctx, a, ref, firstOpt(volume), path, nil, dbview.Request{Op: dbview.OpInfo}, &out); err != nil {
		return DBInfo{}, err
	}
	return out, nil
}

// DBRows reads a page of rows of a table or view.
func (s *Service) DBRows(ctx context.Context, a Actor, ref string, q DBRowsQuery) (DBRows, error) {
	req := dbview.Request{Op: dbview.OpRows, Table: q.Table, Limit: q.Limit, Offset: q.Offset, OrderBy: q.OrderBy, Desc: q.Desc, Search: q.Search}
	for _, f := range q.Filters {
		req.Filters = append(req.Filters, dbview.Filter{Column: f.Column, Op: f.Op, Value: f.Value})
	}
	var out DBRows
	if err := s.dbCall(ctx, a, ref, q.Volume, q.Path, nil, req, &out); err != nil {
		return DBRows{}, err
	}
	return out, nil
}

// DBCell reads one value of a row whole (rows cuts a text at 64 KiB).
func (s *Service) DBCell(ctx context.Context, a Actor, ref string, b DBCellBody) (DBCellResult, error) {
	var out DBCellResult
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, nil, dbview.Request{Op: dbview.OpCell, Table: b.Table, Key: b.Key, Column: b.Column}, &out); err != nil {
		return DBCellResult{}, err
	}
	return out, nil
}

// ---- writing ----

// DBInsert adds a row to a table.
func (s *Service) DBInsert(ctx context.Context, a Actor, ref string, b DBInsertBody) (DBInsertResult, error) {
	var out DBInsertResult
	w := &dbWrite{Table: b.Table, Kinds: []string{"insert"}, Affected: func() int64 { return 1 }}
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, w, dbview.Request{Op: dbview.OpInsert, Table: b.Table, Values: b.Values}, &out); err != nil {
		return DBInsertResult{}, err
	}
	return out, nil
}

// DBUpdate changes one row of a table.
func (s *Service) DBUpdate(ctx context.Context, a Actor, ref string, b DBUpdateBody) (DBUpdateResult, error) {
	var out DBUpdateResult
	w := &dbWrite{Table: b.Table, Kinds: []string{"update"}, Affected: func() int64 { return 1 }}
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, w, dbview.Request{Op: dbview.OpUpdate, Table: b.Table, Key: b.Key, Values: b.Values, Match: b.Match}, &out); err != nil {
		return DBUpdateResult{}, err
	}
	return out, nil
}

// DBUpdateMany sets the same values on many rows of a table, all in one transaction.
func (s *Service) DBUpdateMany(ctx context.Context, a Actor, ref string, b DBUpdateManyBody) (DBUpdateManyResult, error) {
	var out DBUpdateManyResult
	w := &dbWrite{Table: b.Table, Kinds: []string{"update"}, Affected: func() int64 { return out.Updated }}
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, w, dbview.Request{Op: dbview.OpUpdateMany, Table: b.Table, Keys: b.Keys, Matches: b.Match, Values: b.Values}, &out); err != nil {
		return DBUpdateManyResult{}, err
	}
	return out, nil
}

// DBDelete removes rows of a table, all in one transaction.
func (s *Service) DBDelete(ctx context.Context, a Actor, ref string, b DBDeleteBody) (DBDeleteResult, error) {
	var out DBDeleteResult
	w := &dbWrite{Table: b.Table, Kinds: []string{"delete"}, Affected: func() int64 { return out.Deleted }}
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, w, dbview.Request{Op: dbview.OpDelete, Table: b.Table, Keys: b.Keys, Matches: b.Match}, &out); err != nil {
		return DBDeleteResult{}, err
	}
	return out, nil
}

// DBSQL runs SQL on a database. Without write the database is read-only (a
// write fails with db_read_only) and the read scope is enough.
func (s *Service) DBSQL(ctx context.Context, a Actor, ref string, b DBSQLBody) (DBSQLResult, error) {
	if len(b.SQL) > dbview.MaxSQLBytes {
		return DBSQLResult{}, apperr.BadRequest("sql_too_long", "The SQL is longer than 64 KB.", "Split it into several requests.")
	}
	var out DBSQLResult
	var w *dbWrite
	if b.Write {
		w = &dbWrite{Kinds: dbview.Kinds(b.SQL), Partial: true, Affected: func() int64 { return affectedOf(out.Results) }}
	}
	if err := s.dbCall(ctx, a, ref, b.Volume, b.Path, w, dbview.Request{Op: dbview.OpSQL, SQL: b.SQL, Write: b.Write}, &out); err != nil {
		return DBSQLResult{}, err
	}
	return out, nil
}

func affectedOf(rs []dbview.StatementResult) (n int64) {
	for _, r := range rs {
		if r.RowsAffected != nil {
			n += *r.RowsAffected
		}
	}
	return n
}

// UseDBHelper makes the viewer run its requests with fn instead of in a helper
// container, for the tests of the API and the MCP server (which have no
// Docker). Every volume exists; the file browser and backups do not work
// meanwhile.
func (s *Service) UseDBHelper(fn func(ctx context.Context, volume string, request []byte) ([]byte, error)) {
	s.backup.vol = dbOnlyVolumes{run: fn}
}

// dbOnlyVolumes is the volume store of UseDBHelper.
type dbOnlyVolumes struct {
	run func(ctx context.Context, volume string, request []byte) ([]byte, error)
}

var errDBOnly = errors.New("only the database viewer works here")

func (d dbOnlyVolumes) RunDB(ctx context.Context, v string, req []byte) ([]byte, error) {
	return d.run(ctx, v, req)
}

// RunDBServer gets the name of the database's container where RunDB gets a volume.
func (d dbOnlyVolumes) RunDBServer(ctx context.Context, container string, req []byte) ([]byte, error) {
	return d.run(ctx, container, req)
}
func (dbOnlyVolumes) HasVolume(context.Context, string) (bool, error) { return true, nil }
func (dbOnlyVolumes) PrepareArchive(context.Context, string) (volumeArchive, error) {
	return nil, errDBOnly
}
func (dbOnlyVolumes) ReplaceVolume(context.Context, string, io.Reader, bool) error { return errDBOnly }
func (dbOnlyVolumes) ListFiles(context.Context, string, string, int) (dockerx.DirListing, error) {
	return dockerx.DirListing{}, errDBOnly
}
func (dbOnlyVolumes) OpenFile(context.Context, string, string) (io.ReadCloser, int64, time.Time, error) {
	return nil, 0, time.Time{}, errDBOnly
}
func (dbOnlyVolumes) PutFile(context.Context, string, string, string, int64, io.Reader, bool) error {
	return errDBOnly
}
func (dbOnlyVolumes) MakeDir(context.Context, string, string, string) error    { return errDBOnly }
func (dbOnlyVolumes) RemoveFile(context.Context, string, string, string) error { return errDBOnly }
