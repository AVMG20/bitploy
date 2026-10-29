package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/config"
	"github.com/arno/bitploy/internal/store"
)

func catalogService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{PublicURL: "https://bitploy.test", AppsDomain: "apps.test", AppsScheme: "https", SecretKey: make([]byte, 32), DataDir: dir}
	st, err := store.Open(filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.DB.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	cr, _ := store.NewCrypto(cfg.SecretKey)
	return New(cfg, st, cr, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCatalogTemplates(t *testing.T) {
	s := catalogService(t)
	ctx := context.Background()
	list, err := s.ListTemplates(ctx, Actor{})
	if err != nil || len(list) != 7 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	tpl, err := s.GetTemplate(ctx, Actor{}, "node")
	if err != nil || !strings.HasPrefix(tpl.Dockerfile, "FROM node:") {
		t.Fatalf("node = %+v, %v", tpl, err)
	}
	_, err = s.GetTemplate(ctx, Actor{}, "cobol")
	ae := apperr.As(err)
	if ae.Code != "template_unknown" || ae.HTTPStatus != 404 || !strings.Contains(ae.Hint, "static, node, node-spa, python, go, php, generic") {
		t.Errorf("err = %+v", ae)
	}
}

func TestCatalogDeployRules(t *testing.T) {
	s := catalogService(t)
	md, err := s.DeployRules(context.Background(), Actor{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://<slug>.apps.test", "Runtime contract", "0.0.0.0:$PORT", "Every app is built from a Dockerfile"} {
		if !strings.Contains(md, want) {
			t.Errorf("rules lack %q", want)
		}
	}
	if strings.Contains(md, "bitploy.nl") {
		t.Error("example domain must be replaced")
	}
}

func TestCatalogValidateDockerfile(t *testing.T) {
	s := catalogService(t)
	ctx := context.Background()
	res, err := s.ValidateDockerfile(ctx, Actor{}, "FROM node:24-alpine\nUSER node\nEXPOSE 3000\n", []string{"package.json"}, 0)
	if err != nil || !res.OK || res.Port != 3000 || res.Stack != "node" || len(res.NextSteps) == 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	res, _ = s.ValidateDockerfile(ctx, Actor{}, "", []string{"index.html"}, 0)
	if res.OK || res.Findings[0].Code != "dockerfile_missing" || res.Stack != "static" {
		t.Fatalf("res = %+v", res)
	}
	res, _ = s.ValidateDockerfile(ctx, Actor{}, "FROM alpine:3.22\n", nil, 8080)
	if !res.OK || res.Port != 8080 {
		t.Fatalf("app port should satisfy the port check: %+v", res)
	}
	if _, err := s.ValidateDockerfile(ctx, Actor{}, strings.Repeat("#", 300<<10), nil, 0); apperr.As(err).Code != "dockerfile_too_large" {
		t.Errorf("huge Dockerfile: %v", err)
	}
}

func TestCatalogSetGitSourceValidation(t *testing.T) {
	s := catalogService(t)
	ctx := context.Background()
	if _, err := s.SetGitSource(ctx, Actor{UserID: "usr_x", Scope: ScopeRead}, "nope", GitSource{RepoURL: "https://github.com/o/r"}); apperr.As(err).Code != "app_not_found" {
		t.Errorf("unknown app: %v", err)
	}
}
