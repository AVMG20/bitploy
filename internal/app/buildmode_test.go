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

	// The home page: a page name at the top, or empty.
	hp := e.mustApp(t, CreateAppInput{BuildMode: "auto", HomePage: "Planning tool v3.html"})
	if hp.HomePage != "Planning tool v3.html" {
		t.Errorf("home page on create = %q", hp.HomePage)
	}
	for _, bad := range []string{"sub/page.html", `..\page.html`, "notes.txt", "..", " page.html", "page\n.html", strings.Repeat("a", 260) + ".html"} {
		if _, err := e.s.UpdateApp(ctx, e.user, hp.Slug, UpdateAppInput{HomePage: ptr(bad)}); depCode(err) != "home_page_invalid" {
			t.Errorf("home page %q: %v", bad, err)
		}
	}
	got, err = e.s.UpdateApp(ctx, e.user, hp.Slug, UpdateAppInput{HomePage: ptr(`Café "plan" 'v2'.HTM`)})
	if err != nil || got.HomePage != `Café "plan" 'v2'.HTM` || got.BuildMode != "auto" {
		t.Fatalf("home page update: %+v %v", got, err)
	}
	got, err = e.s.UpdateApp(ctx, e.user, hp.Slug, UpdateAppInput{HomePage: ptr("")})
	if err != nil || got.HomePage != "" {
		t.Fatalf("clearing the home page: %+v %v", got, err)
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
// runSource uploads a zip and runs the pipeline up to validation. An error of
// the upload call itself (its early checks) is returned as the result.
func (e *depEnv) runSource(t *testing.T, app App, zipBytes []byte) (*pipeline, *apperr.Error) {
	return e.runSourceThen(t, app, zipBytes, nil)
}

// runSourceThen is runSource with a step between queueing and the pipeline.
func (e *depEnv) runSourceThen(t *testing.T, app App, zipBytes []byte, between func()) (*pipeline, *apperr.Error) {
	t.Helper()
	ctx := context.Background()
	d, err := e.s.UploadAndDeploy(ctx, e.user, app.Slug, bytes.NewReader(zipBytes))
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		t.Fatalf("UploadAndDeploy: %v", err)
	}
	if between != nil {
		between()
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
		if strings.Contains(strings.Join(p.ring.Lines(), "\n"), deploy.CodeNoHealthcheck) {
			t.Errorf("the log talks about the template's HEALTHCHECK: %v", p.ring.Lines())
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
	t.Run("the build uses the mode the app has when it runs", func(t *testing.T) {
		e := depTestSvc(t)
		a := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
		_, ae := e.runSourceThen(t, a, makeZipBytes(t, site), func() {
			e.exec(t, `UPDATE apps SET build_mode = 'dockerfile' WHERE id = ?`, a.ID)
		})
		if ae == nil || ae.Code != "dockerfile_missing" {
			t.Fatalf("validate after switching to dockerfile: %v", ae)
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
	t.Run("a static site gets 128 MB unless a size was chosen", func(t *testing.T) {
		mem := func(e *depEnv, id string) int64 {
			row, err := e.s.Q.GetApp(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			return row.MemoryMb
		}
		e := depTestSvc(t)
		a := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
		if _, ae := e.runSource(t, a, makeZipBytes(t, site)); ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if got := mem(e, a.ID); got != staticMemoryMB {
			t.Errorf("auto static site: %d MB, want %d", got, staticMemoryMB)
		}

		chosen := e.mustApp(t, CreateAppInput{BuildMode: "auto", MemoryMB: ptr(int64(256))})
		if _, ae := e.runSource(t, chosen, makeZipBytes(t, site)); ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if got := mem(e, chosen.ID); got != 256 {
			t.Errorf("chosen size: %d MB, want 256", got)
		}

		df := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
		if _, ae := e.runSource(t, df, makeZipBytes(t, docker)); ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if got := mem(e, df.ID); got != defaultMemoryMB {
			t.Errorf("Dockerfile app: %d MB, want %d", got, defaultMemoryMB)
		}

		if st := e.mustApp(t, CreateAppInput{BuildMode: "static"}); st.MemoryMB != staticMemoryMB {
			t.Errorf("new static app: %d MB, want %d", st.MemoryMB, staticMemoryMB)
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

func TestPipelineHomePage(t *testing.T) {
	// The page is copied to index.html in the build context; the original stays.
	served := func(t *testing.T, p *pipeline, page, body string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(p.srcRoot, "index.html"))
		if err != nil || string(got) != body {
			t.Errorf("index.html = %q, %v; want the content of %s", got, err, page)
		}
		orig, err := os.ReadFile(filepath.Join(p.srcRoot, page))
		if err != nil || string(orig) != body {
			t.Errorf("%s = %q, %v", page, orig, err)
		}
		df, _ := os.ReadFile(filepath.Join(p.srcRoot, deploy.StaticDockerfilePath))
		if p.app.DockerfilePath != deploy.StaticDockerfilePath || strings.Contains(string(df), page) {
			t.Errorf("app %q, Dockerfile:\n%s", p.app.DockerfilePath, df)
		}
		if !strings.Contains(strings.Join(p.ring.Lines(), "\n"), "The home page is "+page) {
			t.Errorf("the log does not name the home page: %v", p.ring.Lines())
		}
	}

	t.Run("one page with a random name", func(t *testing.T) {
		e := depTestSvc(t)
		page := "Planning tool v3.html"
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, map[string]string{page: "<h1>plan</h1>", "logo.png": "png"}))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		served(t, p, page, "<h1>plan</h1>")
	})
	t.Run("one .htm page in a wrapping folder", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, map[string]string{"export/Report 2026.htm": "<p>r</p>"}))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		served(t, p, "Report 2026.htm", "<p>r</p>")
	})
	t.Run("names with quotes, unicode and shell characters", func(t *testing.T) {
		e := depTestSvc(t)
		page := `Plan "v3" – café's $(touch x) ; rm.html`
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "static"}), makeZipBytes(t, map[string]string{page: "odd"}))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		served(t, p, page, "odd")
	})
	t.Run("several pages need a choice", func(t *testing.T) {
		e := depTestSvc(t)
		files := map[string]string{"a.html": "a", "b.html": "b"}
		if _, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, files)); ae == nil || ae.Code != deploy.CodeHomePageUnclear {
			t.Fatalf("no choice: %v", ae)
		}
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto", HomePage: "b.html"}), makeZipBytes(t, files))
		if ae != nil {
			t.Fatalf("chosen: %v", ae)
		}
		served(t, p, "b.html", "b")
	})
	t.Run("a later upload without the chosen page", func(t *testing.T) {
		e := depTestSvc(t)
		a := e.mustApp(t, CreateAppInput{BuildMode: "auto", HomePage: "old.html"})
		if _, ae := e.runSource(t, a, makeZipBytes(t, map[string]string{"a.html": "a", "b.html": "b"})); ae == nil || ae.Code != deploy.CodeHomePageMissing || !strings.Contains(ae.Message, "old.html") {
			t.Fatalf("several pages: %v", ae)
		}
		p, ae := e.runSource(t, a, makeZipBytes(t, map[string]string{"new.html": "n"}))
		if ae != nil {
			t.Fatalf("one page: %v", ae)
		}
		served(t, p, "new.html", "n")
	})
	t.Run("a real index.html is never overwritten", func(t *testing.T) {
		e := depTestSvc(t)
		p, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto", HomePage: "b.html"}), makeZipBytes(t, map[string]string{"index.html": "real", "b.html": "b"}))
		if ae != nil {
			t.Fatalf("validate: %v", ae)
		}
		if got, _ := os.ReadFile(filepath.Join(p.srcRoot, "index.html")); string(got) != "real" {
			t.Errorf("index.html = %q", got)
		}
	})
	t.Run("a page next to package.json is not a static site", func(t *testing.T) {
		e := depTestSvc(t)
		_, ae := e.runSource(t, e.mustApp(t, CreateAppInput{BuildMode: "auto"}), makeZipBytes(t, map[string]string{"tool.html": "x", "package.json": "{}"}))
		if ae == nil || ae.Code != deploy.CodeNothingToBuild || !strings.Contains(ae.Message, "Node.js") {
			t.Fatalf("validate: %v", ae)
		}
	})
	t.Run("the default mode ignores pages", func(t *testing.T) {
		e := depTestSvc(t)
		_, ae := e.runSource(t, e.mustApp(t, CreateAppInput{HomePage: "tool.html"}), makeZipBytes(t, map[string]string{"tool.html": "x"}))
		if ae == nil || ae.Code != "dockerfile_missing" {
			t.Fatalf("validate: %v", ae)
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
