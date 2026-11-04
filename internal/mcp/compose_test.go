package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateCompose(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.readToken)

	// A web app built from the project next to Redis: valid, with a summary naming the public service.
	res := call(t, cs, "validate_compose", map[string]any{"compose": "services:\n  web:\n    build: .\n    ports: [\"3000:3000\"]\n    depends_on: [cache]\n  cache:\n    image: redis:7-alpine\n"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	if m["ok"] != true || len(m["services"].([]any)) != 2 {
		t.Fatalf("result = %v", m)
	}
	if first := strings.Split(text(res), "\n")[0]; !strings.Contains(first, "web (public)") || !strings.Contains(first, "cache") {
		t.Errorf("summary = %q", first)
	}
	if steps := fmt.Sprint(m["next_steps"]); !strings.Contains(steps, "build_mode=compose") {
		t.Errorf("next_steps = %s", steps)
	}

	// A forbidden key: ok=false, a normal result whose errors name the service and key.
	res = call(t, cs, "validate_compose", map[string]any{"compose": "services:\n  web:\n    image: nginx:1.27\n    privileged: true\n"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m = structured(t, res)
	if m["ok"] != false {
		t.Fatalf("must not be ok: %v", m)
	}
	f := m["errors"].([]any)[0].(map[string]any)
	if f["code"] != "compose_forbidden" || f["service"] != "web" || f["key"] != "privileged" || f["hint"] == "" {
		t.Errorf("error = %v", f)
	}
	if !strings.Contains(text(res), "compose_forbidden in service web at privileged") {
		t.Errorf("summary = %q", text(res))
	}

	// Not YAML at all: still ok=false, never a tool error.
	res = call(t, cs, "validate_compose", map[string]any{"compose": "services: [\n"})
	if res.IsError || structured(t, res)["ok"] != false {
		t.Errorf("bad YAML = %s", text(res))
	}
}

func TestCreateComposeApp(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "create_app", map[string]any{"name": "Stack", "build_mode": "compose"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	ap := m["app"].(map[string]any)
	if ap["build_mode"] != "compose" {
		t.Errorf("app = %v", ap)
	}
	if steps := fmt.Sprint(m["next_steps"]); !strings.Contains(steps, "compose file") || !strings.Contains(steps, "set_env") {
		t.Errorf("next_steps = %s", steps)
	}
	res = call(t, cs, "create_app", map[string]any{"name": "Bad", "build_mode": "kubernetes"})
	if !res.IsError {
		t.Errorf("an unknown build_mode must fail: %s", text(res))
	}
}
