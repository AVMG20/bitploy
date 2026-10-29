package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := w.WriteCatchAll("localhost", "http://bitploy:8080"); err != nil {
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
