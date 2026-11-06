//go:build integration

package app

// Database dumps on a real Docker daemon:
//
//	go test -tags=integration -count=1 -run TestIntegrationDatabaseDumps ./internal/app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Each database service is dumped while it runs, changed, and restored from
// the dump; the data from before the change is back and the change is kept
// in the before-restore dump. Valkey gets a snapshot and a copy of its files.
func TestIntegrationDatabaseDumps(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	pgSQL := func(sql string) string {
		return pgLogin + `psql -X -q -t -A -v ON_ERROR_STOP=1 -d "$POSTGRES_DB" -c "` + sql + `"`
	}
	my := func(client, passVar, dbVar string) func(string) string {
		return func(sql string) string {
			return mysqlScript(passVar, client+` --defaults-extra-file="$f" -N -B "$`+dbVar+`" -e "`+sql+`"`)
		}
	}
	cases := []struct {
		tpl, tool string
		sql       func(string) string
	}{
		{"postgres", "pg_dumpall", pgSQL},
		{"pgvector", "pg_dumpall", pgSQL},
		{"mariadb", "mariadb-dump", my("mariadb", "MARIADB_ROOT_PASSWORD", "MARIADB_DATABASE")},
		{"mysql", "mysqldump", my("mysql", "MYSQL_ROOT_PASSWORD", "MYSQL_DATABASE")},
	}
	for _, c := range cases {
		t.Run(c.tpl, func(t *testing.T) {
			p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "dump " + c.tpl + " " + e.suffix})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = e.s.DeleteProject(ctx, e.user, p.ID, p.Name, true) }()
			sv, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: c.tpl})
			if err != nil {
				t.Fatal(err)
			}
			e.slugs = append(e.slugs, sv.Slug)
			if d := e.waitService(t, sv.Slug); d.State != "running" {
				t.Fatalf("%s %+v", d.State, d.Error)
			}
			row, err := e.s.LoadApp(ctx, e.user, sv.Slug, false)
			if err != nil {
				t.Fatal(err)
			}
			run := e.s.runner()
			query := func(sql string) string {
				t.Helper()
				var out bytes.Buffer
				if tail, err := run.Exec(ctx, row, c.sql(sql), nil, &out); err != nil {
					t.Fatalf("%s: %v %s", sql, err, tail)
				}
				return strings.TrimSpace(out.String())
			}
			query("CREATE TABLE t (v varchar(10)); INSERT INTO t VALUES ('one')")

			b, err := e.s.CreateBackup(ctx, e.user, sv.Slug)
			if err != nil {
				t.Fatal(err)
			}
			b = waitBackup(t, e, sv.Slug, b.ID)
			if b.Format != "dump" || !strings.Contains(b.Tool, c.tool) {
				t.Fatalf("backup = %+v", b)
			}

			query("INSERT INTO t VALUES ('two')")
			if _, err := e.s.RestoreBackup(ctx, e.user, sv.Slug, b.ID); err != nil {
				t.Fatal(err)
			}
			var r *BackupRestore
			waitFor(t, 5*time.Minute, "restore", func() bool {
				l, _ := e.s.ListBackups(ctx, e.user, sv.Slug)
				r = l.Restore
				return r != nil && r.State != "running"
			})
			if r.State != "done" {
				t.Fatalf("restore = %+v", r)
			}
			if got := query("SELECT v FROM t ORDER BY v"); got != "one" {
				t.Errorf("after restore: %q", got)
			}
			l, _ := e.s.ListBackups(ctx, e.user, sv.Slug)
			var safe *Backup
			for i := range l.Items {
				if l.Items[i].Kind == BackupBeforeRestore {
					safe = &l.Items[i]
				}
			}
			if safe == nil || safe.Format != "dump" || safe.Status != "ready" {
				t.Fatalf("before-restore = %+v", safe)
			}
			// Undo: the before-restore dump has both rows.
			if _, err := e.s.RestoreBackup(ctx, e.user, sv.Slug, safe.ID); err != nil {
				t.Fatal(err)
			}
			waitFor(t, 5*time.Minute, "undo", func() bool {
				l, _ := e.s.ListBackups(ctx, e.user, sv.Slug)
				r = l.Restore
				return r != nil && r.State != "running" && r.BackupID == safe.ID
			})
			if r.State != "done" {
				t.Fatalf("undo = %+v", r)
			}
			if got := query("SELECT v FROM t ORDER BY v"); got != "one\ntwo" {
				t.Errorf("after undo: %q", got)
			}
		})
	}

	t.Run("valkey", func(t *testing.T) {
		p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "dump valkey " + e.suffix})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = e.s.DeleteProject(ctx, e.user, p.ID, p.Name, true) }()
		sv, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey"})
		if err != nil {
			t.Fatal(err)
		}
		e.slugs = append(e.slugs, sv.Slug)
		if d := e.waitService(t, sv.Slug); d.State != "running" {
			t.Fatalf("%s %+v", d.State, d.Error)
		}
		b, err := e.s.CreateBackup(ctx, e.user, sv.Slug)
		if err != nil {
			t.Fatal(err)
		}
		if b = waitBackup(t, e, sv.Slug, b.ID); b.Format != "volume" {
			t.Fatalf("backup = %+v", b)
		}
	})
}

func waitBackup(t *testing.T, e *itEnv, slug, id string) Backup {
	t.Helper()
	var got Backup
	waitFor(t, 5*time.Minute, "backup", func() bool {
		l, err := e.s.ListBackups(context.Background(), e.user, slug)
		if err != nil {
			return false
		}
		for _, it := range l.Items {
			if it.ID == id {
				got = it
				return it.Status != "running"
			}
		}
		return false
	})
	if got.Status != "ready" {
		t.Fatalf("backup = %+v", got)
	}
	return got
}
