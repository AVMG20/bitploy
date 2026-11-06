package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/templates"
)

func TestApplyBuildVars(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{
		"VITE_SUPABASE_URL": "https://x.supabase.co", "SENTRY_AUTH_TOKEN": "sntrys_abc123", "NODE_ENV": "production",
		"PATH": "/opt/bin", "TARGETARCH": "riscv",
	}, nil); err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Q.GetApp(ctx, app.ID)
	newPipeline := func(dir string) *pipeline {
		return &pipeline{s: e.s, app: row, ring: deploy.NewLineRing(50), buildDir: filepath.Join(dir, "build")}
	}

	t.Run("generated static build", func(t *testing.T) {
		dir := t.TempDir()
		tpl, _ := templates.StaticBuild([]string{"package.json", "package-lock.json"}, "dist", true)
		df := filepath.Join(dir, deploy.NodeSPADockerfilePath)
		_ = os.WriteFile(df, []byte(tpl.Dockerfile), 0o644)
		p := newPipeline(dir)
		spec := deploy.BuildSpec{ContextDir: dir, Dockerfile: df}
		cleanup, ae := p.applyBuildVars(ctx, &spec, singleService, true)
		defer cleanup()
		if ae != nil {
			t.Fatal(ae)
		}
		// Every variable is a secret of the build script, none a build argument.
		if spec.Dockerfile != df || len(spec.Args) != 1 || spec.Secrets["VITE_SUPABASE_URL"] != "https://x.supabase.co" ||
			spec.Secrets["SENTRY_AUTH_TOKEN"] != "sntrys_abc123" || spec.Secrets["NODE_ENV"] != "production" || spec.Secrets["TARGETARCH"] != "riscv" {
			t.Fatalf("%+v", spec)
		}
		if _, ok := spec.Secrets["PATH"]; ok {
			t.Fatal("PATH reached the build script")
		}
		fp := spec.Args[deploy.BuildSecretsArg]
		if len(fp) != 64 || strings.Contains(fp, "sntrys") {
			t.Fatalf("fingerprint %q", fp)
		}
		got, _ := os.ReadFile(df)
		if !strings.Contains(string(got), "ARG "+deploy.BuildSecretsArg+"\nRUN --mount=type=secret,id=NODE_ENV,env=NODE_ENV --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN "+
			"--mount=type=secret,id=TARGETARCH,env=TARGETARCH --mount=type=secret,id=VITE_SUPABASE_URL,env=VITE_SUPABASE_URL npm run build\n") {
			t.Fatalf("Dockerfile:\n%s", got)
		}
		if !p.buildReads["*"] {
			t.Fatalf("reads %v", p.buildReads)
		}
		// The fingerprint follows the value and the app.
		if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"SENTRY_AUTH_TOKEN": "sntrys_new456"}, nil); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(df, []byte(tpl.Dockerfile), 0o644)
		spec2 := deploy.BuildSpec{ContextDir: dir, Dockerfile: df}
		if _, ae := newPipeline(dir).applyBuildVars(ctx, &spec2, singleService, true); ae != nil || spec2.Args[deploy.BuildSecretsArg] == fp {
			t.Fatalf("fingerprint did not change: %v", ae)
		}
		// The log names the variables and masks what may be a secret, not plain words.
		p.writeLog("echo sntrys_abc123 in production")
		log := strings.Join(p.ring.Lines(), "\n")
		if !strings.Contains(log, "NODE_ENV, SENTRY_AUTH_TOKEN, TARGETARCH, VITE_SUPABASE_URL.") || strings.Contains(log, "sntrys_abc123") || !strings.Contains(log, "in production") {
			t.Fatalf("log:\n%s", log)
		}
		_, _ = e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"SENTRY_AUTH_TOKEN": "sntrys_abc123"}, nil)
	})

	t.Run("plain website", func(t *testing.T) {
		dir := t.TempDir()
		tpl, _ := templates.Get("static")
		df := filepath.Join(dir, deploy.StaticDockerfilePath)
		_ = os.WriteFile(df, []byte(tpl.Dockerfile), 0o644)
		p := newPipeline(dir)
		spec := deploy.BuildSpec{ContextDir: dir, Dockerfile: df}
		if _, ae := p.applyBuildVars(ctx, &spec, singleService, true); ae != nil {
			t.Fatal(ae)
		}
		if len(spec.Args) != 0 || len(spec.Secrets) != 0 || len(p.buildReads) != 0 {
			t.Fatalf("%+v %v", spec, p.buildReads)
		}
	})

	t.Run("own Dockerfile", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		_ = os.MkdirAll(src, 0o755)
		df := filepath.Join(src, "Dockerfile")
		content := "FROM node:22\nARG VITE_SUPABASE_URL\nARG TARGETARCH\nARG UNSET_ONE\nCOPY . .\nRUN --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN npm run build\n"
		_ = os.WriteFile(df, []byte(content), 0o644)
		_ = os.WriteFile(df+".dockerignore", []byte("node_modules\n"), 0o644)
		p := newPipeline(dir)
		spec := deploy.BuildSpec{ContextDir: src, Dockerfile: df}
		cleanup, ae := p.applyBuildVars(ctx, &spec, singleService, false)
		if ae != nil {
			t.Fatal(ae)
		}
		// Build arguments only for what it declares (never a platform
		// argument), secrets only for what it mounts.
		if len(spec.Args) != 2 || spec.Args["VITE_SUPABASE_URL"] != "https://x.supabase.co" || spec.Args[deploy.BuildSecretsArg] == "" {
			t.Fatalf("args %v", spec.Args)
		}
		if len(spec.Secrets) != 1 || spec.Secrets["SENTRY_AUTH_TOKEN"] != "sntrys_abc123" {
			t.Fatalf("secrets %v", spec.Secrets)
		}
		if got := encodeBuildReads(p.buildReads); got != "SENTRY_AUTH_TOKEN,TARGETARCH,UNSET_ONE,VITE_SUPABASE_URL" {
			t.Fatalf("reads %s", got)
		}
		if strings.HasPrefix(spec.Dockerfile, src) {
			t.Fatalf("the copy is inside the build context: %s", spec.Dockerfile)
		}
		got, _ := os.ReadFile(spec.Dockerfile)
		if string(got) != strings.Replace(content, "RUN --mount", "ARG "+deploy.BuildSecretsArg+"\nRUN --mount", 1) {
			t.Fatalf("copy:\n%s", got)
		}
		if ign, _ := os.ReadFile(spec.Dockerfile + ".dockerignore"); string(ign) != "node_modules\n" {
			t.Fatalf("dockerignore %q", ign)
		}
		if orig, _ := os.ReadFile(df); string(orig) != content {
			t.Fatal("the user's Dockerfile changed")
		}
		copyPath := spec.Dockerfile
		cleanup()
		if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
			t.Fatal("copy not removed")
		}
		if _, err := os.Stat(copyPath + ".dockerignore"); !os.IsNotExist(err) {
			t.Fatal("dockerignore copy not removed")
		}
	})

	t.Run("compose build args win", func(t *testing.T) {
		dir := t.TempDir()
		df := filepath.Join(dir, "Dockerfile")
		_ = os.WriteFile(df, []byte("FROM node:22\nARG VITE_SUPABASE_URL\nARG NODE_ENV\nRUN npm run build\n"), 0o644)
		p := newPipeline(dir)
		spec := deploy.BuildSpec{ContextDir: dir, Dockerfile: df, Args: map[string]string{"VITE_SUPABASE_URL": "from-compose"}}
		if _, ae := p.applyBuildVars(ctx, &spec, "web", false); ae != nil {
			t.Fatal(ae)
		}
		if spec.Args["VITE_SUPABASE_URL"] != "from-compose" || spec.Args["NODE_ENV"] != "production" || len(spec.Secrets) != 0 || spec.Dockerfile != df {
			t.Fatalf("%+v", spec)
		}
	})
}

func TestFitBuildVars(t *testing.T) {
	big := strings.Repeat("x", 600<<10)
	vars := map[string]string{"A": big, "B": big, "C": "small"}
	kept, dropped := fitBuildVars(vars, []string{"A", "B", "C"})
	if len(dropped) != 1 || len(kept) != 2 || !slices.Contains(kept, "C") {
		t.Fatalf("kept %v dropped %v", kept, dropped)
	}
	if kept, dropped := fitBuildVars(vars, []string{"A", "C"}); len(dropped) != 0 || len(kept) != 2 {
		t.Fatalf("kept %v dropped %v", kept, dropped)
	}
}

func TestMaskedInBuildLog(t *testing.T) {
	for v, want := range map[string]bool{"production": false, "abc": false, "sk_live_123456": true, "postgres://u:p@db/x": true, "3000": false} {
		if got := maskedInBuildLog("X", v); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
	if maskedInBuildLog("VITE_API_URL", "https://api.example.com") {
		t.Error("a value the site shows anyway is masked")
	}
}

func TestVariableNeedsDeploy(t *testing.T) {
	dep := func(reads ...string) *Deployment { return &Deployment{BuildReads: reads} }
	cases := []struct {
		app  App
		name string
		want bool
	}{
		{App{BuildMode: "static_build"}, "ANY", true},
		{App{Static: true}, "ANY", true},
		{App{BuildMode: "dockerfile"}, "ANY", false},
		{App{BuildMode: "dockerfile", Current: dep("TOKEN")}, "TOKEN", true},
		{App{BuildMode: "dockerfile", Current: dep("TOKEN")}, "OTHER", false},
		{App{BuildMode: "auto", Current: dep("*")}, "OTHER", true},
	}
	for _, c := range cases {
		if got := c.app.VariableNeedsDeploy(c.name); got != c.want {
			t.Errorf("%+v %s: %v", c.app, c.name, got)
		}
	}
	if got := decodeBuildReads(encodeBuildReads(map[string]bool{"B": true, "A": true})); !slices.Equal(got, []string{"A", "B"}) || decodeBuildReads("") != nil {
		t.Fatalf("%v", got)
	}
}
