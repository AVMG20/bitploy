package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
)

func TestBuildModeSetting(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()

	// MCP and REST keep the strict rule unless asked otherwise.
	def := e.mustApp(t, CreateAppInput{})
	if def.BuildMode != "dockerfile" {
		t.Errorf("default build mode = %q", def.BuildMode)
	}
	auto := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	if auto.BuildMode != "auto" {
		t.Errorf("auto build mode = %q", auto.BuildMode)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{BuildMode: "magic"}); depCode(err) != "build_mode_invalid" {
		t.Errorf("unknown mode on create: %v", err)
	}
	got, err := e.s.UpdateApp(ctx, e.user, def.Slug, UpdateAppInput{BuildMode: ptr("static")})
	if err != nil || got.BuildMode != "static" {
		t.Fatalf("update: %+v %v", got, err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, def.Slug, UpdateAppInput{BuildMode: ptr("nope")}); depCode(err) != "build_mode_invalid" {
		t.Errorf("unknown mode on update: %v", err)
	}
	again, _ := e.s.GetApp(ctx, e.user, def.Slug)
	if again.BuildMode != "static" {
		t.Errorf("mode after a failed update = %q", again.BuildMode)
	}
}

func TestDeployFilesAutoMode(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	auto := e.mustApp(t, CreateAppInput{BuildMode: "auto"})

	// A website without a Dockerfile is accepted.
	if _, err := e.s.DeployFiles(ctx, e.user, auto.Slug, []FileInput{{Path: "index.html", Content: "<h1>hi</h1>"}}); err != nil {
		t.Errorf("index.html only: %v", err)
	}
	// A Dockerfile still works.
	if _, err := e.s.DeployFiles(ctx, e.user, auto.Slug, staticFiles()); err != nil {
		t.Errorf("Dockerfile: %v", err)
	}
	// Neither: a clear error, nothing queued.
	_, err := e.s.DeployFiles(ctx, e.user, auto.Slug, []FileInput{{Path: "notes.txt", Content: "hi"}})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != deploy.CodeNothingToBuild || ae.Message != "No Dockerfile or index.html found." || ae.Hint == "" {
		t.Errorf("neither: %v", err)
	}

	// The default mode keeps requiring a Dockerfile.
	strict := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.DeployFiles(ctx, e.user, strict.Slug, []FileInput{{Path: "index.html", Content: "hi"}}); depCode(err) != "dockerfile_missing" {
		t.Errorf("strict app: %v", err)
	}
}

// runSource queues an upload deployment and runs the fetch and validate steps
// of the pipeline on it, like the worker does (without Docker).
func (e *depEnv) runSource(t *testing.T, app App, zipBytes []byte) (*pipeline, *apperr.Error) {
	t.Helper()
	ctx := context.Background()
	d, err := e.s.UploadAndDeploy(ctx, e.user, app.Slug, bytes.NewReader(zipBytes))
	if err != nil {
		t.Fatalf("UploadAndDeploy: %v", err)
	}
	if d.SourceKind != "upload" || d.State != "queued" {
		t.Fatalf("deployment: %+v", d)
	}
	dep, err := e.s.Q.GetDeployment(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.s.Q.GetApp(ctx, dep.AppID)
	if err != nil {
		t.Fatal(err)
	}
	p := &pipeline{s: e.s, app: row, dep: dep, ring: deploy.NewLineRing(10)}
	if ae := p.fetch(ctx); ae != nil {
		t.Fatalf("fetch: %v", ae)
	}
	return p, p.validate(ctx)
}

func TestPipelineDetectsTheSource(t *testing.T) {
	site := map[string]string{"index.html": "<h1>hi</h1>", "css/site.css": "body{}"}
	docker := map[string]string{"Dockerfile": depStaticDockerfile, "index.html": "<h1>hi</h1>"}
	wrap := func(files map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range files {
			out["my-site/"+k] = v
		}
		return out
	}

	t.Run("index.html becomes a static site", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, site))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if p.app.DockerfilePath != deploy.StaticDockerfilePath || p.port != 8080 {
			t.Errorf("app %q port %d", p.app.DockerfilePath, p.port)
		}
		df, err := os.ReadFile(filepath.Join(p.srcRoot, deploy.StaticDockerfilePath))
		if err != nil || !strings.Contains(string(df), "nginx") {
			t.Errorf("generated Dockerfile: %v %q", err, df)
		}
		ign, err := os.ReadFile(filepath.Join(p.srcRoot, ".dockerignore"))
		if err != nil || !strings.Contains(string(ign), deploy.StaticDockerfilePath) {
			t.Errorf(".dockerignore: %v %q", err, ign)
		}
		if !strings.Contains(strings.Join(p.ring.Lines(), "\n"), "static site") {
			t.Errorf("the log does not say what was chosen: %v", p.ring.Lines())
		}
	})
	t.Run("a Dockerfile is built as it is", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, docker))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if p.app.DockerfilePath != "Dockerfile" {
			t.Errorf("dockerfile %q", p.app.DockerfilePath)
		}
		if _, err := os.Stat(filepath.Join(p.srcRoot, deploy.StaticDockerfilePath)); err == nil {
			t.Error("a Dockerfile build must not get the static template")
		}
	})
	t.Run("a wrapping folder is stripped before detection", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, wrap(site)))
		if ae != nil {
			t.Fatalf("static in a folder: %v", ae)
		}
		if p.app.DockerfilePath != deploy.StaticDockerfilePath {
			t.Errorf("dockerfile %q", p.app.DockerfilePath)
		}
		e = depTestSvc(t)
		if _, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, wrap(docker))); ae != nil {
			t.Fatalf("Dockerfile in a folder: %v", ae)
		}
	})
	t.Run("neither is a readable failure", func(t *testing.T) {
		e := depTestSvc(t)
		_, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, map[string]string{"README.md": "hi", "src/app.txt": "x"}))
		if ae == nil || ae.Code != deploy.CodeNothingToBuild || ae.Message != "No Dockerfile or index.html found." {
			t.Fatalf("validate: %v", ae)
		}
	})
	t.Run("the default mode still needs a Dockerfile", func(t *testing.T) {
		e := depTestSvc(t)
		_, ae := e.runSource(t, e.mustApp(t, CreateAppInput{}), makeZipBytes(t, site))
		if ae == nil || ae.Code != "dockerfile_missing" {
			t.Fatalf("validate: %v", ae)
		}
	})
	t.Run("static mode ignores the Dockerfile", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "static"}), makeZipBytes(t, docker))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if p.app.DockerfilePath != deploy.StaticDockerfilePath {
			t.Errorf("dockerfile %q", p.app.DockerfilePath)
		}
	})
	t.Run("a .dockerignore of the user is kept and extended", func(t *testing.T) {
		e := depTestSvc(t)
		files := map[string]string{"index.html": "hi", ".dockerignore": "private/\n"}
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, files))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		ign, _ := os.ReadFile(filepath.Join(p.srcRoot, ".dockerignore"))
		if !strings.Contains(string(ign), "private/") || !strings.Contains(string(ign), "node_modules") {
			t.Errorf(".dockerignore:\n%s", ign)
		}
	})
}

func TestUploadAndDeployRules(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	zipOK := makeZipBytes(t, map[string]string{"index.html": "hi"})

	// A git-linked app takes no uploads.
	git := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://github.com/o/r', git_branch = 'main' WHERE id = ?`, git.ID)
	if _, err := e.s.UploadAndDeploy(ctx, e.user, git.Slug, bytes.NewReader(zipOK)); depCode(err) != "git_source_linked" {
		t.Errorf("git-linked app: %v", err)
	}

	// Somebody else's app looks like a missing app.
	mine := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.UploadAndDeploy(ctx, bob, mine.Slug, bytes.NewReader(zipOK)); depCode(err) != "app_not_found" {
		t.Errorf("foreign app: %v", err)
	}

	// Not a zip: refused before anything is queued, and nothing is left behind.
	if _, err := e.s.UploadAndDeploy(ctx, e.user, mine.Slug, strings.NewReader("this is not a zip")); depCode(err) != "source_zip_invalid" {
		t.Errorf("not a zip: %v", err)
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&n)
	if n != 0 {
		t.Errorf("%d deployments queued by a bad upload", n)
	}
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM uploads`).Scan(&n)
	if n != 0 {
		t.Errorf("%d upload rows left by a bad upload", n)
	}

	// Deploys per hour, checked before the body is read.
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 1 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.UploadAndDeploy(ctx, e.user, mine.Slug, bytes.NewReader(zipOK)); err != nil {
		t.Fatalf("first upload: %v", err)
	}
	if _, err := e.s.UploadAndDeploy(ctx, e.user, mine.Slug, bytes.NewReader(zipOK)); depCode(err) != "deploy_rate_limited" {
		t.Errorf("second upload: %v", err)
	}
}
