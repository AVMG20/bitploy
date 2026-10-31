package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const githubAppNameMax = 34 // GitHub's limit for an app name

// GitHubAppDetails is the GitHub App card of the Admin page.
func (s *Service) GitHubAppDetails(ctx context.Context, a Actor) (GitHubAppInfo, error) {
	if err := RequireAdmin(a); err != nil {
		return GitHubAppInfo{}, err
	}
	return s.githubAppInfo(ctx, true)
}

// githubAppInfo describes the GitHub App; admin adds the details only admins see.
func (s *Service) githubAppInfo(ctx context.Context, admin bool) (GitHubAppInfo, error) {
	app, err := s.githubApp(ctx)
	if err != nil {
		return GitHubAppInfo{}, err
	}
	info := GitHubAppInfo{}
	if admin {
		info.WebhookURL = s.githubWebhookURL()
		info.PublicURLProblem = publicURLProblem(s.Cfg.PublicURL)
	}
	if app == nil {
		return info, nil
	}
	info.Configured, info.Name, info.Slug = true, app.Name, app.Slug
	if admin {
		created := app.CreatedAt
		info.HTMLURL, info.Owner, info.CreatedAt = app.HTMLURL, app.Owner, &created
		info.ManageURL = s.githubWebURL() + "/settings/apps/" + app.Slug
		if app.OwnerType == "Organization" && app.Owner != "" {
			info.ManageURL = s.githubWebURL() + "/organizations/" + app.Owner + "/settings/apps/" + app.Slug
		}
	}
	return info, nil
}

// GitHubStartManifest prepares GitHub's "create an app from a manifest" flow.
// The dashboard posts the returned manifest to PostURL; GitHub asks the admin
// to confirm and then redirects back to the Admin page with a code.
func (s *Service) GitHubStartManifest(ctx context.Context, a Actor, in GitHubManifestInput) (GitHubManifest, error) {
	if err := RequireAdmin(a); err != nil {
		return GitHubManifest{}, err
	}
	if app, err := s.githubApp(ctx); err != nil {
		return GitHubManifest{}, err
	} else if app != nil {
		return GitHubManifest{}, apperr.Conflict("github_app_exists",
			"GitHub is already set up ("+app.Name+").",
			"Disconnect it first if you want to create a new app.")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = s.defaultGitHubAppName()
	}
	if utf8.RuneCountInString(name) > githubAppNameMax || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return GitHubManifest{}, apperr.BadRequest("github_app_name_invalid",
			"The app name must be 1 to 34 characters.", "Choose a shorter name. It has to be unique on GitHub.")
	}
	org := strings.TrimSpace(in.Organization)
	if org != "" && !github.ValidName(org) {
		return GitHubManifest{}, apperr.BadRequest("github_org_invalid",
			"That is not a GitHub organization name.", "Use the name as it appears in github.com/<name>, or leave the field empty.")
	}
	public := strings.TrimRight(s.Cfg.PublicURL, "/")
	manifest := map[string]any{
		"name":                     name,
		"url":                      public,
		"hook_attributes":          map[string]any{"url": s.githubWebhookURL(), "active": true},
		"redirect_url":             public + "/admin",
		"callback_urls":            []string{public + "/settings/github"},
		"setup_url":                public + "/settings/github",
		"request_oauth_on_install": true,
		"public":                   true,
		"default_permissions":      map[string]string{"contents": "read", "metadata": "read"},
		"default_events":           []string{"push"},
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		return GitHubManifest{}, apperr.Internal(err)
	}
	state := s.signGitHubState("manifest", a.UserID, githubManifestTTL)
	post := s.githubWebURL() + "/settings/apps/new"
	if org != "" {
		post = s.githubWebURL() + "/organizations/" + org + "/settings/apps/new"
	}
	post += "?state=" + url.QueryEscape(state)
	return GitHubManifest{PostURL: post, Manifest: string(mb), State: state, Name: name}, nil
}

func (s *Service) defaultGitHubAppName() string {
	host := "bitploy"
	if u, err := url.Parse(s.Cfg.PublicURL); err == nil && u.Hostname() != "" {
		host = "bitploy " + u.Hostname()
	}
	r := []rune(host)
	if len(r) > githubAppNameMax {
		r = r[:githubAppNameMax]
	}
	return strings.TrimRight(string(r), " .-")
}

// GitHubFinishManifest exchanges the code GitHub sent back for the app's
// credentials and stores them encrypted. The state proves this admin started
// the flow in the last few minutes.
func (s *Service) GitHubFinishManifest(ctx context.Context, a Actor, in GitHubManifestResult) (GitHubAppInfo, error) {
	if err := RequireAdmin(a); err != nil {
		return GitHubAppInfo{}, err
	}
	if err := s.verifyGitHubState(in.State, "manifest", a.UserID); err != nil {
		return GitHubAppInfo{}, err
	}
	if app, err := s.githubApp(ctx); err != nil {
		return GitHubAppInfo{}, err
	} else if app != nil {
		return GitHubAppInfo{}, apperr.Conflict("github_app_exists", "GitHub is already set up ("+app.Name+").", "Disconnect it first if you want to create a new app.")
	}
	cred, err := s.ghc().ConvertManifest(ctx, strings.TrimSpace(in.Code))
	if err != nil {
		if github.Status(err) == 404 || github.Status(err) == 422 {
			return GitHubAppInfo{}, apperr.BadRequest("github_code_invalid",
				"GitHub did not accept this code. It works once and for one hour.", "Press Create GitHub App again.")
		}
		return GitHubAppInfo{}, githubError(err)
	}
	webPrefix := s.githubWebURL() + "/"
	if cred.ID <= 0 || !github.ValidSlug(cred.Slug) || cred.ClientID == "" || cred.ClientSecret == "" ||
		cred.WebhookSecret == "" || cred.PEM == "" || !strings.HasPrefix(cred.HTMLURL, webPrefix) {
		return GitHubAppInfo{}, apperr.Unavailable("github_error", "GitHub's answer for the new app was incomplete.", "Press Create GitHub App again.")
	}
	if _, err := github.ParsePrivateKey(cred.PEM); err != nil {
		return GitHubAppInfo{}, apperr.Unavailable("github_error", "GitHub sent a private key bitploy cannot read.", "Press Create GitHub App again.")
	}
	owner, ownerType := cred.Owner.Login, "User"
	if owner != "" && !github.ValidName(owner) {
		owner = ""
	}
	if cred.Owner.Type == "Organization" {
		ownerType = "Organization"
	}
	seal := func(v, field string) []byte { return s.Crypto.Seal([]byte(v), "github_app:"+field) }
	err = s.Q.UpsertGitHubApp(ctx, db.UpsertGitHubAppParams{
		GithubAppID: cred.ID, Slug: cred.Slug, Name: cred.Name, HtmlUrl: cred.HTMLURL, OwnerLogin: owner, OwnerType: ownerType,
		ClientID:         cred.ClientID,
		ClientSecretEnc:  seal(cred.ClientSecret, "client_secret"),
		WebhookSecretEnc: seal(cred.WebhookSecret, "webhook_secret"),
		PrivateKeyEnc:    seal(cred.PEM, "private_key"),
		CreatedBy:        nullStr(a.UserID), CreatedAt: s.now(),
	})
	if err != nil {
		return GitHubAppInfo{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "github_app.create", "github_app", cred.Slug, map[string]any{"app_id": cred.ID, "owner": owner})
	return s.githubAppInfo(ctx, true)
}

// GitHubDisconnectApp forgets the app's credentials and every linked
// installation. It does not delete the app on GitHub; the admin does that
// there.
func (s *Service) GitHubDisconnectApp(ctx context.Context, a Actor) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	app, err := s.githubApp(ctx)
	if err != nil {
		return err
	}
	if app == nil {
		return nil
	}
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAllGitHubInstallationRepos(ctx); err != nil {
			return err
		}
		if err := q.DeleteAllGitHubInstallations(ctx); err != nil {
			return err
		}
		return q.DeleteGitHubApp(ctx)
	})
	if err != nil {
		return apperr.Internal(err)
	}
	s.github.mu.Lock()
	s.github.tokens, s.github.repos = nil, nil
	s.github.mu.Unlock()
	s.Audit(ctx, a, "github_app.disconnect", "github_app", app.Slug, nil)
	return nil
}
