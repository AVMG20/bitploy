package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// The dashboard sends the hidden flag it shows; the server guesses nothing.
func TestEnvHiddenOverREST(t *testing.T) {
	e := newEnv(t)
	e.login("env@example.com", false)
	if w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "env-site"}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w := e.asSession("PUT", "/api/v1/apps/env-site/env", map[string]any{
		"set":    map[string]string{"API_TOKEN": "tok-plain-1", "STRIPE_KEY": "sk_hunter2"},
		"hidden": map[string]bool{"STRIPE_KEY": true},
	})
	if w.Code != 200 || strings.Contains(w.Body.String(), "sk_hunter2") {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	w = e.asSession("GET", "/api/v1/apps/env-site/env", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "sk_hunter2") {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	byName := map[string]app.EnvVar{}
	for _, v := range decode[app.Page[app.EnvVar]](t, w).Items {
		byName[v.Name] = v
	}
	// No name rule over REST: API_TOKEN is readable because nothing said hidden.
	if v := byName["API_TOKEN"]; v.Hidden || v.Value == nil || *v.Value != "tok-plain-1" {
		t.Errorf("API_TOKEN: %+v", v)
	}
	if v := byName["STRIPE_KEY"]; !v.Hidden || v.Value != nil {
		t.Errorf("STRIPE_KEY: %+v", v)
	}
	if w := e.asSession("PUT", "/api/v1/apps/env-site/env", map[string]any{"hidden": map[string]bool{"STRIPE_KEY": false}}); w.Code != 400 || !strings.Contains(w.Body.String(), "env_value_needed") {
		t.Errorf("unhide without value: %d %s", w.Code, w.Body)
	}
}
