package proxy

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// routers parses a rendered route file into router name -> middlewares, plus the middleware definitions.
func routers(t *testing.T, b []byte) (map[string][]string, map[string]any) {
	t.Helper()
	var f struct {
		HTTP struct {
			Routers     map[string]struct{ Middlewares []string } `yaml:"routers"`
			Middlewares map[string]any                            `yaml:"middlewares"`
		} `yaml:"http"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("route file is not valid YAML: %v\n%s", err, b)
	}
	out := map[string][]string{}
	for n, r := range f.HTTP.Routers {
		out[n] = r.Middlewares
	}
	return out, f.HTTP.Middlewares
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestRenderBasicAuthOnPublicRoutersOnly(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	hash, err := HashPassword("blue-river-42")
	if err != nil {
		t.Fatal(err)
	}
	r := Route{Slug: "my-app", Host: "my-app.bitploy.nl", Custom: []string{"www.example.com", "example.com"},
		Upstream: "http://bp-my-app-1:8080", Candidate: "http://bp-my-app-2:8080", Auth: &BasicAuth{User: "team", Hash: hash}}
	b, err := w.Render(r)
	if err != nil {
		t.Fatal(err)
	}
	rs, mws := routers(t, b)
	auth := AuthMiddlewareName("my-app")
	// Every public router has it.
	for _, name := range []string{"app-my-app", HostRouterName("my-app", "www.example.com"), HostRouterName("my-app", "example.com")} {
		if !has(rs[name], auth) {
			t.Errorf("public router %s lacks %s: %v\n%s", name, auth, rs[name], b)
		}
	}
	// The internal candidate and live routers never do: health checks send no login.
	for _, name := range []string{"app-my-app_candidate", "app-my-app_live"} {
		m, ok := rs[name]
		if !ok {
			t.Fatalf("router %s missing", name)
		}
		if has(m, auth) {
			t.Errorf("internal router %s must not have basic auth", name)
		}
	}
	if len(rs) != 5 {
		t.Errorf("want 3 public + candidate + live routers, got %v", rs)
	}
	out := string(b)
	for _, host := range []string{CandidateHost("my-app"), LiveHost("my-app")} {
		if !strings.Contains(out, host) {
			t.Errorf("route file lacks internal host %s", host)
		}
	}
	m, _ := mws[auth].(map[string]any)
	ba, _ := m["basicAuth"].(map[string]any)
	if ba == nil {
		t.Fatalf("no basicAuth middleware: %v", mws)
	}
	users, _ := ba["users"].([]any)
	if len(users) != 1 || users[0] != "team:"+hash {
		t.Errorf("users = %v", users)
	}
	if ba["removeHeader"] != true {
		t.Errorf("the login header must not reach the app: %v", ba)
	}
	if strings.Contains(out, "blue-river-42") {
		t.Error("plain password in the route file")
	}
	// Traefik reads route files as Go templates and splits "user:hash" on ":".
	if strings.ContainsAny(hash, "{}:") {
		t.Errorf("hash has template or separator characters: %s", hash)
	}
	// Candidate and live headers still work next to it.
	for _, want := range []string{"X-Bitploy-Candidate: my-app", UpstreamHeader + ": http://bp-my-app-1:8080"} {
		if !strings.Contains(out, want) {
			t.Errorf("route file lacks %q", want)
		}
	}
}

func TestRenderWithoutAuthHasNoBasicAuth(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	b, err := w.Render(Route{Slug: "my-app", Host: "my-app.bitploy.nl", Custom: []string{"www.example.com"}, Upstream: "http://bp-my-app-1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "basicAuth") {
		t.Fatalf("unprotected route has basicAuth:\n%s", b)
	}
	rs, _ := routers(t, b)
	if has(rs["app-my-app"], AuthMiddlewareName("my-app")) {
		t.Fatal("unprotected router references the auth middleware")
	}
	// Protected but nothing to serve yet (candidate only): no public router, so nothing to protect.
	b, err = w.Render(Route{Slug: "my-app", Host: "my-app.bitploy.nl", Candidate: "http://bp-my-app-1:8080", Auth: &BasicAuth{User: "u", Hash: NeverMatchHash}})
	if err != nil {
		t.Fatal(err)
	}
	if rs, _ := routers(t, b); len(rs) != 1 {
		t.Fatalf("candidate-only file has public routers: %v", rs)
	}
}

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("blue-river-42")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2a$08$") || len(h) != 60 {
		t.Fatalf("not a cost-%d bcrypt hash Traefik reads: %s", BcryptCost, h)
	}
	if !CheckPassword(h, "blue-river-42") || CheckPassword(h, "blue-river-43") || CheckPassword(h, "") {
		t.Fatal("hash check wrong")
	}
	h2, _ := HashPassword("blue-river-42")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if len(NeverMatchHash) != 60 || CheckPassword(NeverMatchHash, "") || CheckPassword(NeverMatchHash, "locked") {
		t.Fatalf("NeverMatchHash: %d chars", len(NeverMatchHash))
	}
	// 72 bytes is bcrypt's limit; longer input is rejected by the library, so the service must refuse it first.
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes+1)); err == nil {
		t.Fatal("bcrypt accepted more than 72 bytes: MaxPasswordBytes is wrong")
	}
	if _, err := HashPassword(strings.Repeat("a", MaxPasswordBytes)); err != nil {
		t.Fatal(err)
	}
}
