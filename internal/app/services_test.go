package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/store/db"
)

func names(specs []serviceSpec) string {
	out := make([]string, len(specs))
	for i, sp := range specs {
		out[i] = sp.Name
	}
	return strings.Join(out, ",")
}

func TestStartOrder(t *testing.T) {
	dep := func(s string) []dependency { return []dependency{{Service: s, Condition: condStarted}} }
	got, err := startOrder([]serviceSpec{
		{Name: "web", DependsOn: append(dep("api"), dep("cache")...)},
		{Name: "api", DependsOn: dep("db")},
		{Name: "db"},
		{Name: "cache"},
	})
	if err != nil || names(got) != "db,api,cache,web" {
		t.Fatalf("order = %s, %v", names(got), err)
	}
	// Without dependencies the order stays as stored.
	if got, _ := startOrder([]serviceSpec{{Name: "b"}, {Name: "a"}}); names(got) != "b,a" {
		t.Errorf("order = %s", names(got))
	}
	_, err = startOrder([]serviceSpec{{Name: "a", DependsOn: dep("b")}, {Name: "b", DependsOn: dep("a")}})
	if err == nil || !strings.Contains(err.Error(), "circle: a -> b -> a") {
		t.Errorf("cycle: %v", err)
	}
	_, err = startOrder([]serviceSpec{{Name: "web", DependsOn: dep("db")}})
	if err == nil || !strings.Contains(err.Error(), "web depends on db") {
		t.Errorf("unknown dependency: %v", err)
	}
}

func TestServiceNaming(t *testing.T) {
	single := db.App{Slug: "shop", ServiceLayout: layoutSingle}
	multi := db.App{Slug: "shop", ServiceLayout: layoutMulti}
	for _, c := range []struct{ got, want string }{
		{containerNameOf(single, singleService, 7), "bp-shop-7"},
		{imageNameOf(single, singleService, 7), "bitploy/shop:7"},
		{containerNameOf(multi, "web", 7), "bp-shop_web-7"},
		{imageNameOf(multi, "web", 7), "bitploy/shop_web:7"},
		{dockerx.ServiceVolumeName("shop", "pgdata"), "bp-shop_pgdata"},
	} {
		if c.got != c.want {
			t.Errorf("name = %s, want %s", c.got, c.want)
		}
	}
}

func TestServiceConfigHash(t *testing.T) {
	rs := dockerx.RunSpec{Env: []string{"B=2", "A=1"}, Network: "bp-shop", Aliases: []string{"web"}, MemoryMB: 256,
		Mounts: []dockerx.VolumeMount{{Source: "v2", Target: "/b"}, {Source: "v1", Target: "/a"}}}
	same := rs
	same.Env = []string{"A=1", "B=2"}
	same.Mounts = []dockerx.VolumeMount{{Source: "v1", Target: "/a"}, {Source: "v2", Target: "/b"}}
	if serviceConfigHash("sha256:1", rs) != serviceConfigHash("sha256:1", same) {
		t.Error("order of env and mounts changed the hash")
	}
	changed := rs
	changed.Env = []string{"A=1", "B=3"}
	for name, h := range map[string]string{
		"env":   serviceConfigHash("sha256:1", changed),
		"image": serviceConfigHash("sha256:2", rs),
	} {
		if h == serviceConfigHash("sha256:1", rs) {
			t.Errorf("a new %s kept the hash", name)
		}
	}
}

// seedMulti turns app_1 of an ops test into an app with two services: web
// (public, port 8080, mounts uploads) and worker (depends on web, mounts
// pgdata), with a current deployment that ran both.
func seedMulti(t *testing.T, e *opsEnv) db.App {
	t.Helper()
	ctx := context.Background()
	e.addApp(t, "app_1", "duo", "usr_owner", "web")
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, queued_at)
		VALUES ('dep_seed', 'app_1', 3, 'running', 'files', 'api', 'bitploy/duo_web:3', 'bp-duo_web-3', 8080, 1)`)
	e.exec(t, `UPDATE apps SET service_layout = 'multi' WHERE id = 'app_1'`)
	e.exec(t, `DELETE FROM app_services WHERE app_id = 'app_1'`)
	e.exec(t, `DELETE FROM app_volumes WHERE app_id = 'app_1'`)
	q := e.svc.Q
	for i, sv := range []db.InsertAppServiceParams{
		{Name: "web", Kind: "build", Public: 1, Port: sql.NullInt64{Int64: 8080, Valid: true}, HealthPath: "/health", MemoryMb: 256, CpuMillis: 500},
		{Name: "worker", Kind: "image", Image: "busybox:1.36", MemoryMb: 128, CpuMillis: 250, DependsOn: `[{"service":"web","condition":"service_started"}]`},
	} {
		sv.AppID, sv.Position, sv.CreatedAt, sv.UpdatedAt = "app_1", int64(i), 1, 1
		if err := q.InsertAppService(ctx, sv); err != nil {
			t.Fatal(err)
		}
	}
	for i, v := range []db.InsertAppVolumeParams{
		{Name: "uploads", VolumeName: "bp-duo_uploads", SizeMb: 100, Service: "web", MountPath: "/data"},
		{Name: "pgdata", VolumeName: "bp-duo_pgdata", SizeMb: 300, Service: "worker", MountPath: "/var/lib/data"},
	} {
		v.AppID, v.Position, v.CreatedAt = "app_1", int64(i), 1
		if err := q.InsertAppVolume(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, ds := range []db.UpsertDeploymentServiceParams{
		{Service: "web", Image: sql.NullString{String: "bitploy/duo_web:3", Valid: true}, ContainerName: sql.NullString{String: "bp-duo_web-3", Valid: true}, Port: sql.NullInt64{Int64: 8080, Valid: true}},
		// Unchanged since deployment 2, so its container was kept.
		{Service: "worker", Image: sql.NullString{String: "busybox:1.36", Valid: true}, ContainerName: sql.NullString{String: "bp-duo_worker-2", Valid: true}},
	} {
		ds.DeploymentID = "dep_seed"
		if err := q.UpsertDeploymentService(ctx, ds); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncMultiTotals(ctx, q, "app_1"); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetApp(ctx, "app_1")
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestMultiAppView(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	row := seedMulti(t, e)
	if row.MemoryMb != 384 || row.CpuMillis != 750 || row.VolumeMb != 400 || row.VolumeEnabled != 1 {
		t.Errorf("totals: memory %d, cpu %d, volume %d (%d)", row.MemoryMb, row.CpuMillis, row.VolumeMb, row.VolumeEnabled)
	}
	ap, err := e.svc.GetApp(ctx, e.owner, "duo")
	if err != nil {
		t.Fatal(err)
	}
	if len(ap.Services) != 2 || len(ap.Volumes) != 2 {
		t.Fatalf("services %+v, volumes %+v", ap.Services, ap.Volumes)
	}
	web, worker := ap.Services[0], ap.Services[1]
	if web.Name != "web" || !web.Public || web.Port == nil || *web.Port != 8080 || strings.Join(web.Volumes, ",") != "uploads" || web.Container != "bp-duo_web-3" {
		t.Errorf("web = %+v", web)
	}
	if worker.Name != "worker" || worker.Public || worker.Kind != "image" || worker.Image != "busybox:1.36" ||
		strings.Join(worker.DependsOn, ",") != "web" || worker.Container != "bp-duo_worker-2" {
		t.Errorf("worker = %+v", worker)
	}
	if v := ap.Volumes[1]; v.Name != "pgdata" || v.Service != "worker" || v.MountPath != "/var/lib/data" || v.SizeMB != 300 {
		t.Errorf("volume = %+v", v)
	}
	// The route points at the public service.
	d, _ := e.svc.Q.GetDeployment(ctx, "dep_seed")
	if up := e.svc.publicUpstream(ctx, row, d); up != "http://bp-duo_web-3:8080" {
		t.Errorf("upstream = %q", up)
	}
	// Containers kept from an older deployment count as current.
	cur := e.svc.currentContainers(ctx, row)
	if cur["web"] != "bp-duo_web-3" || cur["worker"] != "bp-duo_worker-2" || len(cur) != 2 {
		t.Errorf("current = %v", cur)
	}
}

func TestSingleAppViewKeepsOneService(t *testing.T) {
	e := newOpsEnv(t)
	e.addApp(t, "app_1", "solo", "usr_owner", "web")
	e.exec(t, `UPDATE apps SET volume_enabled = 1, volume_mb = 200, volume_name = 'bp-solo-data' WHERE id = 'app_1'`)
	if err := syncSingleLayout(context.Background(), e.svc.Q, "app_1"); err != nil {
		t.Fatal(err)
	}
	ap, err := e.svc.GetApp(context.Background(), e.owner, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if len(ap.Services) != 1 || ap.Services[0].Name != "app" || !ap.Services[0].Public || ap.Services[0].MemoryMB != 512 {
		t.Errorf("services = %+v", ap.Services)
	}
	if len(ap.Volumes) != 1 || ap.Volumes[0].Name != "data" || ap.Volumes[0].MountPath != "/data" || ap.Volumes[0].SizeMB != 200 {
		t.Errorf("volumes = %+v", ap.Volumes)
	}
}

func TestPickServiceErrors(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	row := seedMulti(t, e)

	_, _, err := e.svc.pickService(ctx, row, "db")
	wantCode(t, err, "service_not_found")
	var ae *apperr.Error
	errors.As(err, &ae)
	if !strings.Contains(ae.Message, `no service named "db"`) || !strings.Contains(ae.Message, "web, worker") || ae.Next != "get_app" {
		t.Errorf("error = %+v", ae)
	}
	if sp, _, err := e.svc.pickService(ctx, row, " worker "); err != nil || sp == nil || sp.Name != "worker" {
		t.Errorf("worker: %+v, %v", sp, err)
	}

	_, ok, err := e.svc.pickVolume(ctx, row, "nope")
	wantCode(t, err, "volume_not_found")
	if ok {
		t.Error("ok for an unknown volume")
	}
	if v, ok, err := e.svc.pickVolume(ctx, row, ""); err != nil || !ok || v.Name != "uploads" || v.VolumeName != "bp-duo_uploads" {
		t.Errorf("first volume: %+v, %v, %v", v, ok, err)
	}

	// Restart, logs and metrics refuse an unknown service the same way.
	_, err = e.svc.RestartApp(ctx, e.owner, "duo", "db")
	wantCode(t, err, "service_not_found")
	_, err = e.svc.Logs(ctx, e.owner, "duo", LogOptions{Kind: "runtime", Service: "db"})
	wantCode(t, err, "service_not_found")
	_, err = e.svc.GetMetrics(ctx, e.owner, "duo", "1h", "db")
	wantCode(t, err, "service_not_found")
	_, err = e.svc.UpdateApp(ctx, e.owner, "duo", UpdateAppInput{MemoryMB: ptr(int64(512))})
	wantCode(t, err, "set_per_service")

	// A single-service app accepts its own service name and nothing else.
	e.addApp(t, "app_2", "solo", "usr_owner", "web")
	solo, _ := e.svc.Q.GetApp(ctx, "app_2")
	if sp, _, err := e.svc.pickService(ctx, solo, "app"); err != nil || sp == nil {
		t.Errorf("app: %v", err)
	}
	_, _, err = e.svc.pickService(ctx, solo, "web")
	wantCode(t, err, "service_not_found")
}

func TestLogContainerPerService(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	row := seedMulti(t, e)
	for _, c := range []struct{ service, name, svc string }{
		{"", "bp-duo_web-3", "web"}, // the public service
		{"web", "bp-duo_web-3", "web"},
		{"worker", "bp-duo_worker-2", "worker"},
	} {
		name, svc, err := e.svc.logContainer(ctx, row, c.service)
		if err != nil || name != c.name || svc != c.svc {
			t.Errorf("service %q: %s %s %v", c.service, name, svc, err)
		}
	}
	// No public service: the first one.
	e.exec(t, `UPDATE app_services SET public = 0 WHERE app_id = 'app_1'`)
	if name, _, _ := e.svc.logContainer(ctx, row, ""); name != "bp-duo_web-3" {
		t.Errorf("no public service: %s", name)
	}

	// A single-service app keeps reading its deployment's container, with
	// lines that do not name a service.
	e.addApp(t, "app_2", "solo", "usr_owner", "web")
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, queued_at)
		VALUES ('dep_solo', 'app_2', 1, 'running', 'files', 'api', 'bitploy/solo:1', 'bp-solo-1', 8080, 1)`)
	e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_solo' WHERE id = 'app_2'`)
	solo, _ := e.svc.Q.GetApp(ctx, "app_2")
	if name, svc, err := e.svc.logContainer(ctx, solo, ""); err != nil || name != "bp-solo-1" || svc != "" {
		t.Errorf("single: %s %q %v", name, svc, err)
	}
}

func TestGetMetricsPerService(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	seedMulti(t, e)
	for i := 0; i < 3; i++ {
		ts := e.now.Unix() - int64(i)*15
		e.exec(t, `INSERT INTO metric_samples (app_id, service, ts, cpu, mem_bytes, rx_bytes, tx_bytes) VALUES ('app_1', 'web', ?, 10, ?, 1, 2)`, ts, 100<<20)
		e.exec(t, `INSERT INTO metric_samples (app_id, service, ts, cpu, mem_bytes, rx_bytes, tx_bytes) VALUES ('app_1', 'worker', ?, 5, ?, 3, 4)`, ts, 50<<20)
	}
	all, err := e.svc.GetMetrics(ctx, e.owner, "duo", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if all.Service != "" || all.MemoryLimitMB != 384 || len(all.Points) != 3 || all.Points[0].CPU != 15 || all.Points[0].MemoryMB != 150 || all.Points[0].RxBytes != 4 {
		t.Errorf("whole app = %+v", all)
	}
	w, err := e.svc.GetMetrics(ctx, e.owner, "duo", "1h", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if w.Service != "worker" || w.MemoryLimitMB != 128 || len(w.Points) != 3 || w.Points[0].CPU != 5 || w.Points[0].MemoryMB != 50 {
		t.Errorf("worker = %+v", w)
	}
	// The live reading of one service.
	e.svc.metricsMgr().SetLatestForTest("app_1", metrics.Point{TS: e.now, CPU: 1, MemBytes: 1 << 20})
	if w, _ = e.svc.GetMetrics(ctx, e.owner, "duo", "1h", "worker"); w.Latest != nil && w.Latest.MemoryMB == 1 {
		t.Errorf("worker latest is the app's: %+v", w.Latest)
	}
}
