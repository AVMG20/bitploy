package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

// A compose file with problems answers 200 with ok=false and the errors;
// only a body that is not valid JSON is a 4xx.
func TestValidateComposeEndpoint(t *testing.T) {
	e := newEnv(t)
	e.login("owner@example.nl", false)

	w := e.asSession("POST", "/api/v1/compose/validate", map[string]any{
		"compose": "services:\n  web:\n    build: .\n    ports: [\"3000:3000\"]\n  db:\n    image: postgres:16-alpine\n    volumes: [pgdata:/var/lib/postgresql/data]\nvolumes:\n  pgdata: {}\n",
	})
	if w.Code != 200 {
		t.Fatalf("valid file: %d %s", w.Code, w.Body)
	}
	p := decode[app.ComposePreview](t, w)
	if !p.OK || len(p.Services) != 2 || len(p.Volumes) != 1 || p.Volumes[0].Service != "db" || p.Quota.Message == "" {
		t.Errorf("preview = %+v", p)
	}
	var public string
	for _, s := range p.Services {
		if s.Public {
			public = s.Name
		}
	}
	if public != "web" || p.MemoryMBTotal <= 0 {
		t.Errorf("public = %q, memory = %d", public, p.MemoryMBTotal)
	}

	w = e.asSession("POST", "/api/v1/compose/validate", map[string]any{
		"compose": "services:\n  web:\n    image: nginx:1.27\n    volumes: [/etc:/host]\n",
	})
	if w.Code != 200 {
		t.Fatalf("bad file must still be 200: %d %s", w.Code, w.Body)
	}
	p = decode[app.ComposePreview](t, w)
	if p.OK || len(p.Errors) == 0 {
		t.Fatalf("preview = %+v", p)
	}
	if f := p.Errors[0]; f.Code != "compose_forbidden" || f.Service != "web" || !strings.HasPrefix(f.Key, "volumes") || f.Hint == "" {
		t.Errorf("error = %+v", f)
	}

	if w := e.asSession("POST", "/api/v1/compose/validate", map[string]any{"compose": "services: [\n"}); w.Code != 200 || decode[app.ComposePreview](t, w).OK {
		t.Errorf("bad YAML: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/compose/validate", map[string]any{"compose": "services: {}", "app": "nope"}); w.Code != 404 {
		t.Errorf("unknown app: %d %s", w.Code, w.Body)
	}
}
