package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
)

// dbValues explains the value shapes in the descriptions of the operations.
const dbValues = "Values go in and out as JSON: NULL is null, an integer within +-2^53 a number, a bigger one {\"int\": \"9007199254740993\"}, " +
	"a real a number ({\"real\": \"NaN\"} or \"Inf\" when JSON has no number for it), text a string, a blob {\"blob\": \"<base64>\", \"size\": n, \"truncated\": bool}. " +
	"In a result a text over 64 KiB is {\"text\": \"<first 64 KiB>\", \"size\": n, \"truncated\": true} and a blob holds the first 4 KiB. " +
	"A value that came back truncated can not be sent back (400 value_truncated); a text of up to 4 MiB can be sent. "

// dbRoutes registers the SQLite viewer: the databases in an app's /data. The
// access rules are those of the file browser: only the owner's own app, /data
// on and created. Reading works with every token, changing needs the deploy
// scope (the service checks it, like the file browser).
func (a *API) dbRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "getDatabase", Method: http.MethodGet, Path: "/apps/{app}/db", Tags: []string{"Database"},
		Summary: "Tables of a SQLite database in /data, or of a database app",
		Description: "Lists the tables and views of the SQLite file `path` (relative to /data, like app.db) with their columns, indexes and row counts " +
			"(`rows` is null when counting took too long). `editable` says whether rows can be changed one by one: tables with a rowid or a primary key can, views cannot. " +
			"A file that is not SQLite answers 422 not_sqlite. The database is opened read-only, in a throwaway helper container, while the app keeps running. " +
			"For a PostgreSQL, MariaDB or MySQL app (made with add_service) every operation here works on its server instead: `path` names a database on it " +
			"(empty: the one the app was made with), `databases` lists the others, a row's `key` is its primary key, and 409 db_not_running means the app is not running.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Path   string `query:"path" doc:"The database file, relative to /data. For a PostgreSQL, MariaDB or MySQL app: the name of a database on the server, empty for the one the app was made with."`
		Volume string `query:"volume" doc:"For an app with several volumes (a compose app): the volume the database is in (default: the first)"`
	}) (*Out[app.DBInfo], error) {
		return okOrNil(a.svc.DBInfo(ctx, act, in.App, in.Path, in.Volume))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "listDatabaseRows", Method: http.MethodPost, Path: "/apps/{app}/db/rows", Tags: []string{"Database"},
		Summary: "A page of rows of a table or view",
		Description: "Rows of `table` with paging, one sort column and filters (ANDed). Names are checked against the schema and values are bound parameters. " +
			"Each row has a `key` to change it later ({\"rowid\": 5}, or the primary key columns of a WITHOUT ROWID table); `key` is null for views and tables without a usable key. " +
			"`total` is the filtered count, null when counting took too long. " + dbValues,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBRowsQuery
	}) (*Out[app.DBRows], error) {
		return okOrNil(a.svc.DBRows(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "getDatabaseCell", Method: http.MethodPost, Path: "/apps/{app}/db/cell", Tags: []string{"Database"},
		Summary: "One value of a row, whole",
		Description: "Rows cut a text at 64 KiB; this reads one value of the row with `key` without that cut, so that a long text can be edited. " +
			"Answers {\"value\": ...}. A text up to 4 MiB is a plain string and a blob up to 4 MiB {\"blob\": \"<base64>\", \"size\": n, \"truncated\": false}. " +
			"Anything bigger has the normal cut-off form (a text {\"text\": ..., \"truncated\": true}, a blob its first 4 KiB). " +
			"The key rules are those of update (views have no key: 400 not_editable); 404 row_not_found and column_not_found. The read scope is enough. " + dbValues,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBCellBody
	}) (*Out[app.DBCellResult], error) {
		return okOrNil(a.svc.DBCell(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "insertDatabaseRow", Method: http.MethodPost, Path: "/apps/{app}/db/insert", Tags: []string{"Database"},
		Summary:       "Add a row to a table",
		Description:   "Needs the deploy scope. Columns left out get their default. Answers the new row and its key. " + dbValues,
		DefaultStatus: http.StatusCreated, MaxBodyBytes: maxAPIBody,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBInsertBody
	}) (*Out[app.DBInsertResult], error) {
		return okOrNil(a.svc.DBInsert(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateDatabaseRow", Method: http.MethodPost, Path: "/apps/{app}/db/update", Tags: []string{"Database"},
		Summary:      "Change a row of a table",
		Description:  "Needs the deploy scope. Changes the columns in `values` of the row with `key`; 404 row_not_found when the key matches nothing. Answers the row as it is now. " + dbValues,
		MaxBodyBytes: maxAPIBody,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBUpdateBody
	}) (*Out[app.DBUpdateResult], error) {
		return okOrNil(a.svc.DBUpdate(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "updateDatabaseRows", Method: http.MethodPost, Path: "/apps/{app}/db/update-many", Tags: []string{"Database"},
		Summary:      "Set the same values on many rows of a table",
		Description:  "Needs the deploy scope. Sets `values` on every row with one of `keys` (1 to 1000) in one transaction: all or none. With `match` (one set of values per key) a row that changed since it was loaded stops everything: 409 row_changed with `details.index`, the place of that row in `keys`. A key that is gone is skipped. Answers {\"updated\": n}. " + dbValues,
		MaxBodyBytes: maxAPIBody,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBUpdateManyBody
	}) (*Out[app.DBUpdateManyResult], error) {
		return okOrNil(a.svc.DBUpdateMany(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "deleteDatabaseRows", Method: http.MethodPost, Path: "/apps/{app}/db/delete", Tags: []string{"Database"},
		Summary:     "Delete rows of a table",
		Description: "Needs the deploy scope. Deletes the rows with these keys (1 to 1000) in one transaction: all or none. Foreign keys are enforced. This cannot be undone: make a backup first if unsure.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBDeleteBody
	}) (*Out[app.DBDeleteResult], error) {
		return okOrNil(a.svc.DBDelete(ctx, act, in.App, in.Body))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "runDatabaseSQL", Method: http.MethodPost, Path: "/apps/{app}/db/sql", Tags: []string{"Database"},
		Summary: "Run SQL on a database",
		Description: "Runs the statements in order (at most 64 KiB of SQL). With `write` false (the default) the database is read-only and a write fails with 400 db_read_only; the read scope is enough. " +
			"With `write` true each statement runs in autocommit (an explicit BEGIN and COMMIT is allowed) and the deploy scope is needed. " +
			"ATTACH, DETACH, load_extension, VACUUM INTO and PRAGMAs that change files or the safety of the connection are refused (400 statement_not_allowed). " +
			"At most 1000 rows per statement (`truncated` says there are more). When statement N fails the answer is 400 sql_error with `details`: {statement: N, results: [what ran before it]}. " + dbValues,
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body app.DBSQLBody
	}) (*Out[app.DBSQLResult], error) {
		return okOrNil(a.svc.DBSQL(ctx, act, in.App, in.Body))
	})
}

// adminDBRoutes registers the admin viewer of bitploy's own database: the
// same shapes as the app viewer, but read-only always and with every secret
// (password hashes, tokens, session ids, encrypted values, ...) reading as
// NULL. Admin session only, like the rest of /admin.
func (a *API) adminDBRoutes() {
	const secrets = "Columns that hold a secret (password hashes, token hashes, session ids, encrypted values, upload ids, GitHub App keys) have `redacted: true` in the schema and are always NULL: " +
		"in rows, in SQL, through expressions, views, joins and subqueries alike. "
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminGetDatabase", Method: http.MethodGet, Path: "/admin/db", Tags: []string{"Admin"},
		Summary: "Tables of bitploy's own database",
		Description: "Admin only. Lists the tables and views of bitploy's SQLite database with their columns, indexes and row counts, in the shape of getDatabase plus `redacted` on each column. " +
			"`editable` is always false. " + secrets,
	}, func(ctx context.Context, act app.Actor, in *struct{}) (*Out[app.DBInfo], error) {
		return okOrNil(a.svc.AdminDBInfo(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminListDatabaseRows", Method: http.MethodPost, Path: "/admin/db/rows", Tags: []string{"Admin"},
		Summary: "A page of rows of a table of bitploy's own database",
		Description: "Admin only. The body of listDatabaseRows without `path`. Rows have no key (`key` is null, `editable` false). " +
			"Search skips redacted columns; a filter or sort on one is 400 column_redacted. " + secrets + dbValues,
	}, func(ctx context.Context, act app.Actor, in *struct {
		Body app.AdminDBRowsQuery
	}) (*Out[app.DBRows], error) {
		return okOrNil(a.svc.AdminDBRows(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminRunDatabaseSQL", Method: http.MethodPost, Path: "/admin/db/sql", Tags: []string{"Admin"},
		Summary: "Run read-only SQL on bitploy's own database",
		Description: "Admin only. Like runDatabaseSQL, but nothing can be changed: `write` true is 400 db_read_only, and so is every statement that is not a read (INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, VACUUM, ANALYZE, ATTACH, PRAGMAs that set something). " +
			"sqlite_dbpage is refused. Each run is audited as admin.db.query (the kinds of statement, not the SQL). " + secrets + dbValues,
	}, func(ctx context.Context, act app.Actor, in *struct {
		Body app.AdminDBSQLBody
	}) (*Out[app.DBSQLResult], error) {
		return okOrNil(a.svc.AdminDBSQL(ctx, act, in.Body))
	})
}
