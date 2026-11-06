package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/templates"
)

func TestSetEnvBuildFlags(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	flags := func(vars []EnvVar) map[string]string {
		out := map[string]string{}
		for _, v := range vars {
			out[v.Name] = v.Build
		}
		return out
	}

	vars, err := e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "",
		map[string]string{"VITE_API_URL": "https://api.example.com", "SENTRY_AUTH_TOKEN": "sntrys_abc", "SESSION_SECRET": "x"}, nil,
		map[string]string{"VITE_API_URL": "public", "SENTRY_AUTH_TOKEN": "Secret"})
	if err != nil {
		t.Fatal(err)
	}
	if f := flags(vars); f["VITE_API_URL"] != BuildPublic || f["SENTRY_AUTH_TOKEN"] != BuildSecret || f["SESSION_SECRET"] != "" {
		t.Fatalf("%v", f)
	}
	for _, v := range vars {
		if v.Masked != envMask {
			t.Errorf("%s shown as %q", v.Name, v.Masked)
		}
	}
	// A new value keeps the flag; build alone changes the flag of a variable
	// the app has.
	if vars, err = e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"VITE_API_URL": "https://new.example.com"}, nil); err != nil || flags(vars)["VITE_API_URL"] != BuildPublic {
		t.Fatalf("%v %v", err, flags(vars))
	}
	vars, err = e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "", nil, nil, map[string]string{"SESSION_SECRET": "secret", "SENTRY_AUTH_TOKEN": "none"})
	if f := flags(vars); err != nil || f["SESSION_SECRET"] != BuildSecret || f["SENTRY_AUTH_TOKEN"] != "" {
		t.Fatalf("%v %v", err, f)
	}

	for code, call := range map[string]func() error{
		"env_build_invalid": func() error {
			_, err := e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "", nil, nil, map[string]string{"VITE_API_URL": "runtime"})
			return err
		},
		"env_not_found": func() error {
			_, err := e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "", nil, nil, map[string]string{"MISSING": "public"})
			return err
		},
		"env_conflict": func() error {
			_, err := e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "", nil, []string{"VITE_API_URL"}, map[string]string{"VITE_API_URL": "public"})
			return err
		},
	} {
		if got := depCode(call()); got != code {
			t.Errorf("want %s, got %s", code, got)
		}
	}

	var details string
	_ = e.s.Store.DB.QueryRow(`SELECT group_concat(details) FROM audit_log WHERE action = 'env.set'`).Scan(&details)
	if !strings.Contains(details, `"build"`) || strings.Contains(details, "sntrys_abc") || strings.Contains(details, "example.com") {
		t.Errorf("audit: %s", details)
	}
}

func TestApplyBuildVars(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.SetServiceEnvBuild(ctx, e.user, app.Slug, "",
		map[string]string{"VITE_SUPABASE_URL": "https://x.supabase.co", "SENTRY_AUTH_TOKEN": "sntrys_abc", "RUNTIME_ONLY": "r"}, nil,
		map[string]string{"VITE_SUPABASE_URL": "public", "SENTRY_AUTH_TOKEN": "secret"}); err != nil {
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
		if spec.Dockerfile != df || spec.Args["VITE_SUPABASE_URL"] != "https://x.supabase.co" || spec.Secrets["SENTRY_AUTH_TOKEN"] != "sntrys_abc" {
			t.Fatalf("%+v", spec)
		}
		if _, ok := spec.Args["RUNTIME_ONLY"]; ok {
			t.Fatal("a run-time variable reached the build")
		}
		fp := spec.Args[deploy.BuildSecretsArg]
		if len(fp) != 64 || strings.Contains(fp, "sntrys") {
			t.Fatalf("fingerprint %q", fp)
		}
		got, _ := os.ReadFile(df)
		if !strings.Contains(string(got), "ARG VITE_SUPABASE_URL\nARG "+deploy.BuildSecretsArg+"\nRUN --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN npm run build\n") {
			t.Fatalf("Dockerfile:\n%s", got)
		}
		// The fingerprint follows the value and the app.
		if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"SENTRY_AUTH_TOKEN": "sntrys_new"}, nil); err != nil {
			t.Fatal(err)
		}
		spec2 := deploy.BuildSpec{ContextDir: dir, Dockerfile: df}
		if _, ae := newPipeline(dir).applyBuildVars(ctx, &spec2, singleService, true); ae != nil || spec2.Args[deploy.BuildSecretsArg] == fp {
			t.Fatalf("fingerprint did not change: %v", ae)
		}
		// The log names the variables and masks the secret's value.
		p.writeLog("echo sntrys_abc")
		log := strings.Join(p.ring.Lines(), "\n")
		if !strings.Contains(log, "VITE_SUPABASE_URL (public); SENTRY_AUTH_TOKEN (secret)") || strings.Contains(log, "sntrys_abc") {
			t.Fatalf("log:\n%s", log)
		}
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
		if len(spec.Args) != 0 || len(spec.Secrets) != 0 || !strings.Contains(strings.Join(p.ring.Lines(), "\n"), "not used") {
			t.Fatalf("%+v %v", spec, p.ring.Lines())
		}
	})

	t.Run("own Dockerfile", func(t *testing.T) {
		dir := t.TempDir()
		src := filepath.Join(dir, "src")
		_ = os.MkdirAll(src, 0o755)
		df := filepath.Join(src, "Dockerfile")
		content := "FROM node:22\nCOPY . .\nRUN --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN npm run build\n"
		_ = os.WriteFile(df, []byte(content), 0o644)
		_ = os.WriteFile(df+".dockerignore", []byte("node_modules\n"), 0o644)
		p := newPipeline(dir)
		spec := deploy.BuildSpec{ContextDir: src, Dockerfile: df}
		cleanup, ae := p.applyBuildVars(ctx, &spec, singleService, false)
		if ae != nil {
			t.Fatal(ae)
		}
		if strings.HasPrefix(spec.Dockerfile, src) {
			t.Fatalf("the copy is inside the build context: %s", spec.Dockerfile)
		}
		got, _ := os.ReadFile(spec.Dockerfile)
		if string(got) != "FROM node:22\nCOPY . .\nARG "+deploy.BuildSecretsArg+"\nRUN --mount=type=secret,id=SENTRY_AUTH_TOKEN,env=SENTRY_AUTH_TOKEN npm run build\n" {
			t.Fatalf("copy:\n%s", got)
		}
		if ign, _ := os.ReadFile(spec.Dockerfile + ".dockerignore"); string(ign) != "node_modules\n" {
			t.Fatalf("dockerignore %q", ign)
		}
		if orig, _ := os.ReadFile(df); string(orig) != content {
			t.Fatal("the user's Dockerfile changed")
		}
		if !strings.Contains(strings.Join(p.ring.Lines(), "\n"), "no ARG VITE_SUPABASE_URL") {
			t.Fatalf("no hint for the undeclared ARG: %v", p.ring.Lines())
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
}
