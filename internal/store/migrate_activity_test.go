package store

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00510 notes the app, project and owner of the audit rows written
// before it, as far as they can still be found.
func TestMigrateActivityBackfill(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, st.DB, "migrations", 500); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, u := range []string{"usr_1", "usr_2"} {
		exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES (?, ?, 'A', 'x', 'user', 'active', 1, 1)`, u, u+"@b.c")
	}
	exec(`INSERT INTO projects (id, user_id, name, created_at, updated_at) VALUES ('prj_1', 'usr_1', 'Shop', 1, 1)`)
	exec(`INSERT INTO apps (id, user_id, slug, name, project_id, alias, created_at, updated_at) VALUES ('app_1', 'usr_1', 'shop', 'Shop', 'prj_1', 'shop', 1, 1)`)
	exec(`INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES ('dep_1', 'app_1', 1, 'running', 'files', 'api', 1)`)
	row := func(id int, user, action, targetType, targetID, details string) {
		t.Helper()
		var u any
		if user != "" {
			u = user
		}
		channel := "api"
		if strings.HasPrefix(action, "deployment.running") {
			channel = "system"
		}
		exec(`INSERT INTO audit_log (id, ts, user_id, channel, action, target_type, target_id, details) VALUES (?, 1, ?, ?, ?, ?, ?, ?)`,
			id, u, channel, action, targetType, targetID, details)
	}
	row(1, "usr_2", "env.set", "app", "app_1", `{}`)                                 // a member's change to usr_1's app
	row(2, "usr_1", "deployment.queue", "deployment", "dep_1", `{"app":"shop"}`)     // a deployment of a live app
	row(3, "usr_2", "deployment.queue", "deployment", "dep_gone", `{"app":"old"}`)   // of an app deleted since
	row(4, "usr_2", "deployment.running", "deployment", "dep_gone", `{"app":"old"}`) // bitploy deployed it for usr_2
	row(5, "usr_2", "app.delete", "app", "app_old", `{"slug":"old"}`)                // the deletion
	row(6, "", "admin.quota_set", "user", "usr_2", `{}`)                             // about usr_2's account
	row(7, "usr_1", "project.update", "project", "prj_1", `{}`)                      // about the project
	row(8, "usr_1", "token.create", "token", "tok_1", `{}`)                          // usr_1's own token
	row(9, "usr_1", "admin.settings_update", "server", "settings", `{}`)             // the server: nobody's
	row(10, "usr_1", "domain.remove", "domain", "dom_gone", `{"app_id":"app_1"}`)    // a removed domain

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[int][3]string{ // app, project, owner
		1:  {"app_1", "prj_1", "usr_1"},
		2:  {"app_1", "prj_1", "usr_1"},
		3:  {"app_old", "", "usr_2"},
		4:  {"app_old", "", "usr_2"},
		5:  {"app_old", "", "usr_2"},
		6:  {"", "", "usr_2"},
		7:  {"", "prj_1", "usr_1"},
		8:  {"", "", "usr_1"},
		9:  {"", "", ""},
		10: {"app_1", "prj_1", "usr_1"},
	}
	for id, w := range want {
		var got [3]string
		if err := st.DB.QueryRowContext(ctx, `SELECT COALESCE(app_id, ''), COALESCE(project_id, ''), COALESCE(owner_id, '') FROM audit_log WHERE id = ?`, id).
			Scan(&got[0], &got[1], &got[2]); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("row %d: app, project, owner = %v, want %v", id, got, w)
		}
	}
}

// The activity queries read the log through its indexes: a page never scans
// the whole audit_log.
func TestActivityQueriesUseIndexes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("queries/activity.sql")
	if err != nil {
		t.Fatal(err)
	}
	arg := regexp.MustCompile(`sqlc\.arg\((\w+)\)`)
	for _, block := range strings.Split(string(src), "-- name: ")[1:] {
		name, body, _ := strings.Cut(block, "\n")
		if !strings.HasPrefix(name, "ListActivity") {
			continue
		}
		var args []any
		for _, m := range arg.FindAllStringSubmatch(body, -1) {
			if m[1] == "before_id" || m[1] == "lim" {
				args = append(args, 50)
			} else {
				args = append(args, "usr_1")
			}
		}
		rows, err := st.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+arg.ReplaceAllString(body, "?"), args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		for _, p := range plan {
			if strings.HasPrefix(p, "SCAN audit_log") && !strings.Contains(p, "USING") {
				t.Errorf("%s scans the whole audit_log:\n%s", name, strings.Join(plan, "\n"))
			}
		}
		t.Logf("%s:\n%s", name, strings.Join(plan, "\n"))
	}
}
