package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestSetEnvHasNoBuildChoice(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Site", "slug": "vite-site"}); res.IsError {
		t.Fatal(text(res))
	}
	res := call(t, cs, "set_env", map[string]any{"app": "vite-site",
		"set": map[string]any{"VITE_SUPABASE_URL": "https://x.supabase.co", "SENTRY_AUTH_TOKEN": "sntrys_abc"}})
	if res.IsError || strings.Contains(text(res), "sntrys_abc") {
		t.Fatalf("set_env: %s", text(res))
	}
	if steps := fmt.Sprint(structured(t, res)["next_steps"]); !strings.Contains(steps, "first deploy") {
		t.Errorf("next steps: %s", steps)
	}
	res = call(t, cs, "list_env", map[string]any{"app": "vite-site"})
	for _, v := range structured(t, res)["env"].([]any) {
		if _, ok := v.(map[string]any)["build"]; ok {
			t.Errorf("list_env still has build: %v", v)
		}
	}
	if m := assertErrorShape(t, "build", call(t, cs, "set_env", map[string]any{"app": "vite-site", "build": map[string]any{"VITE_SUPABASE_URL": "public"}})); m != nil && m["code"] != "invalid_arguments" {
		t.Errorf("build: %v", m)
	}
}

// set_env asks for a deploy when the live version's build read a changed
// variable, and for a restart otherwise.
func TestSetEnvChangeNext(t *testing.T) {
	running := func(mode string, reads ...string) *app.App {
		return &app.App{Slug: "a", Status: "running", BuildMode: mode, Current: &app.Deployment{State: "running", BuildReads: reads}}
	}
	cases := []struct {
		name    string
		app     *app.App
		changed []string
		deploy  bool
	}{
		{"own Dockerfile reads it", running("dockerfile", "VITE_A", "TOKEN"), []string{"OTHER", "TOKEN"}, true},
		{"own Dockerfile does not read it", running("dockerfile", "VITE_A"), []string{"DATABASE_URL"}, false},
		{"build script reads every one", running("auto", "*"), []string{"DATABASE_URL"}, true},
		{"site bitploy serves", running("static_build"), []string{"VITE_A"}, true},
		{"nothing read", running("auto"), []string{"VITE_A"}, false},
	}
	for _, c := range cases {
		steps := strings.Join(setEnvChangeNext("a", c.app, c.changed), " ")
		if got := strings.Contains(steps, "next deploy"); got != c.deploy {
			t.Errorf("%s: %s", c.name, steps)
		}
		if !c.deploy && !strings.Contains(steps, "restart_app with app=a") {
			t.Errorf("%s: no restart: %s", c.name, steps)
		}
	}
	if steps := fmt.Sprint(setEnvChangeNext("a", running("dockerfile", "TOKEN"), []string{"TOKEN", "B"})); !strings.Contains(steps, "The build reads TOKEN,") {
		t.Errorf("names: %s", steps)
	}
}

func TestGitFirstDeployStepFollowsBuildMode(t *testing.T) {
	for mode, want := range map[string]string{"auto": "builds a Node.js app", "dockerfile": "unless it is a static site", "compose": "compose file"} {
		if got := gitFirstDeployStep(app.App{Slug: "a", BuildMode: mode}); !strings.Contains(got, want) {
			t.Errorf("%s: %s", mode, got)
		}
	}
	if got := gitFirstDeployStep(app.App{Slug: "a", BuildMode: "auto"}); strings.Contains(got, "must have a Dockerfile") {
		t.Errorf("auto asks for a Dockerfile: %s", got)
	}
}

func TestBuildModeAutoOverMCP(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "create_app", map[string]any{"name": "Repo", "slug": "their-repo", "build_mode": "auto"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if steps := fmt.Sprint(structured(t, res)["next_steps"]); !strings.Contains(steps, "Build mode auto") {
		t.Errorf("next steps: %s", steps)
	}
	if res := call(t, cs, "update_app", map[string]any{"app": "their-repo", "build_mode": "dockerfile"}); res.IsError {
		t.Fatal(text(res))
	}
	if res := call(t, cs, "update_app", map[string]any{"app": "their-repo", "build_mode": "auto"}); res.IsError {
		t.Fatal(text(res))
	}
	if m := assertErrorShape(t, "static", call(t, cs, "update_app", map[string]any{"app": "their-repo", "build_mode": "static"})); m == nil {
		t.Error("build_mode static accepted over MCP")
	}
	if got := hintSuffix("deploy", "dockerfile_missing"); !strings.Contains(got, "build_mode=auto") {
		t.Errorf("dockerfile_missing hint for deploy: %s", got)
	}
}
