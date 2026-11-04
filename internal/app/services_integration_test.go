//go:build integration

package app

// An app with two services, built in the database (compose parsing comes
// later): web is nginx and public, worker is alpine and waits for web by its
// service name. Phase 1 deploys such an app by rolling back to a deployment.
//
//	go test -tags=integration -count=1 -run TestIntegrationMultiService ./internal/app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
	"github.com/AVMG20/bitploy/internal/store/db"
)

func TestIntegrationMultiService(t *testing.T) {
	e := itSetup(t)
	ctx := context.Background()
	ap := e.newApp(t, CreateAppInput{Type: "web", Port: ptr(int64(8080))})
	slug := ap.Slug
	q := e.s.Q

	const web, alpine = "nginxinc/nginx-unprivileged:alpine", "alpine:3.20"
	e.exec(t, `UPDATE apps SET service_layout = 'multi' WHERE id = ?`, ap.ID)
	e.exec(t, `DELETE FROM app_services WHERE app_id = ?`, ap.ID)
	e.exec(t, `DELETE FROM app_volumes WHERE app_id = ?`, ap.ID)
	// worker is listed first: the start order comes from depends_on.
	for i, sv := range []db.InsertAppServiceParams{
		{Name: "worker", Kind: "image", Image: alpine, MemoryMb: 64, CpuMillis: 250,
			Command:   `["sh","-c","until wget -q -O /dev/null http://web:8080/; do sleep 1; done; echo reached-web; sleep 3600"]`,
			DependsOn: `[{"service":"web","condition":"service_started"}]`},
		{Name: "web", Kind: "image", Image: web, Public: 1, Port: sql.NullInt64{Int64: 8080, Valid: true}, HealthPath: "/", MemoryMb: 128, CpuMillis: 250},
	} {
		sv.AppID, sv.Position, sv.CreatedAt, sv.UpdatedAt = ap.ID, int64(i), 1, 1
		if err := q.InsertAppService(ctx, sv); err != nil {
			t.Fatal(err)
		}
	}
	vol := dockerx.ServiceVolumeName(slug, "files")
	if err := q.InsertAppVolume(ctx, db.InsertAppVolumeParams{AppID: ap.ID, Name: "files", VolumeName: vol, SizeMb: 50, Service: "worker", MountPath: "/files", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := syncMultiTotals(ctx, q, ap.ID); err != nil {
		t.Fatal(err)
	}
	// The deployment to roll back to: it ran both images.
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, port, queued_at, finished_at)
		VALUES ('dep_multi1', ?, 1, 'superseded', 'files', 'api', ?, 8080, 1, 1)`, ap.ID, web)
	for _, ds := range []db.UpsertDeploymentServiceParams{
		{Service: "web", Image: sql.NullString{String: web, Valid: true}, Port: sql.NullInt64{Int64: 8080, Valid: true}},
		{Service: "worker", Image: sql.NullString{String: alpine, Valid: true}},
	} {
		ds.DeploymentID = "dep_multi1"
		if err := q.UpsertDeploymentService(ctx, ds); err != nil {
			t.Fatal(err)
		}
	}

	d, err := e.s.Rollback(ctx, e.user, slug, "dep_multi1")
	if err != nil {
		t.Fatal(err)
	}
	if d = e.wait(t, d.ID); d.State != "running" {
		t.Fatalf("%s %+v %v", d.State, d.Error, d.LogTail)
	}
	webName, workerName := dockerx.ServiceContainerName(slug, "web", d.Seq), dockerx.ServiceContainerName(slug, "worker", d.Seq)
	for svc, name := range map[string]string{"web": webName, "worker": workerName} {
		info, err := e.dk.InspectContainer(ctx, name)
		if err != nil || info.State == nil || !info.State.Running {
			t.Fatalf("%s: %v", name, err)
		}
		if got := info.Config.Labels[dockerx.LabelService]; got != svc {
			t.Errorf("%s service label = %q", name, got)
		}
		n := info.NetworkSettings.Networks[dockerx.NetworkName(slug)]
		if n == nil || !contains(n.Aliases, svc) {
			t.Errorf("%s is not reachable as %s on the app network: %+v", name, svc, n)
		}
	}
	if info, _ := e.dk.InspectContainer(ctx, workerName); len(info.Mounts) != 1 || info.Mounts[0].Name != vol || info.Mounts[0].Destination != "/files" {
		t.Errorf("worker mounts = %+v", info.Mounts)
	}
	// The route points at the public service; worker reached web by its name.
	e.waitServing(t, slug, "nginx")
	waitFor(t, 30*time.Second, "worker reaching web", func() bool {
		lines, _ := e.s.Logs(ctx, e.user, slug, LogOptions{Kind: "runtime", Service: "worker"})
		for _, l := range lines {
			if strings.Contains(l.Text, "reached-web") && l.Service == "worker" {
				return true
			}
		}
		return false
	})
	got, err := e.s.GetApp(ctx, e.user, slug)
	if err != nil || len(got.Services) != 2 || got.MemoryMB != 192 {
		t.Fatalf("get app: %+v %v", got, err)
	}

	// A second rollback changes nothing about the services: their containers stay.
	d2, err := e.s.Rollback(ctx, e.user, slug, "dep_multi1")
	if err != nil {
		t.Fatal(err)
	}
	if d2 = e.wait(t, d2.ID); d2.State != "running" {
		t.Fatalf("%s %+v %v", d2.State, d2.Error, d2.LogTail)
	}
	row, _ := q.GetApp(ctx, ap.ID)
	if cur := e.s.currentContainers(ctx, row); cur["worker"] != workerName || cur["web"] != webName {
		t.Errorf("unchanged services were replaced: %v", cur)
	}
	e.waitServing(t, slug, "nginx")

	// Restarting one service leaves the other alone.
	before, _ := e.dk.InspectContainer(ctx, webName)
	if _, err := e.s.RestartApp(ctx, e.user, slug, "worker"); err != nil {
		t.Fatal(err)
	}
	after, _ := e.dk.InspectContainer(ctx, webName)
	if before.State == nil || after.State == nil || before.State.StartedAt != after.State.StartedAt {
		t.Error("restarting worker restarted web")
	}
	if _, err := e.s.RestartApp(ctx, e.user, slug, "db"); depCode(err) != "service_not_found" {
		t.Errorf("unknown service: %v", err)
	}

	if err := e.s.DeleteApp(ctx, e.user, slug, slug); err != nil {
		t.Fatal(err)
	}
}
