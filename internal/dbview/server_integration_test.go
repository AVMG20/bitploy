//go:build integration

package dbview

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Needs a Docker daemon. Starts PostgreSQL, MariaDB and MySQL containers on
// free ports of this machine and runs the viewer against each.
// Run: go test -tags=integration -count=1 -run ServerViewer ./internal/dbview/
func TestServerViewerIntegration(t *testing.T) {
	engines := []struct {
		engine, image string
		env           []string
		port          int
		user, pass    string
		setup         string
	}{
		{EnginePostgres, "postgres:17-alpine", []string{"POSTGRES_USER=app", "POSTGRES_PASSWORD=pw", "POSTGRES_DB=app"}, 5432, "app", "pw", `
CREATE TABLE items (id serial PRIMARY KEY, name text NOT NULL, price numeric(10,2), data bytea, created timestamptz DEFAULT now(), flag boolean, f real, doc json, tags text[]);
CREATE TABLE nopk (a int);
CREATE SCHEMA other; CREATE TABLE other.things (k text PRIMARY KEY, v int);
CREATE VIEW cheap AS SELECT id, name FROM items WHERE price < 10;`},
		{EngineMariaDB, "mariadb:11", []string{"MARIADB_USER=app", "MARIADB_PASSWORD=pw", "MARIADB_DATABASE=app", "MARIADB_ROOT_PASSWORD=rootpw"}, 3306, "root", "rootpw", `
CREATE TABLE items (id int AUTO_INCREMENT PRIMARY KEY, name varchar(100) NOT NULL, price decimal(10,2), data blob, created timestamp DEFAULT CURRENT_TIMESTAMP, flag tinyint(1), f float, doc json, tags text);
CREATE TABLE nopk (a int);
CREATE TABLE things (k varchar(20) PRIMARY KEY, v int);
CREATE VIEW cheap AS SELECT id, name FROM items WHERE price < 10;`},
		{EngineMySQL, "mysql:8.4", []string{"MYSQL_USER=app", "MYSQL_PASSWORD=pw", "MYSQL_DATABASE=app", "MYSQL_ROOT_PASSWORD=rootpw"}, 3306, "root", "rootpw", `
CREATE TABLE items (id int AUTO_INCREMENT PRIMARY KEY, name varchar(100) NOT NULL, price decimal(10,2), data blob, created timestamp DEFAULT CURRENT_TIMESTAMP, flag tinyint(1), f float, doc json, tags text);
CREATE TABLE nopk (a int);
CREATE TABLE things (k varchar(20) PRIMARY KEY, v int);
CREATE VIEW cheap AS SELECT id, name FROM items WHERE price < 10;`},
	}
	for _, e := range engines {
		t.Run(e.engine, func(t *testing.T) {
			t.Parallel()
			name := "bp-viewer-test-" + e.engine
			_ = exec.Command("docker", "rm", "-f", name).Run()
			args := []string{"run", "-d", "--name", name, "-p", "127.0.0.1::" + strconv.Itoa(e.port)}
			for _, v := range e.env {
				args = append(args, "-e", v)
			}
			if out, err := exec.Command("docker", append(args, e.image)...).CombinedOutput(); err != nil {
				t.Fatalf("docker run: %v %s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
			out, err := exec.Command("docker", "port", name, strconv.Itoa(e.port)).Output()
			if err != nil {
				t.Fatal(err)
			}
			line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			port, _ := strconv.Atoi(line[strings.LastIndex(line, ":")+1:])
			conn := &ServerConn{Engine: e.engine, Port: port, User: e.user, Password: e.pass, Database: "app"}

			call := func(req Request) Response {
				req.Server = conn
				raw, _ := json.Marshal(req)
				return Handle(context.Background(), raw, HelperOptions{})
			}
			must := func(req Request, out any) {
				t.Helper()
				r := call(req)
				if r.Error != nil {
					t.Fatalf("%s: %s: %s (%v)", req.Op, r.Error.Code, r.Error.Message, r.Error.Details)
				}
				if err := Decode(r.Result, out); err != nil {
					t.Fatal(err)
				}
			}
			wantErr := func(req Request, code string) *Error {
				t.Helper()
				r := call(req)
				if r.Error == nil || r.Error.Code != code {
					t.Fatalf("%s: want %s, got %+v", req.Op, code, r.Error)
				}
				return r.Error
			}

			// Wait until the server takes logins (the images restart once while they set up).
			deadline := time.Now().Add(90 * time.Second)
			for {
				r := call(Request{Op: OpSQL, SQL: "SELECT 1"})
				if r.Error == nil {
					time.Sleep(2 * time.Second)
					if call(Request{Op: OpSQL, SQL: "SELECT 1"}).Error == nil {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("server did not come up: %+v", r.Error)
				}
				time.Sleep(time.Second)
			}

			var sres SQLResult
			must(Request{Op: OpSQL, SQL: e.setup, Write: true}, &sres)

			var info Info
			must(Request{Op: OpInfo}, &info)
			if info.ServerVersion == "" || info.Engine != e.engine || !contains(info.Databases, "app") {
				t.Fatalf("info: %+v", info)
			}
			byName := map[string]Table{}
			for _, tb := range info.Tables {
				byName[tb.Name] = tb
			}
			items := byName["items"]
			if !items.Editable || items.Type != "table" || items.Rows == nil || *items.Rows != 0 {
				t.Fatalf("items: %+v", items)
			}
			if byName["cheap"].Type != "view" || byName["cheap"].Editable || byName["nopk"].Editable {
				t.Fatalf("cheap/nopk: %+v %+v", byName["cheap"], byName["nopk"])
			}
			things := "things"
			if e.engine == EnginePostgres {
				things = "other.things"
			}
			if !byName[things].Editable {
				t.Fatalf("%s not found or not editable: %v", things, info.Tables)
			}
			var idCol Column
			for _, c := range items.Columns {
				if c.Name == "id" {
					idCol = c
				}
			}
			if idCol.PK != 1 || idCol.Default == nil {
				t.Fatalf("id column: %+v", idCol)
			}

			// Insert with the defaults filling in the key and the time.
			vals := map[string]json.RawMessage{"name": raw(`"apple"`), "price": raw(`"1.50"`), "data": raw(`{"blob":"AAEC"}`), "flag": raw(`1`), "f": raw(`0.1`), "doc": raw(`"{\"a\": 1}"`)}
			if e.engine == EnginePostgres {
				vals["flag"] = raw(`"true"`)
				vals["tags"] = raw(`"{x,y}"`)
			}
			var ins InsertResult
			must(Request{Op: OpInsert, Table: "items", Values: vals}, &ins)
			if fmt.Sprint(ins.Key["id"]) != "1" {
				t.Fatalf("insert key: %+v", ins)
			}
			must(Request{Op: OpInsert, Table: "items", Values: map[string]json.RawMessage{"name": raw(`"Banana"`), "price": raw(`20`)}}, &ins)

			var rows RowsResult
			must(Request{Op: OpRows, Table: "items", Search: "APP", OrderBy: "price", Desc: true}, &rows)
			if len(rows.Rows) != 1 || rows.Total == nil || *rows.Total != 1 || rows.KeyColumns[0] != "id" {
				t.Fatalf("search: %+v", rows)
			}
			must(Request{Op: OpRows, Table: "items", Filters: []Filter{{Column: "price", Op: ">", Value: raw(`5`)}, {Column: "name", Op: "like", Value: raw(`"ban%"`)}}}, &rows)
			if len(rows.Rows) != 1 || rows.Rows[0].Values[1] != "Banana" {
				t.Fatalf("filters: %+v", rows)
			}

			// The values as listed match the row: an update with all of them works.
			must(Request{Op: OpRows, Table: "items", Filters: []Filter{{Column: "id", Op: "=", Value: raw(`1`)}}}, &rows)
			row := rows.Rows[0]
			match := map[string]json.RawMessage{}
			for i, c := range rows.Columns {
				b, _ := json.Marshal(row.Values[i])
				match[c] = b
			}
			key := map[string]json.RawMessage{"id": raw(`1`)}
			var up UpdateResult
			must(Request{Op: OpUpdate, Table: "items", Key: key, Values: map[string]json.RawMessage{"name": raw(`"pear"`)}, Match: match}, &up)
			if up.Row.Values[1] != "pear" {
				t.Fatalf("update: %+v", up)
			}
			// The old values no longer match.
			wantErr(Request{Op: OpUpdate, Table: "items", Key: key, Values: map[string]json.RawMessage{"name": raw(`"plum"`)}, Match: match}, "row_changed")
			wantErr(Request{Op: OpUpdate, Table: "items", Key: map[string]json.RawMessage{"id": raw(`99`)}, Values: map[string]json.RawMessage{"name": raw(`"x"`)}}, "row_not_found")
			wantErr(Request{Op: OpUpdate, Table: "cheap", Key: key, Values: map[string]json.RawMessage{"name": raw(`"x"`)}}, "not_editable")

			var cell CellResult
			must(Request{Op: OpCell, Table: "items", Key: key, Column: "name"}, &cell)
			if cell.Value != "pear" {
				t.Fatalf("cell: %+v", cell)
			}

			// A text key, and a key that changes.
			var tIns InsertResult
			must(Request{Op: OpInsert, Table: things, Values: map[string]json.RawMessage{"k": raw(`"a"`), "v": raw(`1`)}}, &tIns)
			must(Request{Op: OpUpdate, Table: things, Key: map[string]json.RawMessage{"k": raw(`"a"`)}, Values: map[string]json.RawMessage{"k": raw(`"b"`)}}, &up)
			if up.Row.Values[0] != "b" {
				t.Fatalf("key change: %+v", up)
			}

			var many UpdateManyResult
			must(Request{Op: OpUpdateMany, Table: "items", Keys: []map[string]json.RawMessage{{"id": raw(`1`)}, {"id": raw(`2`)}, {"id": raw(`77`)}}, Values: map[string]json.RawMessage{"price": raw(`"3.00"`)}}, &many)
			if many.Updated != 2 {
				t.Fatalf("update many: %+v", many)
			}
			var del DeleteResult
			must(Request{Op: OpDelete, Table: "items", Keys: []map[string]json.RawMessage{{"id": raw(`2`)}}}, &del)
			if del.Deleted != 1 {
				t.Fatalf("delete: %+v", del)
			}

			// The console: reads only, unless write is on.
			must(Request{Op: OpSQL, SQL: "SELECT id, name FROM items; -- a comment\nSELECT 'a;b' AS x"}, &sres)
			if len(sres.Results) != 2 || sres.Results[1].Rows[0][0] != "a;b" {
				t.Fatalf("console: %+v", sres)
			}
			wantErr(Request{Op: OpSQL, SQL: "DELETE FROM items"}, "db_read_only")
			wantErr(Request{Op: OpSQL, SQL: "WITH x AS (SELECT 1) SELECT * FROM x; UPDATE items SET name = 'x'"}, "db_read_only")
			if e.engine != EnginePostgres {
				wantErr(Request{Op: OpSQL, SQL: "SELECT 1 INTO OUTFILE '/tmp/x'"}, "db_read_only")
			} else {
				wantErr(Request{Op: OpSQL, SQL: "WITH d AS (DELETE FROM items RETURNING 1) SELECT * FROM d"}, "db_read_only")
			}
			must(Request{Op: OpSQL, SQL: "INSERT INTO items (name) VALUES ('kiwi'); SELECT count(*) FROM items", Write: true}, &sres)
			if sres.Results[0].RowsAffected == nil || *sres.Results[0].RowsAffected != 1 {
				t.Fatalf("console insert: %+v", sres.Results[0])
			}
			wantErr(Request{Op: OpSQL, SQL: "BEGIN; INSERT INTO items (name) VALUES ('lost')", Write: true}, "transaction_open")
			ee := wantErr(Request{Op: OpSQL, SQL: "SELECT 1; SELECT * FROM nothere"}, "sql_error")
			if ee.Details["statement"] != float64(2) && ee.Details["statement"] != 2 {
				t.Fatalf("statement number: %+v", ee.Details)
			}
			wantErr(Request{Op: OpRows, Table: "nothere"}, "table_not_found")

			bad := *conn
			bad.Password = "wrong"
			raw2, _ := json.Marshal(Request{Op: OpInfo, Server: &bad})
			if r := Handle(context.Background(), raw2, HelperOptions{}); r.Error == nil || r.Error.Code != "db_login_failed" {
				t.Fatalf("bad login: %+v", r.Error)
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
