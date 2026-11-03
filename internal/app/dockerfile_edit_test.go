package app

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func readZipFile(t *testing.T, path, name string) (string, bool) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b), true
		}
	}
	return "", false
}

func TestAppDockerfileGetPut(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	// Nothing deployed yet.
	v, err := e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if err != nil || v.Editable || v.Reason == "" || v.Path != "Dockerfile" {
		t.Fatalf("before deploy: %+v %v", v, err)
	}
	if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, "FROM alpine:3.22\nEXPOSE 80\n"); depCode(err) != "source_missing" {
		t.Errorf("source_missing: %v", err)
	}

	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles2()); err != nil {
		t.Fatal(err)
	}
	v, err = e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if err != nil || !v.Editable || v.Reason != "" || !strings.Contains(v.Content, "EXPOSE 8080") {
		t.Fatalf("after deploy: %+v %v", v, err)
	}

	// Replace the Dockerfile: other files stay, the checks run, a deployment is queued.
	const next = "FROM alpine:3.22\nUSER 1000\nEXPOSE 9090\nCMD [\"sleep\",\"1000\"]\n"
	dep, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, next)
	if err != nil {
		t.Fatal(err)
	}
	if dep.State != "queued" || dep.SourceKind != "files" {
		t.Errorf("deployment: %+v", dep)
	}
	zp := e.s.sourceZip(dep.ID)
	if got, ok := readZipFile(t, zp, "Dockerfile"); !ok || got != next {
		t.Errorf("Dockerfile in snapshot: %q %v", got, ok)
	}
	if got, ok := readZipFile(t, zp, "app/main.txt"); !ok || got != "hello" {
		t.Errorf("other file lost: %q %v", got, ok)
	}
	v, _ = e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if v.Content != next {
		t.Errorf("GET after PUT: %q", v.Content)
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'dockerfile.update'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows: %d", n)
	}
	if entries, _ := os.ReadDir(e.s.sourcesDir()); func() bool {
		for _, en := range entries {
			if strings.HasPrefix(en.Name(), "tmp-") {
				return true
			}
		}
		return false
	}() {
		t.Error("temporary snapshot left behind")
	}

	// A pre-built image: no EXPOSE is fine.
	if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, "FROM ghcr.io/foo/bar:1.2\n"); err != nil {
		t.Errorf("FROM only: %v", err)
	}

	for name, c := range map[string]struct {
		content string
		code    string
	}{
		"empty":      {"  \n", "dockerfile_empty"},
		"too large":  {"FROM alpine:3.22\n#" + strings.Repeat("x", 65<<10), "dockerfile_too_large"},
		"no FROM":    {"RUN echo hi\n", "no_from"},
		"ambiguous":  {"FROM alpine:3.22\nEXPOSE 80 81\n", "port_ambiguous"},
		"bitploy/ *": {"FROM bitploy/other:1\nEXPOSE 80\n", "base_image_forbidden"},
	} {
		if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, c.content); depCode(err) != c.code {
			t.Errorf("%s: want %s, got %v", name, c.code, err)
		}
	}

	// Linked to git: not editable here.
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://example.com/r.git', git_branch = 'main' WHERE id = ?`, app.ID)
	v, _ = e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if v.Editable || !strings.Contains(v.Reason, "GitHub") {
		t.Errorf("git: %+v", v)
	}
	if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, next); depCode(err) != "git_source_linked" {
		t.Errorf("git put: %v", err)
	}
}

func TestAppDockerfileFromUploadAndStatic(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	// An uploaded zip with a top folder: the Dockerfile is inside it.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"proj/Dockerfile": "FROM alpine:3.22\nUSER 1000\nEXPOSE 80\n", "proj/run.sh": "#!/bin/sh\n"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	if _, err := e.s.UploadAndDeploy(ctx, e.user, app.Slug, bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}
	v, err := e.s.GetAppDockerfile(ctx, e.user, app.Slug)
	if err != nil || !v.Editable || !strings.Contains(v.Content, "EXPOSE 80") {
		t.Fatalf("upload: %+v %v", v, err)
	}
	dep, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, "FROM alpine:3.22\nUSER 1000\nEXPOSE 81\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readZipFile(t, e.s.sourceZip(dep.ID), "run.sh"); !ok {
		t.Error("the top folder should be gone in the new snapshot")
	}

	// A snapshot without a Dockerfile at the path (a static site from the template).
	site := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	if _, err := e.s.DeployFiles(ctx, e.user, site.Slug, []FileInput{{Path: "index.html", Content: "<h1>hi</h1>"}}); err != nil {
		t.Fatal(err)
	}
	v, err = e.s.GetAppDockerfile(ctx, e.user, site.Slug)
	if err != nil || v.Editable || v.Reason == "" {
		t.Errorf("static: %+v %v", v, err)
	}
}

func staticFiles2() []FileInput {
	return []FileInput{
		{Path: "Dockerfile", Content: "FROM alpine:3.22\nUSER 1000\nEXPOSE 8080\nCMD [\"sleep\",\"1000\"]\n"},
		{Path: "app/main.txt", Content: "hello"},
	}
}

// The editor works on the newest attempt, so a Dockerfile that just failed can
// be fixed, and it honors dockerfile_path and context_dir.
func TestAppDockerfileNewestAttemptAndPaths(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles2()); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'running' WHERE app_id = ?`, app.ID)
	e.exec(t, `UPDATE apps SET current_deployment_id = (SELECT id FROM deployments WHERE app_id = ?) WHERE id = ?`, app.ID, app.ID)
	const broken = "FROM alpine:3.22\nUSER 1000\nEXPOSE 7070\nRUN false\n"
	if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, broken); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'failed' WHERE app_id = ? AND state = 'queued'`, app.ID)
	if v, _ := e.s.GetAppDockerfile(ctx, e.user, app.Slug); v.Content != broken {
		t.Errorf("the editor shows %q, want the newest attempt", v.Content)
	}

	// A Dockerfile in a subfolder with its own build context.
	sub := e.mustApp(t, CreateAppInput{})
	path, ctxDir := "deploy/Dockerfile", "web"
	if _, err := e.s.UpdateApp(ctx, e.user, sub.Slug, UpdateAppInput{DockerfilePath: &path, ContextDir: &ctxDir}); err != nil {
		t.Fatal(err)
	}
	files := []FileInput{
		{Path: "deploy/Dockerfile", Content: "FROM alpine:3.22\nUSER 1000\nCOPY index.html /srv/\nEXPOSE 8080\n"},
		{Path: "web/index.html", Content: "<h1>hi</h1>"},
		{Path: "Dockerfile", Content: "FROM wrong:1\n"},
	}
	if _, err := e.s.DeployFiles(ctx, e.user, sub.Slug, files); err != nil {
		t.Fatal(err)
	}
	v, err := e.s.GetAppDockerfile(ctx, e.user, sub.Slug)
	if err != nil || !v.Editable || v.Path != "deploy/Dockerfile" || !strings.Contains(v.Content, "COPY index.html") {
		t.Fatalf("subfolder: %+v %v", v, err)
	}
	next := "FROM alpine:3.22\nUSER 1000\nCOPY index.html /srv/\nEXPOSE 8081\n"
	if _, err := e.s.PutAppDockerfile(ctx, e.user, sub.Slug, next); depCode(err) != "port_reserved" {
		t.Errorf("the checks run on the edited file: %v", err)
	}
	next = strings.Replace(next, "8081", "9000", 1)
	dep, err := e.s.PutAppDockerfile(ctx, e.user, sub.Slug, next)
	if err != nil {
		t.Fatal(err)
	}
	zp := e.s.sourceZip(dep.ID)
	for name, want := range map[string]string{"deploy/Dockerfile": next, "Dockerfile": "FROM wrong:1\n", "web/index.html": "<h1>hi</h1>"} {
		if got, ok := readZipFile(t, zp, name); !ok || got != want {
			t.Errorf("%s = %q %v", name, got, ok)
		}
	}
	if d, _ := e.s.Q.GetDeployment(ctx, dep.ID); d.Port.Int64 != 9000 {
		t.Errorf("port from the edited EXPOSE: %v", d.Port)
	}
}

// Build modes that ignore the app's Dockerfile do not offer to edit it.
func TestAppDockerfileTemplateModes(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	for _, mode := range []string{"static", "node", "static_build"} {
		app := e.mustApp(t, CreateAppInput{BuildMode: mode})
		v, err := e.s.GetAppDockerfile(ctx, e.user, app.Slug)
		if err != nil || v.Editable || v.Content != "" || !strings.Contains(v.Reason, "template") {
			t.Errorf("%s: %+v %v", mode, v, err)
		}
		if _, err := e.s.PutAppDockerfile(ctx, e.user, app.Slug, "FROM alpine:3.22\nEXPOSE 80\n"); depCode(err) != "dockerfile_not_used" {
			t.Errorf("%s put: %v", mode, err)
		}
	}
	// auto without a Dockerfile builds from the template: nothing to show.
	site := e.mustApp(t, CreateAppInput{BuildMode: "auto"})
	if _, err := e.s.DeployFiles(ctx, e.user, site.Slug, []FileInput{{Path: "index.html", Content: "<h1>hi</h1>"}}); err != nil {
		t.Fatal(err)
	}
	v, _ := e.s.GetAppDockerfile(ctx, e.user, site.Slug)
	if v.Editable || v.Content != "" || !strings.Contains(v.Reason, "template") {
		t.Errorf("auto static: %+v", v)
	}
	// A read-only token cannot save, and a foreign app is not found.
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.PutAppDockerfile(ctx, ro, site.Slug, "FROM alpine:3.22\n"); depCode(err) != "token_read_only" {
		t.Errorf("read token: %v", err)
	}
	other := e.user
	other.UserID = "usr_other"
	if _, err := e.s.GetAppDockerfile(ctx, other, site.Slug); depCode(err) != "app_not_found" {
		t.Errorf("foreign app: %v", err)
	}
}
