package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func envByName(vars []EnvVar) map[string]EnvVar {
	out := map[string]EnvVar{}
	for _, v := range vars {
		out[v.Name] = v
	}
	return out
}

// A variable is readable unless hidden; a hidden value is never returned.
func TestEnvHidden(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	clock := time.Unix(1_800_000_000, 0)
	e.s.Now = func() time.Time { return clock }
	change := func(c EnvChange) ([]EnvVar, error) { return e.s.ChangeEnv(ctx, e.user, app.Slug, c) }

	vars, err := change(EnvChange{
		Set:    map[string]string{"NODE_ENV": "production", "STRIPE_SECRET_KEY": "sk_live_hunter2"},
		Hidden: map[string]bool{"STRIPE_SECRET_KEY": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := envByName(vars)
	if v := m["NODE_ENV"]; v.Hidden || v.Value == nil || *v.Value != "production" {
		t.Fatalf("readable: %+v", v)
	}
	if v := m["STRIPE_SECRET_KEY"]; !v.Hidden || v.Value != nil || v.Masked != envMask {
		t.Fatalf("hidden: %+v", v)
	}

	// A new value keeps the flag; without a guess a new name is readable.
	vars, err = e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"STRIPE_SECRET_KEY": "sk_live_new", "API_TOKEN": "tok_1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m = envByName(vars)
	if !m["STRIPE_SECRET_KEY"].Hidden || m["API_TOKEN"].Hidden {
		t.Fatalf("%+v", m)
	}

	// Hiding needs no value and leaves updated_at alone (no restart to ask for).
	before := m["NODE_ENV"].UpdatedAt
	clock = clock.Add(time.Minute)
	if vars, err = change(EnvChange{Hidden: map[string]bool{"NODE_ENV": true}}); err != nil {
		t.Fatal(err)
	}
	if v := envByName(vars)["NODE_ENV"]; !v.Hidden || v.Value != nil || !v.UpdatedAt.Equal(before) {
		t.Fatalf("hide: %+v", v)
	}

	// Making it readable again needs the value.
	if _, err := change(EnvChange{Hidden: map[string]bool{"NODE_ENV": false}}); depCode(err) != "env_value_needed" {
		t.Fatalf("unhide without value: %v", err)
	}
	if vars, err = change(EnvChange{Set: map[string]string{"NODE_ENV": "production"}, Hidden: map[string]bool{"NODE_ENV": false}}); err != nil {
		t.Fatal(err)
	}
	if v := envByName(vars)["NODE_ENV"]; v.Hidden || v.Value == nil {
		t.Fatalf("unhide: %+v", v)
	}

	if _, err := change(EnvChange{Hidden: map[string]bool{"MISSING": true}}); depCode(err) != "env_not_found" {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := change(EnvChange{Unset: []string{"API_TOKEN"}, Hidden: map[string]bool{"API_TOKEN": true}}); depCode(err) != "env_conflict" {
		t.Errorf("unset and hidden: %v", err)
	}

	// A guess hides a new name, never makes a hidden one readable, and an
	// explicit flag wins over it.
	guess := func(n string) bool { return strings.Contains(n, "SECRET") }
	vars, err = change(EnvChange{
		Set:         map[string]string{"SESSION_SECRET": "s3cr3t-value", "STRIPE_SECRET_KEY": "sk_live_hunter2", "PLAIN": "x", "OTHER_SECRET": "y", "NODE_ENV": "production"},
		Hidden:      map[string]bool{"OTHER_SECRET": false},
		GuessHidden: guess,
	})
	if err != nil {
		t.Fatal(err)
	}
	m = envByName(vars)
	if !m["SESSION_SECRET"].Hidden || !m["STRIPE_SECRET_KEY"].Hidden || m["PLAIN"].Hidden || m["OTHER_SECRET"].Hidden || m["NODE_ENV"].Hidden {
		t.Fatalf("guess: %+v", m)
	}
	if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"NODE_ENV": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	vars, _ = change(EnvChange{Hidden: map[string]bool{"NODE_ENV": true}})
	vars, _ = change(EnvChange{Set: map[string]string{"NODE_ENV": "y"}, GuessHidden: guess})
	if !envByName(vars)["NODE_ENV"].Hidden {
		t.Fatal("a guess made a hidden variable readable")
	}

	// A read token reads readable values, never hidden ones.
	ro := e.user
	ro.Scope = ScopeRead
	list, err := e.s.ListEnv(ctx, ro, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.Hidden && v.Value != nil || !v.Hidden && v.Value == nil {
			t.Errorf("%+v", v)
		}
	}

	// The audit log has the flags, never a value.
	var details string
	_ = e.s.Store.DB.QueryRow(`SELECT group_concat(details) FROM audit_log WHERE action = 'env.set'`).Scan(&details)
	if strings.Contains(details, "hunter2") || strings.Contains(details, "s3cr3t") || !strings.Contains(details, `"hidden":{"NODE_ENV":true}`) {
		t.Errorf("audit: %s", details)
	}
}

// The service "app" of a single app is its shared variables, flags included.
func TestEnvHiddenSingleService(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.ChangeEnv(ctx, e.user, app.Slug, EnvChange{Set: map[string]string{"A": "shared-value"}, Hidden: map[string]bool{"A": true}}); err != nil {
		t.Fatal(err)
	}
	vars, err := e.s.ChangeEnv(ctx, e.user, app.Slug, EnvChange{Service: "app", Hidden: map[string]bool{"A": false}})
	if depCode(err) != "env_value_needed" {
		t.Fatalf("%v %+v", err, vars)
	}
}

// The build log masks a hidden value even when it reads like a plain word.
func TestBuildVarsHidden(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.ChangeEnv(ctx, e.user, app.Slug, EnvChange{
		Set:    map[string]string{"VITE_CODE": "letmein", "NODE_ENV": "production"},
		Hidden: map[string]bool{"VITE_CODE": true},
	}); err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Q.GetApp(ctx, app.ID)
	vars, hidden, ae := e.s.buildVars(ctx, row, singleService)
	if ae != nil || vars["VITE_CODE"] != "letmein" || !hidden["VITE_CODE"] || hidden["NODE_ENV"] {
		t.Fatalf("%v %v %v", ae, vars, hidden)
	}
	if !maskedInBuildLog("VITE_CODE", vars["VITE_CODE"], true) || maskedInBuildLog("NODE_ENV", vars["NODE_ENV"], false) {
		t.Fatal("mask")
	}
}
