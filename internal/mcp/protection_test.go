package mcp

import (
	"strings"
	"testing"
)

func TestSetPasswordProtectionOverMCP(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	call(t, cs, "create_app", map[string]any{"name": "Shop", "slug": "shop-pw"})

	if got := structured(t, call(t, cs, "get_app", map[string]any{"app": "shop-pw"}))["app"].(map[string]any); got["protected"] != false {
		t.Fatalf("a new app is not protected: %v", got["protected"])
	}

	res := call(t, cs, "set_password_protection", map[string]any{"app": "shop-pw", "enabled": true, "username": "team", "password": "blue-river-42"})
	if res.IsError {
		t.Fatalf("enable: %s", text(res))
	}
	if strings.Contains(text(res), "blue-river-42") || strings.Contains(text(res), "$2a$") {
		t.Fatalf("the result repeats the secret: %s", text(res))
	}
	ap := structured(t, res)["app"].(map[string]any)
	if ap["protected"] != true || ap["protected_username"] != "team" {
		t.Fatalf("app: %v", ap)
	}
	for k := range ap {
		if strings.Contains(strings.ToLower(k), "hash") || strings.Contains(strings.ToLower(k), "password") {
			t.Errorf("app result has a secret field %q", k)
		}
	}
	if steps := strings.Join(nextSteps(t, "enable", res), " "); !strings.Contains(steps, "team") || !strings.Contains(steps, "enabled=false") {
		t.Errorf("next steps: %s", steps)
	}

	// get_app and list_apps report it.
	if got := structured(t, call(t, cs, "get_app", map[string]any{"app": "shop-pw"})); got["app"].(map[string]any)["protected"] != true {
		t.Fatalf("get_app: %v", got)
	}
	if r := call(t, cs, "get_app", map[string]any{"app": "shop-pw"}); !strings.Contains(text(r), "password-protected") {
		t.Errorf("get_app summary: %s", text(r))
	}
	apps := structured(t, call(t, cs, "list_apps", map[string]any{}))["apps"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["protected"] != true {
		t.Fatalf("list_apps: %v", apps)
	}

	// Bad input is a normal bitploy error with a way out.
	res = call(t, cs, "set_password_protection", map[string]any{"app": "shop-pw", "enabled": true, "password": "short"})
	if m := assertErrorShape(t, "short password", res); m != nil && m["code"] != "password_too_short" {
		t.Errorf("code = %v", m["code"])
	}
	if strings.Contains(text(res), "\"short\"") {
		t.Errorf("the error repeats the password: %s", text(res))
	}
	res = call(t, cs, "set_password_protection", map[string]any{"app": "shop-pw", "enabled": true, "username": "a:b"})
	if m := assertErrorShape(t, "colon", res); m != nil && m["code"] != "username_invalid" {
		t.Errorf("code = %v", m["code"])
	}

	// A read-only token cannot.
	ro := e.connect(t, e.readToken)
	res = call(t, ro, "set_password_protection", map[string]any{"app": "shop-pw", "enabled": false})
	if m := assertErrorShape(t, "read-only", res); m != nil && m["code"] != "token_read_only" {
		t.Errorf("code = %v", m["code"])
	}

	res = call(t, cs, "set_password_protection", map[string]any{"app": "shop-pw", "enabled": false})
	if res.IsError || structured(t, res)["app"].(map[string]any)["protected"] != false {
		t.Fatalf("disable: %s", text(res))
	}
}

func TestPasswordProtectionIsAskedForOnlyByTheUser(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	for _, tl := range listTools(t, cs) {
		if tl.Name != "set_password_protection" {
			continue
		}
		if !strings.Contains(tl.Description, "ONLY call this when the user asks") {
			t.Errorf("description must say it is only for when the user asks: %s", tl.Description)
		}
		return
	}
	t.Fatal("set_password_protection missing")
}
