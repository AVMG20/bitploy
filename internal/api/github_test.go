package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/github"
)

const whSecret = "whsec_api_test"

// seedGitHubApp stores a GitHub App the way the admin flow does.
func (e *env) seedGitHubApp() {
	e.t.Helper()
	seal := func(v, field string) []byte { return e.svc.Crypto.Seal([]byte(v), "github_app:"+field) }
	_, err := e.svc.Store.DB.Exec(`INSERT INTO github_app (id, github_app_id, slug, name, html_url, owner_login, owner_type, client_id,
		client_secret_enc, webhook_secret_enc, private_key_enc, created_at) VALUES (1, 4242, 'bitploy-test', 'bitploy test',
		'https://github.com/apps/bitploy-test', 'acme', 'Organization', 'Iv1.client', ?, ?, ?, 1)`,
		seal("client-secret", "client_secret"), seal(whSecret, "webhook_secret"), seal("pem", "private_key"))
	if err != nil {
		e.t.Fatal(err)
	}
}

func signed(body string) map[string]string {
	return map[string]string{
		"X-Hub-Signature-256": github.Sign([]byte(whSecret), []byte(body)),
		"X-GitHub-Event":      "ping",
		"X-GitHub-Delivery":   "delivery-1",
		"Content-Type":        "application/json",
	}
}

func TestWebhookEndpoint(t *testing.T) {
	e := newEnv(t)
	const path = "/api/v1/github/webhook"
	body := `{"zen":"keep it logically awesome"}`

	// Not set up: nothing to verify against.
	if w := e.req("POST", path, body, signed(body)); w.Code != 404 || decode[errResp](t, w).Error.Code != "github_app_missing" {
		t.Fatalf("no app: %d %s", w.Code, w.Body)
	}
	e.seedGitHubApp()

	// No session, no token, no CSRF: the signature is the credential.
	hdr := signed(body)
	hdr["Origin"] = "https://evil.example" // GitHub sends none; the CSRF check does not apply here
	if w := e.req("POST", path, body, hdr); w.Code != 202 || !strings.Contains(w.Body.String(), "pong") {
		t.Fatalf("signed ping: %d %s", w.Code, w.Body)
	}
	// The same delivery again is acknowledged and not processed twice.
	if w := e.req("POST", path, body, signed(body)); w.Code != 202 || !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	bad := signed(body)
	bad["X-GitHub-Delivery"] = "delivery-2"
	bad["X-Hub-Signature-256"] = github.Sign([]byte("not the secret"), []byte(body))
	if w := e.req("POST", path, body, bad); w.Code != 401 || decode[errResp](t, w).Error.Code != "github_signature_invalid" {
		t.Fatalf("wrong signature: %d %s", w.Code, w.Body)
	}
	delete(bad, "X-Hub-Signature-256")
	if w := e.req("POST", path, body, bad); w.Code != 401 {
		t.Fatalf("no signature: %d %s", w.Code, w.Body)
	}
	// A signature for another body.
	other := signed(`{"zen":"other"}`)
	other["X-GitHub-Delivery"] = "delivery-3"
	if w := e.req("POST", path, body, other); w.Code != 401 {
		t.Fatalf("signature of another body: %d %s", w.Code, w.Body)
	}
	// A body over 5 MB is refused before it is parsed, whatever its signature says.
	big := strings.Repeat("x", github.MaxWebhookBody+1)
	hugeHdr := signed(big)
	hugeHdr["X-GitHub-Delivery"] = "delivery-4"
	if w := e.req("POST", path, big, hugeHdr); w.Code != 413 {
		t.Fatalf("big body: %d %.200s", w.Code, w.Body)
	}
	// Only POST.
	if w := e.req("GET", path, nil, nil); w.Code == 200 || w.Code == 202 {
		t.Fatalf("GET: %d", w.Code)
	}
	// An error answer never contains the secret.
	if w := e.req("POST", path, body, bad); strings.Contains(w.Body.String(), whSecret) {
		t.Fatal("secret in the answer")
	}
}

func TestGitHubEndpointsNeedASession(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/github", nil}, {"POST", "/api/v1/github/connect", nil}, {"POST", "/api/v1/github/link", map[string]any{"code": "c", "state": "s"}},
		{"DELETE", "/api/v1/github/installations/1", nil}, {"GET", "/api/v1/github/repositories", nil}, {"GET", "/api/v1/github/branches?repo=a/b", nil},
		{"GET", "/api/v1/admin/github", nil}, {"POST", "/api/v1/admin/github/manifest", map[string]any{}}, {"DELETE", "/api/v1/admin/github", nil},
	} {
		if w := e.req(c.method, c.path, c.body, nil); w.Code != 401 {
			t.Errorf("%s %s without login: %d", c.method, c.path, w.Code)
		}
	}
	e.login("me@example.nl", false)
	w := e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "t", "scope": "deploy"})
	tok := decode[app.TokenCreated](t, w)
	bearer := map[string]string{"Authorization": "Bearer " + tok.Value}
	for _, p := range []string{"/api/v1/github", "/api/v1/github/repositories", "/api/v1/admin/github"} {
		if w := e.req("GET", p, nil, bearer); w.Code != 403 {
			t.Errorf("token on %s: %d %s", p, w.Code, w.Body)
		}
	}
	// A user sees the state, but not the admin card.
	w = e.asSession("GET", "/api/v1/github", nil)
	ov := decode[app.GitHubOverview](t, w)
	if w.Code != 200 || ov.App.Configured || ov.Installations == nil || len(ov.Installations) != 0 {
		t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/admin/github", nil); w.Code != 403 || decode[errResp](t, w).Error.Code != "admin_only" {
		t.Fatalf("admin card for a user: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/github/connect", map[string]any{}); w.Code != 409 || decode[errResp](t, w).Error.Code != "github_app_missing" {
		t.Fatalf("connect without an app: %d %s", w.Code, w.Body)
	}
	e.seedGitHubApp()
	w = e.asSession("POST", "/api/v1/github/connect", map[string]any{})
	red := decode[app.GitHubRedirect](t, w)
	if w.Code != 200 || !strings.HasPrefix(red.URL, "https://github.com/apps/bitploy-test/installations/new?state=") {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", "/api/v1/github/connect", map[string]any{"mode": "authorize"})
	if red := decode[app.GitHubRedirect](t, w); !strings.HasPrefix(red.URL, "https://github.com/login/oauth/authorize?") || strings.Contains(red.URL, "client-secret") {
		t.Fatalf("authorize: %d %s", w.Code, w.Body)
	}
	// A made-up state and code link nothing.
	if w := e.asSession("POST", "/api/v1/github/link", map[string]any{"installation_id": 1, "code": "abc", "state": "nope"}); w.Code != 400 || decode[errResp](t, w).Error.Code != "github_state_invalid" {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
}

func TestGitHubAdminEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("admin@example.nl", true)
	w := e.asSession("GET", "/api/v1/admin/github", nil)
	info := decode[app.GitHubAppInfo](t, w)
	// bitploy.test cannot be reached from GitHub: no webhook address to show.
	if w.Code != 200 || info.Configured || info.WebhookURL != "" || info.PublicURLProblem == "" {
		t.Fatalf("admin card: %d %s", w.Code, w.Body)
	}
	w = e.asSession("POST", "/api/v1/admin/github/manifest", map[string]any{"name": "My bitploy", "organization": "acme"})
	m := decode[app.GitHubManifest](t, w)
	if w.Code != 200 || !strings.HasPrefix(m.PostURL, "https://github.com/organizations/acme/settings/apps/new?state=") ||
		!strings.Contains(m.Manifest, `"request_oauth_on_install":true`) || !strings.Contains(m.Manifest, `"name":"My bitploy"`) ||
		strings.Contains(m.Manifest, "hook_attributes") {
		t.Fatalf("manifest: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/admin/github/manifest/complete", map[string]any{"code": "abc", "state": "forged"}); w.Code != 400 || decode[errResp](t, w).Error.Code != "github_state_invalid" {
		t.Fatalf("complete with a forged state: %d %s", w.Code, w.Body)
	}
	e.seedGitHubApp()
	w = e.asSession("GET", "/api/v1/admin/github", nil)
	info = decode[app.GitHubAppInfo](t, w)
	if !info.Configured || info.Slug != "bitploy-test" || strings.Contains(w.Body.String(), "client-secret") || strings.Contains(w.Body.String(), whSecret) {
		t.Fatalf("configured: %s", w.Body)
	}
	if w := e.asSession("DELETE", "/api/v1/admin/github", nil); w.Code != 204 {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/admin/github", nil); decode[app.GitHubAppInfo](t, w).Configured {
		t.Fatal("still configured")
	}
}

func TestGitSourceEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)
	w := e.asSession("POST", "/api/v1/apps", map[string]any{"name": "Site", "slug": "site-one"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("PATCH", "/api/v1/apps/site-one/git-source", map[string]any{"auto_deploy": true}); w.Code != 409 || decode[errResp](t, w).Error.Code != "git_source_missing" {
		t.Fatalf("auto-deploy without a repository: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("DELETE", "/api/v1/apps/site-one/git-source", nil); w.Code != 200 {
		t.Fatalf("unlinking an app without a repository: %d %s", w.Code, w.Body)
	}
	if _, err := e.svc.Store.DB.Exec(`UPDATE apps SET git_repo_url = 'https://github.com/me/site', git_branch = 'main' WHERE slug = 'site-one'`); err != nil {
		t.Fatal(err)
	}
	w = e.asSession("GET", "/api/v1/apps/site-one", nil)
	got := decode[app.App](t, w)
	if got.GitSource == nil || got.GitSource.Repo != "me/site" || !got.GitSource.AutoDeploy || got.GitSource.PushStatus != "app_missing" || got.GitSource.PushReady {
		t.Fatalf("git source: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"push_message"`) || !strings.Contains(w.Body.String(), `"last_push_at":null`) {
		t.Fatalf("the status fields are part of the answer: %s", w.Body)
	}
	for _, p := range []string{"/files", "/uploads"} {
		body := any(map[string]any{"files": []map[string]string{{"path": "Dockerfile", "content": "FROM x"}}})
		if p == "/uploads" {
			body = nil
		}
		w := e.asSession("POST", "/api/v1/apps/site-one"+p, body)
		if w.Code != 409 || decode[errResp](t, w).Error.Code != "git_source_linked" {
			t.Fatalf("%s on a linked app: %d %s", p, w.Code, w.Body)
		}
	}
	w = e.asSession("PATCH", "/api/v1/apps/site-one/git-source", map[string]any{"auto_deploy": false})
	if got := decode[app.App](t, w); w.Code != 200 || got.GitSource.AutoDeploy || got.GitSource.PushStatus != "off" {
		t.Fatalf("auto-deploy off: %d %s", w.Code, w.Body)
	}
	w = e.asSession("DELETE", "/api/v1/apps/site-one/git-source", nil)
	if got := decode[app.App](t, w); w.Code != 200 || got.GitSource != nil {
		t.Fatalf("unlink: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("POST", "/api/v1/apps/site-one/uploads", nil); w.Code != 201 {
		t.Fatalf("upload after unlinking: %d %s", w.Code, w.Body)
	}
}

func TestCSPAllowsPostingTheManifestToGitHubOnly(t *testing.T) {
	e := newEnv(t)
	csp := e.req("GET", "/", nil, nil).Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'self' https://github.com;") {
		t.Fatalf("csp: %s", csp)
	}
	if strings.Contains(csp, "script-src 'self' https") || strings.Contains(csp, "connect-src 'self' https") {
		t.Fatalf("only form-action may mention GitHub: %s", csp)
	}
}
