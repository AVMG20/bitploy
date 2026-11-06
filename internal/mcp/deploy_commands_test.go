package mcp

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestDeployCommandFields(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	res := call(t, cs, "create_app", map[string]any{"name": "Shop", "slug": "shop-app", "before_deploy_command": "php artisan migrate --force"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if ap := structured(t, res)["app"].(map[string]any); ap["before_deploy_command"] != "php artisan migrate --force" || ap["after_deploy_command"] != "" {
		t.Errorf("create_app: %v", ap)
	}
	res = call(t, cs, "update_app", map[string]any{"app": "shop-app", "after_deploy_command": "php artisan config:cache"})
	if res.IsError || !strings.Contains(text(res), "[before-deploy]") {
		t.Fatalf("update_app: %s", text(res))
	}
	res = call(t, cs, "get_app", map[string]any{"app": "shop-app"})
	if ap := structured(t, res)["app"].(map[string]any); ap["before_deploy_command"] != "php artisan migrate --force" || ap["after_deploy_command"] != "php artisan config:cache" {
		t.Errorf("get_app: %v", ap)
	}

	if res := call(t, cs, "create_app", map[string]any{"name": "Stack", "slug": "stack-app", "build_mode": "compose"}); res.IsError {
		t.Fatal(text(res))
	}
	if m := assertErrorShape(t, "compose", call(t, cs, "update_app", map[string]any{"app": "stack-app", "before_deploy_command": "migrate"})); m != nil && m["code"] != "deploy_commands_unsupported" {
		t.Errorf("compose: %v", m)
	}
}

func TestDeploymentResultAfterDeploy(t *testing.T) {
	d := app.Deployment{ID: "dep_1", AppSlug: "a", Seq: 1, State: "running", URL: "https://a.bitploy.test", AfterDeploy: "running"}
	r := deploymentResult("Deployment", d)
	if !strings.Contains(r.Summary, "after-deploy command still runs") || len(r.Next) != 1 || !strings.Contains(r.Next[0], "wait_seconds=30") {
		t.Errorf("running: %s %v", r.Summary, r.Next)
	}
	d.AfterDeploy = "failed"
	r = deploymentResult("Deployment", d)
	if !strings.Contains(r.Summary, "after-deploy command failed") || !strings.Contains(r.Next[0], "[after-deploy]") {
		t.Errorf("failed: %s %v", r.Summary, r.Next)
	}
}
