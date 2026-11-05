package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00240 puts every app in a project of its own, named after the
// app, with the slug as its short name.
func TestMigrateProjectsBackfill(t *testing.T) {
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
	if err := goose.UpToContext(ctx, st.DB, "migrations", 230); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, email, name, password_hash, role, status, created_at, updated_at) VALUES ('usr_1', 'a@b.c', 'A', 'x', 'user', 'active', 1, 1)`,
		`INSERT INTO apps (id, user_id, slug, name, type, volume_name, created_at, updated_at) VALUES ('app_AbC1', 'usr_1', 'shop', 'Shop', 'web', 'bp-shop-data', 10, 20)`,
		`INSERT INTO apps (id, user_id, slug, name, type, volume_name, created_at, updated_at) VALUES ('app_XyZ2', 'usr_1', 'jobs', 'Jobs', 'worker', 'bp-jobs-data', 11, 21)`,
	} {
		if _, err := st.DB.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := st.DB.QueryContext(ctx, `SELECT a.slug, a.alias, a.template, p.id, p.name, p.auto, p.user_id, p.created_at
		FROM apps a JOIN projects p ON p.id = a.project_id ORDER BY a.slug`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		slug, alias, tpl, pid, name string
		auto                        int64
		user                        string
		created                     int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.slug, &r.alias, &r.tpl, &r.pid, &r.name, &r.auto, &r.user, &r.created); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := []row{
		{"jobs", "jobs", "", "prj_XyZ2", "Jobs", 1, "usr_1", 11},
		{"shop", "shop", "", "prj_AbC1", "Shop", 1, "usr_1", 10},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// One short name per project.
	if _, err := st.DB.ExecContext(ctx, `UPDATE apps SET project_id = 'prj_AbC1', alias = 'shop' WHERE slug = 'jobs'`); err == nil {
		t.Error("two apps with the same alias in one project")
	}
}
