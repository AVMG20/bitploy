package deploy

// OWNER: MCP agent (git sources, M4). Signatures are a shared contract: the
// deploy pipeline calls Clone in the `fetching` state.
//
// Security rules for this file:
//   - HTTPS only; no credentials inside the URL.
//   - The GitHub installation token is used only in memory as basic auth
//     (x-access-token) and only for github.com. It is never written to disk, to .git/config,
//     to a log line or to an error message.
//   - Clone removes .git before returning, so the build context never holds
//     the repository's config.
//   - Outgoing connections to loopback, private, link-local and metadata
//     addresses are refused, so a repo URL cannot probe the server's network.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// GitSource describes one branch of a repository. Token is a short-lived
// GitHub App installation token, used only in memory as basic auth
// (x-access-token).
type GitSource struct {
	URL    string
	Branch string
	Subdir string
	Token  string
	// SettingsURL is where the user connects GitHub (PUBLIC_URL/settings/github).
	// Used in hints only; when empty the hints say "Settings > GitHub in the bitploy dashboard".
	SettingsURL string
	// AccessHint, when set, replaces the "if it is private" advice of the
	// error for a repository bitploy cannot read. The caller knows whether the
	// GitHub App is set up and who is asking; this package does not.
	AccessHint string
}

// CloneResult is what a shallow clone produced.
type CloneResult struct {
	CommitSHA     string
	CommitMessage string
	ContextDir    string // dest joined with Subdir
}

// DefaultCloneLimit is the clone size limit when the caller passes 0.
const DefaultCloneLimit = 500 << 20

const (
	lsRemoteTimeout = 30 * time.Second
	cloneTimeout    = 5 * time.Minute
	errBlockedAddr  = "bitploy refuses connections to private addresses"
)

// allowPrivateAddrs, baseTransport and tokenHosts exist so tests can clone from a local server.
var (
	allowPrivateAddrs atomic.Bool
	baseTransport     *http.Transport
	tokenHosts        = map[string]bool{"github.com": true, "www.github.com": true}
)

func init() {
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivateAddrs.Load() {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			ip = ip.Unmap()
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
				ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
				isCGNAT(ip) {
				return errors.New(errBlockedAddr)
			}
			return nil
		},
	}
	baseTransport = &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	hc := &http.Client{Transport: &countingTransport{base: baseTransport}}
	client.InstallProtocol("https", githttp.NewClient(hc))
}

func isCGNAT(ip netip.Addr) bool {
	return ip.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

// ---- download size accounting ----

type byteBudget struct {
	limit    int64
	read     atomic.Int64
	exceeded atomic.Bool
	cancel   context.CancelFunc
}

func (b *byteBudget) add(n int64) {
	if b.limit <= 0 {
		return
	}
	if b.read.Add(n) > b.limit && b.exceeded.CompareAndSwap(false, true) {
		b.cancel()
	}
}

type budgetKey struct{}

type countingTransport struct{ base http.RoundTripper }

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if b, ok := req.Context().Value(budgetKey{}).(*byteBudget); ok && resp.Body != nil {
		resp.Body = &countingBody{ReadCloser: resp.Body, b: b}
	}
	return resp, nil
}

type countingBody struct {
	io.ReadCloser
	b *byteBudget
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.b.add(int64(n))
	return n, err
}

// ---- input validation ----

var credsInURLRe = regexp.MustCompile(`(?i)(https?://)[^/\s@]+@`)

// scrub removes credentials and tokens from text that may reach users or logs.
func scrub(s string, src GitSource) string {
	s = apperr.Redact(s)
	s = credsInURLRe.ReplaceAllString(s, "$1")
	if len(src.Token) >= 8 {
		s = strings.ReplaceAll(s, src.Token, "[REDACTED]")
	}
	return s
}

// IsGitHubURL reports whether the URL points at github.com (the only host that receives the token).
func IsGitHubURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return tokenHosts[strings.ToLower(u.Hostname())]
}

// NormalizeGitURL validates a repository URL and returns it cleaned up.
// Only https:// URLs without credentials are accepted.
func NormalizeGitURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	invalid := func(msg, hint string) error {
		return apperr.BadRequest("source_url_invalid", msg, hint)
	}
	if raw == "" {
		return "", invalid("The repository URL is empty.", "Pass the HTTPS address of the repository, for example https://github.com/owner/repo.")
	}
	if strings.HasPrefix(raw, "git@") || strings.HasPrefix(raw, "ssh://") || strings.HasPrefix(raw, "git://") || strings.HasPrefix(raw, "http://") {
		return "", invalid("bitploy clones only over HTTPS, and this address does not start with https://.",
			"Use the HTTPS address, for example https://github.com/owner/repo (on GitHub: Code, then HTTPS).")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", invalid("This is not a valid HTTPS repository address.",
			"Use the form https://github.com/owner/repo.")
	}
	if u.User != nil {
		return "", invalid("The repository URL contains a user name or password.",
			"Remove the credentials from the URL. For a private GitHub repository, connect GitHub in Settings > GitHub in the bitploy dashboard instead.")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("The repository URL must not have a ? or # part.", "Use the plain address, for example https://github.com/owner/repo.")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	if strings.Trim(u.Path, "/") == "" {
		return "", invalid("The repository URL has no repository path.", "Use the form https://github.com/owner/repo.")
	}
	if IsGitHubURL(raw) {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 {
			return "", invalid("A GitHub repository address looks like https://github.com/owner/repo, and this one has a different shape.",
				"Pass only the owner and repository name. Choose the branch with the branch setting and a folder with subdir.")
		}
		if h := strings.ToLower(u.Hostname()); h == "www.github.com" {
			u.Host = "github.com"
		}
	}
	return u.String(), nil
}

// CleanSubdir validates a subdirectory inside the repository and returns it
// in clean slash form ("" for the repository root).
func CleanSubdir(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == "./" {
		return "", nil
	}
	bad := func() error {
		return apperr.BadRequest("source_subdir_invalid",
			fmt.Sprintf("The subdirectory %q is not allowed.", s),
			"Use a relative folder inside the repository, such as apps/web. No leading /, no .. and no backslashes.")
	}
	if strings.ContainsAny(s, "\\\x00") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") {
		return "", bad()
	}
	c := path.Clean(s)
	if c == ".." || strings.HasPrefix(c, "../") || c == ".git" || strings.HasPrefix(c, ".git/") {
		return "", bad()
	}
	if c == "." {
		return "", nil
	}
	return c, nil
}

func (s GitSource) auth() transport.AuthMethod {
	if s.Token != "" && IsGitHubURL(s.URL) {
		return &githttp.BasicAuth{Username: "x-access-token", Password: s.Token}
	}
	return nil
}

func (s GitSource) settingsText() string {
	if s.SettingsURL != "" {
		return s.SettingsURL
	}
	return "Settings > GitHub in the bitploy dashboard"
}

// prepare validates the source and returns a normalized copy.
func (s GitSource) prepare() (GitSource, error) {
	u, err := NormalizeGitURL(s.URL)
	if err != nil {
		return s, err
	}
	sub, err := CleanSubdir(s.Subdir)
	if err != nil {
		return s, err
	}
	s.URL, s.Subdir = u, sub
	s.Branch = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s.Branch), "refs/heads/"))
	if strings.ContainsAny(s.Branch, " \t\n\\~^:?*[") || strings.Contains(s.Branch, "..") || strings.HasPrefix(s.Branch, "-") {
		return s, apperr.BadRequest("source_branch_not_found",
			fmt.Sprintf("%q is not a valid branch name.", s.Branch),
			"Pass the branch name only, for example main.")
	}
	return s, nil
}

// ---- LsRemote ----

// LsRemote checks that the branch exists and is readable. Errors are
// *apperr.Error with codes like source_repo_not_found, github_access_missing,
// source_branch_not_found.
func LsRemote(ctx context.Context, src GitSource) (sha string, err error) {
	sha, _, err = LsRemoteInfo(ctx, src)
	return sha, err
}

// LsRemoteInfo is LsRemote that also returns the branch it resolved: the
// repository's default branch when src.Branch is empty.
func LsRemoteInfo(ctx context.Context, src GitSource) (sha, branch string, err error) {
	src, err = src.prepare()
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()
	rem := gogit.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{src.URL}})
	refs, err := rem.ListContext(ctx, &gogit.ListOptions{Auth: src.auth()})
	if err != nil {
		return "", "", mapGitError(err, src)
	}
	want := src.Branch
	if want == "" {
		for _, r := range refs {
			if r.Name() == plumbing.HEAD && r.Type() == plumbing.SymbolicReference {
				want = r.Target().Short()
			}
		}
		if want == "" {
			for _, cand := range []string{"main", "master"} {
				for _, r := range refs {
					if r.Name() == plumbing.NewBranchReferenceName(cand) {
						want = cand
					}
				}
				if want != "" {
					break
				}
			}
		}
	}
	for _, r := range refs {
		if want != "" && r.Name() == plumbing.NewBranchReferenceName(want) {
			return r.Hash().String(), want, nil
		}
	}
	return "", "", branchNotFound(src, refs, want)
}

func branchNotFound(src GitSource, refs []*plumbing.Reference, want string) error {
	var branches []string
	for _, r := range refs {
		if r.Name().IsBranch() {
			branches = append(branches, r.Name().Short())
		}
	}
	if len(branches) > 8 {
		branches = branches[:8]
	}
	if want == "" {
		return apperr.Unprocessable("source_branch_not_found",
			"The repository has no branches yet, so there is nothing to deploy.",
			"Push a first commit to the repository, then call set_git_source again.").WithNext("set_git_source")
	}
	hint := "Check the branch name and call set_git_source again."
	if len(branches) > 0 {
		hint = fmt.Sprintf("Branches that exist: %s. Pass one of them to set_git_source.", strings.Join(branches, ", "))
	}
	return apperr.Unprocessable("source_branch_not_found",
		fmt.Sprintf("The repository has no branch called %q.", want), hint).WithNext("set_git_source")
}

// ---- Clone ----

// Clone shallow-clones (depth 1) one branch into dest, aborting past maxBytes.
// The token never reaches .git/config, a log line or the build context: Clone
// removes dest/.git before returning.
func Clone(ctx context.Context, src GitSource, dest string, maxBytes int64) (res CloneResult, err error) {
	src, err = src.prepare()
	if err != nil {
		return CloneResult{}, err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultCloneLimit
	}
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	budget := &byteBudget{limit: maxBytes, cancel: stop}
	ctx = context.WithValue(ctx, budgetKey{}, budget)

	parent := filepath.Dir(filepath.Clean(dest))
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return CloneResult{}, apperr.Internal(err)
	}
	tmp, err := os.MkdirTemp(parent, ".bp-clone-")
	if err != nil {
		return CloneResult{}, apperr.Internal(err)
	}
	defer os.RemoveAll(tmp)

	// Watch the unpacked size while go-git writes; the download counter
	// alone cannot see how large the checkout becomes.
	watchDone := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-watchDone:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if n, _ := dirSize(tmp, maxBytes); n > maxBytes && budget.exceeded.CompareAndSwap(false, true) {
					stop()
					return
				}
			}
		}
	}()

	opts := &gogit.CloneOptions{
		URL:               src.URL,
		Auth:              src.auth(),
		SingleBranch:      true,
		Depth:             1,
		Tags:              gogit.NoTags,
		RecurseSubmodules: gogit.NoRecurseSubmodules,
	}
	if src.Branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(src.Branch)
	}
	repo, cloneErr := gogit.PlainCloneContext(ctx, tmp, false, opts)
	close(watchDone)
	if budget.exceeded.Load() {
		return CloneResult{}, tooLarge(maxBytes)
	}
	if cloneErr != nil {
		return CloneResult{}, mapGitError(cloneErr, src)
	}

	if head, herr := repo.Head(); herr == nil {
		res.CommitSHA = head.Hash().String()
		if c, cerr := repo.CommitObject(head.Hash()); cerr == nil {
			res.CommitMessage = firstLineOf(c)
		}
	}
	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return CloneResult{}, apperr.Internal(err)
	}
	if n, _ := dirSize(tmp, maxBytes); n > maxBytes {
		return CloneResult{}, tooLarge(maxBytes)
	}
	if err := dropEscapingSymlinks(tmp); err != nil {
		return CloneResult{}, apperr.Internal(err)
	}
	if src.Subdir != "" {
		if err := checkSubdir(tmp, src.Subdir); err != nil {
			return CloneResult{}, err
		}
	}
	if err := moveInto(tmp, dest); err != nil {
		return CloneResult{}, apperr.Internal(err)
	}
	res.ContextDir = dest
	if src.Subdir != "" {
		res.ContextDir = filepath.Join(dest, filepath.FromSlash(src.Subdir))
	}
	return res, nil
}

func firstLineOf(c *object.Commit) string {
	msg := strings.TrimSpace(c.Message)
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return apperr.Redact(msg)
}

func tooLarge(max int64) *apperr.Error {
	return apperr.Unprocessable("source_too_large",
		fmt.Sprintf("The repository is larger than the limit of %d MB.", max>>20),
		"Deploy a smaller folder with the subdir setting, remove big files (videos, datasets, build output) from the repository, or add them to .gitignore.").WithNext("set_git_source")
}

// dirSize adds up file sizes, stopping early once past limit.
func dirSize(root string, limit int64) (int64, error) {
	var total int64
	gitDir := filepath.Join(root, ".git")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// The pack in .git is limited by the download counter; counting it here
		// too would halve the limit for repositories that compress badly.
		if d.IsDir() && path == gitDir {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
			if limit > 0 && total > limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return total, err
}

// dropEscapingSymlinks removes every symlink whose fully resolved target is
// not inside the checkout (filepath.EvalSymlinks, so chains such as
// x -> ., y1 -> x/.., y2 -> y1/.. are followed on disk). Dangling and looping
// links are removed too. Removing one link can change what another resolves
// to, so it repeats until a pass removes nothing. Links that stay always
// resolve to a path inside the checkout.
func dropEscapingSymlinks(root string) error {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	for pass := 0; pass < 1000; pass++ {
		removed := 0
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.Type()&os.ModeSymlink == 0 {
				return nil
			}
			real, rerr := filepath.EvalSymlinks(p)
			if rerr == nil && (real == rootReal || strings.HasPrefix(real, rootReal+string(filepath.Separator))) {
				return nil
			}
			removed++
			return os.Remove(p)
		})
		if err != nil {
			return err
		}
		if removed == 0 {
			return nil
		}
	}
	return errors.New("symlink cleanup did not settle")
}

func checkSubdir(root, sub string) error {
	missing := apperr.Unprocessable("source_subdir_not_found",
		fmt.Sprintf("The repository has no folder called %q on that branch.", sub),
		"Check the folder name (it is relative to the repository root, such as apps/web) and call set_git_source again.").WithNext("set_git_source")
	full := filepath.Join(root, filepath.FromSlash(sub))
	rootReal, err1 := filepath.EvalSymlinks(root)
	real, err2 := filepath.EvalSymlinks(full)
	if err1 != nil || err2 != nil {
		return missing
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return missing
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return missing
	}
	return nil
}

// moveInto moves the contents of src into dest (created if needed).
func moveInto(src, dest string) error {
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return os.Rename(src, dest)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(src, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// ---- error mapping ----

func mapGitError(err error, src GitSource) *apperr.Error {
	github := IsGitHubURL(src.URL)
	hasToken := github && src.Token != ""
	text := scrub(err.Error(), src)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.Unavailable("source_git_failed",
			"Reaching the repository took too long and was stopped.",
			"Try again in a minute. For a very large repository use the subdir setting or deploy a zip instead.").WithNext("deploy")
	case errors.Is(err, context.Canceled):
		return apperr.Unavailable("source_git_failed", "The download was cancelled.", "Run deploy again.").WithNext("deploy")
	case strings.Contains(err.Error(), errBlockedAddr):
		return apperr.BadRequest("source_url_invalid",
			"That address points inside a private network, which bitploy cannot reach.",
			"Use the public HTTPS address of the repository, such as https://github.com/owner/repo.").WithNext("set_git_source")
	case errors.Is(err, transport.ErrRepositoryNotFound), errors.Is(err, transport.ErrAuthenticationRequired):
		return notReadable(src, github, hasToken)
	case errors.Is(err, transport.ErrAuthorizationFailed):
		if github {
			return notReadable(src, github, hasToken)
		}
		return apperr.NotFound("source_repo_not_found",
			"The server refused access to this repository.",
			"bitploy clones public repositories from any host, and private ones only from github.com. Check the address or make the repository public.").WithNext("set_git_source")
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return apperr.Unprocessable("source_branch_not_found",
			"The repository is empty, so there is nothing to deploy.",
			"Push a first commit, then call set_git_source again.").WithNext("set_git_source")
	case errors.Is(err, plumbing.ErrReferenceNotFound), errors.As(err, new(gogit.NoMatchingRefSpecError)),
		strings.Contains(err.Error(), "couldn't find remote ref"):
		name := src.Branch
		if name == "" {
			name = "(default)"
		}
		return apperr.Unprocessable("source_branch_not_found",
			fmt.Sprintf("The repository has no branch called %q.", name),
			"Check the branch name (main or master are common) and call set_git_source again.").WithNext("set_git_source")
	}
	return apperr.Unavailable("source_git_failed",
		"bitploy could not read the repository: "+text,
		"Check the address and that the host is up, then try again. If it fails again, deploy a zip of the folder instead.").WithNext("set_git_source")
}

// notReadable covers GitHub's "401 or 404" answer, which looks the same for a
// private repo, a typo in the URL and a connection that does not include the repo.
func notReadable(src GitSource, github, hasToken bool) *apperr.Error {
	if github {
		advice := src.AccessHint
		if advice == "" {
			advice = fmt.Sprintf("Open %s, press Connect GitHub and give bitploy access to this repository.", src.settingsText())
		}
		msg := "bitploy cannot read this GitHub repository. It is private, or the address is misspelled."
		if hasToken {
			msg = "bitploy's GitHub connection does not include this repository, or the address is misspelled."
		}
		return apperr.Unprocessable("github_access_missing", msg,
			"Check the address. If the repository is private: "+advice+" Then call set_git_source again.").WithNext("set_git_source")
	}
	return apperr.NotFound("source_repo_not_found",
		"The repository was not found, or it needs a login.",
		"Check the address. bitploy clones public repositories from any host, and private ones only from github.com. Make the repository public or move it to GitHub.").WithNext("set_git_source")
}
