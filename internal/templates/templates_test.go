package templates

import (
	"strings"
	"testing"
)

func TestAllStacksEmbedded(t *testing.T) {
	for _, s := range Stacks {
		tpl, ok := Get(s)
		if !ok {
			t.Fatalf("%s: not found", s)
		}
		if tpl.Stack != s || tpl.Dockerfile == "" || tpl.Dockerignore == "" || tpl.Readme == "" {
			t.Errorf("%s: incomplete template %+v", s, tpl)
		}
		if !strings.Contains(tpl.Dockerfile, "FROM ") || !strings.Contains(tpl.Dockerfile, "EXPOSE ") {
			t.Errorf("%s: Dockerfile lacks FROM or EXPOSE", s)
		}
		if strings.Contains(tpl.Dockerfile, ":latest") {
			t.Errorf("%s: uses :latest", s)
		}
		if !strings.Contains(tpl.Dockerignore, ".env") {
			t.Errorf("%s: .dockerignore must exclude .env", s)
		}
	}
}

func TestStaticIsTheSpecExample(t *testing.T) {
	tpl, _ := Get("static")
	want := "FROM nginxinc/nginx-unprivileged:alpine\nCOPY . /usr/share/nginx/html\nEXPOSE 8080\n"
	if tpl.Dockerfile != want {
		t.Errorf("static Dockerfile = %q", tpl.Dockerfile)
	}
}

func TestUnknown(t *testing.T) {
	if _, ok := Get("ruby"); ok {
		t.Error("ruby should be unknown")
	}
	if _, ok := Get("  Node-SPA "); !ok {
		t.Error("lookup should ignore case and spaces")
	}
}
