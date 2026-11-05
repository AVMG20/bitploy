package app

// GitHub App plumbing: stored credentials, signed state tokens, installation
// access tokens and the small caches in front of GitHub. The REST helpers are
// in internal/github; who may do what is decided in github_admin.go,
// github_link.go and github_push.go.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	githubStateAAD     = "github-state"
	githubStateTTL     = 15 * time.Minute // install and sign-in round trips
	githubManifestTTL  = 10 * time.Minute // creating the app on GitHub
	githubTokenMargin  = 5 * time.Minute  // refresh an installation token this long before it expires
	githubRepoCacheTTL = time.Minute
	githubMaxLinks     = 10 // GitHub accounts one user may link
)

// githubState holds caches that are only an optimisation: installation tokens
// (valid an hour) and repository lists (a minute).
type githubState struct {
	mu     sync.Mutex
	tokens map[string]ghToken
	repos  map[int64]ghRepoList
}

type ghToken struct {
	token string
	exp   time.Time
}

type ghRepoList struct {
	at    time.Time
	repos []github.Repo
}

// ghApp is the stored GitHub App with its secrets opened. Never return it to a client.
type ghApp struct {
	ID            int64
	Slug          string
	Name          string
	HTMLURL       string
	Owner         string
	OwnerType     string
	ClientID      string
	ClientSecret  string
	WebhookSecret []byte // empty when the app was created without a webhook
	PEM           string
	CreatedAt     time.Time
}

func (s *Service) ghc() *github.Client {
	return github.NewClient(s.Cfg.GitHubAPIURL, s.Cfg.GitHubWebURL)
}

func (s *Service) githubWebURL() string { return s.ghc().WebURL }

// githubApp returns the stored GitHub App, or nil when the admin has not set
// it up.
func (s *Service) githubApp(ctx context.Context) (*ghApp, error) {
	row, err := s.Q.GetGitHubApp(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	open := func(b []byte, field string) ([]byte, error) {
		p, err := s.Crypto.Open(b, "github_app:"+field)
		if err != nil {
			return nil, apperr.Internal(fmt.Errorf("open github app %s: %w", field, err))
		}
		return p, nil
	}
	secret, err := open(row.ClientSecretEnc, "client_secret")
	if err != nil {
		return nil, err
	}
	var hook []byte
	if len(row.WebhookSecretEnc) > 0 {
		if hook, err = open(row.WebhookSecretEnc, "webhook_secret"); err != nil {
			return nil, err
		}
	}
	pem, err := open(row.PrivateKeyEnc, "private_key")
	if err != nil {
		return nil, err
	}
	return &ghApp{
		ID: row.GithubAppID, Slug: row.Slug, Name: row.Name, HTMLURL: row.HtmlUrl, Owner: row.OwnerLogin, OwnerType: row.OwnerType,
		ClientID: row.ClientID, ClientSecret: string(secret), WebhookSecret: hook, PEM: string(pem),
		CreatedAt: ts(row.CreatedAt),
	}, nil
}

func githubNotSetUp(admin bool) *apperr.Error {
	if admin {
		return apperr.Conflict("github_app_missing",
			"GitHub is not set up on this bitploy yet.",
			"Create the GitHub App on the Admin page of the dashboard (section GitHub). It takes one minute.")
	}
	return apperr.Conflict("github_app_missing",
		"GitHub is not set up on this bitploy yet.",
		"Ask your bitploy admin to set it up on the Admin page. Until then only public repositories work, and you deploy them by hand.")
}

// ---- signed state ----

type githubStatePayload struct {
	Purpose string `json:"p"`
	UserID  string `json:"u"`
	Expires int64  `json:"e"`
	Nonce   string `json:"n"`
}

// signGitHubState returns an opaque token tied to one bitploy user and one
// purpose that stops working after ttl. It is sealed (encrypted and
// authenticated) with the server's secret key, so it cannot be forged or read.
func (s *Service) signGitHubState(purpose, userID string, ttl time.Duration) string {
	n := make([]byte, 12)
	_, _ = rand.Read(n)
	b, _ := json.Marshal(githubStatePayload{
		Purpose: purpose, UserID: userID, Expires: s.Now().Add(ttl).Unix(), Nonce: base64.RawURLEncoding.EncodeToString(n),
	})
	return base64.RawURLEncoding.EncodeToString(s.Crypto.Seal(b, githubStateAAD))
}

func (s *Service) verifyGitHubState(token, purpose, userID string) error {
	bad := apperr.BadRequest("github_state_invalid",
		"This link back from GitHub has expired, or it was started by another bitploy account.",
		"Start again. The link works for a few minutes, only for the account that started it.")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) == 0 || len(raw) > 1024 {
		return bad
	}
	plain, err := s.Crypto.Open(raw, githubStateAAD)
	if err != nil {
		return bad
	}
	var p githubStatePayload
	if json.Unmarshal(plain, &p) != nil || p.Purpose != purpose || p.UserID == "" || p.UserID != userID || s.Now().Unix() > p.Expires {
		return bad
	}
	return nil
}

// ---- installation access tokens ----

func (s *Service) githubJWT(app *ghApp) (string, error) {
	key, err := github.ParsePrivateKey(app.PEM)
	if err != nil {
		return "", apperr.Unavailable("github_app_broken",
			"The private key of the GitHub App cannot be read.",
			"An admin must disconnect GitHub on the Admin page and create the app again.").WithCause(err)
	}
	jwt, err := github.AppJWT(app.ID, key, s.Now())
	if err != nil {
		return "", apperr.Internal(err)
	}
	return jwt, nil
}

// installationToken returns a token for one installation. repo ("owner/name",
// lower case) narrows it to that repository with read access to contents,
// which is what a clone needs; "" gives the general token used for listing.
// Tokens are cached until a few minutes before they expire.
func (s *Service) installationToken(ctx context.Context, app *ghApp, installationID int64, repo string) (string, error) {
	key := fmt.Sprintf("%d/%s", installationID, repo)
	s.github.mu.Lock()
	if t, ok := s.github.tokens[key]; ok && s.Now().Add(githubTokenMargin).Before(t.exp) {
		s.github.mu.Unlock()
		return t.token, nil
	}
	s.github.mu.Unlock()

	jwt, err := s.githubJWT(app)
	if err != nil {
		return "", err
	}
	var repos []string
	if repo != "" {
		_, name := github.SplitRepoKey(repo)
		repos = []string{name}
	}
	tok, exp, err := s.ghc().InstallationToken(ctx, jwt, installationID, repos, repo != "")
	if err != nil {
		return "", err
	}
	s.github.mu.Lock()
	defer s.github.mu.Unlock()
	if s.github.tokens == nil || len(s.github.tokens) > 500 {
		s.github.tokens = map[string]ghToken{}
	}
	s.github.tokens[key] = ghToken{token: tok, exp: exp}
	return tok, nil
}

// forgetInstallationCaches drops cached tokens and repositories of an installation.
func (s *Service) forgetInstallationCaches(installationID int64) {
	prefix := fmt.Sprintf("%d/", installationID)
	s.github.mu.Lock()
	defer s.github.mu.Unlock()
	for k := range s.github.tokens {
		if strings.HasPrefix(k, prefix) {
			delete(s.github.tokens, k)
		}
	}
	delete(s.github.repos, installationID)
}

// githubError turns a failed GitHub call into a plain error for the user.
func githubError(err error) *apperr.Error {
	if err == nil {
		return nil
	}
	if github.IsUnreachable(err) {
		return apperr.Unavailable("github_unreachable", "bitploy could not reach GitHub.", "Try again in a minute.").WithCause(err)
	}
	switch github.Status(err) {
	case 401:
		return apperr.Unavailable("github_app_rejected",
			"GitHub does not accept the credentials of the bitploy GitHub App.",
			"An admin should disconnect GitHub on the Admin page and create the app again.").WithCause(err)
	case 403, 429:
		return apperr.Unavailable("github_busy", "GitHub is limiting requests from bitploy right now.", "Try again in a few minutes.").WithCause(err)
	case 404:
		return apperr.NotFound("github_not_found", "GitHub says this does not exist, or bitploy has no access to it.", "Check Settings > GitHub and give bitploy access to the repository.").WithCause(err)
	}
	return apperr.Unavailable("github_error", "GitHub answered with an error.", "Try again in a minute.").WithCause(err)
}

// ---- public address check ----

// publicURLProblem explains why GitHub cannot reach rawURL, or returns "".
func publicURLProblem(rawURL string) string {
	u, err := url.Parse(rawURL)
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	msg := func() string {
		return "GitHub cannot reach " + host + " over the internet, so pushes will not deploy apps by themselves. Deploying by hand works, also from private repositories. For deploy on push, set BITPLOY_PUBLIC_URL to the public address of this server and restart bitploy."
	}
	switch {
	case host == "":
		return "BITPLOY_PUBLIC_URL is not a valid address."
	case host == "localhost", strings.HasSuffix(host, ".localhost"), strings.HasSuffix(host, ".local"),
		strings.HasSuffix(host, ".internal"), strings.HasSuffix(host, ".test"), !strings.Contains(host, ".") && net.ParseIP(host) == nil:
		return msg()
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return msg()
		}
	}
	return ""
}

func (s *Service) githubWebhookURL() string {
	return strings.TrimRight(s.Cfg.PublicURL, "/") + "/api/v1/github/webhook"
}

// installationManageURL is the page on GitHub where the account owner chooses
// repositories for the installation or uninstalls it.
func (s *Service) installationManageURL(login, accountType string, id int64) string {
	web := s.githubWebURL()
	if accountType == "Organization" {
		return fmt.Sprintf("%s/organizations/%s/settings/installations/%d", web, login, id)
	}
	return fmt.Sprintf("%s/settings/installations/%d", web, id)
}

func (s *Service) installationView(l db.GithubInstallation) GitHubInstallation {
	v := GitHubInstallation{
		ID: l.InstallationID, AccountLogin: l.AccountLogin, AccountType: l.AccountType,
		RepositorySelection: l.RepositorySelection, Status: l.Status,
		ManageURL: s.installationManageURL(l.AccountLogin, l.AccountType, l.InstallationID), ConnectedAt: ts(l.CreatedAt),
		Limited: l.Limited != 0,
	}
	return v
}
