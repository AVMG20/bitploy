package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dbview"
)

// The admin viewer of bitploy's own database (the control plane), with the
// rules of dbview.ControlPolicy: read-only always, secrets always NULL. It runs
// `bitploy db-helper --control` as a process (dbview.ControlProcess), so a
// query that hangs, crashes or eats memory does not take bitploy with it.

// adminDBMax is how many of these run at once.
const adminDBMax = 2

// adminDBState counts the helper processes that run.
type adminDBState struct {
	once sync.Once
	sem  chan struct{}
	// run is the helper; nil: a process of the bitploy binary. Tests replace it.
	run func(ctx context.Context, file string, request []byte) ([]byte, error)
}

func (a *adminDBState) acquire(ctx context.Context) (release func(), ok bool) {
	a.once.Do(func() { a.sem = make(chan struct{}, adminDBMax) })
	timer := time.NewTimer(dbSlotWait)
	defer timer.Stop()
	select {
	case a.sem <- struct{}{}:
		return func() { <-a.sem }, true
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil, false
}

// UseAdminDBHelper makes the admin viewer call fn instead of starting a
// process, for the tests of the API (the test binary is not bitploy).
func (s *Service) UseAdminDBHelper(fn func(ctx context.Context, file string, request []byte) ([]byte, error)) {
	s.adb.run = fn
}

// AdminDBRowsQuery asks for a page of rows of a table of bitploy's own database.
type AdminDBRowsQuery struct {
	Table   string     `json:"table" doc:"A table or view of bitploy's database"`
	Limit   int        `json:"limit,omitempty" doc:"Rows per page, 1 to 1000 (default 100)"`
	Offset  int        `json:"offset,omitempty" doc:"Rows to skip"`
	OrderBy string     `json:"order_by,omitempty" doc:"A column to sort by (not a redacted one: 400 column_redacted)"`
	Desc    bool       `json:"desc,omitempty" doc:"Sort from high to low"`
	Filters []DBFilter `json:"filters,omitempty" doc:"Conditions, all of which must hold (not on a redacted column: 400 column_redacted)"`
	Search  string     `json:"search,omitempty" doc:"Free text, at most 200 characters, ANDed with the filters. Redacted columns are skipped."`
}

// AdminDBSQLBody runs read-only SQL on bitploy's own database.
type AdminDBSQLBody struct {
	SQL   string `json:"sql" doc:"One or more statements separated by semicolons (at most 64 KB). Only statements that read."`
	Write bool   `json:"write,omitempty" doc:"Not accepted: the database is read-only here. true answers 400 db_read_only."`
}

// codeNotStarted is the code adminDBCall gives when the helper never ran.
const codeNotStarted = "not_started"

// adminDBCall sends one request to the helper and decodes the answer into out.
// It returns the code of an error of the helper too, for the audit.
func (s *Service) adminDBCall(ctx context.Context, a Actor, req dbview.Request, out any) (code string, err error) {
	if err := RequireAdmin(a); err != nil {
		return "", err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", apperr.Internal(err)
	}
	file, err := filepath.Abs(s.Cfg.DBPath())
	if err != nil {
		return "", apperr.Internal(err)
	}
	release, ok := s.adb.acquire(ctx)
	if !ok {
		if ctx.Err() != nil {
			return codeNotStarted, filesErr(ctx.Err())
		}
		return codeNotStarted, dbTooBusyErr()
	}
	defer release()

	run := s.adb.run
	if run == nil {
		exe, err := os.Executable()
		if err != nil {
			return "", dbHelperUnavailable("bitploy cannot find its own program to open its database with.").WithCause(err)
		}
		run = func(ctx context.Context, file string, request []byte) ([]byte, error) {
			return dbview.ControlProcess(ctx, exe, nil, file, request)
		}
	}
	raw, err := run(ctx, file, body)
	if err != nil {
		return "unavailable", adminRunErr(err)
	}
	var resp dbview.Response
	if err := json.Unmarshal(raw, &resp); err != nil || (resp.Error == nil && len(resp.Result) == 0) {
		return "unavailable", dbHelperUnavailable("The database helper gave an answer bitploy could not read.").WithCause(err)
	}
	if resp.Error != nil {
		return resp.Error.Code, dbErr(resp.Error)
	}
	if err := dbview.Decode(resp.Result, out); err != nil {
		return "unavailable", dbHelperUnavailable("The database helper gave an answer bitploy could not read.").WithCause(err)
	}
	return "", nil
}

func adminRunErr(err error) error {
	switch {
	case errors.Is(err, dbview.ErrTimeout):
		return apperr.Unprocessable("db_timeout", "The database did not answer in time and the helper was stopped.",
			"Narrow the query down with a filter or a LIMIT, or look at fewer rows.")
	case errors.Is(err, dbview.ErrOutOfMemory):
		return apperr.Unprocessable("db_out_of_memory", "The query needed more memory than the database helper may use (1 GB).",
			"Narrow the query down, or use LIMIT.")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return filesErr(err)
	}
	msg := apperr.Redact(err.Error())
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300]) + "..."
	}
	return dbHelperUnavailable("bitploy could not run the database helper: " + msg).WithCause(err)
}

// AdminDBInfo lists the tables and views of bitploy's own database. Columns
// that hold secrets say redacted: true.
func (s *Service) AdminDBInfo(ctx context.Context, a Actor) (DBInfo, error) {
	var out DBInfo
	if _, err := s.adminDBCall(ctx, a, dbview.Request{Op: dbview.OpInfo}, &out); err != nil {
		return DBInfo{}, err
	}
	return out, nil
}

// AdminDBRows reads a page of rows. A secret column is NULL in it.
func (s *Service) AdminDBRows(ctx context.Context, a Actor, q AdminDBRowsQuery) (DBRows, error) {
	req := dbview.Request{Op: dbview.OpRows, Table: q.Table, Limit: q.Limit, Offset: q.Offset, OrderBy: q.OrderBy, Desc: q.Desc, Search: q.Search}
	for _, f := range q.Filters {
		req.Filters = append(req.Filters, dbview.Filter{Column: f.Column, Op: f.Op, Value: f.Value})
	}
	var out DBRows
	if _, err := s.adminDBCall(ctx, a, req, &out); err != nil {
		return DBRows{}, err
	}
	return out, nil
}

// AdminDBSQL runs read-only SQL. Every run is audited as admin.db.query: the
// kinds of statement and how it went, never the SQL text.
func (s *Service) AdminDBSQL(ctx context.Context, a Actor, b AdminDBSQLBody) (DBSQLResult, error) {
	if err := RequireAdmin(a); err != nil {
		return DBSQLResult{}, err
	}
	if b.Write {
		return DBSQLResult{}, apperr.BadRequest("db_read_only", "bitploy's own database is read-only here: write is not accepted.", "Leave write out.")
	}
	if len(b.SQL) > dbview.MaxSQLBytes {
		return DBSQLResult{}, apperr.BadRequest("sql_too_long", "The SQL is longer than 64 KB.", "Split it into several requests.")
	}
	var out DBSQLResult
	code, err := s.adminDBCall(ctx, a, dbview.Request{Op: dbview.OpSQL, SQL: b.SQL}, &out)
	outcome := "ok"
	d := map[string]any{"statements": dbview.Kinds(b.SQL)}
	if err != nil {
		outcome = "error"
		d["code"] = code
	}
	d["outcome"] = outcome
	if code != codeNotStarted { // a query that never ran is not audited
		s.Audit(ctx, a, "admin.db.query", "database", "bitploy", d)
	}
	if err != nil {
		return DBSQLResult{}, err
	}
	return out, nil
}
