package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
	"github.com/AVMG20/bitploy/internal/dockerx"

	_ "modernc.org/sqlite"
)

// dbVol is a fakeVol that runs the viewer's requests for real, with the
// volume's files in a temp folder: SQLite and the path rules are the real ones,
// only the Docker container is missing.
type dbVol struct {
	*fsVol
	root string
	mu   sync.Mutex
	reqs []dbview.Request
	err  error         // RunDB fails with it
	hold chan struct{} // RunDB waits for it to be closed
	raw  []byte        // RunDB answers this instead
}

func newDBVol(t *testing.T) *dbVol {
	t.Helper()
	d := &dbVol{fsVol: newFsVol(), root: t.TempDir()}
	db, err := sql.Open("sqlite", filepath.Join(d.root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT)`,
		`INSERT INTO notes (title, body) VALUES ('one', 'first'), ('two', NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d.root, "readme.txt"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func (d *dbVol) RunDB(ctx context.Context, _ string, request []byte) ([]byte, error) {
	if d.hold != nil {
		<-d.hold
	}
	var req dbview.Request
	_ = json.Unmarshal(request, &req)
	d.mu.Lock()
	d.reqs = append(d.reqs, req)
	d.mu.Unlock()
	if d.err != nil {
		return nil, d.err
	}
	if d.raw != nil {
		return d.raw, nil
	}
	resp := dbview.Handle(ctx, request, dbview.HelperOptions{Root: d.root})
	return json.Marshal(resp)
}

func newDBEnv(t *testing.T) (*bkEnv, *dbVol) {
	t.Helper()
	e := newBkEnv(t)
	dv := newDBVol(t)
	e.s.backup.vol = dv
	return e, dv
}

func auditRows(t *testing.T, e *bkEnv, action string) []map[string]any {
	t.Helper()
	rows, err := e.s.Store.DB.Query(`SELECT details FROM audit_log WHERE action = ? ORDER BY ts, rowid`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(d), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func TestDBReadFlow(t *testing.T) {
	e, dv := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug

	info, err := e.s.DBInfo(ctx, e.user, slug, "/app.db")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != "app.db" || len(info.Tables) != 1 || info.Tables[0].Name != "notes" || info.Tables[0].Rows == nil || *info.Tables[0].Rows != 2 {
		t.Fatalf("info = %+v", info)
	}
	if dv.reqs[0].Path != "app.db" || dv.reqs[0].Op != dbview.OpInfo {
		t.Errorf("the helper got %+v", dv.reqs[0])
	}
	rows, err := e.s.DBRows(ctx, e.user, slug, DBRowsQuery{Path: "app.db", Table: "notes", OrderBy: "title", Desc: true,
		Filters: []DBFilter{{Column: "title", Op: "like", Value: raw(`"%o%"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0].Values[1] != "two" || rows.Total == nil || *rows.Total != 2 || !rows.Editable {
		t.Fatalf("rows = %+v", rows)
	}
	if got := dv.reqs[len(dv.reqs)-1]; got.Table != "notes" || got.OrderBy != "title" || !got.Desc || len(got.Filters) != 1 || got.Filters[0].Op != "like" {
		t.Errorf("rows request = %+v", got)
	}
	// The JSON of a row is what the contract says.
	b, _ := json.Marshal(rows.Rows[1])
	if string(b) != `{"key":{"rowid":1},"values":[1,"one","first"]}` {
		t.Errorf("row json = %s", b)
	}
	q, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "SELECT count(*) AS n FROM notes"})
	if err != nil || len(q.Results) != 1 || q.Results[0].Rows[0][0] != json.Number("2") {
		t.Fatalf("sql = %+v %v", q, err)
	}
	if len(auditRows(t, e, "db.write")) != 0 {
		t.Error("a read was audited as a write")
	}
}

func TestDBErrors(t *testing.T) {
	e, dv := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug
	want := func(err error, code string, status int) {
		t.Helper()
		if depCode(err) != code {
			t.Errorf("code = %q (%v), want %s", depCode(err), err, code)
			return
		}
		if st := apperr.As(err).HTTPStatus; st != status {
			t.Errorf("%s: status %d, want %d", code, st, status)
		}
	}
	_, err := e.s.DBInfo(ctx, e.user, slug, "readme.txt")
	want(err, "not_sqlite", 422)
	_, err = e.s.DBInfo(ctx, e.user, slug, "nope.db")
	want(err, "db_not_found", 404)
	_, err = e.s.DBRows(ctx, e.user, slug, DBRowsQuery{Path: "app.db", Table: "nope"})
	want(err, "table_not_found", 404)
	_, err = e.s.DBRows(ctx, e.user, slug, DBRowsQuery{Path: "app.db", Table: "notes", OrderBy: "nope"})
	want(err, "column_not_found", 400)
	_, err = e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes"})
	want(err, "db_read_only", 400)
	_, err = e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "ATTACH ':memory:' AS m"})
	want(err, "statement_not_allowed", 400)
	_, err = e.s.DBUpdate(ctx, e.user, slug, DBUpdateBody{Path: "app.db", Table: "notes", Key: map[string]json.RawMessage{"rowid": raw("99")}, Values: map[string]json.RawMessage{"title": raw(`"x"`)}})
	want(err, "row_not_found", 404)
	_, err = e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: strings.Repeat(" ", dbview.MaxSQLBytes+1)})
	want(err, "sql_too_long", 400)

	// Paths are checked before the helper is asked.
	n := len(dv.reqs)
	for _, p := range []string{"", "/", "..", "a/../../b", "a\x00b"} {
		_, err := e.s.DBInfo(ctx, e.user, slug, p)
		want(err, "path_invalid", 400)
	}
	if len(dv.reqs) != n {
		t.Error("the helper was asked with a bad path")
	}

	// A sql_error brings the statement and what ran before it.
	_, err = e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "SELECT 1; SELECT * FROM missing"})
	want(err, "sql_error", 400)
	d := apperr.As(err).Details
	if d["statement"] == nil || d["results"] == nil {
		t.Errorf("details = %v", d)
	}

	// Trouble with the helper container.
	dv.err = dockerx.ErrNoDBHelper
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "db_helper_unavailable", 503)
	dv.err = dockerx.ErrDBTimeout
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "db_timeout", 422)
	dv.err = dockerx.ErrDBOutOfMemory
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "db_out_of_memory", 422)
	dv.err = dockerx.ErrNoVolume
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "volume_not_ready", 409)
	dv.err = errors.New("docker said no: token bp_abcdefghijklmnopqrstuvwx")
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "db_helper_unavailable", 503)
	if strings.Contains(apperr.As(err).Message, "bp_abcdefghijklmnopqrstuvwx") {
		t.Errorf("token leaked: %v", err)
	}
	dv.err = nil
	dv.raw = []byte("not json")
	_, err = e.s.DBInfo(ctx, e.user, slug, "app.db")
	want(err, "db_helper_unavailable", 503)
	dv.raw = nil
}

func TestDBWritesAndAudit(t *testing.T) {
	e, _ := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug

	ins, err := e.s.DBInsert(ctx, e.user, slug, DBInsertBody{Path: "app.db", Table: "notes", Values: map[string]json.RawMessage{"title": raw(`"three"`), "body": raw(`"secret body"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if ins.Key["rowid"] != json.Number("3") || ins.Row.Values[1] != "three" {
		t.Fatalf("insert = %+v", ins)
	}
	up, err := e.s.DBUpdate(ctx, e.user, slug, DBUpdateBody{Path: "app.db", Table: "notes", Key: map[string]json.RawMessage{"rowid": raw("3")}, Values: map[string]json.RawMessage{"title": raw(`"THREE"`)}})
	if err != nil || up.Row.Values[1] != "THREE" {
		t.Fatalf("update = %+v %v", up, err)
	}
	del, err := e.s.DBDelete(ctx, e.user, slug, DBDeleteBody{Path: "app.db", Table: "notes", Keys: []map[string]json.RawMessage{{"rowid": raw("1")}, {"rowid": raw("2")}}})
	if err != nil || del.Deleted != 2 {
		t.Fatalf("delete = %+v %v", del, err)
	}
	r, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", Write: true, SQL: "INSERT INTO notes (title) VALUES ('sql secret'); UPDATE notes SET body = 'x'"})
	if err != nil || len(r.Results) != 2 {
		t.Fatalf("sql = %+v %v", r, err)
	}
	// A failing statement: the first one stays, and that is audited too.
	if _, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", Write: true, SQL: "INSERT INTO notes (title) VALUES ('kept'); INSERT INTO notes (id) VALUES (NULL)"}); depCode(err) != "sql_error" {
		t.Fatalf("failing sql: %v", err)
	}

	a := auditRows(t, e, "db.write")
	if len(a) != 5 {
		t.Fatalf("audit rows = %d: %v", len(a), a)
	}
	check := func(i int, table string, kinds string, affected float64, outcome string) {
		t.Helper()
		m := a[i]
		k, _ := json.Marshal(m["statements"])
		if m["app"] != slug || m["path"] != "app.db" || string(k) != kinds || m["rows_affected"] != affected || (table != "" && m["table"] != table) || (table == "" && m["table"] != nil) || m["outcome"] != outcome {
			t.Errorf("audit %d = %v, want table %q kinds %s affected %v outcome %s", i, m, table, kinds, affected, outcome)
		}
	}
	check(0, "notes", `["insert"]`, 1, "ok")
	check(1, "notes", `["update"]`, 1, "ok")
	check(2, "notes", `["delete"]`, 2, "ok")
	check(3, "", `["insert","update"]`, 3, "ok")
	check(4, "", `["insert","insert"]`, 1, "error")
	if a[4]["error"] != "sql_error" || a[4]["completed"] != float64(1) {
		t.Errorf("failed sql audit = %v", a[4])
	}
	// Never the values or the SQL.
	var leaked int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details LIKE '%secret%' OR details LIKE '%INSERT INTO%' OR details LIKE '%three%'`).Scan(&leaked)
	if leaked != 0 {
		t.Errorf("%d audit rows hold values or SQL", leaked)
	}
	// A failed insert is no change.
	if _, err := e.s.DBInsert(ctx, e.user, slug, DBInsertBody{Path: "app.db", Table: "notes", Values: map[string]json.RawMessage{"body": raw(`"no title"`)}}); err == nil {
		t.Fatal("insert without a title worked")
	}
	// ... but it was tried, and that is in the log too.
	a = auditRows(t, e, "db.write")
	if len(a) != 6 || a[5]["outcome"] != "error" || a[5]["rows_affected"] != float64(0) || a[5]["error"] != "sql_error" {
		t.Errorf("failed insert audit = %v", a)
	}
}

func TestDBAuthAndState(t *testing.T) {
	e, dv := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug

	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.DBInfo(ctx, ro, slug, "app.db"); err != nil {
		t.Errorf("read token info: %v", err)
	}
	if _, err := e.s.DBRows(ctx, ro, slug, DBRowsQuery{Path: "app.db", Table: "notes"}); err != nil {
		t.Errorf("read token rows: %v", err)
	}
	if _, err := e.s.DBSQL(ctx, ro, slug, DBSQLBody{Path: "app.db", SQL: "SELECT 1"}); err != nil {
		t.Errorf("read token sql: %v", err)
	}
	n := len(dv.reqs)
	if _, err := e.s.DBSQL(ctx, ro, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "token_read_only" {
		t.Errorf("read token sql write: %v", err)
	}
	if _, err := e.s.DBInsert(ctx, ro, slug, DBInsertBody{Path: "app.db", Table: "notes"}); depCode(err) != "token_read_only" {
		t.Errorf("read token insert: %v", err)
	}
	if _, err := e.s.DBUpdate(ctx, ro, slug, DBUpdateBody{Path: "app.db", Table: "notes"}); depCode(err) != "token_read_only" {
		t.Errorf("read token update: %v", err)
	}
	if _, err := e.s.DBDelete(ctx, ro, slug, DBDeleteBody{Path: "app.db", Table: "notes"}); depCode(err) != "token_read_only" {
		t.Errorf("read token delete: %v", err)
	}
	if len(dv.reqs) != n {
		t.Error("the helper was asked on behalf of a read-only token that writes")
	}

	other := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.DBInfo(ctx, other, slug, "app.db"); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	admin := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	admin.Scope, admin.Channel = ScopeSession, ChannelDashboard
	if _, err := e.s.DBInfo(ctx, admin, slug, "app.db"); depCode(err) != "app_not_found" {
		t.Errorf("admin: %v", err)
	}

	// A suspended owner changes nothing, but may look.
	e.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, e.user.UserID)
	if _, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "account_suspended" {
		t.Errorf("suspended: %v", err)
	}
	e.exec(t, `UPDATE users SET status = 'active' WHERE id = ?`, e.user.UserID)

	// /data off.
	e.exec(t, `UPDATE apps SET volume_enabled = 0 WHERE id = ?`, e.app.ID)
	if _, err := e.s.DBInfo(ctx, e.user, slug, "app.db"); depCode(err) != "volume_off" {
		t.Errorf("volume off: %v", err)
	}
	e.exec(t, `UPDATE apps SET volume_enabled = 1 WHERE id = ?`, e.app.ID)

	// No volume yet.
	dv.fakeVol.mu.Lock()
	dv.fakeVol.noVolume = true
	dv.fakeVol.mu.Unlock()
	if _, err := e.s.DBInfo(ctx, e.user, slug, "app.db"); depCode(err) != "volume_not_ready" {
		t.Errorf("no volume: %v", err)
	}
	dv.fakeVol.mu.Lock()
	dv.fakeVol.noVolume = false
	dv.fakeVol.mu.Unlock()

	// A restore is running: neither reading nor writing.
	e.s.backup.mu.Lock()
	e.s.backup.restore = map[string]*BackupRestore{e.app.ID: {State: "running"}}
	e.s.backup.mu.Unlock()
	if _, err := e.s.DBInfo(ctx, e.user, slug, "app.db"); depCode(err) != "backup_busy" {
		t.Errorf("restore, read: %v", err)
	}
	if _, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "backup_busy" {
		t.Errorf("restore, write: %v", err)
	}
	e.s.backup.mu.Lock()
	delete(e.s.backup.restore, e.app.ID)
	e.s.backup.mu.Unlock()
	if _, err := e.s.DBInfo(ctx, e.user, slug, "app.db"); err != nil {
		t.Errorf("after the restore: %v", err)
	}
}

func TestDBWriteHoldsTheBackupLock(t *testing.T) {
	e, dv := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug
	dv.hold = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true})
		done <- err
	}()
	// Wait until the write is inside the helper (it holds the lock then).
	for i := 0; i < 5000 && !e.s.backupBusy(e.app.ID); i++ {
		select {
		case err := <-done:
			t.Fatalf("finished early: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	if !e.s.backupBusy(e.app.ID) {
		t.Fatal("a write does not hold the backup lock")
	}
	// A second write is refused, a backup can not start, a read still works.
	if _, err := e.s.DBSQL(ctx, e.user, slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "files_busy" {
		t.Errorf("second write: %v", err)
	}
	if _, err := e.s.CreateBackup(ctx, e.user, slug); err == nil {
		t.Error("a backup started during a database write")
	}
	close(dv.hold)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.s.backupBusy(e.app.ID) {
		t.Error("the lock is still held")
	}
	if _, err := e.s.DBInfo(ctx, e.user, slug, "app.db"); err != nil {
		t.Errorf("read after the write: %v", err)
	}
}

func TestDBRunnerMissing(t *testing.T) {
	e := newBkEnv(t)
	fv := newFsVol() // a store that can browse files but not run databases
	e.s.backup.vol = fv
	if _, err := e.s.DBInfo(context.Background(), e.user, e.app.Slug, "app.db"); depCode(err) != "docker_unavailable" {
		t.Errorf("no runner: %v", err)
	}
}

func TestDBMatchThroughTheService(t *testing.T) {
	e, _ := newDBEnv(t)
	ctx := context.Background()
	slug := e.app.Slug
	key := map[string]json.RawMessage{"rowid": raw("1")}
	_, err := e.s.DBUpdate(ctx, e.user, slug, DBUpdateBody{Path: "app.db", Table: "notes", Key: key, Values: map[string]json.RawMessage{"title": raw(`"x"`)},
		Match: map[string]json.RawMessage{"title": raw(`"stale"`)}})
	if depCode(err) != "row_changed" || apperr.As(err).HTTPStatus != 409 {
		t.Errorf("stale update: %v", err)
	}
	if _, err := e.s.DBUpdate(ctx, e.user, slug, DBUpdateBody{Path: "app.db", Table: "notes", Key: key, Values: map[string]json.RawMessage{"title": raw(`"x"`)},
		Match: map[string]json.RawMessage{"title": raw(`"one"`), "body": raw(`"first"`)}}); err != nil {
		t.Errorf("update with a current match: %v", err)
	}
	_, err = e.s.DBDelete(ctx, e.user, slug, DBDeleteBody{Path: "app.db", Table: "notes", Keys: []map[string]json.RawMessage{key, {"rowid": raw("2")}},
		Match: []map[string]json.RawMessage{{"title": raw(`"x"`)}, {"title": raw(`"changed"`)}}})
	if depCode(err) != "row_changed" {
		t.Errorf("stale delete: %v", err)
	}
	d, err := e.s.DBDelete(ctx, e.user, slug, DBDeleteBody{Path: "app.db", Table: "notes", Keys: []map[string]json.RawMessage{key},
		Match: []map[string]json.RawMessage{{"title": raw(`"x"`)}}})
	if err != nil || d.Deleted != 1 {
		t.Errorf("delete: %+v %v", d, err)
	}
}

func TestDBAuditWhenTheHelperDoesNotAnswer(t *testing.T) {
	e, dv := newDBEnv(t)
	ctx := context.Background()
	dv.err = dockerx.ErrDBTimeout
	if _, err := e.s.DBSQL(ctx, e.user, e.app.Slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "db_timeout" {
		t.Fatalf("err = %v", err)
	}
	a := auditRows(t, e, "db.write")
	if len(a) != 1 || a[0]["outcome"] != "unknown" || a[0]["rows_affected"] != float64(0) {
		t.Errorf("audit = %v", a)
	}
	// A read leaves no trace.
	dv.err = dockerx.ErrDBTimeout
	_, _ = e.s.DBInfo(ctx, e.user, e.app.Slug, "app.db")
	if len(auditRows(t, e, "db.write")) != 1 {
		t.Error("a read was audited")
	}
	// A caller that is gone: the audit row is still written.
	dv.err = nil
	cctx, cancel := context.WithCancel(ctx)
	dv.hold = make(chan struct{})
	go func() { time.Sleep(20 * time.Millisecond); cancel(); close(dv.hold) }()
	_, _ = e.s.DBSQL(cctx, e.user, e.app.Slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes WHERE id = 1", Write: true})
	if n := len(auditRows(t, e, "db.write")); n != 2 {
		t.Errorf("audit rows = %d, want 2", n)
	}
}

func TestDBConcurrencyLimits(t *testing.T) {
	e, dv := newDBEnv(t)
	old := dbSlotWait
	dbSlotWait = 150 * time.Millisecond
	defer func() { dbSlotWait = old }()
	ctx := context.Background()
	dv.hold = make(chan struct{})
	var wg sync.WaitGroup
	start := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.s.DBInfo(ctx, e.user, e.app.Slug, "app.db")
		}()
	}
	for i := 0; i < dbMaxPerApp; i++ {
		start()
	}
	for i := 0; i < 5000; i++ {
		e.s.dbv.mu.Lock()
		n := e.s.dbv.perApp[e.app.ID]
		e.s.dbv.mu.Unlock()
		if n == dbMaxPerApp {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// The next one waits a moment, then says it is busy: 409.
	t0 := time.Now()
	_, err := e.s.DBInfo(ctx, e.user, e.app.Slug, "app.db")
	if depCode(err) != "db_busy" || apperr.As(err).HTTPStatus != 409 || time.Since(t0) < 100*time.Millisecond {
		t.Errorf("fourth request: %v after %v", err, time.Since(t0))
	}
	if _, err := e.s.DBSQL(ctx, e.user, e.app.Slug, DBSQLBody{Path: "app.db", SQL: "DELETE FROM notes", Write: true}); depCode(err) != "db_busy" {
		t.Errorf("write while the places are taken: %v", err)
	}
	// While the reads run, a restore would have to wait for them.
	drained := make(chan struct{})
	e.s.backup.mu.Lock()
	e.s.backup.restore = map[string]*BackupRestore{e.app.ID: {State: "running"}}
	e.s.backup.mu.Unlock()
	go func() { e.s.waitDBReads(ctx, e.app.ID); close(drained) }()
	select {
	case <-drained:
		t.Error("the restore did not wait for the running reads")
	case <-time.After(150 * time.Millisecond):
	}
	// New reads are refused meanwhile.
	if _, err := e.s.DBInfo(ctx, e.user, e.app.Slug, "app.db"); depCode(err) != "backup_busy" {
		t.Errorf("read during a restore: %v", err)
	}
	close(dv.hold)
	wg.Wait()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Error("the restore still waits")
	}
	e.s.backup.mu.Lock()
	delete(e.s.backup.restore, e.app.ID)
	e.s.backup.mu.Unlock()
	if _, err := e.s.DBInfo(ctx, e.user, e.app.Slug, "app.db"); err != nil {
		t.Errorf("after: %v", err)
	}
	if e.s.dbv.total != 0 || len(e.s.dbv.perApp) != 0 || len(e.s.dbv.readers) != 0 {
		t.Errorf("places leaked: %+v", &e.s.dbv)
	}
}

func TestDBGlobalLimit(t *testing.T) {
	var v dbViewerState
	ctx := context.Background()
	old := dbSlotWait
	dbSlotWait = 50 * time.Millisecond
	defer func() { dbSlotWait = old }()
	var rel []func()
	for i := 0; i < dbMaxTotal; i++ {
		r, ok := v.acquire(ctx, "app"+string(rune('a'+i))) // one place each
		if !ok {
			t.Fatalf("place %d refused", i)
		}
		rel = append(rel, r)
	}
	if _, ok := v.acquire(ctx, "other"); ok {
		t.Error("a ninth helper was let in")
	}
	// A freed place is taken by the one that waits.
	go func() { time.Sleep(10 * time.Millisecond); rel[0]() }()
	if r, ok := v.acquire(ctx, "other"); !ok {
		t.Error("a freed place was not given")
	} else {
		r()
	}
}

func TestDBUpdateManyAudit(t *testing.T) {
	e, _ := newDBEnv(t)
	ctx := context.Background()
	keys := []map[string]json.RawMessage{{"rowid": raw("1")}, {"rowid": raw("2")}, {"rowid": raw("9")}}
	r, err := e.s.DBUpdateMany(ctx, e.user, e.app.Slug, DBUpdateManyBody{Path: "app.db", Table: "notes", Keys: keys, Values: map[string]json.RawMessage{"body": raw(`"secret"`)}})
	if err != nil || r.Updated != 2 {
		t.Fatalf("update many = %+v %v", r, err)
	}
	a := auditRows(t, e, "db.write")
	if len(a) != 1 || a[0]["rows_affected"] != float64(2) || a[0]["table"] != "notes" || a[0]["outcome"] != "ok" || a[0]["statements"].([]any)[0] != "update" {
		t.Errorf("audit = %v", a)
	}
	_, err = e.s.DBUpdateMany(ctx, e.user, e.app.Slug, DBUpdateManyBody{Path: "app.db", Table: "notes", Keys: keys[:2], Values: map[string]json.RawMessage{"body": raw(`"x"`)},
		Match: []map[string]json.RawMessage{{"body": raw(`"secret"`)}, {"body": raw(`"other"`)}}})
	if depCode(err) != "row_changed" || apperr.As(err).Details["index"] == nil {
		t.Errorf("stale: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.DBUpdateMany(ctx, ro, e.app.Slug, DBUpdateManyBody{Path: "app.db", Table: "notes", Keys: keys[:1], Values: map[string]json.RawMessage{"body": raw(`"x"`)}}); depCode(err) != "token_read_only" {
		t.Errorf("read token: %v", err)
	}
	var leaked int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details LIKE '%secret%'`).Scan(&leaked)
	if leaked != 0 {
		t.Error("values in the audit log")
	}
}
