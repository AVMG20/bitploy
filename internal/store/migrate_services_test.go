package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 00210 turns every app into an app with one service and, with
// /data on, one volume, without changing a Docker name.
func TestMigrateAppServicesBackfill(t *testing.T) {
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
	if err := goose.UpToContext(ctx, st.DB, "migrations", 200); err != nil {
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
	exec(`INSERT INTO apps (id, user_id, slug, name, type, port, health_path, dockerfile_path, context_dir,
	        volume_enabled, volume_mb, volume_name, memory_mb, cpu_millis, desired_state, status, status_message,
	        deploy_seq, created_at, updated_at)
	      VALUES ('app_web', 'usr_1', 'shop', 'Shop', 'web', 8080, '/healthz', 'docker/Dockerfile', 'srv',
	        1, 2048, 'bp-app_web-data', 512, 500, 'running', 'running', '', 2, 10, 20)`)
	exec(`INSERT INTO apps (id, user_id, slug, name, type, port, volume_enabled, volume_mb, volume_name,
	        memory_mb, cpu_millis, desired_state, status, status_message, deploy_seq, created_at, updated_at)
	      VALUES ('app_wrk', 'usr_1', 'jobs', 'Jobs', 'worker', NULL, 0, 1024, 'bp-jobs-data',
	        256, 250, 'running', 'running', '', 1, 10, 20)`)
	exec(`INSERT INTO app_disk (app_id, image_bytes, volume_bytes, measured_at) VALUES ('app_web', 1, 4096, 1)`)
	exec(`INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, has_healthcheck, queued_at)
	      VALUES ('dep_1', 'app_web', 1, 'superseded', 'files', 'api', 'bitploy/shop:1', 'bp-shop-1', 8080, 0, 1),
	             ('dep_2', 'app_web', 2, 'running', 'files', 'api', 'bitploy/shop:2', 'bp-shop-2', 8080, 1, 2),
	             ('dep_3', 'app_wrk', 1, 'failed', 'files', 'api', NULL, NULL, NULL, 0, 3)`)
	exec(`UPDATE apps SET current_deployment_id = 'dep_2' WHERE id = 'app_web'`)
	exec(`INSERT INTO env_vars (app_id, name, value_enc, updated_at) VALUES ('app_web', 'KEY', x'01', 5)`)
	exec(`INSERT INTO metric_samples (app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes) VALUES ('app_web', 100, 1.5, 10, 1, 2)`)
	exec(`INSERT INTO metric_rollups (app_id, ts, cpu, mem_bytes, rx_bytes, tx_bytes, cpu_max, mem_max)
	      VALUES ('app_web', 0, 1.5, 10, 1, 2, 2.0, 20)`)
	exec(`INSERT INTO app_backups (id, app_id, kind, status, created_at) VALUES ('bak_1', 'app_web', 'manual', 'ready', 1)`)

	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	svcs, err := st.Q.ListAppServices(ctx, "app_web")
	if err != nil || len(svcs) != 1 {
		t.Fatalf("services = %+v, %v", svcs, err)
	}
	s := svcs[0]
	if s.Name != "app" || s.Kind != "build" || s.Public != 1 || s.Port.Int64 != 8080 || s.HealthPath != "/healthz" ||
		s.DockerfilePath != "docker/Dockerfile" || s.ContextDir != "srv" || s.MemoryMb != 512 || s.CpuMillis != 500 {
		t.Errorf("web service = %+v", s)
	}
	if w, _ := st.Q.ListAppServices(ctx, "app_wrk"); len(w) != 1 || w[0].Public != 0 || w[0].Port.Valid {
		t.Errorf("worker service = %+v", w)
	}

	vols, err := st.Q.ListAppVolumes(ctx, "app_web")
	if err != nil || len(vols) != 1 {
		t.Fatalf("volumes = %+v, %v", vols, err)
	}
	if v := vols[0]; v.Name != "data" || v.VolumeName != "bp-app_web-data" || v.SizeMb != 2048 ||
		v.Service != "app" || v.MountPath != "/data" || v.UsedBytes != 4096 {
		t.Errorf("volume = %+v", v)
	}
	if v, _ := st.Q.ListAppVolumes(ctx, "app_wrk"); len(v) != 0 {
		t.Errorf("worker without /data got volumes %+v", v)
	}

	ds, err := st.Q.ListDeploymentServices(ctx, "dep_2")
	if err != nil || len(ds) != 1 {
		t.Fatalf("deployment services = %+v, %v", ds, err)
	}
	if d := ds[0]; d.Service != "app" || d.Image.String != "bitploy/shop:2" || d.ContainerName.String != "bp-shop-2" ||
		d.Port.Int64 != 8080 || d.HasHealthcheck != 1 {
		t.Errorf("deployment service = %+v", d)
	}
	if ds, _ := st.Q.ListDeploymentServices(ctx, "dep_3"); len(ds) != 1 || ds[0].Image.Valid || ds[0].ContainerName.Valid {
		t.Errorf("failed deployment = %+v", ds)
	}

	env, err := st.Q.ListEnvVars(ctx, "app_web")
	if err != nil || len(env) != 1 || env[0].Service != "" || env[0].Name != "KEY" {
		t.Errorf("env = %+v, %v", env, err)
	}

	var layout string
	if err := st.DB.QueryRowContext(ctx, `SELECT service_layout FROM apps WHERE id = 'app_web'`).Scan(&layout); err != nil || layout != "single" {
		t.Errorf("layout = %q, %v", layout, err)
	}
	var svc string
	if err := st.DB.QueryRowContext(ctx, `SELECT service FROM metric_samples`).Scan(&svc); err != nil || svc != "app" {
		t.Errorf("sample service = %q, %v", svc, err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT service FROM metric_rollups`).Scan(&svc); err != nil || svc != "app" {
		t.Errorf("rollup service = %q, %v", svc, err)
	}
	var vol string
	if err := st.DB.QueryRowContext(ctx, `SELECT volume FROM app_backups`).Scan(&vol); err != nil || vol != "data" {
		t.Errorf("backup volume = %q, %v", vol, err)
	}
	cur, err := st.Q.OpsListCurrentContainers(ctx, "app_web")
	if err != nil || len(cur) != 2 { // the service row and the deployment row, same container
		t.Errorf("current containers = %+v, %v", cur, err)
	}
	for _, c := range cur {
		if c.ContainerName.String != "bp-shop-2" {
			t.Errorf("current container = %+v", c)
		}
	}

	// And back down.
	if err := goose.DownToContext(ctx, st.DB, "migrations", 200); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM env_vars WHERE app_id = 'app_web'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("env after down = %d, %v", n, err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollups`).Scan(&n); err != nil || n != 1 {
		t.Errorf("rollups after down = %d, %v", n, err)
	}
}
