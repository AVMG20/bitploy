package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// The fields the dashboard reads (docs/CONTRACT.md): Deployment.services,
// DeployError.service and App.service_layout.
func TestDeploymentServicesView(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	seedMulti(t, e)

	ap, err := e.svc.GetApp(ctx, e.owner, "duo")
	if err != nil {
		t.Fatal(err)
	}
	if ap.ServiceLayout != "multi" || ap.Current == nil {
		t.Fatalf("layout %q, current %+v", ap.ServiceLayout, ap.Current)
	}
	got := map[string]DeploymentService{}
	for _, s := range ap.Current.Services {
		got[s.Service] = s
	}
	web, worker := got["web"], got["worker"]
	if len(got) != 2 || web.Reused || web.State != "running" || web.Image == nil || *web.Image != "bitploy/duo_web:3" || web.Error != nil {
		t.Errorf("web = %+v", web)
	}
	if !worker.Reused || worker.State != "running" {
		t.Errorf("worker = %+v (its container is from deployment 2)", worker)
	}

	// A failed deployment names the service it failed in; the others are cancelled.
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at, services_spec,
		error_code, error_message, error_service) VALUES ('dep_bad', 'app_1', 4, 'failed', 'files', 'api', 2, '{}',
		'app_crashed', 'Service worker: Your worker stopped.', 'worker')`)
	e.exec(t, `INSERT INTO deployment_services (deployment_id, service, image) VALUES ('dep_bad', 'web', 'bitploy/duo_web:4'), ('dep_bad', 'worker', 'busybox:1.36')`)
	d, err := e.svc.GetDeployment(ctx, e.owner, "dep_bad", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Error == nil || d.Error.Service != "worker" || len(d.Services) != 2 {
		t.Fatalf("deployment = %+v", d)
	}
	for _, s := range d.Services {
		switch s.Service {
		case "worker":
			if s.State != "failed" || s.Error == nil || s.Error.Code != "app_crashed" {
				t.Errorf("worker = %+v", s)
			}
		case "web":
			if s.State != "cancelled" || s.Error != nil || s.Reused {
				t.Errorf("web = %+v", s)
			}
		}
	}
	b, _ := json.Marshal(d)
	for _, k := range []string{`"services":[`, `"reused":`, `"service":"worker"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("JSON lacks %s: %s", k, b)
		}
	}

	// A single-service app says so and has no per-service rows.
	e.addApp(t, "app_2", "solo", "usr_owner", "web")
	solo, err := e.svc.GetApp(ctx, e.owner, "solo")
	if err != nil || solo.ServiceLayout != "single" {
		t.Errorf("solo layout %q, %v", solo.ServiceLayout, err)
	}
}

// serviceErr puts the service in details.service, which a failure stores as
// error_service.
func TestServiceErrDetail(t *testing.T) {
	p := &pipeline{}
	p.app.ServiceLayout = layoutMulti
	orig := apperr.Unprocessable("x", "Broke.", "").WithDetail("other", 1)
	out := p.serviceErr(&serviceRun{spec: serviceSpec{Name: "api"}}, orig)
	if out.Message != "Service api: Broke." || errorService(out) != "api" || out.Details["other"] != 1 {
		t.Errorf("out = %+v", out)
	}
	if _, ok := orig.Details["service"]; ok {
		t.Error("the original error must not change")
	}
	if errorService(nil) != "" || errorService(apperr.Unprocessable("x", "y", "")) != "" {
		t.Error("an error without a service has none")
	}
}

// The dashboard's pasted compose file: create with build_mode compose and no
// volume, then deploy_files with only compose.yaml.
func TestPastedComposeCreatePath(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	ap := e.mustApp(t, CreateAppInput{Name: "Shop", Slug: "shop", BuildMode: "compose"})
	if ap.BuildMode != "compose" || ap.ServiceLayout != "single" {
		t.Fatalf("app = %+v", ap)
	}
	file := "services:\n  web:\n    image: ghcr.io/owner/app:1.0\n    ports: [\"3000\"]\n    depends_on: [postgres]\n" +
		"  postgres:\n    image: postgres:16\n    volumes: [pgdata:/var/lib/postgresql/data]\nvolumes:\n  pgdata:\n"
	d, err := e.s.DeployFiles(ctx, e.user, "shop", []FileInput{{Path: "compose.yaml", Content: file}})
	if err != nil {
		t.Fatalf("deploy compose.yaml: %v", err)
	}
	if d.State != "queued" {
		t.Errorf("deployment = %+v", d)
	}
}
