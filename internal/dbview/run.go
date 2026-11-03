package dbview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
)

// sqliteMagic starts every SQLite database file.
const sqliteMagic = "SQLite format 3\x00"

// SQLite result codes this package tells apart (the low byte of the code).
const (
	rcBusy      = 5
	rcLocked    = 6
	rcReadonly  = 8
	rcNoMem     = 7
	rcInterrupt = 9
	rcCantOpen  = 14
	rcAuth      = 23
	rcNotADB    = 26
)

// session is one open database: a single connection, so the settings that
// belong to a connection (query_only, the authorizer, transactions) stick.
type session struct {
	ctx    context.Context
	db     *sql.DB
	conn   *sql.Conn
	write  bool
	denied string  // why the authorizer said no the last time
	vacuum bool    // a plain VACUUM runs: it attaches its own scratch database
	pol    *Policy // nil: no limits (an app's database)
}

// queryer is what a connection and a transaction have in common.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Run does the request on the database file. The file must exist; Run checks
// that it is a SQLite database. It never panics and never returns both a
// result and an error.
func Run(ctx context.Context, file string, req Request) Response {
	return runWith(ctx, file, req, nil)
}

func runWith(ctx context.Context, file string, req Request, pol *Policy) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			resp = Response{Error: errf("db_failed", fmt.Sprintf("The database helper failed: %v.", r), "Try again. If it keeps happening, tell the bitploy admin.")}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()

	result, e := run(ctx, file, req, pol)
	if e != nil {
		return Response{Error: e}
	}
	raw, err := encode(result)
	if err != nil {
		return Response{Error: errf("db_failed", "The result could not be turned into JSON: "+err.Error(), "")}
	}
	if len(raw) > MaxResponse {
		if isWrite(req) {
			// The change is committed already: say so, it must not be repeated.
			return Response{Error: errf("result_too_large", "The changes were saved, but the answer is too big to send.",
				"Do not repeat the change. Look at the rows with a smaller query.")}
		}
		return Response{Error: errf("result_too_large", "The result is too big to send.", "Ask for fewer rows or fewer columns.")}
	}
	return Response{Result: raw}
}

func run(ctx context.Context, file string, req Request, pol *Policy) (any, *Error) {
	if e := checkHeader(file); e != nil {
		return nil, e
	}
	write := isWrite(req)
	s, e := openSession(ctx, file, write, pol)
	if e != nil {
		return nil, e
	}
	defer s.close()
	switch req.Op {
	case OpInfo:
		return s.info(req.Path, file)
	case OpRows:
		return s.rows(req)
	case OpInsert:
		return s.insert(req)
	case OpUpdate:
		return s.update(req)
	case OpUpdateMany:
		return s.updateMany(req)
	case OpDelete:
		return s.delete(req)
	case OpSQL:
		return s.console(req)
	case OpCell:
		return s.cell(req)
	}
	return nil, errf("op_invalid", fmt.Sprintf("%q is not a known operation.", req.Op), "")
}

// isWrite: the request may change the database.
func isWrite(req Request) bool {
	return req.Op == OpInsert || req.Op == OpUpdate || req.Op == OpUpdateMany || req.Op == OpDelete || (req.Op == OpSQL && req.Write)
}

// checkHeader answers not_sqlite for a file that does not start like a
// database, before SQLite opens it (SQLite would accept an empty file).
func checkHeader(file string) *Error {
	f, err := os.Open(file)
	if err != nil {
		return fileErr(err)
	}
	defer f.Close()
	buf := make([]byte, len(sqliteMagic))
	if _, err := io.ReadFull(f, buf); err != nil || string(buf) != sqliteMagic {
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return fileErr(err)
		}
		return errf("not_sqlite", "This file is not a SQLite database.", "Only files that start with the SQLite header can be opened. Download the file to look at it.")
	}
	return nil
}

func fileErr(err error) *Error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return errf("db_not_found", "There is no such file in /data.", "Reload the list of files: it may have been deleted.")
	case errors.Is(err, os.ErrPermission):
		return errf("db_unreadable", "The database file cannot be read.", "Its owner or permissions do not allow it.")
	}
	return errf("db_unreadable", "The database file cannot be opened: "+err.Error(), "")
}

// dsnPath escapes what has a meaning in a SQLite file: URI.
var dsnPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

func openSession(ctx context.Context, file string, write bool, pol *Policy) (*session, *Error) {
	dsn := "file:" + dsnPath.Replace(file) + "?mode=rw&_txlock=immediate" +
		"&_pragma=busy_timeout(" + fmt.Sprint(BusyTimeout.Milliseconds()) + ")&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, classify(ctx, err, write)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, classify(ctx, err, write)
	}
	s := &session{ctx: ctx, db: db, conn: conn, write: write, pol: pol}
	// A read-only mount would not do: a WAL database needs its -shm file even
	// to be read. query_only keeps a reader from changing anything instead.
	if !write {
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only=1"); err != nil {
			s.close()
			return nil, classify(ctx, err, write)
		}
	}
	// A cheap read: a file that has the header but is damaged fails here.
	if err := s.touch(); err != nil {
		s.close()
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code()&0xff == rcReadonly {
			return nil, errf("db_unreadable", "SQLite cannot open this database: it has to write next to it (the -wal and -shm files) and may not.",
				"Check that the folder of the database can be written by the owner of the file, as the app itself needs.")
		}
		return nil, classify(ctx, err, write)
	}
	if err := conn.Raw(func(dc any) error {
		r, ok := dc.(sqlite.AuthorizerRegisterer)
		if !ok {
			return errors.New("the SQLite driver has no authorizer")
		}
		return r.RegisterAuthorizer(s.authorize)
	}); err != nil {
		s.close()
		return nil, errf("db_failed", "The database could not be locked down: "+err.Error(), "")
	}
	return s, nil
}

func (s *session) touch() error {
	rows, err := s.conn.QueryContext(s.ctx, "SELECT count(*) FROM sqlite_schema")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}

func (s *session) close() {
	_ = s.conn.Close()
	_ = s.db.Close()
}

// authorize is asked by SQLite about every operation of every statement. It
// keeps a statement from reaching outside this database file, and a reader
// from switching off query_only.
func (s *session) authorize(action sqlite.AuthorizerActionCode, arg1, arg2, _, _ string) sqlite.AuthorizerReturnCode {
	deny := func(why string) sqlite.AuthorizerReturnCode {
		s.denied = why
		return sqlite.AuthorizerDeny
	}
	if s.pol != nil {
		if rc, decided := s.pol.authorize(action, arg1, arg2); decided {
			if rc == sqlite.AuthorizerDeny {
				s.denied = s.pol.why(action, arg1)
			}
			return rc
		}
	}
	switch action {
	case sqlite.AuthAttach:
		if s.vacuum && arg1 == "" {
			return sqlite.AuthorizerOK
		}
		return deny("ATTACH")
	case sqlite.AuthDetach:
		if s.vacuum {
			return sqlite.AuthorizerOK
		}
		return deny("DETACH")
	case sqlite.AuthFunction:
		switch strings.ToLower(arg2) {
		case "load_extension", "fts3_tokenizer":
			return deny(arg2)
		}
	case sqlite.AuthPragma:
		name := strings.ToLower(arg1)
		switch name {
		case "query_only", "writable_schema", "trusted_schema", "temp_store_directory", "data_store_directory",
			"locking_mode", "hard_heap_limit", "soft_heap_limit":
			return deny("PRAGMA " + name)
		case "schema_version":
			if arg2 != "" {
				return deny("PRAGMA " + name)
			}
		case "journal_mode":
			if arg2 != "" && !s.write {
				return deny("PRAGMA " + name)
			}
		}
	}
	return sqlite.AuthorizerOK
}

var (
	codeSuffix = regexp.MustCompile(` \(\d+\)$`)
	classPref  = regexp.MustCompile(`^(SQL logic error|constraint failed|library routine called out of sequence|unknown error|access permission denied|not an error|interrupted|database is locked|attempt to write a readonly database)?: `)
)

// message is the text of a SQLite error without the code behind it.
func message(err error) string {
	m := codeSuffix.ReplaceAllString(err.Error(), "")
	if i := classPref.FindStringIndex(m); i != nil {
		m = m[i[1]:]
	}
	return m
}

// classify turns an error of the driver into the error of the contract.
func classify(ctx context.Context, err error, write bool) *Error {
	var se *sqlite.Error
	code := 0
	if errors.As(err, &se) {
		code = se.Code() & 0xff
	}
	if code == rcInterrupt || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return errf("db_timeout", "The query took too long and was stopped.", "Narrow it down with a filter or a LIMIT, or look at fewer rows.")
	}
	switch code {
	case rcBusy, rcLocked:
		return errf("db_busy", "The database is in use by the app and stayed locked for 5 seconds.", "Try again in a moment, or stop the app first.")
	case rcReadonly:
		if write {
			return errf("db_read_only", "The database file is read-only.", "Its file permissions do not allow writing.")
		}
		return errf("db_read_only", "Turn on writes to change data.", "This query was run read-only. Run it with write on if you mean to change the database.")
	case rcNoMem:
		return errf("db_out_of_memory", "The query needed more memory than the helper may use.", "Narrow the query down, or use LIMIT.")
	case rcNotADB:
		return errf("not_sqlite", "This file is not a SQLite database.", "It may be damaged or encrypted.")
	case rcCantOpen:
		return errf("db_unreadable", "The database file cannot be opened.", "Check the permissions of the file and of its folder: SQLite must be able to create its -wal and -shm files next to it.")
	}
	return errf("sql_error", message(err), "")
}

// fail is classify with what the authorizer knows.
func (s *session) fail(err error) *Error {
	var se *sqlite.Error
	if s.pol != nil && s.pol.ReadOnly && errors.As(err, &se) {
		if c := se.Code() & 0xff; c == rcReadonly || (c == rcAuth && s.denied == "a change") {
			return errf("db_read_only", "This database is read-only here: nothing in it can be changed.", "Only statements that read (SELECT, WITH, EXPLAIN, read-only PRAGMAs) run on it.")
		}
	}
	if errors.As(err, &se) && se.Code()&0xff == rcAuth {
		what := s.denied
		if what == "" {
			what = "this operation"
		}
		return errf("statement_not_allowed", "This statement is not allowed here: "+what+".",
			"ATTACH, DETACH, load_extension and PRAGMAs that change files or the safety settings of the connection are blocked.")
	}
	return classify(s.ctx, err, s.write)
}

// scanRow reads the current row into plain values.
func scanRow(rows *sql.Rows, n int) ([]any, error) {
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return vals, nil
}

// since is the time passed in milliseconds.
func since(t time.Time) int64 { return time.Since(t).Milliseconds() }
