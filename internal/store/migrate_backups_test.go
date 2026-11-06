package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00300 turns on the default automatic backups of the apps whose
// schedule was never set, and leaves the ones a user chose alone.
func TestMigrateBackupDefaults(t *testing.T) {
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
	if err := goose.UpToContext(ctx, st.DB, "migrations", 290); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at)
	      VALUES ('usr_1', 'a@b.c', 'A', 'x', 'user', 'active', 1, 1)`)
	app := func(id string, volume int, template, schedule, mode string, keep int) {
		t.Helper()
		exec(`INSERT INTO apps (id, user_id, slug, name, type, volume_enabled, volume_mb, volume_name, memory_mb, cpu_millis,
		        desired_state, status, status_message, deploy_seq, created_at, updated_at,
		        template, backup_schedule, backup_keep_mode, backup_keep)
		      VALUES (?, 'usr_1', ?, ?, 'worker', ?, 1024, 'bp-'||?||'-data', 256, 250, 'running', 'running', '', 1, 10, 20, ?, ?, ?, ?)`,
			id, id, id, volume, id, template, schedule, mode, keep)
	}
	app("never", 1, "", "off", "last", 7)
	app("nodata", 0, "", "off", "last", 7)
	app("pg", 1, "postgres", "off", "last", 7)
	app("cache", 1, "valkey", "off", "last", 7)
	app("weekly", 1, "", "weekly", "last", 7)
	app("kept", 1, "", "off", "days", 30)
	app("turnedoff", 1, "", "off", "last", 7)
	exec(`INSERT INTO audit_log (ts, user_id, channel, action, target_type, target_id, details)
	      VALUES (5, 'usr_1', 'dashboard', 'app.backup_settings', 'app', 'turnedoff', '{"schedule":"off"}')`)

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		schedule, mode string
		keep, set      int
		defaulted      bool
	}{
		"never":     {"daily", "days", 14, 1, true},
		"nodata":    {"off", "last", 7, 0, false}, // gets the default when /data is turned on
		"pg":        {"daily", "days", 14, 1, true},
		"cache":     {"off", "last", 7, 0, false},
		"weekly":    {"weekly", "last", 7, 1, false},
		"kept":      {"off", "days", 30, 1, false},
		"turnedoff": {"off", "last", 7, 1, false},
	}
	for id, w := range want {
		var schedule, mode string
		var keep, set, defaultAt int64
		if err := st.DB.QueryRowContext(ctx, `SELECT backup_schedule, backup_keep_mode, backup_keep, backup_schedule_set, backup_default_at FROM apps WHERE id = ?`, id).
			Scan(&schedule, &mode, &keep, &set, &defaultAt); err != nil {
			t.Fatal(err)
		}
		if schedule != w.schedule || mode != w.mode || keep != int64(w.keep) || set != int64(w.set) || (defaultAt > 0) != w.defaulted {
			t.Errorf("%s: %s/%s/%d set=%d default_at=%d, want %+v", id, schedule, mode, keep, set, defaultAt, w)
		}
	}
}
