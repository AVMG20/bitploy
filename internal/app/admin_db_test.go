package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
)

func adminActor() Actor {
	return Actor{UserID: "usr_admin", Role: "admin", Channel: ChannelDashboard, Scope: ScopeSession}
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("not an apperr: %v", err)
	}
	return ae.Code
}

func TestAdminDBAccessAndAudit(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	var calls int
	e.s.UseAdminDBHelper(func(ctx context.Context, file string, req []byte) ([]byte, error) {
		calls++
		return json.Marshal(dbview.HandleControl(ctx, req, e.s.Cfg.DBPath()))
	})

	// Only an admin with a dashboard session.
	for name, a := range map[string]Actor{
		"user":        e.user,
		"admin token": {UserID: "usr_admin", Role: "admin", Channel: ChannelAPI, Scope: ScopeDeploy, TokenID: "tok_1"},
		"nobody":      {},
	} {
		if _, err := e.s.AdminDBInfo(ctx, a); codeOf(t, err) != "admin_only" {
			t.Errorf("%s: info: %v", name, err)
		}
		if _, err := e.s.AdminDBRows(ctx, a, AdminDBRowsQuery{Table: "users"}); codeOf(t, err) != "admin_only" {
			t.Errorf("%s: rows: %v", name, err)
		}
		if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT 1"}); codeOf(t, err) != "admin_only" {
			t.Errorf("%s: sql: %v", name, err)
		}
	}
	if calls != 0 {
		t.Fatal("the helper ran for somebody who is not an admin")
	}

	a := adminActor()
	if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT 1", Write: true}); codeOf(t, err) != "db_read_only" {
		t.Errorf("write: %v", err)
	}
	if calls != 0 {
		t.Error("the helper ran for write")
	}
	info, err := e.s.AdminDBInfo(ctx, a)
	if err != nil || len(info.Tables) < 10 {
		t.Fatalf("info: %v", err)
	}
	if _, err := e.s.AdminDBRows(ctx, a, AdminDBRowsQuery{Table: "users", Search: "alice"}); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if rows := auditRows(t, &bkEnv{depEnv: e}, "admin.db.query"); len(rows) != 0 {
		t.Errorf("browsing was audited: %v", rows)
	}

	res, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT email, password_hash FROM users; PRAGMA table_info(users)"})
	if err != nil || res.Results[0].Rows[0][1] != nil {
		t.Fatalf("sql: %v %+v", err, res)
	}
	if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "DELETE FROM users WHERE email = 'alice@example.com'"}); codeOf(t, err) != "db_read_only" {
		t.Errorf("delete: %v", err)
	}
	if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: strings.Repeat("a", dbview.MaxSQLBytes+1)}); codeOf(t, err) != "sql_too_long" {
		t.Errorf("long sql: %v", err)
	}
	rows := auditRows(t, &bkEnv{depEnv: e}, "admin.db.query")
	if len(rows) != 2 {
		t.Fatalf("audit = %v", rows)
	}
	if rows[0]["outcome"] != "ok" || toJSON(rows[0]["statements"]) != `["select","pragma"]` {
		t.Errorf("audit ok = %v", rows[0])
	}
	if rows[1]["outcome"] != "error" || rows[1]["code"] != "db_read_only" || toJSON(rows[1]["statements"]) != `["delete"]` {
		t.Errorf("audit error = %v", rows[1])
	}
	var leaked int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'admin.db.query' AND (details LIKE '%alice%' OR details LIKE '%password_hash%' OR details LIKE '%FROM%' OR details LIKE '%users%')`).Scan(&leaked)
	if leaked != 0 {
		t.Error("the audit log has SQL text")
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestAdminDBErrors(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := adminActor()
	for _, c := range []struct {
		err  error
		code string
	}{
		{dbview.ErrTimeout, "db_timeout"},
		{dbview.ErrOutOfMemory, "db_out_of_memory"},
		{errors.New("exec: not found"), "db_helper_unavailable"},
	} {
		e.s.UseAdminDBHelper(func(context.Context, string, []byte) ([]byte, error) { return nil, c.err })
		if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT 1"}); codeOf(t, err) != c.code {
			t.Errorf("%v: %v", c.err, err)
		}
	}
	e.s.UseAdminDBHelper(func(context.Context, string, []byte) ([]byte, error) { return []byte("garbage"), nil })
	if _, err := e.s.AdminDBInfo(ctx, a); codeOf(t, err) != "db_helper_unavailable" {
		t.Errorf("garbage: %v", err)
	}
	e.s.UseAdminDBHelper(func(context.Context, string, []byte) ([]byte, error) {
		return json.Marshal(dbview.Response{Error: &dbview.Error{Code: "db_busy", Message: "locked"}})
	})
	if _, err := e.s.AdminDBInfo(ctx, a); codeOf(t, err) != "db_busy" {
		t.Errorf("helper db_busy: %v", err)
	}
}

// At most two helpers run; the next request is told db_busy, and a busy
// answer is not a query.
func TestAdminDBConcurrency(t *testing.T) {
	old := dbSlotWait
	dbSlotWait = 100 * time.Millisecond
	defer func() { dbSlotWait = old }()
	e := depTestSvc(t)
	ctx := context.Background()
	a := adminActor()
	hold := make(chan struct{})
	var mu sync.Mutex
	running, most := 0, 0
	e.s.UseAdminDBHelper(func(ctx context.Context, file string, req []byte) ([]byte, error) {
		mu.Lock()
		running++
		if running > most {
			most = running
		}
		mu.Unlock()
		<-hold
		mu.Lock()
		running--
		mu.Unlock()
		return json.Marshal(dbview.HandleControl(ctx, req, e.s.Cfg.DBPath()))
	})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT 1"}); err != nil {
				t.Errorf("run: %v", err)
			}
		}()
	}
	for i := 0; i < 200; i++ {
		mu.Lock()
		n := running
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.s.AdminDBInfo(ctx, a); codeOf(t, err) != "db_busy" {
		t.Errorf("third: %v", err)
	}
	if _, err := e.s.AdminDBSQL(ctx, a, AdminDBSQLBody{SQL: "SELECT 3"}); codeOf(t, err) != "db_busy" {
		t.Errorf("third sql: %v", err)
	}
	close(hold)
	wg.Wait()
	if most != 2 {
		t.Errorf("at most %d ran at once, want 2", most)
	}
	if _, err := e.s.AdminDBInfo(ctx, a); err != nil {
		t.Errorf("after: %v", err)
	}
	if rows := auditRows(t, &bkEnv{depEnv: e}, "admin.db.query"); len(rows) != 2 {
		t.Errorf("audit rows = %d, want 2 (the two that ran)", len(rows))
	}
}
