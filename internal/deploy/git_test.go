package deploy

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/AVMG20/bitploy/internal/apperr"
)

func TestNormalizeGitURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		{"https://github.com/owner/repo", "https://github.com/owner/repo", ""},
		{"  https://github.com/owner/repo/  ", "https://github.com/owner/repo", ""},
		{"https://github.com/owner/repo.git", "https://github.com/owner/repo.git", ""},
		{"https://www.github.com/owner/repo", "https://github.com/owner/repo", ""},
		{"https://gitlab.com/group/sub/repo.git", "https://gitlab.com/group/sub/repo.git", ""},
		{"", "", "source_url_invalid"},
		{"git@github.com:owner/repo.git", "", "source_url_invalid"},
		{"ssh://git@github.com/owner/repo", "", "source_url_invalid"},
		{"http://github.com/owner/repo", "", "source_url_invalid"},
		{"git://github.com/owner/repo", "", "source_url_invalid"},
		{"file:///etc/passwd", "", "source_url_invalid"},
		{"/etc/passwd", "", "source_url_invalid"},
		{"https://user:secret@github.com/owner/repo", "", "source_url_invalid"},
		{"https://x-access-token@github.com/owner/repo", "", "source_url_invalid"},
		{"https://github.com/owner/repo?x=1", "", "source_url_invalid"},
		{"https://github.com/owner", "", "source_url_invalid"},
		{"https://github.com/owner/repo/tree/main", "", "source_url_invalid"},
		{"https://github.com/", "", "source_url_invalid"},
		{"https://", "", "source_url_invalid"},
	}
	for _, tc := range tests {
		got, err := NormalizeGitURL(tc.in)
		if tc.wantErr == "" {
			if err != nil || got != tc.want {
				t.Errorf("NormalizeGitURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
			continue
		}
		ae := apperr.As(err)
		if err == nil || ae.Code != tc.wantErr {
			t.Errorf("NormalizeGitURL(%q) = %q, %v; want error %s", tc.in, got, err, tc.wantErr)
		} else if strings.Contains(ae.Message+ae.Hint, "secret") {
			t.Errorf("error leaks the credential: %+v", ae)
		}
	}
}

func TestCleanSubdir(t *testing.T) {
	tests := []struct {
		in   string
		want string
		bad  bool
	}{
		{"", "", false},
		{".", "", false},
		{"./", "", false},
		{"apps/web", "apps/web", false},
		{"./apps//web/", "apps/web", false},
		{"a/../b", "b", false},
		{"..", "", true},
		{"../x", "", true},
		{"a/../../x", "", true},
		{"/etc", "", true},
		{"~/x", "", true},
		{"a\\b", "", true},
		{".git", "", true},
		{".git/hooks", "", true},
		{"a\x00b", "", true},
	}
	for _, tc := range tests {
		got, err := CleanSubdir(tc.in)
		if (err != nil) != tc.bad || got != tc.want {
			t.Errorf("CleanSubdir(%q) = %q, %v; want %q bad=%v", tc.in, got, err, tc.want, tc.bad)
		}
		if err != nil && apperr.As(err).Code != "source_subdir_invalid" {
			t.Errorf("CleanSubdir(%q) code = %s", tc.in, apperr.As(err).Code)
		}
	}
}

func TestMapGitErrorCodes(t *testing.T) {
	gh := GitSource{URL: "https://github.com/o/r", SettingsURL: "https://bitploy.nl/settings"}
	ghTok := GitSource{URL: "https://github.com/o/r", Token: "github_pat_ABCDEFGHIJKLMNOPQRSTUV", SettingsURL: "https://bitploy.nl/settings"}
	other := GitSource{URL: "https://gitlab.com/o/r"}
	e := mapGitError(transportErr("repository not found"), gh)
	if e.Code != "github_token_missing" || !strings.Contains(e.Hint, "https://bitploy.nl/settings") {
		t.Errorf("github no token: %+v", e)
	}
	if e := mapGitError(transportErr("authentication required"), ghTok); e.Code != "github_repo_forbidden" || !strings.Contains(e.Hint, "https://bitploy.nl/settings") {
		t.Errorf("github with token: %+v", e)
	}
	if e := mapGitError(transportErr("authorization failed"), ghTok); e.Code != "github_repo_forbidden" {
		t.Errorf("authz failed: %+v", e)
	}
	if e := mapGitError(transportErr("repository not found"), other); e.Code != "source_repo_not_found" {
		t.Errorf("other host: %+v", e)
	}
	if e := mapGitError(errString(`couldn't find remote ref "refs/heads/nope"`), gh); e.Code != "source_branch_not_found" {
		t.Errorf("branch: %+v", e)
	}
	e = mapGitError(errString("Get \"https://x-access-token:github_pat_ABCDEFGHIJKLMNOPQRSTUV@github.com/o/r\": boom bp_abcdefghijklmnopqrstuvwxyz"), ghTok)
	if e.Code != "source_git_failed" || strings.Contains(e.Message, "github_pat_") || strings.Contains(e.Message, "x-access-token") || strings.Contains(e.Message, "bp_abcdefghij") {
		t.Errorf("generic error must be scrubbed: %q", e.Message)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func transportErr(s string) error {
	switch s {
	case "repository not found":
		return errTransportNotFound
	case "authentication required":
		return errTransportAuthRequired
	}
	return errTransportAuthzFailed
}

func TestScrubRemovesCredentials(t *testing.T) {
	src := GitSource{Token: "supersecrettokenvalue"}
	got := scrub("fetch https://user:pw@example.com/x failed with supersecrettokenvalue", src)
	if strings.Contains(got, "pw@") || strings.Contains(got, "supersecret") {
		t.Errorf("scrub = %q", got)
	}
}

func TestMoveIntoAndSymlinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644)
	os.Symlink("/etc/passwd", filepath.Join(src, "abs"))
	os.Symlink("../../outside", filepath.Join(src, "sub", "up"))
	os.Symlink("a.txt", filepath.Join(src, "ok"))
	if err := dropEscapingSymlinks(src); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"abs", "sub/up"} {
		if _, err := os.Lstat(filepath.Join(src, gone)); err == nil {
			t.Errorf("%s should be removed", gone)
		}
	}
	if _, err := os.Lstat(filepath.Join(src, "ok")); err != nil {
		t.Errorf("inner symlink should stay: %v", err)
	}
	dest := filepath.Join(root, "dest")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "existing"), []byte("x"), 0o644)
	if err := moveInto(src, dest); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"existing", "a.txt", "sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// ---- end to end against a local git server (git http-backend over TLS) ----

type gitServer struct {
	URL      string
	Host     string
	mu       sync.Mutex
	auths    []string
	required string // when set, requests must carry this basic auth password
}

func (g *gitServer) seenAuth() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.auths...)
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newGitServer serves bare repository owner/repo.git whose contents are files.
func newGitServer(t *testing.T, files map[string]string, symlinks map[string]string) *gitServer {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skip("git exec-path unavailable")
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend not available")
	}
	base := t.TempDir()
	work := filepath.Join(base, "work")
	os.MkdirAll(work, 0o755)
	run(t, work, "init", "-q", "-b", "main")
	for name, content := range files {
		p := filepath.Join(work, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	for name, target := range symlinks {
		os.Symlink(target, filepath.Join(work, name))
	}
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "first commit\n\nbody")
	run(t, work, "branch", "dev")
	root := filepath.Join(base, "root")
	os.MkdirAll(filepath.Join(root, "owner"), 0o755)
	run(t, base, "clone", "-q", "--bare", work, filepath.Join(root, "owner", "repo.git"))

	gs := &gitServer{}
	h := &cgi.Handler{
		Path: backend,
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pw, ok := r.BasicAuth()
		if ok {
			gs.mu.Lock()
			gs.auths = append(gs.auths, user+":"+pw)
			gs.mu.Unlock()
		}
		if gs.required != "" && (!ok || pw != gs.required || user != "x-access-token") {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	allowPrivateAddrs.Store(true)
	baseTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	host := strings.TrimPrefix(srv.URL, "https://")
	hostname := strings.Split(host, ":")[0]
	tokenHosts[hostname] = true
	t.Cleanup(func() {
		allowPrivateAddrs.Store(false)
		baseTransport.TLSClientConfig = nil
		delete(tokenHosts, hostname)
		baseTransport.CloseIdleConnections()
	})
	gs.URL = srv.URL + "/owner/repo"
	gs.Host = host
	return gs
}

func TestCloneEndToEnd(t *testing.T) {
	gs := newGitServer(t, map[string]string{
		"index.html":          "<h1>hi</h1>",
		"apps/web/Dockerfile": "FROM alpine:3.22\n",
		"apps/web/app.txt":    "web",
	}, map[string]string{"evil": "/etc/passwd"})
	gs.required = "ghp_secretsecretsecretsecretsecret123456"
	ctx := context.Background()

	t.Run("token missing", func(t *testing.T) {
		_, err := LsRemote(ctx, GitSource{URL: gs.URL, Branch: "main", SettingsURL: "https://bitploy.test/settings"})
		ae := apperr.As(err)
		if ae.Code != "github_token_missing" || !strings.Contains(ae.Hint, "https://bitploy.test/settings") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		_, err := LsRemote(ctx, GitSource{URL: gs.URL, Branch: "main", Token: "ghp_wrongwrongwrongwrongwrongwrong1234"})
		if ae := apperr.As(err); ae.Code != "github_repo_forbidden" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ls-remote ok", func(t *testing.T) {
		sha, err := LsRemote(ctx, GitSource{URL: gs.URL, Branch: "main", Token: gs.required})
		if err != nil || len(sha) != 40 {
			t.Fatalf("sha=%q err=%v", sha, err)
		}
	})
	t.Run("default branch", func(t *testing.T) {
		sha, branch, err := LsRemoteInfo(ctx, GitSource{URL: gs.URL, Token: gs.required})
		if err != nil || branch != "main" || len(sha) != 40 {
			t.Fatalf("sha=%q branch=%q err=%v", sha, branch, err)
		}
	})
	t.Run("branch not found lists branches", func(t *testing.T) {
		_, err := LsRemote(ctx, GitSource{URL: gs.URL, Branch: "nope", Token: gs.required})
		ae := apperr.As(err)
		if ae.Code != "source_branch_not_found" || !strings.Contains(ae.Hint, "main") {
			t.Fatalf("got %+v", ae)
		}
	})
	t.Run("clone", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "builds", "dep_1")
		res, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "main", Token: gs.required}, dest, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.CommitSHA) != 40 || res.CommitMessage != "first commit" || res.ContextDir != dest {
			t.Errorf("result = %+v", res)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "index.html")); string(b) != "<h1>hi</h1>" {
			t.Errorf("index.html = %q", b)
		}
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			t.Error(".git must be removed")
		}
		if _, err := os.Lstat(filepath.Join(dest, "evil")); err == nil {
			t.Error("escaping symlink must be removed")
		}
		// The token must not be anywhere on disk below dest or its parent.
		filepath.WalkDir(filepath.Dir(dest), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if b, _ := os.ReadFile(p); strings.Contains(string(b), gs.required) {
					t.Errorf("token found in %s", p)
				}
			}
			return nil
		})
		leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".bp-clone-*"))
		if len(leftovers) > 0 {
			t.Errorf("temp dirs left: %v", leftovers)
		}
	})
	t.Run("clone subdir", func(t *testing.T) {
		dest := t.TempDir() // exists and is empty
		res, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "dev", Subdir: "./apps/web/", Token: gs.required}, dest, 0)
		if err != nil {
			t.Fatal(err)
		}
		if res.ContextDir != filepath.Join(dest, "apps", "web") {
			t.Errorf("ContextDir = %s", res.ContextDir)
		}
		if _, err := os.Stat(filepath.Join(res.ContextDir, "Dockerfile")); err != nil {
			t.Error(err)
		}
	})
	t.Run("subdir missing", func(t *testing.T) {
		_, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "main", Subdir: "nope", Token: gs.required}, filepath.Join(t.TempDir(), "d"), 0)
		if ae := apperr.As(err); ae.Code != "source_subdir_not_found" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("subdir traversal", func(t *testing.T) {
		_, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "main", Subdir: "../x", Token: gs.required}, filepath.Join(t.TempDir(), "d"), 0)
		if ae := apperr.As(err); ae.Code != "source_subdir_invalid" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("too large", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "d")
		_, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "main", Token: gs.required}, dest, 10)
		if ae := apperr.As(err); ae.Code != "source_too_large" {
			t.Fatalf("got %v", err)
		}
		if _, err := os.Stat(dest); err == nil {
			t.Error("dest must not exist after a failed clone")
		}
	})
	t.Run("branch missing on clone", func(t *testing.T) {
		_, err := Clone(ctx, GitSource{URL: gs.URL, Branch: "nope", Token: gs.required}, filepath.Join(t.TempDir(), "d"), 0)
		if ae := apperr.As(err); ae.Code != "source_branch_not_found" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("token only sent as x-access-token basic auth", func(t *testing.T) {
		for _, a := range gs.seenAuth() {
			if a != "x-access-token:"+gs.required && !strings.HasSuffix(a, "wrongwrongwrongwrongwrongwrong1234") {
				t.Errorf("unexpected credentials seen by server: %q", a)
			}
		}
	})
}

func TestCloneRefusesPrivateAddresses(t *testing.T) {
	allowPrivateAddrs.Store(false)
	_, err := LsRemote(context.Background(), GitSource{URL: "https://127.0.0.1:1/owner/repo", Branch: "main"})
	if ae := apperr.As(err); ae.Code != "source_url_invalid" {
		t.Fatalf("got %v", err)
	}
	_, err = LsRemote(context.Background(), GitSource{URL: "https://169.254.169.254/latest/meta-data", Branch: "main"})
	if ae := apperr.As(err); ae.Code != "source_url_invalid" {
		t.Fatalf("got %v", err)
	}
}

var (
	errTransportNotFound     = transport.ErrRepositoryNotFound
	errTransportAuthRequired = transport.ErrAuthenticationRequired
	errTransportAuthzFailed  = transport.ErrAuthorizationFailed
)

func TestDirSizeIgnoresTheGitPack(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "objects", "pack"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", ".git", "keep"), make([]byte, 30), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, _ := dirSize(root, 0); n != 130 {
		t.Errorf("dirSize = %d, want 130 (worktree only, nested .git counts)", n)
	}
}
