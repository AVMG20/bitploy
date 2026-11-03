package dbview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/store"
)

// controlDB is a database made by the real migrations of bitploy, with a
// secret in every secret column. Each secret holds a marker that must never
// show up in an answer.
func controlDB(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "bitploy.db")
	st, err := store.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, email, name, password_hash, role, created_at, updated_at) VALUES ('usr_1', 'a@x.nl', 'Ann', 'SECRET-PASSWORD-HASH', 'admin', 1, 1)`,
		`INSERT INTO users (id, email, name, password_hash, role, created_at, updated_at) VALUES ('usr_2', 'b@x.nl', 'Bob', 'SECRET-PASSWORD-HASH-2', 'user', 1, 1)`,
		`INSERT INTO sessions (id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, absolute_expires_at) VALUES ('SECRET-SESSION-HASH', 'usr_1', 'SECRET-CSRF', 1, 1, 9, 9)`,
		`INSERT INTO api_tokens (id, user_id, name, prefix, hash, scope, created_at) VALUES ('tok_1', 'usr_1', 'ci', 'bp_abcd', 'SECRET-TOKEN-HASH', 'deploy', 1)`,
		`INSERT INTO invites (id, code_hash, role, expires_at, created_at) VALUES ('inv_1', 'SECRET-INVITE-HASH', 'user', 9, 1)`,
		`INSERT INTO apps (id, user_id, slug, name, created_at, updated_at, protect_hash) VALUES ('app_1', 'usr_1', 'photos', 'Photos', 1, 1, 'SECRET-PROTECT-HASH')`,
		`INSERT INTO env_vars (app_id, name, value_enc, updated_at) VALUES ('app_1', 'DB_PASSWORD', x'534543524554454e43', 1)`, // SECRETENC
		`INSERT INTO uploads (id, app_id, user_id, expires_at, created_at) VALUES ('upl_SECRET-UPLOAD-ID', 'app_1', 'usr_1', 9, 1)`,
		`INSERT INTO domains (id, app_id, hostname, txt_token, check_until, created_at) VALUES ('dom_1', 'app_1', 'x.example.nl', 'SECRET-TXT-TOKEN', 9, 1)`,
		`INSERT INTO github_app (id, github_app_id, slug, name, html_url, client_id, client_secret_enc, webhook_secret_enc, private_key_enc, created_at) VALUES (1, 7, 'bp', 'bp', 'https://x', 'Iv1.public', 'SECRET-CLIENT', 'SECRET-WEBHOOK', 'SECRET-PRIVATE-KEY', 1)`,
		`INSERT INTO idempotency_keys (user_id, key, method, path, status, body, created_at) VALUES ('usr_1', 'SECRET-IDEM-KEY', 'POST', '/x', 201, 'SECRET-IDEM-BODY', 1)`,
		`CREATE VIEW v_hashes AS SELECT email, password_hash AS ph FROM users`,
		`CREATE VIEW v_join AS SELECT u.email, s.csrf_token AS c, s.id_hash AS h FROM users u JOIN sessions s ON s.user_id = u.id`,
		`ANALYZE`, // sqlite_stat4 keeps samples of the indexed values
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

// markers are the secrets of controlDB, in every spelling a test could see.
var markers = []string{"SECRET", "534543524554", "U0VDUkVU", "53454352", "SECRETENC"}

func ctl[T any](t *testing.T, file string, req Request) T {
	t.Helper()
	resp := RunControl(context.Background(), file, req)
	if resp.Error != nil {
		t.Fatalf("%s: %s: %s", req.Op, resp.Error.Code, resp.Error.Message)
	}
	var out T
	if err := Decode(resp.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func ctlFail(t *testing.T, file string, req Request, code string) *Error {
	t.Helper()
	resp := RunControl(context.Background(), file, req)
	if resp.Error == nil {
		t.Fatalf("%s: expected %s, got %s", req.Op, code, resp.Result)
	}
	if resp.Error.Code != code {
		t.Fatalf("%s: expected %s, got %s: %s", req.Op, code, resp.Error.Code, resp.Error.Message)
	}
	return resp.Error
}

// A new column with a name like a secret must be classified: that is the list
// of secrets staying complete when the schema grows.
func TestControlSecretsCoverMigrations(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.DB.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DB.Query(`SELECT m.name, p.name FROM pragma_table_list m, pragma_table_info(m.name) p WHERE m.schema = 'main' AND m.type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	var unclassified []string
	in := func(list map[string][]string, table, col string) bool {
		for _, c := range list[table] {
			if strings.EqualFold(c, col) {
				return true
			}
		}
		return false
	}
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		have[table+"."+col] = true
		secret, safe := in(controlSecrets, table, col), in(controlSafe, table, col)
		if secret && safe {
			t.Errorf("%s.%s is listed as secret and as safe", table, col)
		}
		if LooksSecret(col) && !secret && !safe {
			unclassified = append(unclassified, table+"."+col)
		}
	}
	rows.Close()
	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Errorf("these columns have a name that looks like a secret and are not classified: %s.\n"+
			"Add each to controlSecrets (it is a credential: the admin database viewer must show NULL) or to controlSafe with a reason, in internal/dbview/secrets.go.",
			strings.Join(unclassified, ", "))
	}
	for _, list := range []map[string][]string{controlSecrets, controlSafe} {
		for table, cols := range list {
			if strings.HasPrefix(table, "sqlite_") {
				continue // made by ANALYZE, not by a migration
			}
			for _, c := range cols {
				if !have[table+"."+c] {
					t.Errorf("secrets.go lists %s.%s, which is not in the schema (renamed or dropped?)", table, c)
				}
			}
		}
	}
	// What the scan looks for.
	for _, n := range []string{"password_hash", "client_secret_enc", "csrf_token", "api_key", "Hash", "private_key", "credential"} {
		if !LooksSecret(n) {
			t.Errorf("LooksSecret(%q) = false", n)
		}
	}
	for _, n := range []string{"email", "name", "created_at", "prefix", "status", "log_tail"} {
		if LooksSecret(n) {
			t.Errorf("LooksSecret(%q) = true", n)
		}
	}
}

// Every way to read a secret gives NULL, or nothing.
func TestControlRedaction(t *testing.T) {
	file := controlDB(t)
	queries := []string{
		`SELECT * FROM users`,
		`SELECT password_hash FROM users`,
		`SELECT substr(password_hash, 1, 3), hex(password_hash), length(password_hash), typeof(password_hash), upper(password_hash), password_hash || 'x' FROM users`,
		`SELECT instr(password_hash, 'S'), password_hash IS NULL, coalesce(password_hash, 'none'), ifnull(password_hash, 'none') FROM users`,
		`SELECT json_object('h', password_hash), json_array(password_hash), json_group_array(password_hash), json_group_object(id, password_hash) FROM users`,
		`SELECT group_concat(password_hash), min(password_hash), max(password_hash), count(password_hash) FROM users`,
		`SELECT quote(password_hash), printf('%s', password_hash), CAST(password_hash AS BLOB) FROM users`,
		`SELECT * FROM (SELECT * FROM users)`,
		`SELECT u.* FROM users u JOIN sessions s ON s.user_id = u.id`,
		`SELECT * FROM users u JOIN sessions s ON s.user_id = u.id JOIN api_tokens t ON t.user_id = u.id`,
		`SELECT * FROM users NATURAL JOIN users u2`,
		`SELECT * FROM v_hashes`,
		`SELECT ph, hex(ph) FROM v_hashes`,
		`SELECT * FROM v_join`,
		`WITH t AS (SELECT password_hash FROM users) SELECT * FROM t`,
		`WITH t(a) AS (SELECT password_hash FROM users) SELECT hex(a), a FROM t`,
		`WITH RECURSIVE r(x) AS (SELECT password_hash FROM users UNION ALL SELECT x || 'a' FROM r WHERE length(x) < 3) SELECT x FROM r`,
		`SELECT (SELECT password_hash FROM users LIMIT 1)`,
		`SELECT email FROM users WHERE password_hash = 'SECRET-PASSWORD-HASH'`,
		`SELECT email FROM users WHERE password_hash LIKE 'SECRET%'`,
		`SELECT email FROM users WHERE substr(password_hash, 1, 1) = 'S'`,
		`SELECT email FROM users WHERE id IN (SELECT id FROM users WHERE password_hash LIKE 'S%')`,
		`SELECT email FROM users WHERE EXISTS (SELECT 1 FROM users u2 WHERE u2.password_hash = 'SECRET-PASSWORD-HASH')`,
		`SELECT email FROM users WHERE password_hash IS NOT NULL`,
		`SELECT email FROM users ORDER BY password_hash DESC`,
		`SELECT password_hash, count(*) FROM users GROUP BY password_hash`,
		`SELECT email FROM users WHERE password_hash BETWEEN 'A' AND 'Z'`,
		`SELECT * FROM sessions`,
		`SELECT id_hash, csrf_token FROM sessions WHERE id_hash > ''`,
		`SELECT * FROM api_tokens`,
		`SELECT hash, hex(hash) FROM api_tokens`,
		`SELECT * FROM invites`,
		`SELECT * FROM apps`,
		`SELECT protect_hash FROM apps`,
		`SELECT * FROM env_vars`,
		`SELECT value_enc, hex(value_enc), length(value_enc), CAST(value_enc AS TEXT) FROM env_vars`,
		`SELECT * FROM uploads`,
		`SELECT id FROM uploads WHERE id LIKE 'upl_%'`,
		`SELECT * FROM domains`,
		`SELECT * FROM github_app`,
		`SELECT client_secret_enc, webhook_secret_enc, private_key_enc FROM github_app`,
		`SELECT * FROM idempotency_keys`,
		`SELECT key, body FROM idempotency_keys`,
		`SELECT * FROM sqlite_stat4`,
		`SELECT hex(sample), neq, nlt FROM sqlite_stat4`,
		`SELECT json_group_array(json_object('k', key, 'b', body)) FROM idempotency_keys`,
		`SELECT t.value FROM sessions s, json_each(json_array(s.id_hash, s.csrf_token)) t`,
		`SELECT value FROM json_each((SELECT json_group_array(password_hash) FROM users))`,
		`SELECT sql FROM sqlite_schema WHERE sql LIKE '%SECRET%'`,
		`SELECT name FROM sqlite_schema WHERE name LIKE 'SECRET%'`,
		`EXPLAIN SELECT password_hash FROM users`,
	}
	for _, q := range queries {
		resp := RunControl(context.Background(), file, Request{Op: OpSQL, SQL: q})
		if resp.Error != nil {
			// A statement may be refused, but never answer with a secret.
			for _, m := range markers {
				if strings.Contains(resp.Error.Message, m) {
					t.Errorf("%s: the error shows a secret: %s", q, resp.Error.Message)
				}
			}
			continue
		}
		var res SQLResult
		if err := Decode(resp.Result, &res); err != nil {
			t.Fatal(err)
		}
		rowsJSON, _ := json.Marshal(res.Results[0].Rows) // not the sql: it holds the marker the query looks for
		out := string(rowsJSON)
		for _, m := range markers {
			if strings.Contains(out, m) {
				t.Errorf("%s: the answer has %q: %s", q, m, out)
			}
		}
	}

	// The same statements are not vacuous: ordinary columns are there.
	res := ctl[SQLResult](t, file, Request{Op: OpSQL, SQL: `SELECT email, password_hash, typeof(password_hash), length(password_hash) FROM users ORDER BY email`})
	if rows := res.Results[0].Rows; len(rows) != 2 || rows[0][0] != "a@x.nl" || rows[0][1] != nil || rows[0][2] != "null" || rows[0][3] != nil {
		t.Errorf("secret column: %v", res.Results[0].Rows)
	}
	for _, c := range []struct{ q, want string }{
		{`SELECT count(*) FROM users WHERE password_hash = 'SECRET-PASSWORD-HASH'`, "0"},
		{`SELECT count(*) FROM users WHERE password_hash IS NOT NULL`, "0"},
		{`SELECT count(password_hash) FROM users`, "0"},
		{`SELECT count(*) FROM sessions WHERE id_hash IS NULL`, "1"},
		{`SELECT count(*) FROM sqlite_stat4`, ""}, // there are samples, hidden
		{`SELECT count(*) FROM v_join WHERE h IS NULL AND c IS NULL AND email = 'a@x.nl'`, "1"},
		{`SELECT email FROM v_hashes WHERE ph IS NULL ORDER BY email LIMIT 1`, "a@x.nl"},
		{`SELECT count(*) FROM apps WHERE protect_hash <> ''`, "0"},
		{`SELECT slug FROM apps`, "photos"},
		{`SELECT client_id FROM github_app`, "Iv1.public"},
		{`SELECT prefix FROM api_tokens`, "bp_abcd"},
	} {
		r := ctl[SQLResult](t, file, Request{Op: OpSQL, SQL: c.q})
		got := fmt.Sprint(r.Results[0].Rows[0][0])
		if c.want != "" && got != c.want {
			t.Errorf("%s = %v, want %s", c.q, got, c.want)
		}
	}
	if r := ctl[SQLResult](t, file, Request{Op: OpSQL, SQL: `SELECT count(*) FROM sqlite_stat4`}); fmt.Sprint(r.Results[0].Rows[0][0]) == "0" {
		t.Log("no sqlite_stat4 rows: the sample test is empty")
	}
	// The pages of the file are not a way around the columns.
	for _, q := range []string{`SELECT data FROM sqlite_dbpage`, `SELECT count(*) FROM sqlite_dbpage`, `SELECT * FROM sqlite_dbpage WHERE pgno = 2`} {
		ctlFail(t, file, Request{Op: OpSQL, SQL: q}, "statement_not_allowed")
	}
	// The DDL stays visible.
	r := ctl[SQLResult](t, file, Request{Op: OpSQL, SQL: `SELECT sql FROM sqlite_schema WHERE name = 'users'`})
	if s, _ := r.Results[0].Rows[0][0].(string); !strings.Contains(s, "password_hash") {
		t.Errorf("sqlite_schema: %v", r.Results[0].Rows)
	}
}

// The schema marks the secrets, rows hide them, and nothing has a key.
func TestControlSchemaAndRows(t *testing.T) {
	file := controlDB(t)
	info := ctl[Info](t, file, Request{Op: OpInfo})
	redacted := map[string]bool{}
	byName := map[string]Table{}
	for _, tb := range info.Tables {
		byName[tb.Name] = tb
		if tb.Editable {
			t.Errorf("table %s is editable", tb.Name)
		}
		for _, c := range tb.Columns {
			if c.Redacted {
				redacted[tb.Name+"."+c.Name] = true
			}
		}
	}
	for table, cols := range controlSecrets {
		for _, c := range cols {
			if _, there := byName[table]; there && !redacted[table+"."+c] {
				t.Errorf("%s.%s is not marked redacted", table, c)
			}
		}
	}
	if len(redacted) == 0 || redacted["users.email"] || redacted["api_tokens.prefix"] || redacted["audit_log.token_id"] {
		t.Errorf("redacted = %v", redacted)
	}
	if info.Path != "bitploy.db" || byName["users"].Rows == nil || *byName["users"].Rows != 2 || byName["v_hashes"].Type != "view" {
		t.Errorf("info = %+v", info)
	}
	raw, _ := json.Marshal(byName["users"].Columns)
	if !strings.Contains(string(raw), `"name":"password_hash"`) || !strings.Contains(string(raw), `"redacted":true`) {
		t.Errorf("column json = %s", raw)
	}
	// "sqlite_schema" shows the DDL, secret columns included (not their values).
	if !strings.Contains(byName["users"].SQL, "password_hash") {
		t.Errorf("DDL = %s", byName["users"].SQL)
	}

	rows := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "users", OrderBy: "email", Desc: true})
	if rows.Editable || len(rows.KeyColumns) != 0 || len(rows.Rows) != 2 || rows.Total == nil || *rows.Total != 2 {
		t.Errorf("rows = %+v", rows)
	}
	idx := -1
	for i, c := range rows.Columns {
		if c == "password_hash" {
			idx = i
		}
	}
	for _, r := range rows.Rows {
		if r.Key != nil || r.Values[idx] != nil {
			t.Errorf("row = %+v", r)
		}
	}
	if rows.Rows[0].Values[1] != "b@x.nl" {
		t.Errorf("sorted rows = %+v", rows.Rows)
	}
	// A view is read through the policy too.
	vr := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "v_hashes"})
	if vr.Rows[0].Values[1] != nil {
		t.Errorf("view rows = %+v", vr.Rows)
	}

	// Filtering or sorting by a secret is refused, not answered with NULLs.
	ctlFail(t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "password_hash", Op: "=", Value: json.RawMessage(`"x"`)}}}, "column_redacted")
	ctlFail(t, file, Request{Op: OpRows, Table: "users", Filters: []Filter{{Column: "PASSWORD_HASH", Op: "is null"}}}, "column_redacted")
	ctlFail(t, file, Request{Op: OpRows, Table: "sessions", OrderBy: "id_hash"}, "column_redacted")
	// ... and a search skips them: it finds email and name, never the secrets.
	total := func(search string) int64 {
		r := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "users", Search: search})
		return *r.Total
	}
	if total("SECRET") != 0 || total("PASSWORD") != 0 || total("SECRET-PASSWORD-HASH-2") != 0 {
		t.Error("a search found a secret")
	}
	if total("ann") != 1 || total("x.nl") != 2 {
		t.Errorf("search of visible columns: ann=%d x.nl=%d", total("ann"), total("x.nl"))
	}
	if r := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "uploads", Search: "upl_"}); *r.Total != 0 {
		t.Errorf("search found the id of an upload: %d", *r.Total)
	}
	if r := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "uploads", Search: "app_1"}); *r.Total != 1 || r.Rows[0].Values[0] != nil {
		t.Errorf("upload rows = %+v", r.Rows)
	}
	// A table without any visible column to search finds nothing, without an error.
	if r := ctl[RowsResult](t, file, Request{Op: OpRows, Table: "github_app", Search: "SECRET"}); *r.Total != 0 {
		t.Errorf("github_app search: %d", *r.Total)
	}
	for _, table := range []string{"idempotency_keys", "env_vars", "invites", "api_tokens", "sessions", "domains", "apps"} {
		r := ctl[RowsResult](t, file, Request{Op: OpRows, Table: table})
		raw, _ := json.Marshal(r)
		for _, m := range markers {
			if strings.Contains(string(raw), m) {
				t.Errorf("rows of %s have %q", table, m)
			}
		}
	}
}

// Nothing changes: not by the console, not by an operation, not with write.
func TestControlReadOnly(t *testing.T) {
	file := controlDB(t)
	before := func() string {
		db, err := sql.Open("sqlite", "file:"+file+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var s string
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM users) || ',' || (SELECT group_concat(password_hash) FROM users) || ',' || (SELECT count(*) FROM sqlite_schema) || ',' || (SELECT user_version FROM pragma_user_version)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	was := before()
	for _, q := range []string{
		`INSERT INTO users (id, email, name, password_hash, created_at, updated_at) VALUES ('u', 'c@x.nl', 'C', 'h', 1, 1)`,
		`UPDATE users SET name = 'X'`,
		`UPDATE users SET password_hash = 'mine'`,
		`DELETE FROM users`,
		`DROP TABLE users`,
		`CREATE TABLE t (a)`,
		`CREATE TEMP TABLE t (a)`,
		`CREATE VIEW vv AS SELECT 1`,
		`CREATE TEMP VIEW vv AS SELECT password_hash FROM users`,
		`CREATE INDEX i ON users(name)`,
		`CREATE TRIGGER tr AFTER INSERT ON users BEGIN SELECT 1; END`,
		`ALTER TABLE users ADD COLUMN x`,
		`ALTER TABLE users RENAME TO people`,
		`REINDEX`,
		`ANALYZE`,
		`VACUUM`,
		`VACUUM INTO '/tmp/bitploy-copy.db'`,
		`ATTACH ':memory:' AS m`,
		`PRAGMA user_version = 5`,
		`PRAGMA writable_schema = 1`,
		`PRAGMA journal_mode = DELETE`,
		`PRAGMA query_only = 0`,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
		`PRAGMA optimize`,
		`PRAGMA integrity_check`,
		`REPLACE INTO users (id, email, name, password_hash, created_at, updated_at) VALUES ('usr_1', 'a@x.nl', 'A', 'h', 1, 1)`,
		`BEGIN; DELETE FROM users; COMMIT`,
		`SELECT load_extension('x')`,
		`WITH x AS (SELECT 1) DELETE FROM users`,
	} {
		resp := RunControl(context.Background(), file, Request{Op: OpSQL, SQL: q})
		if resp.Error == nil {
			t.Errorf("%s: it ran: %s", q, resp.Result)
		}
	}
	ctlFail(t, file, Request{Op: OpSQL, SQL: `DELETE FROM users`}, "db_read_only")
	ctlFail(t, file, Request{Op: OpSQL, SQL: `SELECT 1`, Write: true}, "db_read_only")
	for _, op := range []string{OpInsert, OpUpdate, OpDelete, OpUpdateMany, OpCell, "nope"} {
		ctlFail(t, file, Request{Op: op, Table: "users", Values: map[string]json.RawMessage{"name": json.RawMessage(`"x"`)}, Key: map[string]json.RawMessage{"rowid": json.RawMessage("1")}}, "db_read_only")
	}
	if now := before(); now != was {
		t.Errorf("the database changed: %q -> %q", was, now)
	}
	// Reading still works afterwards, with the pragmas the viewer needs.
	ctl[SQLResult](t, file, Request{Op: OpSQL, SQL: `SELECT name FROM pragma_table_info('users'); SELECT * FROM pragma_index_list('users'); SELECT sqlite_version(); PRAGMA table_info(users)`})
}

func TestControlHandle(t *testing.T) {
	file := controlDB(t)
	resp := HandleControl(context.Background(), []byte(`{"op":"sql","sql":"SELECT email FROM users ORDER BY email","path":"../../etc/passwd"}`), file)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"a@x.nl"`) {
		t.Fatalf("handle: %+v %s", resp.Error, resp.Result)
	}
	if r := HandleControl(context.Background(), []byte(`{"op":"sql","sql":"SELECT 1"}`), filepath.Join(t.TempDir(), "none.db")); r.Error == nil || r.Error.Code != "db_not_found" {
		t.Errorf("missing file: %+v", r.Error)
	}
	if r := HandleControl(context.Background(), []byte(`nope`), file); r.Error == nil || r.Error.Code != "request_invalid" {
		t.Errorf("bad json: %+v", r.Error)
	}
}

// TestControlHelperProcess is not a test: ControlProcess runs the test binary
// as the helper (`<test binary> -test.run=... -- db-helper --control <file> -`).
func TestControlHelperProcess(t *testing.T) {
	for i, a := range os.Args {
		if a == "db-helper" {
			os.Exit(HelperMain(os.Args[i+1:], os.Stdin, os.Stdout))
		}
	}
}

var helperPre = []string{"-test.run=^TestControlHelperProcess$", "--"}

func process(t *testing.T, file string, req Request) (Response, error) {
	t.Helper()
	body, _ := json.Marshal(req)
	raw, err := ControlProcess(context.Background(), os.Args[0], helperPre, file, body)
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("the helper printed %q", raw)
	}
	return resp, nil
}

// The helper as a process of its own: the same answers, the same secrets hidden.
func TestControlProcess(t *testing.T) {
	file := controlDB(t)
	resp, err := process(t, file, Request{Op: OpSQL, SQL: `SELECT email, password_hash, hex(id_hash) FROM users, sessions WHERE users.id = sessions.user_id`})
	if err != nil || resp.Error != nil {
		t.Fatalf("process: %v %+v", err, resp.Error)
	}
	if s := string(resp.Result); !strings.Contains(s, `["a@x.nl",null,""]`) || strings.Contains(s, "SECRET-") && !strings.Contains(s, `"sql":`) {
		t.Errorf("result = %s", s)
	}
	resp, err = process(t, file, Request{Op: OpInfo})
	var info Info
	if err != nil || resp.Error != nil || Decode(resp.Result, &info) != nil || info.Path != "bitploy.db" || len(info.Tables) < 10 {
		t.Fatalf("info: %v %+v", err, resp.Error)
	}
	// Errors travel as errors of the contract.
	if resp, err = process(t, file, Request{Op: OpSQL, SQL: `DELETE FROM users`}); err != nil || resp.Error == nil || resp.Error.Code != "db_read_only" {
		t.Errorf("write: %v %+v", err, resp.Error)
	}
	if resp, err = process(t, file, Request{Op: OpSQL, SQL: `SELECT * FROM sqlite_dbpage`}); err != nil || resp.Error == nil || resp.Error.Code != "statement_not_allowed" {
		t.Errorf("dbpage: %v %+v", err, resp.Error)
	}
	if resp, err = process(t, filepath.Join(t.TempDir(), "none.db"), Request{Op: OpInfo}); err != nil || resp.Error == nil || resp.Error.Code != "db_not_found" {
		t.Errorf("missing: %v %+v", err, resp.Error)
	}
	// The files are as they were: the helper left no -wal or -shm of its own behind
	// that would stay owned by another user (it is the same user here), and the data is whole.
	if _, err := os.Stat(file); err != nil {
		t.Error(err)
	}
}

// A query that never ends is killed, and so is one that prints too much.
func TestControlProcessLimits(t *testing.T) {
	file := controlDB(t)
	oldQ, oldG, oldR := QueryTimeout, ProcessGrace, MaxResponse
	defer func() { QueryTimeout, ProcessGrace, MaxResponse = oldQ, oldG, oldR }()

	QueryTimeout, ProcessGrace = 0, 300*time.Millisecond
	start := time.Now()
	// The helper process has the real 15 s of its own; the grace ends it first.
	_, err := process(t, file, Request{Op: OpSQL, SQL: `WITH RECURSIVE r(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM r) SELECT count(*) FROM r`})
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 5*time.Second {
		t.Errorf("endless query: %v after %v", err, time.Since(start))
	}
	QueryTimeout, ProcessGrace = oldQ, oldG

	MaxResponse = 100
	if r, err := process(t, file, Request{Op: OpSQL, SQL: `SELECT hex(randomblob(40000)) FROM pragma_function_list LIMIT 5`}); err == nil || !strings.Contains(err.Error(), "more than it may") {
		t.Errorf("long answer: %v %+v len=%d", err, r.Error, len(r.Result))
	}
	MaxResponse = oldR

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ControlProcess(ctx, os.Args[0], helperPre, file, []byte(`{"op":"info"}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	// A helper that fails to start: an error, not a panic.
	if _, err := ControlProcess(context.Background(), filepath.Join(t.TempDir(), "nope"), nil, file, []byte(`{}`)); err == nil {
		t.Error("a missing program ran")
	}
}
