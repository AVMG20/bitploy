package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Traefik has one namespace per kind for all route files, and the access log
// maps router names to apps, so no two apps may ever produce the same name.
func TestNamesNeverCollideAcrossApps(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	route := func(slug string, custom ...string) Route {
		return Route{Slug: slug, Host: slug + ".bitploy.nl", Custom: custom, Upstream: "http://bp-" + slug + "-1:80",
			Candidate: "http://bp-" + slug + "-2:80", Auth: &BasicAuth{User: "u", Hash: "h"}}
	}
	for _, pair := range [][2]Route{
		{route("my", "blog.app"), route("my-blog-app")},
		{route("foo"), route("foo-candidate")},
		{route("foo"), route("foo-live")},
		{route("foo"), route("foo-auth")},
		{route("foo", "live.example.com"), route("foo-live-example-com")},
	} {
		seen := map[string]string{}
		for _, r := range pair {
			b, err := w.Render(r)
			if err != nil {
				t.Fatal(err)
			}
			var f file
			if err := yaml.Unmarshal(b, &f); err != nil {
				t.Fatal(err)
			}
			var names []string
			for n := range f.HTTP.Routers {
				names = append(names, "router "+n)
			}
			for n := range f.HTTP.Services {
				names = append(names, "service "+n)
			}
			for n := range f.HTTP.Middlewares {
				names = append(names, "middleware "+n)
			}
			for _, n := range names {
				if other, ok := seen[n]; ok && other != r.Slug {
					t.Errorf("%s is used by app %s and app %s", n, other, r.Slug)
				}
				seen[n] = r.Slug
			}
		}
	}
}

func TestRenderCandidateAndLive(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	r := Route{Slug: "my-app", Host: "my-app.bitploy.nl", Custom: []string{"www.example.com"},
		Upstream: "http://bp-my-app-1:8080", Candidate: "http://bp-my-app-2:8080"}
	b, err := w.Render(r)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{
		"Host(`my-app.bitploy.nl`)", "http://bp-my-app-1:8080", "http://bp-my-app-2:8080",
		"Host(`" + CandidateHost("my-app") + "`)", "entryPoints:", "- internal",
		"X-Bitploy-Candidate: my-app", "certResolver: letsencrypt", "Host(`www.example.com`)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("route file lacks %q:\n%s", want, out)
		}
	}
}

func TestWriteRemoveAndList(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "web"}
	if err := w.Write(Route{Slug: "a-app", Host: "a-app.localhost", Upstream: "http://bp-a-app-1:80"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCatchAll([]string{"localhost"}, nil, "http://bitploy:8080"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.List(); len(got) != 1 || got[0] != "a-app" {
		t.Fatalf("catch-all must not be listed: %v", got)
	}
	// Neither upstream nor candidate: the file goes away and the catch-all answers.
	if err := w.Write(Route{Slug: "a-app", Host: "a-app.localhost"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "a-app.yml")); !os.IsNotExist(err) {
		t.Fatalf("route file should be removed: %v", err)
	}
	if b, _ := w.Read("a-app"); b != nil {
		t.Fatal("Read of a missing file must be nil")
	}
	// Candidate only (first deploy): a file exists, but only on the internal entrypoint.
	if err := w.Write(Route{Slug: "a-app", Host: "a-app.localhost", Candidate: "http://bp-a-app-1:80"}); err != nil {
		t.Fatal(err)
	}
	b, _ := w.Read("a-app")
	if strings.Contains(string(b), "Host(`a-app.localhost`)") {
		t.Fatalf("public router must not exist before the swap:\n%s", b)
	}
}

func TestRemoveRewritesCatchAll(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "web"}
	w.Write(Route{Slug: "a-app", Host: "a-app.localhost", Upstream: "http://bp-a-app-1:80"})
	w.WriteCatchAll([]string{"localhost"}, nil, "http://bitploy:8080")
	catch := filepath.Join(w.Dir, "_catchall.yml")
	before, _ := os.ReadFile(catch)
	i1, _ := os.Stat(catch)
	if err := w.Remove("a-app"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(catch)
	i2, _ := os.Stat(catch)
	if string(before) != string(after) {
		t.Fatal("catch-all content must not change")
	}
	if os.SameFile(i1, i2) {
		t.Fatal("catch-all must be written again (new file) so Traefik reloads")
	}
	// Removing a route that does not exist is not an error and writes nothing.
	if err := w.Remove("nope"); err != nil {
		t.Fatal(err)
	}
}
