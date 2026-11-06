package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00340 makes every variable there is readable: no name makes one
// hidden.
func TestMigrateEnvHidden(t *testing.T) {
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
	if err := goose.UpToContext(ctx, st.DB, "migrations", 330); err != nil {
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
	exec(`INSERT INTO apps (id, user_id, slug, name, type, port, volume_enabled, volume_mb, volume_name,
	        memory_mb, cpu_millis, desired_state, status, status_message, deploy_seq, created_at, updated_at)
	      VALUES ('app_web', 'usr_1', 'shop', 'Shop', 'web', 8080, 0, 1024, 'bp-shop-data',
	        256, 250, 'running', 'running', '', 1, 10, 20)`)
	exec(`INSERT INTO env_vars (app_id, name, value_enc, updated_at) VALUES ('app_web', 'STRIPE_SECRET_KEY', x'01', 5), ('app_web', 'NODE_ENV', x'02', 5)`)

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Q.ListEnvVars(ctx, "app_web")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	for _, r := range rows {
		if r.Hidden != 0 || r.UpdatedAt != 5 {
			t.Errorf("%s: hidden %d, updated_at %d", r.Name, r.Hidden, r.UpdatedAt)
		}
	}
	if err := goose.DownToContext(ctx, st.DB, "migrations", 330); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM env_vars`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("after down: %d, %v", n, err)
	}
}
