package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// ---- a fake GitHub ----

type fakeInst struct {
	ID        int64
	Login     string
	Type      string
	Selection string
	Suspended bool
}

type tokenReq struct {
	Inst  int64
	Body  map[string]any
	Token string
}

// fakeGitHub answers the few GitHub endpoints bitploy uses, and checks what a
// real GitHub would check: the app JWT signature, client id and secret.
type fakeGitHub struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey
	pem string
	now func() time.Time

	mu            sync.Mutex
	webhookSecret string
	codes         map[string]string             // oauth code -> user token (each works once)
	visible       map[string][]fakeInst         // user token -> installations that user can see
	repos         map[int64][]string            // installation id -> repositories, "owner/name"
	tokens        map[string]int64              // issued installation token -> installation id
	reqs          []tokenReq                    // access token requests
	branches      map[string][]string           // "owner/name" -> branches
	defaultBranch map[string]string             // "owner/name" -> default branch
	userRepos     map[string]map[int64][]string // user token -> installation -> repositories that user can open (default: all)
	manifestCodes map[string]bool               // manifest codes that convert
	hits          map[string]int                // method + path -> count
	tokenStatus   int                           // when set, access token requests answer with it (a rate limit)
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{
		t: t, key: key, now: time.Now, webhookSecret: "whsec_fake_secret",
		pem:   string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		codes: map[string]string{}, visible: map[string][]fakeInst{}, repos: map[int64][]string{}, tokens: map[string]int64{},
		branches: map[string][]string{}, defaultBranch: map[string]string{}, userRepos: map[string]map[int64][]string{}, manifestCodes: map[string]bool{"goodcode": true}, hits: map[string]int{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (f *fakeGitHub) verifyJWT(tok string) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a jwt")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		return err
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iat, Exp int64
		Iss      string
	}
	if err := json.Unmarshal(cb, &c); err != nil {
		return err
	}
	if c.Iss != "4242" {
		return fmt.Errorf("iss %q", c.Iss)
	}
	if c.Exp-f.now().Unix() > 600 || c.Exp <= f.now().Unix() || c.Iat > f.now().Unix() {
		return fmt.Errorf("bad times iat=%d exp=%d now=%d", c.Iat, c.Exp, f.now().Unix())
	}
	return nil
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[r.Method+" "+r.URL.Path]++
	js := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	notFound := func() { js(404, map[string]string{"message": "Not Found"}) }
	switch {
	case strings.HasPrefix(r.URL.Path, "/app-manifests/") && strings.HasSuffix(r.URL.Path, "/conversions"):
		code := strings.Split(r.URL.Path, "/")[2]
		if !f.manifestCodes[code] {
			notFound()
			return
		}
		delete(f.manifestCodes, code) // works once
		js(201, map[string]any{
			"id": 4242, "slug": "bitploy-test", "name": "bitploy test", "html_url": f.srv.URL + "/apps/bitploy-test",
			"client_id": "Iv1.client", "client_secret": "client-secret-value", "webhook_secret": f.webhookSecret, "pem": f.pem,
			"owner": map[string]any{"login": "acme", "type": "Organization"},
		})
	case r.URL.Path == "/login/oauth/access_token":
		_ = r.ParseForm()
		tok, ok := f.codes[r.Form.Get("code")]
		if r.Form.Get("client_id") != "Iv1.client" || r.Form.Get("client_secret") != "client-secret-value" || !ok {
			js(200, map[string]string{"error": "bad_verification_code"})
			return
		}
		delete(f.codes, r.Form.Get("code"))
		js(200, map[string]string{"access_token": tok, "token_type": "bearer"})
	case r.URL.Path == "/user":
		// The GitHub login of token ghu_usr_alice is alice.
		if _, ok := f.visible[f.bearer(r)]; !ok {
			js(401, map[string]string{"message": "Bad credentials"})
			return
		}
		js(200, map[string]any{"login": strings.TrimPrefix(f.bearer(r), "ghu_usr_")})
	case strings.HasPrefix(r.URL.Path, "/user/installations/") && strings.HasSuffix(r.URL.Path, "/repositories"):
		tok := f.bearer(r)
		id, _ := strconv.ParseInt(strings.Split(r.URL.Path, "/")[3], 10, 64)
		seen := false
		for _, i := range f.visible[tok] {
			seen = seen || i.ID == id
		}
		if !seen {
			notFound()
			return
		}
		list := f.repos[id]
		if mine, ok := f.userRepos[tok][id]; ok {
			list = mine
		}
		out := []map[string]any{}
		for _, full := range list {
			out = append(out, map[string]any{"full_name": full})
		}
		js(200, map[string]any{"total_count": len(out), "repositories": out})
	case r.URL.Path == "/user/installations":
		list, ok := f.visible[f.bearer(r)]
		if !ok {
			js(401, map[string]string{"message": "Bad credentials"})
			return
		}
		out := []map[string]any{}
		for _, i := range list {
			m := map[string]any{"id": i.ID, "repository_selection": i.Selection, "account": map[string]any{"login": i.Login, "type": i.Type}}
			if i.Suspended {
				m["suspended_at"] = "2026-01-01T00:00:00Z"
			}
			out = append(out, m)
		}
		js(200, map[string]any{"total_count": len(out), "installations": out})
	case strings.HasPrefix(r.URL.Path, "/app/installations/") && strings.HasSuffix(r.URL.Path, "/access_tokens"):
		if err := f.verifyJWT(f.bearer(r)); err != nil {
			js(401, map[string]string{"message": "A JSON web token could not be decoded: " + err.Error()})
			return
		}
		if f.tokenStatus != 0 {
			js(f.tokenStatus, map[string]string{"message": "You have exceeded a secondary rate limit."})
			return
		}
		id, _ := strconv.ParseInt(strings.Split(r.URL.Path, "/")[3], 10, 64)
		repos, ok := f.repos[id]
		if !ok {
			notFound()
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if names, _ := body["repositories"].([]any); len(names) > 0 {
			for _, n := range names {
				found := false
				for _, full := range repos {
					if strings.HasSuffix(full, "/"+n.(string)) {
						found = true
					}
				}
				if !found {
					js(422, map[string]string{"message": "repository not accessible"})
					return
				}
			}
		}
		tok := fmt.Sprintf("ghs_I%dI%dI%s", id, len(f.reqs)+1, strings.Repeat("x", 24))
		f.tokens[tok] = id
		f.reqs = append(f.reqs, tokenReq{Inst: id, Body: body, Token: tok})
		js(201, map[string]any{"token": tok, "expires_at": f.now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case r.URL.Path == "/installation/repositories":
		id, ok := f.tokens[f.bearer(r)]
		if !ok {
			js(401, map[string]string{"message": "Bad credentials"})
			return
		}
		out := []map[string]any{}
		for _, full := range f.repos[id] {
			owner, name, _ := strings.Cut(full, "/")
			out = append(out, map[string]any{"full_name": full, "name": name, "private": true, "html_url": "https://github.com/" + full,
				"default_branch": "main", "pushed_at": "2026-10-01T10:00:00Z", "owner": map[string]any{"login": owner}})
		}
		js(200, map[string]any{"total_count": len(out), "repositories": out})
	case strings.HasPrefix(r.URL.Path, "/repos/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
		if _, ok := f.tokens[f.bearer(r)]; !ok || len(parts) < 2 {
			notFound()
			return
		}
		full := strings.ToLower(parts[0] + "/" + parts[1])
		if len(parts) == 3 && parts[2] == "branches" {
			out := []map[string]string{}
			for _, b := range f.branches[full] {
				out = append(out, map[string]string{"name": b})
			}
			js(200, out)
			return
		}
		js(200, map[string]any{"full_name": full, "default_branch": f.defaultBranch[full], "private": true})
	default:
		notFound()
	}
}

func (f *fakeGitHub) tokenRequests() []tokenReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tokenReq(nil), f.reqs...)
}

// ---- harness ----

type ghEnv struct {
	*depEnv
	f     *fakeGitHub
	admin Actor
	clock *acctClock
}

func sessionOf(a Actor) Actor {
	a.Channel, a.Scope = ChannelDashboard, ScopeSession
	return a
}

// ghTestEnv returns an env where the admin already created the GitHub App.
func ghTestEnv(t *testing.T) *ghEnv {
	t.Helper()
	e := depTestSvc(t)
	f := newFakeGitHub(t)
	clk := &acctClock{}
	clk.now.Store(time.Now().Unix())
	e.s.Now = clk.Now
	f.now = clk.Now
	e.s.Cfg.GitHubAPIURL, e.s.Cfg.GitHubWebURL = f.srv.URL, f.srv.URL
	g := &ghEnv{depEnv: e, f: f, clock: clk}
	g.admin = sessionOf(e.addUser(t, "usr_admin", "admin@example.com", "admin"))
	m, err := e.s.GitHubStartManifest(context.Background(), g.admin, GitHubManifestInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GitHubFinishManifest(context.Background(), g.admin, GitHubManifestResult{Code: "goodcode", State: m.State}); err != nil {
		t.Fatal(err)
	}
	return g
}

// connect runs the browser round trip for a bitploy user: the start URL, then
// the redirect back with a code. visible are the installations GitHub shows
// the GitHub user behind that code.
func (g *ghEnv) connect(t *testing.T, user Actor, installationID int64, visible ...fakeInst) (GitHubOverview, error) {
	t.Helper()
	sess := sessionOf(user)
	red, err := g.s.GitHubConnectURL(context.Background(), sess, "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(red.URL)
	state := u.Query().Get("state")
	code := "code-" + user.UserID + "-" + state[:8]
	g.f.mu.Lock()
	g.f.codes[code] = "ghu_" + user.UserID
	g.f.visible["ghu_"+user.UserID] = visible
	for _, v := range visible {
		if _, ok := g.f.repos[v.ID]; !ok {
			g.f.repos[v.ID] = nil
		}
	}
	g.f.mu.Unlock()
	return g.s.GitHubLink(context.Background(), sess, GitHubLinkInput{InstallationID: installationID, Code: code, State: state})
}

// linkApp points an app at a repository without asking GitHub.
func (g *ghEnv) linkApp(t *testing.T, app App, repoURL, branch string, auto bool) {
	t.Helper()
	g.exec(t, `UPDATE apps SET git_repo_url = ?, git_branch = ?, git_auto_deploy = ? WHERE id = ?`, repoURL, branch, b2i(auto), app.ID)
}

func (g *ghEnv) deliver(event, id string, body []byte) (string, error) {
	return g.s.HandleGitHubWebhook(context.Background(), event, id, github.Sign([]byte(g.f.webhookSecret), body), body)
}

func pushBody(repo, ref, after string, inst int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"ref": ref, "after": after, "deleted": strings.HasPrefix(after, "0000"),
		"repository":   map[string]any{"full_name": repo},
		"installation": map[string]any{"id": inst},
		"head_commit":  map[string]any{"id": after, "message": "Fix the footer\n\nlonger text"},
	})
	return b
}

func (g *ghEnv) deployments(t *testing.T, app App) []string {
	t.Helper()
	rows, err := g.s.Q.ListAllDeploymentsByApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range rows {
		out = append(out, d.State+"/"+d.Channel+"/"+d.CommitSha.String)
	}
	return out
}

var sha1a = strings.Repeat("a", 40)
var sha1b = strings.Repeat("b", 40)

func alice(g *ghEnv) Actor { return g.user }

// ---- the admin: creating the app ----

func TestGitHubManifestFlow(t *testing.T) {
	e := depTestSvc(t)
	f := newFakeGitHub(t)
	e.s.Cfg.GitHubAPIURL, e.s.Cfg.GitHubWebURL = f.srv.URL, f.srv.URL
	e.s.Cfg.PublicURL = "https://bitploy.example.com" // reachable for GitHub, so the app gets a webhook
	ctx := context.Background()
	admin := sessionOf(e.addUser(t, "usr_admin", "admin@example.com", "admin"))
	other := sessionOf(e.addUser(t, "usr_admin2", "admin2@example.com", "admin"))
	user := sessionOf(e.user)

	if _, err := e.s.GitHubStartManifest(ctx, user, GitHubManifestInput{}); depCode(err) != "admin_only" {
		t.Fatalf("a normal user must not start it: %v", err)
	}
	if _, err := e.s.GitHubStartManifest(ctx, Actor{UserID: admin.UserID, Role: "admin", Scope: ScopeDeploy, Channel: ChannelAPI}, GitHubManifestInput{}); depCode(err) != "admin_only" {
		t.Fatalf("an API token must not start it: %v", err)
	}

	m, err := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{})
	if err != nil {
		t.Fatal(err)
	}
	var man struct {
		Name      string `json:"name"`
		URL       string `json:"url"`
		HookAttrs struct {
			URL    string `json:"url"`
			Active bool   `json:"active"`
		} `json:"hook_attributes"`
		RedirectURL  string            `json:"redirect_url"`
		CallbackURLs []string          `json:"callback_urls"`
		SetupURL     string            `json:"setup_url"`
		RequestOAuth bool              `json:"request_oauth_on_install"`
		Public       bool              `json:"public"`
		Permissions  map[string]string `json:"default_permissions"`
		Events       []string          `json:"default_events"`
	}
	if err := json.Unmarshal([]byte(m.Manifest), &man); err != nil {
		t.Fatal(err)
	}
	if man.Name != "bitploy bitploy.example.com" || man.URL != "https://bitploy.example.com" ||
		man.HookAttrs.URL != "https://bitploy.example.com/api/v1/github/webhook" || !man.HookAttrs.Active ||
		man.RedirectURL != "https://bitploy.example.com/admin" || man.SetupURL != "https://bitploy.example.com/settings/github" ||
		len(man.CallbackURLs) != 1 || man.CallbackURLs[0] != man.SetupURL || !man.RequestOAuth || !man.Public ||
		man.Permissions["contents"] != "read" || man.Permissions["metadata"] != "read" || len(man.Permissions) != 2 ||
		len(man.Events) != 1 || man.Events[0] != "push" {
		t.Fatalf("manifest: %s", m.Manifest)
	}
	if !strings.HasPrefix(m.PostURL, f.srv.URL+"/settings/apps/new?state=") || m.State == "" {
		t.Fatalf("post url %q", m.PostURL)
	}
	if o, err := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{Name: "My bitploy", Organization: "acme-inc"}); err != nil ||
		!strings.HasPrefix(o.PostURL, f.srv.URL+"/organizations/acme-inc/settings/apps/new?state=") || o.Name != "My bitploy" {
		t.Fatalf("org: %+v %v", o, err)
	}
	for _, bad := range []GitHubManifestInput{{Organization: "a/b"}, {Organization: "../x"}, {Name: strings.Repeat("n", 35)}, {Name: "bad\nname"}} {
		if _, err := e.s.GitHubStartManifest(ctx, admin, bad); depCode(err) == "" {
			t.Errorf("accepted %+v", bad)
		}
	}

	// The state is bound to the admin who started it, and to the manifest purpose.
	if _, err := e.s.GitHubFinishManifest(ctx, other, GitHubManifestResult{Code: "goodcode", State: m.State}); depCode(err) != "github_state_invalid" {
		t.Errorf("another admin's state: %v", err)
	}
	if _, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode", State: e.s.signGitHubState("connect", admin.UserID, time.Minute)}); depCode(err) != "github_state_invalid" {
		t.Errorf("wrong purpose: %v", err)
	}
	if _, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode", State: m.State + "x"}); depCode(err) != "github_state_invalid" {
		t.Errorf("tampered state: %v", err)
	}
	if _, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode"}); depCode(err) != "github_state_invalid" {
		t.Errorf("no state: %v", err)
	}
	if _, err := e.s.GitHubFinishManifest(ctx, user, GitHubManifestResult{Code: "goodcode", State: m.State}); depCode(err) != "admin_only" {
		t.Errorf("user: %v", err)
	}
	if info, _ := e.s.GitHubAppDetails(ctx, admin); info.Configured {
		t.Fatal("configured by failed attempts")
	}
	if f.hits["POST /app-manifests/goodcode/conversions"] != 0 {
		t.Fatal("the code must not be sent to GitHub before the state is verified")
	}
	// Expired.
	clk := &acctClock{}
	clk.now.Store(time.Now().Unix())
	e.s.Now = clk.Now
	old, _ := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{})
	clk.Advance(11 * time.Minute)
	if _, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode", State: old.State}); depCode(err) != "github_state_invalid" {
		t.Errorf("expired state: %v", err)
	}
	fresh, _ := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{})
	if _, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "wrongcode", State: fresh.State}); depCode(err) != "github_code_invalid" {
		t.Errorf("code GitHub does not know: %v", err)
	}

	info, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode", State: fresh.State})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Configured || info.Slug != "bitploy-test" || info.Owner != "acme" || info.WebhookURL != "https://bitploy.example.com/api/v1/github/webhook" || !info.PushEvents ||
		info.ManageURL != f.srv.URL+"/organizations/acme/settings/apps/bitploy-test" {
		t.Fatalf("info: %+v", info)
	}
	// Secrets are encrypted at rest, with a purpose-bound key, and never in answers.
	var cs, ws, pk []byte
	if err := e.s.Store.DB.QueryRow(`SELECT client_secret_enc, webhook_secret_enc, private_key_enc FROM github_app`).Scan(&cs, &ws, &pk); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]byte{cs, ws, pk} {
		if strings.Contains(string(c), "client-secret-value") || strings.Contains(string(c), "whsec_") || strings.Contains(string(c), "PRIVATE KEY") {
			t.Fatal("a secret is stored in the clear")
		}
	}
	if p, err := e.s.Crypto.Open(cs, "github_app:client_secret"); err != nil || string(p) != "client-secret-value" {
		t.Fatalf("client secret: %q %v", p, err)
	}
	if _, err := e.s.Crypto.Open(cs, "github_app:webhook_secret"); err == nil {
		t.Fatal("ciphertexts can be swapped between fields")
	}
	for _, v := range []any{info, mustOverview(t, e.s, admin), mustOverview(t, e.s, user)} {
		b, _ := json.Marshal(v)
		for _, secret := range []string{"client-secret-value", f.webhookSecret, "PRIVATE KEY", "Iv1.client"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("%q leaked in %s", secret, b)
			}
		}
	}
	var leaked int
	e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details LIKE '%client-secret-value%' OR details LIKE '%whsec_%' OR details LIKE '%PRIVATE KEY%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("a GitHub App secret reached the audit log")
	}
	// A normal user sees only that it exists.
	ov := mustOverview(t, e.s, user)
	if !ov.App.Configured || ov.App.Name == "" || ov.App.Slug == "" || ov.App.HTMLURL != "" || ov.App.WebhookURL != "" || ov.App.ManageURL != "" || ov.App.Owner != "" {
		t.Errorf("user view: %+v", ov.App)
	}
	if _, err := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{}); depCode(err) != "github_app_exists" {
		t.Errorf("second app: %v", err)
	}
	if err := e.s.GitHubDisconnectApp(ctx, user); depCode(err) != "admin_only" {
		t.Errorf("user disconnect: %v", err)
	}
	if err := e.s.GitHubDisconnectApp(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.s.GitHubAppDetails(ctx, admin); info.Configured {
		t.Fatal("still configured after disconnect")
	}
	var n int
	e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM github_app`).Scan(&n)
	if n != 0 {
		t.Fatal("credentials survived the disconnect")
	}
	if _, err := e.s.GitHubConnectURL(ctx, user, ""); depCode(err) != "github_app_missing" {
		t.Errorf("connect without an app: %v", err)
	}
}

func mustOverview(t *testing.T, s *Service, a Actor) GitHubOverview {
	t.Helper()
	ov, err := s.GitHubOverview(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return ov
}

func TestGitHubPublicURLProblem(t *testing.T) {
	for raw, bad := range map[string]bool{
		"https://bitploy.nl": false, "https://deploy.example.com": false, "http://203.0.113.9:8080": false,
		"http://localhost:8080": true, "http://localhost:5173": true, "http://bitploy.localhost": true, "http://127.0.0.1:8080": true,
		"http://192.168.1.20": true, "http://10.0.0.5": true, "http://mybox": true, "https://nas.local": true, "": true,
	} {
		if got := publicURLProblem(raw) != ""; got != bad {
			t.Errorf("publicURLProblem(%q) = %v, want %v", raw, got, bad)
		}
	}
}

func TestGitHubStateTokens(t *testing.T) {
	e := depTestSvc(t)
	clk := &acctClock{}
	clk.now.Store(time.Now().Unix())
	e.s.Now = clk.Now
	tok := e.s.signGitHubState("connect", "usr_a", time.Minute)
	if err := e.s.verifyGitHubState(tok, "connect", "usr_a"); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"other user":    func() error { return e.s.verifyGitHubState(tok, "connect", "usr_b") },
		"other purpose": func() error { return e.s.verifyGitHubState(tok, "manifest", "usr_a") },
		"empty":         func() error { return e.s.verifyGitHubState("", "connect", "usr_a") },
		"garbage":       func() error { return e.s.verifyGitHubState("not base64 !!", "connect", "usr_a") },
		"flipped byte": func() error {
			raw, _ := base64.RawURLEncoding.DecodeString(tok)
			raw[len(raw)-1] ^= 1
			return e.s.verifyGitHubState(base64.RawURLEncoding.EncodeToString(raw), "connect", "usr_a")
		},
		"no user": func() error {
			return e.s.verifyGitHubState(e.s.signGitHubState("connect", "", time.Minute), "connect", "")
		},
	} {
		if depCode(fn()) != "github_state_invalid" {
			t.Errorf("%s accepted", name)
		}
	}
	clk.Advance(2 * time.Minute)
	if depCode(e.s.verifyGitHubState(tok, "connect", "usr_a")) != "github_state_invalid" {
		t.Error("expired state accepted")
	}
	// A state sealed with another server key is no state.
	otherKey, _ := store.NewCrypto([]byte("another-secret-key-32-bytes-long"))
	payload, _ := json.Marshal(githubStatePayload{Purpose: "connect", UserID: "usr_a", Expires: clk.Now().Add(time.Hour).Unix(), Nonce: "n"})
	forged := base64.RawURLEncoding.EncodeToString(otherKey.Seal(payload, githubStateAAD))
	if depCode(e.s.verifyGitHubState(forged, "connect", "usr_a")) != "github_state_invalid" {
		t.Error("state of another instance accepted")
	}
}

// ---- the user: connecting an account ----

func TestGitHubLinkVerifiesTheInstallation(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	bob := g.addUser(t, "usr_bob", "bob@example.com", "user")
	g.f.repos[101] = []string{"alice/site", "alice/other"}
	g.f.repos[202] = []string{"acme/secret"}
	aliceInst := fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}
	acmeInst := fakeInst{ID: 202, Login: "acme", Type: "Organization", Selection: "all"}

	// Bob is a different person on GitHub who sees only his own installation.
	// He types alice's installation id into the redirect.
	if _, err := g.connect(t, bob, 101, fakeInst{ID: 303, Login: "bob", Type: "User", Selection: "all"}); depCode(err) != "github_installation_forbidden" {
		t.Fatalf("a claim for someone else's installation must fail: %v", err)
	}
	if ov := mustOverview(t, g.s, sessionOf(bob)); len(ov.Installations) != 0 {
		t.Fatalf("something was linked: %+v", ov.Installations)
	}
	// Someone else's state does not work for Alice.
	sess := sessionOf(g.user)
	bobURL, _ := g.s.GitHubConnectURL(ctx, sessionOf(bob), "")
	bu, _ := url.Parse(bobURL.URL)
	g.f.codes["stolen"] = "ghu_usr_alice"
	g.f.visible["ghu_usr_alice"] = []fakeInst{aliceInst}
	if _, err := g.s.GitHubLink(ctx, sess, GitHubLinkInput{InstallationID: 101, Code: "stolen", State: bu.Query().Get("state")}); depCode(err) != "github_state_invalid" {
		t.Fatalf("state of another user: %v", err)
	}
	if _, ok := g.f.codes["stolen"]; !ok {
		t.Fatal("the code was exchanged although the state was wrong")
	}
	// Without a state, or with a state of the wrong kind.
	if _, err := g.s.GitHubLink(ctx, sess, GitHubLinkInput{InstallationID: 101, Code: "stolen"}); depCode(err) != "github_state_invalid" {
		t.Fatalf("no state: %v", err)
	}
	if _, err := g.s.GitHubLink(ctx, sess, GitHubLinkInput{InstallationID: 101, Code: "stolen", State: g.s.signGitHubState("manifest", sess.UserID, time.Minute)}); depCode(err) != "github_state_invalid" {
		t.Fatalf("manifest state: %v", err)
	}
	// An API token cannot link.
	if _, err := g.s.GitHubLink(ctx, g.user, GitHubLinkInput{InstallationID: 101, Code: "stolen", State: "x"}); depCode(err) != "session_required" {
		t.Fatalf("token: %v", err)
	}

	// Alice: both accounts are visible, but she asked for 101 only.
	ov, err := g.connect(t, g.user, 101, aliceInst, acmeInst)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Installations) != 1 || ov.Installations[0].ID != 101 || ov.Installations[0].AccountLogin != "alice" ||
		ov.Installations[0].RepositorySelection != "selected" || ov.Installations[0].Status != "active" ||
		ov.Installations[0].RepoCount == nil || *ov.Installations[0].RepoCount != 2 ||
		ov.Installations[0].ManageURL != g.f.srv.URL+"/settings/installations/101" {
		t.Fatalf("overview: %+v", ov.Installations)
	}
	if n, _ := g.s.countInstallationRepos(ctx, 101); n != 2 {
		t.Fatalf("repositories stored: %d", n)
	}
	// Signing in only (no installation id) links every installation GitHub shows her.
	ov, err = g.connect(t, g.user, 0, aliceInst, acmeInst)
	if err != nil || len(ov.Installations) != 2 {
		t.Fatalf("link all: %+v %v", ov, err)
	}
	if ov.Installations[0].AccountLogin != "acme" || ov.Installations[0].ManageURL != g.f.srv.URL+"/organizations/acme/settings/installations/202" {
		t.Errorf("org manage url: %+v", ov.Installations[0])
	}
	// A used code fails; so does a code of nobody.
	state := g.s.signGitHubState("connect", sess.UserID, time.Minute)
	if _, err := g.s.GitHubLink(ctx, sess, GitHubLinkInput{Code: "never-issued", State: state}); depCode(err) != "github_code_invalid" {
		t.Fatalf("unknown code: %v", err)
	}
	if _, err := g.s.GitHubLink(ctx, sess, GitHubLinkInput{State: state}); depCode(err) != "github_code_missing" {
		t.Fatalf("no code: %v", err)
	}
	// Nothing installed yet.
	if _, err := g.connect(t, bob, 0); depCode(err) != "github_not_installed" {
		t.Fatalf("nothing installed: %v", err)
	}
	// The link is per user: Bob cannot forget Alice's account, Alice can.
	if err := g.s.GitHubUnlink(ctx, sessionOf(bob), 101); depCode(err) != "github_account_not_found" {
		t.Fatalf("unlink of another user's account: %v", err)
	}
	if err := g.s.GitHubUnlink(ctx, sess, 101); err != nil {
		t.Fatal(err)
	}
	if ov := mustOverview(t, g.s, sess); len(ov.Installations) != 1 || ov.Installations[0].ID != 202 {
		t.Fatalf("after unlink: %+v", ov.Installations)
	}
	if n, _ := g.s.countInstallationRepos(ctx, 101); n != 0 {
		t.Fatal("repositories of a forgotten installation stay")
	}
	// No tokens or codes in the audit log or in answers.
	var leaked int
	g.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details LIKE '%ghu_%' OR details LIKE '%ghs_%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("a GitHub token reached the audit log")
	}
}

func TestGitHubLinkSuspendedAndLimit(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	g.f.repos[101] = nil
	ov, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "all", Suspended: true})
	if err != nil || ov.Installations[0].Status != "suspended" {
		t.Fatalf("suspended installation: %+v %v", ov, err)
	}
	var many []fakeInst
	for i := 0; i < githubMaxLinks+1; i++ {
		many = append(many, fakeInst{ID: int64(1000 + i), Login: fmt.Sprintf("org%d", i), Type: "Organization", Selection: "all"})
	}
	if _, err := g.connect(t, g.user, 0, many...); depCode(err) != "github_too_many_accounts" {
		t.Fatalf("limit: %v", err)
	}
	_ = ctx
}

// ---- JWT and installation tokens ----

func TestInstallationTokensAreSignedAndCached(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	g.f.repos[101] = []string{"alice/site"}
	app, err := g.s.githubApp(ctx)
	if err != nil || app == nil {
		t.Fatal(err)
	}
	// The fake checks the RS256 signature, issuer and the 10-minute limit.
	t1, err := g.s.installationToken(ctx, app, 101, "")
	if err != nil || !strings.HasPrefix(t1, "ghs_I101I") {
		t.Fatalf("token: %q %v", t1, err)
	}
	t2, _ := g.s.installationToken(ctx, app, 101, "")
	if t1 != t2 || len(g.f.tokenRequests()) != 1 {
		t.Fatalf("a valid token must be reused: %d requests", len(g.f.tokenRequests()))
	}
	// A narrow token for a clone asks for one repository and read access to contents only.
	t3, err := g.s.installationToken(ctx, app, 101, "alice/site")
	if err != nil || t3 == t1 {
		t.Fatalf("narrow token: %q %v", t3, err)
	}
	body := g.f.tokenRequests()[1].Body
	repos, _ := body["repositories"].([]any)
	perms, _ := body["permissions"].(map[string]any)
	if len(repos) != 1 || repos[0] != "site" || perms["contents"] != "read" || len(perms) != 1 {
		t.Fatalf("narrow token request: %v", body)
	}
	// Refreshed a few minutes before it expires, not before.
	g.clock.Advance(54 * time.Minute)
	if t4, _ := g.s.installationToken(ctx, app, 101, ""); t4 != t1 {
		t.Fatal("refreshed too early")
	}
	g.clock.Advance(2 * time.Minute) // 56 minutes: inside the 5 minute margin
	t5, err := g.s.installationToken(ctx, app, 101, "")
	if err != nil || t5 == t1 {
		t.Fatalf("not refreshed before expiry: %q %v", t5, err)
	}
	// An installation GitHub does not know.
	if _, err := g.s.installationToken(ctx, app, 999, ""); github.Status(err) != 404 {
		t.Fatalf("unknown installation: %v", err)
	}
	// The private key signs the JWT: a different key is rejected by GitHub.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := *app
	bad.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(other)}))
	g.s.forgetInstallationCaches(101)
	if _, err := g.s.installationToken(ctx, &bad, 101, ""); github.Status(err) != 401 {
		t.Fatalf("wrong key: %v", err)
	}
	// Tokens do not show up in redacted logs.
	if got := apperr.Redact("clone failed for " + t1); strings.Contains(got, t1) {
		t.Fatalf("installation token not redacted: %s", got)
	}
}

// ---- clone credentials ----

func TestCloneCredentialOrder(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	bob := g.addUser(t, "usr_bob", "bob@example.com", "user")
	g.f.repos[101] = []string{"alice/site"}
	g.f.repos[202] = []string{"acme/secret"}
	g.f.repos[303] = []string{"acme/other"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.connect(t, bob, 202, fakeInst{ID: 202, Login: "acme", Type: "Organization", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}

	// 1. The owner's installation that covers the repository.
	tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/Alice/Site.git")
	if !strings.HasPrefix(tok, "ghs_I101I") {
		t.Fatalf("own installation: %q", tok)
	}
	// 2. Nothing that covers it: anonymous (public repositories keep working).
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/someone/public-repo"); tok != "" {
		t.Fatalf("no installation for that owner must be anonymous: %q", tok)
	}
	// Another user's installation is never used, even for a repository it covers.
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/acme/secret"); tok != "" {
		t.Fatalf("alice got a token from bob's installation: %q", tok)
	}
	if tok := g.s.gitCloneToken(ctx, bob.UserID, "https://github.com/acme/secret"); !strings.HasPrefix(tok, "ghs_I202I") {
		t.Fatalf("bob's own: %q", tok)
	}
	// A repository of the owner's account that the installation does not include.
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/alice/not-shared"); tok != "" {
		t.Fatalf("a repository outside the installation: %q", tok)
	}
	// Other hosts never get a token.
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://gitlab.com/alice/site"); tok != "" {
		t.Fatalf("other host: %q", tok)
	}
	// Every token request was narrow: one repository, contents read.
	for _, r := range g.f.tokenRequests() {
		if r.Body["repositories"] == nil {
			continue
		}
		if p, _ := r.Body["permissions"].(map[string]any); p["contents"] != "read" {
			t.Errorf("clone token request: %v", r.Body)
		}
	}
	// A deleted installation falls back to anonymous and is shown as such.
	g.f.mu.Lock()
	delete(g.f.repos, 101)
	g.f.mu.Unlock()
	g.s.forgetInstallationCaches(101)
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/alice/site"); tok != "" {
		t.Fatalf("deleted installation: %q", tok)
	}
	if ov := mustOverview(t, g.s, sessionOf(g.user)); ov.Installations[0].Status != "deleted" {
		t.Fatalf("status: %+v", ov.Installations[0])
	}
	// Without a GitHub App everything is anonymous.
	if err := g.s.GitHubDisconnectApp(ctx, g.admin); err != nil {
		t.Fatal(err)
	}
	if tok := g.s.gitCloneToken(ctx, bob.UserID, "https://github.com/acme/secret"); tok != "" {
		t.Fatalf("no app: %q", tok)
	}
}

// A 403 from GitHub is often a secondary rate limit: the installation stays
// active (real suspensions come with the installation webhook).
func TestCloneTokenForbiddenKeepsInstallationActive(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	g.f.repos[101] = []string{"alice/site"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	g.f.mu.Lock()
	g.f.tokenStatus = 403
	g.f.mu.Unlock()
	if tok := g.s.gitCloneToken(ctx, g.user.UserID, "https://github.com/alice/site"); tok != "" {
		t.Fatalf("token while GitHub answers 403: %q", tok)
	}
	if ov := mustOverview(t, g.s, sessionOf(g.user)); ov.Installations[0].Status != "active" {
		t.Fatalf("status after a 403: %+v", ov.Installations[0])
	}
}

// ---- repositories and branches ----

func TestGitHubRepositoriesAndBranches(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	bob := g.addUser(t, "usr_bob", "bob@example.com", "user")
	g.f.repos[101] = []string{"alice/site", "alice/blog"}
	g.f.repos[202] = []string{"acme/secret"}
	g.f.branches["alice/site"] = []string{"main", "dev"}
	g.f.defaultBranch["alice/site"] = "main"
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.connect(t, bob, 202, fakeInst{ID: 202, Login: "acme", Type: "Organization", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	sess := sessionOf(g.user)
	repos, err := g.s.GitHubRepositories(ctx, sess, "")
	if err != nil || len(repos) != 2 || repos[0].InstallationID != 101 {
		t.Fatalf("repos: %+v %v", repos, err)
	}
	for _, r := range repos {
		if strings.HasPrefix(r.FullName, "acme/") {
			t.Fatal("another user's repository is listed")
		}
	}
	if repos, _ := g.s.GitHubRepositories(ctx, sess, "BLO"); len(repos) != 1 || repos[0].FullName != "alice/blog" {
		t.Fatalf("search: %+v", repos)
	}
	br, err := g.s.GitHubBranches(ctx, sess, "alice/site")
	if err != nil || br.DefaultBranch != "main" || len(br.Branches) != 2 {
		t.Fatalf("branches: %+v %v", br, err)
	}
	if _, err := g.s.GitHubBranches(ctx, sess, "acme/secret"); depCode(err) != "github_repo_not_connected" {
		t.Fatalf("branches of another user's repository: %v", err)
	}
	if _, err := g.s.GitHubBranches(ctx, sess, "not a repo"); depCode(err) != "source_url_invalid" {
		t.Fatalf("bad repo: %v", err)
	}
	if _, err := g.s.GitHubRepositories(ctx, g.user, ""); depCode(err) != "session_required" {
		t.Fatalf("token: %v", err)
	}
}

// ---- the webhook ----

func TestWebhookSignatureAndDelivery(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	body := []byte(`{"zen":"hi"}`)
	if _, err := g.s.HandleGitHubWebhook(ctx, "ping", "d-1", "", body); depCode(err) != "github_signature_invalid" {
		t.Fatalf("no signature: %v", err)
	}
	if _, err := g.s.HandleGitHubWebhook(ctx, "ping", "d-1", github.Sign([]byte("wrong"), body), body); depCode(err) != "github_signature_invalid" {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := g.s.HandleGitHubWebhook(ctx, "ping", "d-1", github.Sign([]byte(g.f.webhookSecret), []byte(`{"zen":"other"}`)), body); depCode(err) != "github_signature_invalid" {
		t.Fatalf("signature of another body: %v", err)
	}
	var n int
	g.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM github_deliveries`).Scan(&n)
	if n != 0 {
		t.Fatal("a delivery with a bad signature was recorded")
	}
	if msg, err := g.deliver("ping", "d-1", body); err != nil || msg != "pong" {
		t.Fatalf("ping: %q %v", msg, err)
	}
	if msg, err := g.deliver("ping", "d-1", body); err != nil || !strings.Contains(msg, "duplicate") {
		t.Fatalf("the same delivery twice: %q %v", msg, err)
	}
	if _, err := g.deliver("ping", "", body); depCode(err) != "github_delivery_missing" {
		t.Fatalf("no delivery id: %v", err)
	}
	if msg, err := g.deliver("star", "d-2", body); err != nil || !strings.Contains(msg, "ignored") {
		t.Fatalf("unknown event: %q %v", msg, err)
	}
	// A body that is signed but unreadable is refused and may be redelivered.
	if _, err := g.deliver("push", "d-3", []byte(`{not json`)); depCode(err) != "github_payload_invalid" {
		t.Fatalf("bad json: %v", err)
	}
	g.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM github_deliveries WHERE delivery_id = 'd-3'`).Scan(&n)
	if n != 0 {
		t.Fatal("a failed delivery must be forgotten so GitHub's redeliver works")
	}
	// Old delivery ids are pruned.
	g.clock.Advance(8 * 24 * time.Hour)
	if err := g.s.Q.PruneGitHubDeliveries(ctx, g.s.now()-7*86400); err != nil {
		t.Fatal(err)
	}
	g.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM github_deliveries`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d old deliveries left", n)
	}
	// Without a GitHub App the endpoint says so.
	if err := g.s.GitHubDisconnectApp(ctx, g.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := g.deliver("ping", "d-9", body); depCode(err) != "github_app_missing" {
		t.Fatalf("no app: %v", err)
	}
}

func TestWebhookPushMatching(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	app := g.mustApp(t, CreateAppInput{Slug: "site"})
	g.f.repos[101] = []string{"alice/site"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	g.linkApp(t, app, "https://github.com/alice/site", "main", true)

	push := func(id, repo, ref, after string, inst int64) string {
		t.Helper()
		msg, err := g.deliver("push", id, pushBody(repo, ref, after, inst))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return msg
	}
	count := func() int { return len(g.deployments(t, app)) }

	// Branches, tags, deletions.
	for name, args := range map[string][]any{
		"other branch":    {"alice/site", "refs/heads/dev", sha1a, int64(101)},
		"tag":             {"alice/site", "refs/tags/v1", sha1a, int64(101)},
		"deleted branch":  {"alice/site", "refs/heads/main", strings.Repeat("0", 40), int64(101)},
		"other repo":      {"alice/blog", "refs/heads/main", sha1a, int64(101)},
		"no installation": {"alice/site", "refs/heads/main", sha1a, int64(0)},
	} {
		push("p-"+name, args[0].(string), args[1].(string), args[2].(string), args[3].(int64))
		if count() != 0 {
			t.Fatalf("%s started a deployment: %v", name, g.deployments(t, app))
		}
	}
	if got, _ := g.s.GetApp(ctx, g.user, "site"); got.GitSource.LastPushAt != nil {
		t.Fatal("an ignored event set last_push_at")
	}

	// The real thing, with the repository written in other ways.
	for i, url := range []string{"https://github.com/alice/site", "https://github.com/Alice/Site", "https://github.com/alice/site.git", "https://www.github.com/ALICE/SITE/"} {
		g.exec(t, `UPDATE apps SET git_repo_url = ? WHERE id = ?`, url, app.ID)
		before := count()
		msg := push(fmt.Sprintf("p-ok-%d", i), "Alice/Site", "refs/heads/main", sha1a, 101)
		if count() != before+1 || !strings.Contains(msg, "1 deployment(s) queued") {
			t.Fatalf("%s: %q %v", url, msg, g.deployments(t, app))
		}
	}
	deps, _ := g.s.Q.ListAllDeploymentsByApp(ctx, app.ID)
	newest := deps[0]
	if newest.Channel != "github" || newest.SourceKind != "git" || newest.SourceRef != "main" || newest.CommitSha.String != sha1a ||
		newest.CommitMessage.String != "Fix the footer" || newest.State != "queued" || newest.CreatedBy.String != g.user.UserID {
		t.Fatalf("deployment: %+v", newest)
	}
	got, _ := g.s.GetApp(ctx, g.user, "site")
	if got.GitSource.LastPushAt == nil {
		t.Fatal("last_push_at not recorded")
	}
	if dv := g.s.deploymentBase(newest, "site", "web", ""); dv.Channel != "github" || dv.CommitSHA == nil || *dv.CommitSHA != sha1a {
		t.Fatalf("view: %+v", dv)
	}

	// Quick pushes collapse: older queued ones are cancelled, the newest stays.
	cancelled := 0
	for _, s := range g.deployments(t, app) {
		if strings.HasPrefix(s, "cancelled") {
			cancelled++
		}
	}
	if cancelled != 3 || g.deployments(t, app)[0] != "queued/github/"+sha1a {
		t.Fatalf("collapse: %v", g.deployments(t, app))
	}
	push("p-newest", "alice/site", "refs/heads/main", sha1b, 101)
	if d := g.deployments(t, app); d[0] != "queued/github/"+sha1b || d[1] != "cancelled/github/"+sha1a {
		t.Fatalf("newest commit wins: %v", d)
	}

	// The same delivery is handled once.
	before := count()
	if msg := push("p-newest", "alice/site", "refs/heads/main", sha1b, 101); !strings.Contains(msg, "duplicate") || count() != before {
		t.Fatalf("duplicate: %q", msg)
	}
}

func TestWebhookAutoDeployOffStoppedAndSuspended(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	app := g.mustApp(t, CreateAppInput{Slug: "site"})
	g.f.repos[101] = []string{"alice/site"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	g.linkApp(t, app, "https://github.com/alice/site", "main", false)
	if _, err := g.deliver("push", "a1", pushBody("alice/site", "refs/heads/main", sha1a, 101)); err != nil {
		t.Fatal(err)
	}
	if len(g.deployments(t, app)) != 0 {
		t.Fatal("auto-deploy is off but a push deployed")
	}
	got, _ := g.s.GetApp(ctx, g.user, "site")
	if got.GitSource.LastPushAt == nil || got.GitSource.PushStatus != "off" || got.GitSource.PushReady {
		t.Fatalf("the push is still recorded for the UI: %+v", got.GitSource)
	}
	// An app stopped on purpose stays stopped.
	g.linkApp(t, app, "https://github.com/alice/site", "main", true)
	g.exec(t, `UPDATE apps SET status = 'stopped', desired_state = 'stopped' WHERE id = ?`, app.ID)
	if _, err := g.deliver("push", "a2", pushBody("alice/site", "refs/heads/main", sha1a, 101)); err != nil {
		t.Fatal(err)
	}
	if len(g.deployments(t, app)) != 0 {
		t.Fatal("a stopped app was started by a push")
	}
	// A suspended owner cannot deploy; the push is not an error for GitHub.
	g.exec(t, `UPDATE apps SET status = 'running', desired_state = 'running' WHERE id = ?`, app.ID)
	g.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, g.user.UserID)
	if msg, err := g.deliver("push", "a3", pushBody("alice/site", "refs/heads/main", sha1a, 101)); err != nil || !strings.Contains(msg, "0 deployment(s)") {
		t.Fatalf("suspended: %q %v", msg, err)
	}
	var audited int
	g.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'github.push_not_deployed'`).Scan(&audited)
	if audited != 1 {
		t.Fatalf("a push that could not deploy leaves an audit row, got %d", audited)
	}
}

func TestWebhookTenantIsolation(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	bob := g.addUser(t, "usr_bob", "bob@example.com", "user")
	g.f.repos[101] = []string{"alice/site"}
	g.f.repos[202] = []string{"bob/stuff"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.connect(t, bob, 202, fakeInst{ID: 202, Login: "bob", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	aliceApp := g.mustApp(t, CreateAppInput{Slug: "alice-site"})
	g.linkApp(t, aliceApp, "https://github.com/alice/site", "main", true)
	bobApp, err := g.s.CreateApp(ctx, bob, CreateAppInput{Slug: "bob-site"})
	if err != nil {
		t.Fatal(err)
	}
	g.linkApp(t, bobApp, "https://github.com/alice/site", "main", true) // bob deploys alice's public repository

	// Bob's installation fires for alice's repository name: it must not touch alice's app.
	if _, err := g.deliver("push", "t1", pushBody("alice/site", "refs/heads/main", sha1a, 202)); err != nil {
		t.Fatal(err)
	}
	if n := len(g.deployments(t, aliceApp)); n != 0 {
		t.Fatalf("another user's installation started alice's app: %v", g.deployments(t, aliceApp))
	}
	if got, _ := g.s.GetApp(ctx, g.user, "alice-site"); got.GitSource.LastPushAt != nil {
		t.Fatal("another user's installation wrote alice's last_push_at")
	}
	// An installation nobody linked here does nothing either.
	if _, err := g.deliver("push", "t2", pushBody("alice/site", "refs/heads/main", sha1a, 999)); err != nil {
		t.Fatal(err)
	}
	if len(g.deployments(t, aliceApp))+len(g.deployments(t, bobApp)) != 0 {
		t.Fatal("an unknown installation started an app")
	}
	// Alice's installation deploys alice's app, and not bob's app on the same repository.
	if _, err := g.deliver("push", "t3", pushBody("alice/site", "refs/heads/main", sha1a, 101)); err != nil {
		t.Fatal(err)
	}
	if len(g.deployments(t, aliceApp)) != 1 || len(g.deployments(t, bobApp)) != 0 {
		t.Fatalf("alice: %v bob: %v", g.deployments(t, aliceApp), g.deployments(t, bobApp))
	}
}

func TestInstallationEventsUpdateTheStatus(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	app := g.mustApp(t, CreateAppInput{Slug: "site"})
	g.f.repos[101] = []string{"alice/site", "alice/blog"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	g.linkApp(t, app, "https://github.com/alice/site", "main", true)
	state := func() GitSourceState {
		got, err := g.s.GetApp(ctx, g.user, "site")
		if err != nil {
			t.Fatal(err)
		}
		return *got.GitSource
	}
	if s := state(); !s.PushReady || s.PushStatus != "ready" || s.Repo != "alice/site" || s.RepoHTMLURL != g.f.srv.URL+"/alice/site" {
		t.Fatalf("start: %+v", s)
	}
	ev := func(action string, extra map[string]any) []byte {
		m := map[string]any{"action": action, "installation": map[string]any{"id": 101, "account": map[string]any{"login": "alice"}}}
		for k, v := range extra {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return b
	}
	n := 0
	deliver := func(event string, body []byte) string {
		n++
		msg, err := g.deliver(event, fmt.Sprintf("e-%d", n), body)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}
	// The repository is taken away from the installation.
	deliver("installation_repositories", ev("removed", map[string]any{
		"repository_selection": "selected", "repositories_removed": []map[string]any{{"full_name": "alice/site"}},
	}))
	if s := state(); s.PushReady || s.PushStatus != "no_access" || !strings.Contains(s.PushMessage, "alice/site") {
		t.Fatalf("after removal: %+v", s)
	}
	deliver("installation_repositories", ev("added", map[string]any{
		"repository_selection": "selected", "repositories_added": []map[string]any{{"full_name": "Alice/Site"}},
	}))
	if s := state(); !s.PushReady {
		t.Fatalf("after adding again: %+v", s)
	}
	// Suspend, unsuspend, delete.
	deliver("installation", ev("suspend", nil))
	if s := state(); s.PushReady || s.PushStatus != "reconnect" {
		t.Fatalf("suspended: %+v", s)
	}
	deliver("installation", ev("unsuspend", nil))
	if s := state(); !s.PushReady {
		t.Fatalf("unsuspended: %+v", s)
	}
	deliver("installation", ev("deleted", nil))
	s := state()
	if s.PushReady || s.PushStatus != "reconnect" || !strings.Contains(s.PushMessage, "Connect GitHub again") {
		t.Fatalf("deleted: %+v", s)
	}
	if ov := mustOverview(t, g.s, sessionOf(g.user)); ov.Installations[0].Status != "deleted" {
		t.Fatalf("overview: %+v", ov.Installations[0])
	}
	// A deleted installation's pushes (if any still arrive) deploy nothing wrong: the link exists, so tenant rules decide.
	// Unknown installation: ignored.
	other, _ := json.Marshal(map[string]any{"action": "removed", "installation": map[string]any{"id": 5555}, "repository_selection": "all"})
	if msg := deliver("installation_repositories", other); !strings.Contains(msg, "ignored") {
		t.Fatalf("unknown: %q", msg)
	}
	// Switching to "all repositories" drops the stored list.
	g.exec(t, `UPDATE github_installations SET status = 'active' WHERE installation_id = 101`)
	deliver("installation_repositories", ev("added", map[string]any{"repository_selection": "all"}))
	if c, _ := g.s.countInstallationRepos(ctx, 101); c != 0 {
		t.Fatalf("stored repositories after selecting all: %d", c)
	}
	if s := state(); !s.PushReady {
		t.Fatalf("all repositories: %+v", s)
	}
}

func TestPushStatusMessages(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	app := g.mustApp(t, CreateAppInput{Slug: "site"})
	status := func(actor Actor) GitSourceState {
		got, err := g.s.GetApp(ctx, actor, "site")
		if err != nil {
			t.Fatal(err)
		}
		return *got.GitSource
	}
	g.linkApp(t, app, "https://gitlab.com/alice/site", "main", true)
	if s := status(g.user); s.PushStatus != "other_host" || s.Repo != "" || s.RepoHTMLURL != "" {
		t.Fatalf("other host: %+v", s)
	}
	g.linkApp(t, app, "https://github.com/alice/site", "main", true)
	if s := status(g.user); s.PushStatus != "not_connected" || s.PushMessage != "Connect GitHub to deploy on every push." {
		t.Fatalf("not connected: %+v", s)
	}
	g.f.repos[101] = []string{"alice/blog"}
	g.f.repos[202] = []string{"acme/x"}
	if _, err := g.connect(t, g.user, 0, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}, fakeInst{ID: 202, Login: "acme", Type: "Organization", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if s := status(g.user); s.PushStatus != "no_access" || strings.Contains(s.PushMessage, "token") {
		t.Fatalf("no access: %+v", s)
	}
	g.linkApp(t, app, "https://github.com/someone-else/site", "main", true)
	if s := status(g.user); s.PushStatus != "not_connected" || !strings.Contains(s.PushMessage, "someone-else") {
		t.Fatalf("other owner: %+v", s)
	}
	if err := g.s.GitHubDisconnectApp(ctx, g.admin); err != nil {
		t.Fatal(err)
	}
	if s := status(g.user); s.PushStatus != "app_missing" || !strings.Contains(s.PushMessage, "Ask your bitploy admin") {
		t.Fatalf("app missing, user: %+v", s)
	}
	if s := status(g.admin); s.PushStatus != "app_missing" || !strings.Contains(s.PushMessage, "Admin page") {
		t.Fatalf("app missing, admin: %+v", s)
	}
}

// ---- linking and unlinking an app's repository ----

func TestSetGitSourceUsesTheOwnersInstallation(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	app := g.mustApp(t, CreateAppInput{Slug: "site"})
	g.f.repos[101] = []string{"alice/site"}
	if _, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	var seen []deploy.GitSource
	g.s.lsRemote = func(_ context.Context, src deploy.GitSource) (string, string, error) {
		seen = append(seen, src)
		if src.Branch == "" {
			return sha1a, "main", nil
		}
		return sha1a, src.Branch, nil
	}
	got, err := g.s.SetGitSource(ctx, g.user, "site", GitSource{RepoURL: "https://github.com/alice/site/", Subdir: "./web/"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen[0].Token, "ghs_I101I") {
		t.Fatalf("the check must use the owner's installation: %+v", seen[0])
	}
	gs := got.GitSource
	if gs.RepoURL != "https://github.com/alice/site" || gs.Branch != "main" || gs.Subdir != "web" || !gs.AutoDeploy || !gs.PushReady {
		t.Fatalf("default branch, auto-deploy on by default: %+v", gs)
	}
	// Explicitly off; other host forced off.
	got, err = g.s.SetGitSource(ctx, g.user, "site", GitSource{RepoURL: "https://github.com/alice/site", Branch: "dev", AutoDeploy: ptr(false)})
	if err != nil || got.GitSource.AutoDeploy || got.GitSource.Branch != "dev" || got.GitSource.PushStatus != "off" {
		t.Fatalf("auto-deploy off: %+v %v", got.GitSource, err)
	}
	got, err = g.s.SetGitSource(ctx, g.user, "site", GitSource{RepoURL: "https://gitlab.com/alice/site", AutoDeploy: ptr(true)})
	if err != nil || got.GitSource.AutoDeploy || got.GitSource.PushStatus != "other_host" || seen[len(seen)-1].Token != "" {
		t.Fatalf("other host: %+v %v", got.GitSource, err)
	}
	// Someone else's app and read-only tokens.
	bob := g.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := g.s.SetGitSource(ctx, bob, app.Slug, GitSource{RepoURL: "https://github.com/alice/site"}); depCode(err) != "app_not_found" {
		t.Fatalf("other user: %v", err)
	}
	ro := g.user
	ro.Scope = ScopeRead
	if _, err := g.s.SetGitSource(ctx, ro, app.Slug, GitSource{RepoURL: "https://github.com/alice/site"}); depCode(err) != "token_read_only" {
		t.Fatalf("read token: %v", err)
	}
	// The hint for an unreadable repository follows who is asking.
	g.s.lsRemote = deploy.LsRemoteInfo // not used: only the hint text is checked
	if h := g.s.gitAccessHint(ctx, g.user); !strings.Contains(h, "Connect GitHub") || !strings.Contains(h, "/settings/github") {
		t.Fatalf("hint: %s", h)
	}
	if err := g.s.GitHubDisconnectApp(ctx, g.admin); err != nil {
		t.Fatal(err)
	}
	if h := g.s.gitAccessHint(ctx, g.user); !strings.Contains(h, "Ask your bitploy admin") || strings.Contains(h, "token") {
		t.Fatalf("hint without app, user: %s", h)
	}
	if h := g.s.gitAccessHint(ctx, g.admin); !strings.Contains(h, "/admin") {
		t.Fatalf("hint without app, admin: %s", h)
	}
}

func TestLinkedAppRefusesUploadsAndUnlinks(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{Slug: "site"})
	// A zip uploaded before the app was linked.
	up, err := e.s.CreateUpload(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://github.com/alice/site', git_branch = 'main', git_auto_deploy = 1 WHERE id = ?`, app.ID)

	check := func(what string, err error) {
		t.Helper()
		ae := apperr.As(err)
		if ae.Code != "git_source_linked" || ae.HTTPStatus != 409 {
			t.Fatalf("%s: %v", what, err)
		}
		for _, want := range []string{"alice/site", "main"} {
			if !strings.Contains(ae.Message, want) {
				t.Errorf("%s: message lacks %q: %s", what, want, ae.Message)
			}
		}
		for _, want := range []string{"Push your change to main", "get_deployment", "remove=true", "Disconnect repo"} {
			if !strings.Contains(ae.Hint, want) {
				t.Errorf("%s: hint lacks %q: %s", what, want, ae.Hint)
			}
		}
	}
	_, err = e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	check("deploy_files", err)
	_, err = e.s.CreateUpload(ctx, e.user, app.Slug)
	check("create_upload", err)
	_, err = e.s.Deploy(ctx, e.user, app.Slug, up.ID)
	check("deploy with upload id", err)
	// Building the repository itself is what is allowed.
	if d, err := e.s.Deploy(ctx, e.user, app.Slug, ""); err != nil || d.SourceKind != "git" {
		t.Fatalf("deploy from git: %+v %v", d, err)
	}
	if d, err := e.s.Redeploy(ctx, e.user, app.Slug); err != nil || d.SourceKind != "git" {
		t.Fatalf("redeploy of a linked app builds the repository: %+v %v", d, err)
	}
	// With auto-deploy off the hint says to call deploy.
	e.exec(t, `UPDATE apps SET git_auto_deploy = 0 WHERE id = ?`, app.ID)
	_, err = e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	if h := apperr.As(err).Hint; !strings.Contains(h, "call deploy") {
		t.Errorf("hint with auto-deploy off: %s", h)
	}
	// Auto-deploy switch.
	if got, err := e.s.SetGitAutoDeploy(ctx, e.user, app.Slug, true); err != nil || !got.GitSource.AutoDeploy {
		t.Fatalf("auto-deploy on: %+v %v", got.GitSource, err)
	}
	if got, err := e.s.SetGitAutoDeploy(ctx, e.user, app.Slug, false); err != nil || got.GitSource.AutoDeploy {
		t.Fatalf("auto-deploy off: %+v %v", got.GitSource, err)
	}
	// Other users and read-only tokens cannot unlink.
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.ClearGitSource(ctx, bob, app.Slug); depCode(err) != "app_not_found" {
		t.Fatalf("other user: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.ClearGitSource(ctx, ro, app.Slug); depCode(err) != "token_read_only" {
		t.Fatalf("read-only: %v", err)
	}
	// Unlink.
	got, err := e.s.ClearGitSource(ctx, e.user, app.Slug)
	if err != nil || got.GitSource != nil {
		t.Fatalf("unlink: %+v %v", got.GitSource, err)
	}
	var url, branch, subdir *string
	var last *int64
	e.s.Store.DB.QueryRow(`SELECT git_repo_url, git_branch, git_subdir, git_last_push_at FROM apps WHERE id = ?`, app.ID).Scan(&url, &branch, &subdir, &last)
	if url != nil || branch != nil || subdir != nil || last != nil {
		t.Fatal("the link is not fully removed")
	}
	if _, err := e.s.ClearGitSource(ctx, e.user, app.Slug); err != nil {
		t.Fatalf("unlinking twice must be harmless: %v", err)
	}
	if _, err := e.s.SetGitAutoDeploy(ctx, e.user, app.Slug, true); depCode(err) != "git_source_missing" {
		t.Fatalf("auto-deploy without a repository: %v", err)
	}
	// Uploads work again.
	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles()); err != nil {
		t.Fatalf("deploy_files after unlinking: %v", err)
	}
	if _, err := e.s.CreateUpload(ctx, e.user, app.Slug); err != nil {
		t.Fatalf("create_upload after unlinking: %v", err)
	}
	// And apps that never had a repository are not affected.
	other := e.mustApp(t, CreateAppInput{Slug: "plain"})
	if _, err := e.s.DeployFiles(ctx, e.user, other.Slug, staticFiles()); err != nil {
		t.Fatalf("plain app: %v", err)
	}
}

func TestGitAutoDeployOnlyForGitHub(t *testing.T) {
	e := depTestSvc(t)
	app := e.mustApp(t, CreateAppInput{Slug: "site"})
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://gitlab.com/o/r', git_branch = 'main', git_auto_deploy = 0 WHERE id = ?`, app.ID)
	if _, err := e.s.SetGitAutoDeploy(context.Background(), e.user, app.Slug, true); depCode(err) != "git_auto_deploy_unsupported" {
		t.Fatalf("%v", err)
	}
}

func TestMigrationDroppedTheToken(t *testing.T) {
	e := depTestSvc(t)
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name LIKE 'github%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("users still has GitHub token columns: %d %v", n, err)
	}
}

// ---- links to accounts the user does not own ----

// Carol is a collaborator on alice/site only. GitHub shows her alice's
// installation, which also covers alice/private. Linking it must give her
// alice/site and nothing else; the acme organization works the same way.
func TestGitHubLimitedLink(t *testing.T) {
	g := ghTestEnv(t)
	ctx := context.Background()
	carol := g.addUser(t, "usr_carol", "carol@example.com", "user")
	g.f.repos[101] = []string{"alice/site", "alice/private"}
	g.f.repos[202] = []string{"acme/web", "acme/payroll"}
	g.f.branches["alice/site"] = []string{"main"}
	g.f.branches["alice/private"] = []string{"main"}
	g.f.userRepos["ghu_usr_carol"] = map[int64][]string{101: {"alice/site"}, 202: {"acme/web"}}
	ov, err := g.connect(t, carol, 0,
		fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"},
		fakeInst{ID: 202, Login: "acme", Type: "Organization", Selection: "all"})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range ov.Installations {
		if !i.Limited || i.RepoCount == nil || *i.RepoCount != 1 {
			t.Fatalf("limited link: %+v", i)
		}
	}
	// Alice links her own account: not limited.
	ova, err := g.connect(t, g.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"})
	if err != nil || ova.Installations[0].Limited || *ova.Installations[0].RepoCount != 2 {
		t.Fatalf("owner link: %+v %v", ova, err)
	}

	sess := sessionOf(carol)
	repos, err := g.s.GitHubRepositories(ctx, sess, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
	}
	if len(names) != 2 || strings.Contains(strings.Join(names, ","), "private") || strings.Contains(strings.Join(names, ","), "payroll") {
		t.Fatalf("carol's picker: %v", names)
	}
	if _, err := g.s.GitHubBranches(ctx, sess, "alice/private"); depCode(err) != "github_repo_not_connected" {
		t.Fatalf("branches of a repository carol cannot open: %v", err)
	}
	if _, err := g.s.GitHubBranches(ctx, sess, "alice/site"); err != nil {
		t.Fatalf("branches of her repository: %v", err)
	}
	for _, repo := range []string{"https://github.com/alice/private", "https://github.com/acme/payroll"} {
		if tok := g.s.gitCloneToken(ctx, carol.UserID, repo); tok != "" {
			t.Fatalf("carol got a clone token for %s", repo)
		}
	}
	if tok := g.s.gitCloneToken(ctx, carol.UserID, "https://github.com/acme/web"); tok == "" {
		t.Fatal("carol has no token for acme/web")
	}

	// A push to a repository she may not use does not deploy her app, and its status says so.
	app, err := g.s.CreateApp(ctx, carol, CreateAppInput{Slug: "carol-site"})
	if err != nil {
		t.Fatal(err)
	}
	g.linkApp(t, app, "https://github.com/alice/private", "main", true)
	if _, err := g.deliver("push", "l1", pushBody("alice/private", "refs/heads/main", sha1a, 101)); err != nil {
		t.Fatal(err)
	}
	if n := len(g.deployments(t, app)); n != 0 {
		t.Fatalf("a push to a repository carol cannot open deployed her app")
	}
	if got, _ := g.s.GetApp(ctx, carol, "carol-site"); got.GitSource.PushStatus != "no_access" {
		t.Fatalf("status: %+v", got.GitSource)
	}
	g.linkApp(t, app, "https://github.com/alice/site", "main", true)
	if _, err := g.deliver("push", "l2", pushBody("alice/site", "refs/heads/main", sha1a, 101)); err != nil {
		t.Fatal(err)
	}
	if n := len(g.deployments(t, app)); n != 1 {
		t.Fatalf("her own repository: %v", g.deployments(t, app))
	}

	// Syncing again picks up new access; disconnecting forgets the list.
	g.f.userRepos["ghu_usr_carol"][101] = []string{"alice/site", "alice/private"}
	if _, err := g.connect(t, carol, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if tok := g.s.gitCloneToken(ctx, carol.UserID, "https://github.com/alice/private"); tok == "" {
		t.Fatal("access granted later was not picked up")
	}
	if err := g.s.GitHubUnlink(ctx, sess, 101); err != nil {
		t.Fatal(err)
	}
	if n, _ := g.s.Q.CountGitHubUserRepos(ctx, db.CountGitHubUserReposParams{UserID: carol.UserID, InstallationID: 101}); n != 0 {
		t.Fatalf("%d repositories kept after disconnect", n)
	}
}

// ---- an app created without a webhook ----

func TestGitHubAppWithoutWebhook(t *testing.T) {
	e := depTestSvc(t)
	f := newFakeGitHub(t)
	e.s.Cfg.GitHubAPIURL, e.s.Cfg.GitHubWebURL = f.srv.URL, f.srv.URL
	e.s.Cfg.PublicURL = "http://localhost:5173"
	ctx := context.Background()
	admin := sessionOf(e.addUser(t, "usr_admin", "admin@example.com", "admin"))
	m, err := e.s.GitHubStartManifest(ctx, admin, GitHubManifestInput{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Manifest, "hook_attributes") || strings.Contains(m.Manifest, "default_events") {
		t.Fatalf("GitHub refuses a webhook on localhost: %s", m.Manifest)
	}
	f.webhookSecret = "" // GitHub sends none for an app without a webhook
	info, err := e.s.GitHubFinishManifest(ctx, admin, GitHubManifestResult{Code: "goodcode", State: m.State})
	if err != nil || !info.Configured || info.PushEvents || info.WebhookURL != "" || info.PublicURLProblem == "" {
		t.Fatalf("info: %+v %v", info, err)
	}
	app, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: "site"})
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://github.com/alice/site', git_branch = 'main' WHERE id = ?`, app.ID)
	if got, _ := e.s.GetApp(ctx, e.user, "site"); got.GitSource.PushStatus != "not_connected" {
		t.Fatalf("access problems come first: %+v", got.GitSource)
	}
	g := &ghEnv{depEnv: e, f: f, admin: admin}
	f.mu.Lock()
	f.repos[101] = []string{"alice/site"}
	f.mu.Unlock()
	if _, err := g.connect(t, e.user, 101, fakeInst{ID: 101, Login: "alice", Type: "User", Selection: "selected"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, "site"); got.GitSource.PushStatus != "no_webhook" || got.GitSource.PushReady {
		t.Fatalf("status: %+v", got.GitSource)
	}
	body := []byte(`{"zen":"hi"}`)
	if _, err := e.s.HandleGitHubWebhook(ctx, "ping", "d-1", github.Sign(nil, body), body); depCode(err) != "github_signature_invalid" {
		t.Fatalf("a delivery without a secret must not verify: %v", err)
	}
}
