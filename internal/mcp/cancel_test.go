package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestCancelDeploymentTool(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "create_app", map[string]any{"name": "Site", "slug": "site-app"})
	if res.IsError {
		t.Fatal(text(res))
	}
	var appID string
	if err := e.svc.Store.DB.QueryRow(`SELECT id FROM apps WHERE slug = 'site-app'`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	add := func(id string, seq int, state string) {
		t.Helper()
		if _, err := e.svc.Store.DB.Exec(`INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES (?, ?, ?, ?, 'files', 'api', ?)`,
			id, appID, seq, state, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}

	// Nothing to cancel by app; neither id nor app is a mistake in the call.
	res = call(t, cs, "cancel_deployment", map[string]any{"app": "site-app"})
	if m := structured(t, res); !res.IsError || m["code"] != "no_deployment_to_cancel" || !strings.Contains(m["message"].(string), "no deployment that is queued") {
		t.Errorf("none: %v", m)
	}
	res = call(t, cs, "cancel_deployment", map[string]any{})
	if m := structured(t, res); !res.IsError || m["code"] != "invalid_arguments" {
		t.Errorf("no arguments: %v", m)
	}

	// By id.
	add("dep_a", 1, "queued")
	res = call(t, cs, "cancel_deployment", map[string]any{"id": "dep_a"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	d, _ := m["deployment"].(map[string]any)
	if d == nil || d["state"] != "cancelled" || d["id"] != "dep_a" {
		t.Errorf("by id: %v", m)
	}
	out := text(res)
	if !strings.Contains(out, "cancelled") || strings.Contains(strings.ToLower(out), "failed") || strings.Contains(out, "3 failures") {
		t.Errorf("text = %s", out)
	}
	if steps, _ := m["next_steps"].([]any); len(steps) == 0 || !strings.Contains(steps[0].(string), "not a failure") {
		t.Errorf("next steps = %v", m["next_steps"])
	}

	// By app: the open one, not the finished ones.
	add("dep_old", 2, "failed")
	add("dep_b", 3, "queued")
	res = call(t, cs, "cancel_deployment", map[string]any{"app": "site-app"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if d, _ := structured(t, res)["deployment"].(map[string]any); d == nil || d["id"] != "dep_b" || d["state"] != "cancelled" {
		t.Errorf("by app: %v", structured(t, res))
	}

	// Refusals: finished, too late, unknown, and a read token.
	res = call(t, cs, "cancel_deployment", map[string]any{"id": "dep_a"})
	if m := structured(t, res); !res.IsError || m["code"] != "deployment_finished" {
		t.Errorf("finished: %v", m)
	}
	add("dep_s", 4, "swapping")
	res = call(t, cs, "cancel_deployment", map[string]any{"id": "dep_s"})
	if m := structured(t, res); !res.IsError || m["code"] != "too_late_to_cancel" || m["hint"] != "Wait a moment; roll back if needed." {
		t.Errorf("swapping: %v", m)
	}
	res = call(t, cs, "cancel_deployment", map[string]any{"id": "dep_nope"})
	if m := structured(t, res); !res.IsError || m["code"] != "deployment_not_found" {
		t.Errorf("unknown: %v", m)
	}
	add("dep_c", 5, "queued")
	ro := e.connect(t, e.readToken)
	res = call(t, ro, "cancel_deployment", map[string]any{"id": "dep_c"})
	if m := structured(t, res); !res.IsError || m["code"] != "token_read_only" {
		t.Errorf("read token: %v", m)
	}
	var state string
	_ = e.svc.Store.DB.QueryRow(`SELECT state FROM deployments WHERE id = 'dep_c'`).Scan(&state)
	if state != "queued" {
		t.Errorf("a read token cancelled a deployment: %s", state)
	}
}
