package mcp

import (
	"strings"
	"testing"
)

// The same cases as web/tests/secretName.test.ts.
func TestLooksSecretName(t *testing.T) {
	for name, want := range map[string]bool{
		"STRIPE_SECRET_KEY": true, "github_token": true, "API_KEY": true, "DB_PASSWORD": true, "Monkey": true,
		"NODE_ENV": false, "DATABASE_URL": false, "VITE_API_URL": false, "PORT_NUMBER": false,
	} {
		if got := looksSecretName(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestSetEnvHidden(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Bot", "slug": "env-bot"}); res.IsError {
		t.Fatal(text(res))
	}
	// Left out: hidden by the name. Passed: the flag wins.
	res := call(t, cs, "set_env", map[string]any{"app": "env-bot",
		"set":    map[string]any{"NODE_ENV": "production", "DISCORD_TOKEN": "tok-abc123", "PUBLIC_KEY": "pk_123", "SECRET_PHRASE": "s3cr3t-phrase"},
		"hidden": map[string]any{"PUBLIC_KEY": false, "NODE_ENV": true}})
	if res.IsError || strings.Contains(text(res), "tok-abc123") || strings.Contains(text(res), "s3cr3t-phrase") {
		t.Fatalf("set_env: %s", text(res))
	}
	listed := func() map[string]map[string]any {
		t.Helper()
		res := call(t, cs, "list_env", map[string]any{"app": "env-bot"})
		if res.IsError {
			t.Fatal(text(res))
		}
		out := map[string]map[string]any{}
		for _, v := range structured(t, res)["env"].([]any) {
			m := v.(map[string]any)
			out[m["name"].(string)] = m
		}
		return out
	}
	got := listed()
	for name, hidden := range map[string]bool{"NODE_ENV": true, "DISCORD_TOKEN": true, "PUBLIC_KEY": false, "SECRET_PHRASE": true} {
		v := got[name]
		_, hasValue := v["value"]
		if (v["hidden"] == true) != hidden || hasValue == hidden || hidden && v["masked"] == nil {
			t.Errorf("%s: %v", name, v)
		}
	}
	if got["PUBLIC_KEY"]["value"] != "pk_123" {
		t.Errorf("readable value: %v", got["PUBLIC_KEY"])
	}

	// A new value without hidden keeps a hidden variable hidden.
	if res := call(t, cs, "set_env", map[string]any{"app": "env-bot", "set": map[string]any{"NODE_ENV": "development"}}); res.IsError {
		t.Fatal(text(res))
	}
	if listed()["NODE_ENV"]["hidden"] != true {
		t.Error("NODE_ENV turned readable")
	}
	// Readable again only with its value.
	if m := assertErrorShape(t, "unhide", call(t, cs, "set_env", map[string]any{"app": "env-bot", "hidden": map[string]any{"NODE_ENV": false}})); m == nil || m["code"] != "env_value_needed" {
		t.Errorf("unhide without value: %v", m)
	}
	res = call(t, cs, "set_env", map[string]any{"app": "env-bot", "set": map[string]any{"NODE_ENV": "production"}, "hidden": map[string]any{"NODE_ENV": false}})
	if res.IsError || listed()["NODE_ENV"]["value"] != "production" {
		t.Fatalf("unhide: %s", text(res))
	}
	// Hiding alone changes no value, so nothing to restart.
	res = call(t, cs, "set_env", map[string]any{"app": "env-bot", "hidden": map[string]any{"PUBLIC_KEY": true}})
	if res.IsError || !strings.Contains(text(res), "needs no restart") || listed()["PUBLIC_KEY"]["hidden"] != true {
		t.Fatalf("hide: %s", text(res))
	}
}
