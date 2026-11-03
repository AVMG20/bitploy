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
