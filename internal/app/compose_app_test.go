package app

import (
	"context"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

func TestSingleMappable(t *testing.T) {
	base := func() deploy.ComposeService {
		return deploy.ComposeService{Name: "web", Kind: "build", Context: ".", Dockerfile: "Dockerfile"}
	}
	for name, tc := range map[string]struct {
		edit func(*deploy.ComposeService)
		want bool
	}{
		"plain build":       {func(*deploy.ComposeService) {}, true},
		"data volume":       {func(s *deploy.ComposeService) { s.Mounts = []deploy.ComposeMount{{Volume: "data", Target: "/data/"}} }, true},
		"environment, port": {func(s *deploy.ComposeService) { s.Environment = map[string]string{"A": "1"}; s.Port = 3000 }, true},
		"image":             {func(s *deploy.ComposeService) { s.Kind, s.Image = "image", "nginx:1.27" }, false},
		"build args":        {func(s *deploy.ComposeService) { s.Args = map[string]string{"A": "1"} }, false},
		"target":            {func(s *deploy.ComposeService) { s.Target = "prod" }, false},
		"command":           {func(s *deploy.ComposeService) { s.Command = []string{"node", "x.js"} }, false},
		"entrypoint":        {func(s *deploy.ComposeService) { s.Entrypoint = []string{"sh"} }, false},
		"user":              {func(s *deploy.ComposeService) { s.User = "node" }, false},
		"working dir":       {func(s *deploy.ComposeService) { s.WorkingDir = "/app" }, false},
		"healthcheck":       {func(s *deploy.ComposeService) { s.Healthcheck = &deploy.ComposeHealthcheck{Test: []string{"NONE"}} }, false},
		"other mount":       {func(s *deploy.ComposeService) { s.Mounts = []deploy.ComposeMount{{Volume: "up", Target: "/app/uploads"}} }, false},
		"two mounts": {func(s *deploy.ComposeService) {
			s.Mounts = []deploy.ComposeMount{{Volume: "a", Target: "/data"}, {Volume: "b", Target: "/data2"}}
		}, false},
	} {
		s := base()
		tc.edit(&s)
		if got := singleMappable(s); got != tc.want {
			t.Errorf("%s: singleMappable = %v, want %v", name, got, tc.want)
		}
	}
}

const twoServices = "services:\n  web:\n    build: .\n    ports: [\"3000:3000\"]\n  cache:\n    image: redis:7-alpine\n"

func TestChooseCompose(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	read := func(files map[string]string) func(string) []byte {
		return func(p string) []byte {
			if c, ok := files[p]; ok {
				return []byte(c)
			}
			return nil
		}
	}
	list := func(files map[string]string) []string {
		var out []string
		for p := range files {
			out = append(out, p)
		}
		return out
	}
	appWith := func(id, slug, mode string, deployed bool) db.App {
		e.addApp(t, id, slug, "usr_owner", "web")
		e.exec(t, `UPDATE apps SET build_mode = ? WHERE id = ?`, mode, id)
		if !deployed {
			e.exec(t, `UPDATE apps SET current_deployment_id = NULL WHERE id = ?`, id)
		}
		row, err := e.svc.Q.GetApp(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	two := map[string]string{"compose.yaml": twoServices, "Dockerfile": "FROM node:24-alpine\n"}
	one := map[string]string{"docker-compose.yml": "services:\n  web:\n    build: .\n    ports: [\"8080:8080\"]\n", "Dockerfile": "FROM node:24-alpine\n"}

	// Build mode dockerfile (what create_app makes): never compose, even on the first deploy.
	ch, ae := e.svc.chooseCompose(ctx, appWith("app_d", "dock", "dockerfile", false), list(two), read(two))
	if ae != nil || ch.multi || ch.single != nil {
		t.Errorf("dockerfile: %+v, %v", ch, ae)
	}
	// Auto, first deploy, two services: compose.
	ch, ae = e.svc.chooseCompose(ctx, appWith("app_a", "auto", "auto", false), list(two), read(two))
	if ae != nil || !ch.multi || ch.path != "compose.yaml" || len(ch.proj.Services) != 2 {
		t.Errorf("auto first: %+v, %v", ch, ae)
	}
	// Auto, already deployed: never switches by itself.
	ch, ae = e.svc.chooseCompose(ctx, appWith("app_l", "live", "auto", true), list(two), read(two))
	if ae != nil || ch.multi || ch.single != nil {
		t.Errorf("auto live: %+v, %v", ch, ae)
	}
	// Auto, first deploy, one plain build service: built like a Dockerfile app.
	ch, ae = e.svc.chooseCompose(ctx, appWith("app_o", "one", "auto", false), list(one), read(one))
	if ae != nil || ch.multi || ch.single == nil || ch.single.Name != "web" || ch.single.Port != 8080 {
		t.Errorf("auto one: %+v, %v", ch, ae)
	}
	// Auto, first deploy, one broken service: the compose file is left out.
	bad := map[string]string{"compose.yaml": "services:\n  web:\n    build: .\n    privileged: true\n", "Dockerfile": "FROM node:24-alpine\n"}
	ch, ae = e.svc.chooseCompose(ctx, appWith("app_b", "broken", "auto", false), list(bad), read(bad))
	if ae != nil || ch.multi || ch.single != nil {
		t.Errorf("auto broken one: %+v, %v", ch, ae)
	}
	// Auto, first deploy, two services with an error: the error, with a way out.
	badTwo := map[string]string{"compose.yaml": twoServices + "    privileged: true\n"}
	_, ae = e.svc.chooseCompose(ctx, appWith("app_bt", "broken-two", "auto", false), list(badTwo), read(badTwo))
	if ae == nil || ae.Code != deploy.CodeComposeForbidden || ae.Hint == "" {
		t.Fatalf("auto broken two: %v", ae)
	}
	// Build mode compose without a compose file: compose_file_missing.
	noFile := map[string]string{"Dockerfile": "FROM node:24-alpine\n"}
	_, ae = e.svc.chooseCompose(ctx, appWith("app_c", "comp", "compose", true), list(noFile), read(noFile))
	if ae == nil || ae.Code != "compose_file_missing" {
		t.Errorf("compose, no file: %v", ae)
	}
	// Build mode compose with one service: still the whole compose file.
	ch, ae = e.svc.chooseCompose(ctx, appWith("app_c1", "comp-one", "compose", true), list(one), read(one))
	if ae != nil || !ch.multi {
		t.Errorf("compose one: %+v, %v", ch, ae)
	}
}

func TestValidateComposeQuotaLeavesTheAppOut(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	seedMulti(t, e)
	e.exec(t, `UPDATE users SET quota_memory_mb = 1000 WHERE id = 'usr_owner'`)
	file := "services:\n  web:\n    build: .\n    ports: [\"3000:3000\"]\n    deploy: {resources: {limits: {memory: 700M}}}\n  cache:\n    image: redis:7-alpine\n    deploy: {resources: {limits: {memory: 128M}}}\n"

	// Without app: duo's 384 MB counts, so 828 MB does not fit in 1000.
	p, err := e.svc.ValidateCompose(ctx, e.owner, ValidateComposeInput{Compose: file})
	if err != nil {
		t.Fatal(err)
	}
	if p.OK || p.Quota.Fits || len(p.Errors) == 0 || p.Errors[len(p.Errors)-1].Code != "quota_exceeded" || p.MemoryMBTotal != 828 {
		t.Errorf("without app: %+v", p)
	}
	// With app=duo: its own use is replaced, so it fits.
	p, err = e.svc.ValidateCompose(ctx, e.owner, ValidateComposeInput{Compose: file, App: "duo"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.OK || !p.Quota.Fits || p.Quota.Message == "" {
		t.Errorf("with app: %+v", p)
	}
	// Another user's app is not found.
	if _, err := e.svc.ValidateCompose(ctx, e.other, ValidateComposeInput{Compose: file, App: "duo"}); err == nil {
		t.Error("another user's app must not be found")
	}
}

func TestSetServiceEnv(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	seedMulti(t, e)

	if _, err := e.svc.SetServiceEnv(ctx, e.owner, "duo", "", map[string]string{"SHARED": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	vars, err := e.svc.SetServiceEnv(ctx, e.owner, "duo", "worker", map[string]string{"SHARED": "2", "ONLY": "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, v := range vars {
		got[v.Service+"/"+v.Name] = true
	}
	if len(vars) != 3 || !got["/SHARED"] || !got["worker/SHARED"] || !got["worker/ONLY"] {
		t.Errorf("vars = %+v", vars)
	}
	// Unset only touches that service.
	vars, err = e.svc.SetServiceEnv(ctx, e.owner, "duo", "worker", nil, []string{"SHARED"})
	if err != nil || len(vars) != 2 {
		t.Errorf("after unset: %+v, %v", vars, err)
	}
	_, err = e.svc.SetServiceEnv(ctx, e.owner, "duo", "db", map[string]string{"A": "1"}, nil)
	wantCode(t, err, "service_not_found")

	// An app with one service takes its own service name and stores shared variables.
	e.addApp(t, "app_2", "solo", "usr_owner", "web")
	vars, err = e.svc.SetServiceEnv(ctx, e.owner, "solo", "app", map[string]string{"A": "1"}, nil)
	if err != nil || len(vars) != 1 || vars[0].Service != "" {
		t.Errorf("solo: %+v, %v", vars, err)
	}
}

func TestListBackupsNamesEveryVolume(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	seedMulti(t, e)
	l, err := e.svc.ListBackups(ctx, e.owner, "duo")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Volumes) != 2 {
		t.Fatalf("volumes = %+v", l.Volumes)
	}
	up, pg := l.Volumes[0], l.Volumes[1]
	if up.Name != "uploads" || up.Service != "web" || up.MountPath != "/data" || up.SizeMB != 100 || up.UploadMaxBytes <= 0 {
		t.Errorf("uploads = %+v", up)
	}
	if pg.Name != "pgdata" || pg.Service != "worker" || pg.SizeMB != 300 || pg.UploadMaxBytes <= up.UploadMaxBytes {
		t.Errorf("pgdata = %+v", pg)
	}
	if l.UploadMaxBytes != up.UploadMaxBytes {
		t.Errorf("upload_max_bytes = %d, want the first volume's %d", l.UploadMaxBytes, up.UploadMaxBytes)
	}
}
