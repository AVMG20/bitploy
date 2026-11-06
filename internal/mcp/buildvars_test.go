package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestSetEnvBuildVariables(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Site", "slug": "vite-site"}); res.IsError {
		t.Fatal(text(res))
	}
	res := call(t, cs, "set_env", map[string]any{"app": "vite-site",
		"set":   map[string]any{"VITE_SUPABASE_URL": "https://x.supabase.co", "SENTRY_AUTH_TOKEN": "sntrys_abc"},
		"build": map[string]any{"VITE_SUPABASE_URL": "public", "SENTRY_AUTH_TOKEN": "secret"}})
	if res.IsError || strings.Contains(text(res), "sntrys_abc") {
		t.Fatalf("set_env: %s", text(res))
	}
	if steps := fmt.Sprint(structured(t, res)["next_steps"]); !strings.Contains(steps, "first deploy") {
		t.Errorf("next steps: %s", steps)
	}

	res = call(t, cs, "list_env", map[string]any{"app": "vite-site"})
	env := structured(t, res)["env"].([]any)
	got := map[string]any{}
	for _, v := range env {
		m := v.(map[string]any)
		got[m["name"].(string)] = m["build"]
	}
	if got["VITE_SUPABASE_URL"] != "public" || got["SENTRY_AUTH_TOKEN"] != "secret" {
		t.Errorf("list_env build = %v", got)
	}

	if m := assertErrorShape(t, "bad build", call(t, cs, "set_env", map[string]any{"app": "vite-site", "build": map[string]any{"VITE_SUPABASE_URL": "maybe"}})); m != nil && m["code"] != "env_build_invalid" {
		t.Errorf("bad build: %v", m)
	}
	if m := assertErrorShape(t, "unknown name", call(t, cs, "set_env", map[string]any{"app": "vite-site", "build": map[string]any{"NOPE": "public"}})); m != nil && m["code"] != "env_not_found" {
		t.Errorf("unknown name: %v", m)
	}
}
