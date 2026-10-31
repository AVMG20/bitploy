package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// GitHubOverview is Settings > GitHub: whether the GitHub App exists and
// which GitHub accounts the user connected.
func (s *Service) GitHubOverview(ctx context.Context, a Actor) (GitHubOverview, error) {
	if err := RequireSession(a); err != nil {
		return GitHubOverview{}, err
	}
	info, err := s.githubAppInfo(ctx, a.IsAdmin())
	if err != nil {
		return GitHubOverview{}, err
	}
	rows, err := s.Q.ListGitHubInstallationsByUser(ctx, a.UserID)
	if err != nil {
		return GitHubOverview{}, apperr.Internal(err)
	}
	out := GitHubOverview{App: info, Installations: make([]GitHubInstallation, 0, len(rows))}
	for _, r := range rows {
		v := s.installationView(r)
		if r.Limited != 0 && r.Status == "active" {
			if n, err := s.Q.CountGitHubUserRepos(ctx, db.CountGitHubUserReposParams{UserID: r.UserID, InstallationID: r.InstallationID}); err == nil {
				c := int(n)
				v.RepoCount = &c
			}
		} else if r.RepositorySelection == "selected" && r.Status == "active" {
			// A count is shown only when the list was read at least once.
			if r.ReposSyncedAt.Valid {
				if n, err := s.countInstallationRepos(ctx, r.InstallationID); err == nil {
					v.RepoCount = &n
				}
			}
		}
		out.Installations = append(out.Installations, v)
	}
	return out, nil
}

func (s *Service) countInstallationRepos(ctx context.Context, id int64) (int, error) {
	n, err := s.Q.CountGitHubInstallationRepos(ctx, id)
	return int(n), err
}

// GitHubConnectURL returns the GitHub address that starts connecting an
// account. mode "install" (default) opens the page where the user installs the
// app on an account; "authorize" only asks GitHub who the user is, which links
// accounts where the app is already installed.
func (s *Service) GitHubConnectURL(ctx context.Context, a Actor, mode string) (GitHubRedirect, error) {
	if err := RequireSession(a); err != nil {
		return GitHubRedirect{}, err
	}
	app, err := s.githubApp(ctx)
	if err != nil {
		return GitHubRedirect{}, err
	}
	if app == nil {
		return GitHubRedirect{}, githubNotSetUp(a.IsAdmin())
	}
	state := s.signGitHubState("connect", a.UserID, githubStateTTL)
	if mode == "authorize" {
		q := url.Values{"client_id": {app.ClientID}, "state": {state}, "redirect_uri": {strings.TrimRight(s.Cfg.PublicURL, "/") + "/settings/github"}}
		return GitHubRedirect{URL: s.githubWebURL() + "/login/oauth/authorize?" + q.Encode()}, nil
	}
	return GitHubRedirect{URL: s.githubWebURL() + "/apps/" + app.Slug + "/installations/new?state=" + url.QueryEscape(state)}, nil
}

// GitHubLink finishes connecting. GitHub sent the browser back with an OAuth
// code and the state. The installation id in the URL is only a claim anyone
// can type, so it is never trusted: the code is exchanged for a token of the
// GitHub user who is signed in, and only installations GitHub itself lists for
// that user (GET /user/installations) are linked to the bitploy user.
func (s *Service) GitHubLink(ctx context.Context, a Actor, in GitHubLinkInput) (GitHubOverview, error) {
	if err := RequireSession(a); err != nil {
		return GitHubOverview{}, err
	}
	app, err := s.githubApp(ctx)
	if err != nil {
		return GitHubOverview{}, err
	}
	if app == nil {
		return GitHubOverview{}, githubNotSetUp(a.IsAdmin())
	}
	if err := s.verifyGitHubState(in.State, "connect", a.UserID); err != nil {
		return GitHubOverview{}, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" {
		return GitHubOverview{}, apperr.BadRequest("github_code_missing",
			"GitHub did not send the sign-in code.",
			"Press Connect GitHub again and finish both steps on GitHub.")
	}
	userToken, err := s.ghc().ExchangeOAuthCode(ctx, app.ClientID, app.ClientSecret, code)
	if err != nil {
		if github.IsUnreachable(err) {
			return GitHubOverview{}, githubError(err)
		}
		return GitHubOverview{}, apperr.BadRequest("github_code_invalid",
			"GitHub did not accept the sign-in. The code works once and only for a few minutes.",
			"Press Connect GitHub again.").WithCause(err)
	}
	login, err := s.ghc().AuthenticatedUser(ctx, userToken)
	if err != nil {
		return GitHubOverview{}, githubError(err)
	}
	visible, err := s.ghc().UserInstallations(ctx, userToken)
	if err != nil {
		return GitHubOverview{}, githubError(err)
	}
	var chosen []github.Installation
	for _, inst := range visible {
		if in.InstallationID == 0 || inst.ID == in.InstallationID {
			chosen = append(chosen, inst)
		}
	}
	if in.InstallationID != 0 && len(chosen) == 0 {
		return GitHubOverview{}, apperr.Forbidden("github_installation_forbidden",
			"The GitHub account you signed in with cannot see this installation, so bitploy did not connect it.",
			"Sign in to GitHub with the account that owns the repositories and press Connect GitHub again.")
	}
	if len(chosen) == 0 {
		return GitHubOverview{}, apperr.NotFound("github_not_installed",
			"The bitploy app is not installed on any of your GitHub accounts yet.",
			"Press Connect GitHub and choose an account and the repositories to share.")
	}
	existing, err := s.Q.ListGitHubInstallationsByUser(ctx, a.UserID)
	if err != nil {
		return GitHubOverview{}, apperr.Internal(err)
	}
	have := map[int64]bool{}
	for _, e := range existing {
		have[e.InstallationID] = true
	}
	for _, inst := range chosen {
		have[inst.ID] = true
	}
	if len(have) > githubMaxLinks {
		return GitHubOverview{}, apperr.Conflict("github_too_many_accounts",
			"You can connect at most 10 GitHub accounts.", "Disconnect one you no longer use.")
	}
	for _, inst := range chosen {
		// Only the person who owns a GitHub account gets everything its
		// installation covers. Anyone else (an organization member, a
		// collaborator on someone's repository) gets the repositories GitHub
		// says they can open themselves, read now.
		limited := !(inst.Account.Type == "User" && strings.EqualFold(inst.Account.Login, login))
		var mine []github.Repo
		if limited {
			mine, err = s.ghc().UserInstallationRepos(ctx, userToken, inst.ID)
			if err != nil && inst.SuspendedAt == nil {
				return GitHubOverview{}, githubError(err)
			}
		}
		if err := s.linkInstallation(ctx, a, app, inst, login, limited, mine); err != nil {
			return GitHubOverview{}, err
		}
	}
	return s.GitHubOverview(ctx, a)
}

func (s *Service) linkInstallation(ctx context.Context, a Actor, app *ghApp, inst github.Installation, login string, limited bool, mine []github.Repo) error {
	account := inst.Account.Login
	if !github.ValidName(account) || inst.ID <= 0 {
		return apperr.Unavailable("github_error", "GitHub's answer about the installation was not complete.", "Try again in a minute.")
	}
	accountType := "User"
	if inst.Account.Type == "Organization" {
		accountType = "Organization"
	}
	selection := "selected"
	if inst.RepositorySelection == "all" {
		selection = "all"
	}
	now := s.now()
	err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.UpsertGitHubInstallation(ctx, db.UpsertGitHubInstallationParams{
			UserID: a.UserID, InstallationID: inst.ID, AccountLogin: account, AccountType: accountType,
			RepositorySelection: selection, ReposSyncedAt: sql.NullInt64{}, GithubLogin: login, Limited: b2i(limited),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := q.ClearGitHubUserRepos(ctx, db.ClearGitHubUserReposParams{UserID: a.UserID, InstallationID: inst.ID}); err != nil {
			return err
		}
		for _, r := range mine {
			if key, ok := github.RepoKey(r.FullName); ok {
				if err := q.AddGitHubUserRepo(ctx, db.AddGitHubUserRepoParams{UserID: a.UserID, InstallationID: inst.ID, Repo: key}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return apperr.Internal(err)
	}
	status := "active"
	if inst.SuspendedAt != nil {
		status = "suspended"
		_, _ = s.Q.SetGitHubInstallationStatus(ctx, db.SetGitHubInstallationStatusParams{Status: status, UpdatedAt: now, InstallationID: inst.ID})
	}
	s.forgetInstallationCaches(inst.ID)
	if status == "active" {
		if err := s.syncInstallationRepos(ctx, app, inst.ID, selection); err != nil {
			// Linked anyway: the list is read again when the repository picker opens.
			s.Log.Warn("github: could not read the repositories of an installation", "installation", inst.ID, "err", err)
		}
	}
	s.Audit(ctx, a, "github.installation_link", "github_installation", itoa64(inst.ID), map[string]any{
		"account": account, "type": accountType, "selection": selection, "github_login": login, "limited": limited, "repos": len(mine),
	})
	return nil
}

// syncInstallationRepos stores which repositories an installation covers when
// it covers selected ones. With "all" the list is not needed.
func (s *Service) syncInstallationRepos(ctx context.Context, app *ghApp, id int64, selection string) error {
	if selection == "all" {
		return s.Q.ClearGitHubInstallationRepos(ctx, id)
	}
	tok, err := s.installationToken(ctx, app, id, "")
	if err != nil {
		return err
	}
	repos, err := s.ghc().InstallationRepos(ctx, tok)
	if err != nil {
		return err
	}
	return s.storeInstallationRepos(ctx, id, repos)
}

func (s *Service) storeInstallationRepos(ctx context.Context, id int64, repos []github.Repo) error {
	now := s.now()
	return s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.ClearGitHubInstallationRepos(ctx, id); err != nil {
			return err
		}
		for _, r := range repos {
			if key, ok := github.RepoKey(r.FullName); ok {
				if err := q.AddGitHubInstallationRepo(ctx, db.AddGitHubInstallationRepoParams{InstallationID: id, Repo: key}); err != nil {
					return err
				}
			}
		}
		return q.SetGitHubInstallationSynced(ctx, db.SetGitHubInstallationSyncedParams{ReposSyncedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now, InstallationID: id})
	})
}

// GitHubUnlink makes bitploy forget a connected account. It changes nothing on
// GitHub: the user uninstalls the app there.
func (s *Service) GitHubUnlink(ctx context.Context, a Actor, installationID int64) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	n, err := s.Q.DeleteGitHubInstallationLink(ctx, db.DeleteGitHubInstallationLinkParams{UserID: a.UserID, InstallationID: installationID})
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return apperr.NotFound("github_account_not_found", "That GitHub account is not connected.", "Open Settings > GitHub to see the connected accounts.")
	}
	_ = s.Q.ClearGitHubUserRepos(ctx, db.ClearGitHubUserReposParams{UserID: a.UserID, InstallationID: installationID})
	if left, err := s.Q.CountGitHubInstallationLinks(ctx, installationID); err == nil && left == 0 {
		_ = s.Q.ClearGitHubInstallationRepos(ctx, installationID)
	}
	s.forgetInstallationCaches(installationID)
	s.Audit(ctx, a, "github.installation_unlink", "github_installation", itoa64(installationID), nil)
	return nil
}

// ---- repositories and branches ----

// userInstallations are the user's linked installations that work right now.
func (s *Service) activeInstallations(ctx context.Context, userID string) ([]db.GithubInstallation, error) {
	rows, err := s.Q.ListGitHubInstallationsByUser(ctx, userID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	var out []db.GithubInstallation
	for _, r := range rows {
		if r.Status == "active" {
			out = append(out, r)
		}
	}
	return out, nil
}

// installationRepos lists an installation's repositories, cached for a minute.
// It also refreshes the stored list of an installation with selected repositories.
func (s *Service) installationRepos(ctx context.Context, app *ghApp, l db.GithubInstallation) ([]github.Repo, error) {
	s.github.mu.Lock()
	if c, ok := s.github.repos[l.InstallationID]; ok && s.Now().Sub(c.at) < githubRepoCacheTTL {
		s.github.mu.Unlock()
		return c.repos, nil
	}
	s.github.mu.Unlock()
	tok, err := s.installationToken(ctx, app, l.InstallationID, "")
	if err != nil {
		return nil, err
	}
	repos, err := s.ghc().InstallationRepos(ctx, tok)
	if err != nil {
		return nil, err
	}
	s.github.mu.Lock()
	if s.github.repos == nil {
		s.github.repos = map[int64]ghRepoList{}
	}
	s.github.repos[l.InstallationID] = ghRepoList{at: s.Now(), repos: repos}
	s.github.mu.Unlock()
	if l.RepositorySelection == "selected" {
		_ = s.storeInstallationRepos(ctx, l.InstallationID, repos)
	}
	return repos, nil
}

// userRepoSet is nil for a link that may use everything its installation
// covers, else the repositories (owner/name, lower case) its user may use.
func (s *Service) userRepoSet(ctx context.Context, l db.GithubInstallation) (map[string]bool, error) {
	if l.Limited == 0 {
		return nil, nil
	}
	rows, err := s.Q.ListGitHubUserRepos(ctx, db.ListGitHubUserReposParams{UserID: l.UserID, InstallationID: l.InstallationID})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[r] = true
	}
	return set, nil
}

// markInstallationGone records that GitHub no longer knows an installation.
func (s *Service) markInstallationGone(ctx context.Context, id int64) {
	_, _ = s.Q.SetGitHubInstallationStatus(ctx, db.SetGitHubInstallationStatusParams{Status: "deleted", UpdatedAt: s.now(), InstallationID: id})
	_ = s.Q.ClearGitHubInstallationRepos(ctx, id)
	_ = s.Q.ClearGitHubUserReposOfInstallation(ctx, id)
	s.forgetInstallationCaches(id)
}

const maxRepoPicks = 100

// GitHubRepositories lists the repositories of the user's connected accounts,
// most recently pushed first, filtered by a search text.
func (s *Service) GitHubRepositories(ctx context.Context, a Actor, query string) ([]GitHubRepo, error) {
	if err := RequireSession(a); err != nil {
		return nil, err
	}
	app, err := s.githubApp(ctx)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, githubNotSetUp(a.IsAdmin())
	}
	links, err := s.activeInstallations(ctx, a.UserID)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	out := []GitHubRepo{}
	var firstErr error
	failed := 0
	for _, l := range links {
		repos, err := s.installationRepos(ctx, app, l)
		if err != nil {
			if github.Status(err) == 404 {
				s.markInstallationGone(ctx, l.InstallationID)
			}
			failed++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		allowed, err := s.userRepoSet(ctx, l)
		if err != nil {
			return nil, err
		}
		for _, r := range repos {
			if query != "" && !strings.Contains(strings.ToLower(r.FullName), query) {
				continue
			}
			if key, _ := github.RepoKey(r.FullName); allowed != nil && !allowed[key] {
				continue
			}
			v := GitHubRepo{FullName: r.FullName, Private: r.Private, HTMLURL: r.HTMLURL, DefaultBranch: r.DefaultBranch, InstallationID: l.InstallationID}
			if !r.PushedAt.IsZero() {
				t := r.PushedAt.UTC()
				v.PushedAt = &t
			}
			out = append(out, v)
		}
	}
	if failed > 0 && failed == len(links) && len(out) == 0 {
		return nil, githubError(firstErr)
	}
	sort.SliceStable(out, func(i, j int) bool {
		switch {
		case out[i].PushedAt == nil:
			return false
		case out[j].PushedAt == nil:
			return true
		}
		return out[i].PushedAt.After(*out[j].PushedAt)
	})
	if len(out) > maxRepoPicks {
		out = out[:maxRepoPicks]
	}
	return out, nil
}

// GitHubBranches lists the branches of one repository ("owner/name") of the
// user's connected accounts.
func (s *Service) GitHubBranches(ctx context.Context, a Actor, repo string) (GitHubBranches, error) {
	if err := RequireSession(a); err != nil {
		return GitHubBranches{}, err
	}
	key, ok := github.RepoKey(repo)
	if !ok {
		return GitHubBranches{}, apperr.BadRequest("source_url_invalid", "That is not a GitHub repository name.", "Use the form owner/name.")
	}
	app, err := s.githubApp(ctx)
	if err != nil {
		return GitHubBranches{}, err
	}
	if app == nil {
		return GitHubBranches{}, githubNotSetUp(a.IsAdmin())
	}
	owner, name := github.SplitRepoKey(key)
	cands, err := s.installationCandidates(ctx, a.UserID, key)
	if err != nil {
		return GitHubBranches{}, err
	}
	var lastErr error
	for _, l := range cands {
		tok, err := s.installationToken(ctx, app, l.InstallationID, key)
		if err != nil {
			lastErr = err
			continue
		}
		info, err := s.ghc().RepoInfo(ctx, tok, owner, name)
		if err != nil {
			lastErr = err
			continue
		}
		names, err := s.ghc().Branches(ctx, tok, owner, name)
		if err != nil {
			lastErr = err
			continue
		}
		return GitHubBranches{DefaultBranch: info.DefaultBranch, Branches: names}, nil
	}
	if lastErr != nil && !errors.Is(lastErr, sql.ErrNoRows) && github.IsUnreachable(lastErr) {
		return GitHubBranches{}, githubError(lastErr)
	}
	return GitHubBranches{}, apperr.NotFound("github_repo_not_connected",
		"Bitploy cannot see "+repo+" through your connected GitHub accounts.",
		"Give bitploy access to this repository in Settings > GitHub, or type the branch name yourself.")
}

func itoa64(n int64) string { return acctItoa(n) }
