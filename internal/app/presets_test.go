package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// Every build kind that is a static site is left out of both quota queries, and
// no other kind is: the SQL lists must agree with deploy.IsStaticKind.
func TestQuotaQueriesMatchIsStaticKind(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 20, quota_memory_mb = 4096, quota_cpu_millis = 8000 WHERE id = ?`, e.user.UserID)
	for _, kind := range []string{
		deploy.BuildKindStatic, deploy.BuildKindNodeSPA, deploy.BuildKindNodeStatic,
		deploy.BuildKindNode, deploy.BuildKindDockerfile, "",
	} {
		a := e.mustApp(t, CreateAppInput{})
		e.goLive(t, a, kind, 100, 100)
		sum, err := e.s.Q.SumAppResourcesByUser(ctx, db.SumAppResourcesByUserParams{UserID: e.user.UserID, ID: "none"})
		if err != nil {
			t.Fatal(err)
		}
		ops, err := e.s.Q.OpsUsageAppTotals(ctx, e.user.UserID)
		if err != nil {
			t.Fatal(err)
		}
		counted := int64(100)
		if deploy.IsStaticKind(kind) {
			counted = 0
		}
		// Each app is the only one running: the previous ones are stopped below.
		if sum.MemoryMb != counted || ops.MemoryMb != counted {
			t.Errorf("kind %q: quota sum %d, ops %d, want %d", kind, sum.MemoryMb, ops.MemoryMb, counted)
		}
		e.exec(t, `UPDATE apps SET desired_state = 'stopped' WHERE id = ?`, a.ID)
	}
}

func TestOutputDirAndNewModes(t *testing.T) {
	ctx := context.Background()
	e := depTestSvc(t)

	for _, in := range []string{"dist", "./build/", "web/dist", ""} {
		if _, err := normalizeOutputDir(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	for _, bad := range []string{"/etc", "../x", "a/../../b", "a b", "dist;x", "."} {
		if _, err := normalizeOutputDir(bad); depCode(err) != "output_dir_invalid" {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := normalizeBuildMode("magic"); depCode(err) != "build_mode_invalid" {
		t.Errorf("unknown mode: %v", err)
	}
	for _, m := range []string{"auto", "node", "static_build", "static", "dockerfile", "Static_Build"} {
		if _, err := normalizeBuildMode(m); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}

	// Create with a static build: fixed size, folder stored and returned.
	a, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "site", BuildMode: "static_build", OutputDir: "./build/"})
	if err != nil {
		t.Fatal(err)
	}
	if a.BuildMode != "static_build" || a.OutputDir != "build" || a.MemoryMB != staticMemoryMB || a.CPUMillis != staticCPUMillis {
		t.Errorf("created: %+v", a)
	}
	n := e.mustApp(t, CreateAppInput{BuildMode: "node"})
	if n.MemoryMB != defaultMemoryMB || n.OutputDir != "" {
		t.Errorf("node app: %+v", n)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Name: "x", BuildMode: "static_build", OutputDir: "../x"}); depCode(err) != "output_dir_invalid" {
		t.Errorf("bad folder on create: %v", err)
	}

	// Update: set, validate, clear.
	got, err := e.s.UpdateApp(ctx, e.user, n.Slug, UpdateAppInput{BuildMode: ptr("auto"), OutputDir: ptr("out")})
	if err != nil || got.BuildMode != "auto" || got.OutputDir != "out" {
		t.Fatalf("update: %+v %v", got, err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, n.Slug, UpdateAppInput{OutputDir: ptr("/abs")}); depCode(err) != "output_dir_invalid" {
		t.Errorf("bad folder on update: %v", err)
	}
	got, err = e.s.UpdateApp(ctx, e.user, n.Slug, UpdateAppInput{OutputDir: ptr("")})
	if err != nil || got.OutputDir != "" || got.BuildMode != "auto" {
		t.Errorf("clear: %+v %v", got, err)
	}
}

func TestPipelineStaticBuildAndNodePresets(t *testing.T) {
	ctx := context.Background()
	vite := map[string]string{
		"package.json":      `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^7"}}`,
		"package-lock.json": "{}",
		"index.html":        "<div id=app></div>",
	}
	t.Run("static build uses the folder of the app and is free", func(t *testing.T) {
		e := depTestSvc(t)
		a := e.mustApp(t, CreateAppInput{BuildMode: "static_build", OutputDir: "web/out"})
		p, ae := e.runSource(t, a, makeZipBytes(t, vite))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID)
		if d.BuildKind != deploy.BuildKindNodeSPA || !deploy.IsStaticKind(d.BuildKind) {
			t.Errorf("kind %q", d.BuildKind)
		}
		if p.app.DockerfilePath != deploy.NodeSPADockerfilePath {
			t.Errorf("dockerfile %q", p.app.DockerfilePath)
		}
		if ae := p.settleResources(ctx); ae != nil || !p.setSize || p.sizeMem != staticMemoryMB {
			t.Errorf("settle: %v %+v", ae, p)
		}
	})
	t.Run("auto picks a node app for SvelteKit and a static build for Astro", func(t *testing.T) {
		e := depTestSvc(t)
		kit := map[string]string{"package.json": `{"scripts":{"build":"vite build"},"devDependencies":{"@sveltejs/kit":"2","@sveltejs/adapter-node":"5"}}`, "package-lock.json": "{}"}
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, kit))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID); d.BuildKind != deploy.BuildKindNode {
			t.Errorf("sveltekit kind %q", d.BuildKind)
		}
		astro := map[string]string{"package.json": `{"scripts":{"build":"astro build"},"dependencies":{"astro":"5"}}`, "package-lock.json": "{}", "astro.config.mjs": "export default {}"}
		p, ae = e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, astro))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID); d.BuildKind != deploy.BuildKindNodeStatic || p.app.DockerfilePath != deploy.NodeStaticDockerfilePath {
			t.Errorf("astro kind %q dockerfile %q", d.BuildKind, p.app.DockerfilePath)
		}
	})
	t.Run("next.config is read from the uploaded files", func(t *testing.T) {
		e := depTestSvc(t)
		files := map[string]string{
			"package.json":      `{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"16"}}`,
			"package-lock.json": "{}", "next.config.mjs": "export default { output: 'export' }",
		}
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, files))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if d, _ := e.s.Q.GetDeployment(ctx, p.dep.ID); d.BuildKind != deploy.BuildKindNodeStatic {
			t.Errorf("next export kind %q", d.BuildKind)
		}
	})
	t.Run("node mode reports a missing start script", func(t *testing.T) {
		e := depTestSvc(t)
		_, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "node"}), makeZipBytes(t, vite))
		if ae == nil || !strings.Contains(ae.Code, "start_script_missing") {
			t.Errorf("want start_script_missing, got %v", ae)
		}
	})
}

// Migration 00160 marks the live deployment of an app in build mode static and
// gives the app the fixed size. It leaves auto apps alone, and an app whose
// build mode changed after the deployment (it may be a Dockerfile build).
func TestBackfillStaticKindMigration(t *testing.T) {
	e := depTestSvc(t)
	e.exec(t, `UPDATE users SET quota_apps = 20 WHERE id = ?`, e.user.UserID)
	mk := func(mode string) App {
		a := e.mustApp(t, CreateAppInput{})
		e.goLive(t, a, "", 512, 500)
		e.exec(t, `UPDATE apps SET build_mode = ? WHERE id = ?`, mode, a.ID)
		return a
	}
	static, changedLater, changedBefore, auto, dockerfile := mk("static"), mk("static"), mk("static"), mk("auto"), mk("dockerfile")
	audit := func(a App, ts int) {
		e.exec(t, `INSERT INTO audit_log (ts, channel, action, target_type, target_id, details) VALUES (?, 'api', 'app.update', 'app', ?, '{"build_mode":"static","old_slug":"x"}')`, ts, a.ID)
	}
	audit(changedLater, 100) // after the deployment (queued_at 1)
	audit(changedBefore, 0)  // before it

	src, err := os.ReadFile("../store/migrations/00160_backfill_static_kind.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(src), "-- +goose Down")
	e.exec(t, up)
	e.exec(t, up) // running it again changes nothing

	state := func(a App) (kind string, mem, cpu int64) {
		row := e.s.Store.DB.QueryRow(`SELECT d.build_kind, a.memory_mb, a.cpu_millis FROM apps a JOIN deployments d ON d.id = a.current_deployment_id WHERE a.id = ?`, a.ID)
		if err := row.Scan(&kind, &mem, &cpu); err != nil {
			t.Fatal(err)
		}
		return
	}
	for name, want := range map[string]struct {
		a    App
		kind string
		mem  int64
		cpu  int64
	}{
		"static":              {static, "static", 64, 500},
		"mode changed before": {changedBefore, "static", 64, 500},
		"mode changed after":  {changedLater, "", 512, 500},
		"auto":                {auto, "", 512, 500},
		"dockerfile":          {dockerfile, "", 512, 500},
	} {
		if k, m, c := state(want.a); k != want.kind || m != want.mem || c != want.cpu {
			t.Errorf("%s: kind %q, %d MB, %d CPU millis; want %q, %d, %d", name, k, m, c, want.kind, want.mem, want.cpu)
		}
	}
	if got, _ := e.s.GetApp(context.Background(), e.user, static.Slug); !got.Static {
		t.Error("the backfilled app is not reported as static")
	}
}
