package app

import (
	"context"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// goLive makes a deployment of the given build kind the live version of the
// app, the way a finished pipeline does (without Docker), at the app's size.
func (e *depEnv) goLive(t *testing.T, a App, kind string, memMB, cpuMillis int64) string {
	t.Helper()
	id := ids.New(ids.Deployment)
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, build_kind, queued_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM deployments WHERE app_id = ?), 'running', 'files', 'api', ?, 1)`, id, a.ID, a.ID, kind)
	e.exec(t, `UPDATE apps SET desired_state = 'running', status = 'running', current_deployment_id = ?, memory_mb = ?, cpu_millis = ?,
		deploy_seq = (SELECT MAX(seq) FROM deployments WHERE app_id = apps.id) WHERE id = ?`, id, memMB, cpuMillis, a.ID)
	return id
}

func TestQuotaLeavesOutStaticSites(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 1024, quota_cpu_millis = 1000 WHERE id = ?`, e.user.UserID)

	// One app from its own Dockerfile (even one that starts from nginx), plus
	// plain sites and a Vite site built by bitploy's templates.
	own := e.mustApp(t, CreateAppInput{})
	e.goLive(t, own, deploy.BuildKindDockerfile, 512, 500)
	for _, kind := range []string{deploy.BuildKindStatic, deploy.BuildKindNodeSPA, deploy.BuildKindStatic} {
		e.goLive(t, e.mustApp(t, CreateAppInput{}), kind, staticMemoryMB, staticCPUMillis)
	}
	// An app built from the node template runs the user's code: it counts.
	node := e.mustApp(t, CreateAppInput{})
	e.goLive(t, node, deploy.BuildKindNode, 256, 250)

	u, err := e.s.Usage(ctx, e.user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Apps != 5 || u.MemoryReservedMB != 768 || u.CPUReservedMillis != 750 {
		t.Errorf("usage: %d apps, %d MB, %d CPU millis; static sites must not count", u.Apps, u.MemoryReservedMB, u.CPUReservedMillis)
	}
	// The server still reserves what really runs.
	real, err := e.s.Q.OpsSumMemoryLimitsMB(ctx)
	if err != nil || real != 512+64*3+256 {
		t.Errorf("memory reserved on the host = %d, %v", real, err)
	}

	// 768 of 1024 MB are taken: another 256 MB app fits, 512 MB does not.
	next := e.mustApp(t, CreateAppInput{})
	if err := e.s.checkResourceQuota(ctx, userRow(t, e), next.ID, 256, 250); err != nil {
		t.Errorf("256 MB: %v", err)
	}
	if err := e.s.checkResourceQuota(ctx, userRow(t, e), next.ID, 512, 250); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("512 MB: %v", err)
	}
	// A static site is never refused for the quota, even when it is full.
	e.exec(t, `UPDATE users SET quota_memory_mb = 600, quota_cpu_millis = 500 WHERE id = ?`, e.user.UserID)
	stopped := e.mustApp(t, CreateAppInput{BuildMode: "static"})
	if _, err := e.s.DeployFiles(ctx, e.user, stopped.Slug, []FileInput{{Path: "index.html", Content: "hi"}}); err != nil {
		t.Errorf("static site with a full quota: %v", err)
	}
	auto := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	if _, err := e.s.DeployFiles(ctx, e.user, auto.Slug, []FileInput{{Path: "index.html", Content: "hi"}}); err != nil {
		t.Errorf("auto site with a full quota: %v", err)
	}
	// Starting a stopped static site needs no room either.
	e.exec(t, `UPDATE apps SET desired_state = 'stopped' WHERE id != ?`, node.ID)
	row, _ := e.s.Q.GetApp(ctx, e.mustStaticID(t))
	if err := e.s.checkStartQuota(ctx, row); err != nil {
		t.Errorf("start of a stopped static site: %v", err)
	}
	// An app of its own Dockerfile is still checked.
	ownRow, _ := e.s.Q.GetApp(ctx, own.ID)
	if err := e.s.checkStartQuota(ctx, ownRow); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("start of an app: %v", err)
	}
}

// mustStaticID returns the ID of an app whose live version is static.
func (e *depEnv) mustStaticID(t *testing.T) string {
	t.Helper()
	var id string
	if err := e.s.Store.DB.QueryRow(`SELECT a.id FROM apps a JOIN deployments d ON d.id = a.current_deployment_id WHERE d.build_kind = 'static' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestStaticAppAPIFlag(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	if got, _ := e.s.GetApp(ctx, e.user, a.Slug); got.Static {
		t.Error("a new app is not static")
	}
	e.goLive(t, a, deploy.BuildKindNodeSPA, staticMemoryMB, staticCPUMillis)
	if got, _ := e.s.GetApp(ctx, e.user, a.Slug); !got.Static {
		t.Error("an app with a node-spa version is static")
	}
	e.goLive(t, a, deploy.BuildKindDockerfile, 512, 500)
	if got, _ := e.s.GetApp(ctx, e.user, a.Slug); got.Static {
		t.Error("an app with its own Dockerfile is not static")
	}
}

func TestStaticSizeAndStorageAreFixed(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	e.goLive(t, a, deploy.BuildKindStatic, staticMemoryMB, staticCPUMillis)

	for name, in := range map[string]UpdateAppInput{
		"memory": {MemoryMB: ptr(int64(512))},
		"cpu":    {CPUMillis: ptr(int64(1000))},
		"both":   {MemoryMB: ptr(int64(128)), CPUMillis: ptr(int64(100))},
	} {
		if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, in); depCode(err) != "static_fixed_size" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Sending the fixed size back is not a change.
	if got, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{MemoryMB: ptr(int64(staticMemoryMB)), CPUMillis: ptr(int64(staticCPUMillis))}); err != nil || got.MemoryMB != staticMemoryMB {
		t.Errorf("same size: %+v %v", got, err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{Volume: ptr(true)}); depCode(err) != "static_no_storage" {
		t.Errorf("volume on: %v", err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, a.Slug); got.Volume || got.MemoryMB != staticMemoryMB || got.CPUMillis != staticCPUMillis {
		t.Errorf("settings changed: %+v", got)
	}
	// Other settings still change.
	if got, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{Name: ptr("My site")}); err != nil || got.Name != "My site" {
		t.Errorf("rename: %+v %v", got, err)
	}
	// After a deploy with the user's own Dockerfile the size can be chosen again.
	e.goLive(t, a, deploy.BuildKindDockerfile, 512, 500)
	if got, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{MemoryMB: ptr(int64(256))}); err != nil || got.MemoryMB != 256 {
		t.Errorf("app size: %+v %v", got, err)
	}
	if got, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{Volume: ptr(true)}); err != nil || !got.Volume {
		t.Errorf("app volume: %+v %v", got, err)
	}
}

// pipelineFor builds the pipeline of a queued deployment, like the worker does.
func (e *depEnv) pipelineFor(t *testing.T, a App, kind string) *pipeline {
	t.Helper()
	ctx := context.Background()
	id := ids.New(ids.Deployment)
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, build_kind, queued_at)
		VALUES (?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM deployments WHERE app_id = ?), 'health_check', 'files', 'api', ?, 1)`, id, a.ID, a.ID, kind)
	dep, err := e.s.Q.GetDeployment(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.s.Q.GetApp(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &pipeline{s: e.s, app: row, dep: dep, kind: kind, ring: deploy.NewLineRing(20)}
}

func TestStaticSiteBecomesAnApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 1024, quota_cpu_millis = 1000 WHERE id = ?`, e.user.UserID)
	site := e.mustApp(t, CreateAppInput{Type: "worker"}) // a worker has no route, so the swap needs no proxy
	e.goLive(t, site, deploy.BuildKindStatic, staticMemoryMB, staticCPUMillis)
	other := e.mustApp(t, CreateAppInput{})
	e.goLive(t, other, deploy.BuildKindDockerfile, 768, 500)

	// 768 of 1024 MB are used by the other app: the default 512 MB does not fit.
	p := e.pipelineFor(t, site, deploy.BuildKindDockerfile)
	ae := p.settleResources(ctx)
	if ae == nil || ae.Code != "memory_quota_exceeded" || !strings.Contains(ae.Message, "was a static site") || ae.Hint == "" {
		t.Fatalf("no room: %v", ae)
	}
	if row, _ := e.s.Q.GetApp(ctx, site.ID); row.MemoryMb != staticMemoryMB {
		t.Errorf("a refused deployment changed the size to %d MB", row.MemoryMb)
	}
	// CPU is checked as well.
	e.exec(t, `UPDATE apps SET memory_mb = 256, cpu_millis = 800 WHERE id = ?`, other.ID)
	if ae := e.pipelineFor(t, site, deploy.BuildKindDockerfile).settleResources(ctx); ae == nil || ae.Code != "cpu_quota_exceeded" {
		t.Errorf("cpu: %v", ae)
	}

	// With room it gets the default size, written when the version goes live.
	e.exec(t, `UPDATE apps SET memory_mb = 256, cpu_millis = 400 WHERE id = ?`, other.ID)
	p = e.pipelineFor(t, site, deploy.BuildKindDockerfile)
	if ae := p.settleResources(ctx); ae != nil {
		t.Fatalf("room: %v", ae)
	}
	if !p.setSize || p.sizeMem != defaultMemoryMB || p.sizeCPU != defaultCPUMillis {
		t.Fatalf("size = %+v", p)
	}
	if ae := p.swap(ctx); ae != nil {
		t.Fatalf("swap: %v", ae)
	}
	got, _ := e.s.GetApp(ctx, e.user, site.Slug)
	if got.Static || got.MemoryMB != defaultMemoryMB || got.CPUMillis != defaultCPUMillis {
		t.Errorf("after the swap: static %v, %d MB, %d CPU millis", got.Static, got.MemoryMB, got.CPUMillis)
	}
	if u, _ := e.s.Usage(ctx, e.user.UserID); u.MemoryReservedMB != 768 {
		t.Errorf("reserved = %d MB, want 768", u.MemoryReservedMB)
	}
}

func TestAppBecomesAStaticSite(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 1024, quota_cpu_millis = 1000 WHERE id = ?`, e.user.UserID)
	app := e.mustApp(t, CreateAppInput{Type: "worker"})
	e.goLive(t, app, deploy.BuildKindDockerfile, 512, 500)
	other := e.mustApp(t, CreateAppInput{})
	e.goLive(t, other, deploy.BuildKindDockerfile, 512, 500)
	if u, _ := e.s.Usage(ctx, e.user.UserID); u.MemoryReservedMB != 1024 {
		t.Fatalf("reserved = %d MB", u.MemoryReservedMB)
	}

	p := e.pipelineFor(t, app, deploy.BuildKindNodeSPA)
	if ae := p.settleResources(ctx); ae != nil {
		t.Fatalf("settle: %v", ae)
	}
	if !p.setSize || p.sizeMem != staticMemoryMB || p.sizeCPU != staticCPUMillis {
		t.Fatalf("size = %+v", p)
	}
	if ae := p.swap(ctx); ae != nil {
		t.Fatalf("swap: %v", ae)
	}
	got, _ := e.s.GetApp(ctx, e.user, app.Slug)
	if !got.Static || got.MemoryMB != staticMemoryMB || got.CPUMillis != staticCPUMillis {
		t.Errorf("after the swap: static %v, %d MB, %d CPU millis", got.Static, got.MemoryMB, got.CPUMillis)
	}
	if u, _ := e.s.Usage(ctx, e.user.UserID); u.MemoryReservedMB != 512 || u.CPUReservedMillis != 500 {
		t.Errorf("the allowance was not freed: %d MB, %d CPU millis", u.MemoryReservedMB, u.CPUReservedMillis)
	}
}

func TestRollbackKeepsTheBuildKind(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	target := e.goLive(t, a, deploy.BuildKindStatic, staticMemoryMB, staticCPUMillis)
	e.exec(t, `UPDATE deployments SET image = 'bitploy/x:1' WHERE id = ?`, target)
	e.goLive(t, a, deploy.BuildKindDockerfile, 512, 500)
	d, err := e.s.queueDeployment(ctx, e.user, mustRow(t, e, a.ID), queueSpec{kind: "rollback", ref: target, image: "bitploy/x:1", buildKind: deploy.BuildKindStatic})
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.s.Q.GetDeployment(ctx, d.ID)
	if err != nil || row.BuildKind != deploy.BuildKindStatic {
		t.Errorf("rollback deployment kind = %q, %v", row.BuildKind, err)
	}
}

func userRow(t *testing.T, e *depEnv) db.User {
	t.Helper()
	u, err := e.s.Q.GetUser(context.Background(), e.user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
