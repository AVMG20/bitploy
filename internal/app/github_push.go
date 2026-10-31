package app

// Push = deploy: the webhook, the rules that decide which apps a push may
// start, and the clone credential for GitHub repositories.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// installationCandidates are the user's active installations whose account
// owns the repository ("owner/name", lower case), those that certainly cover
// the repository first.
func (s *Service) installationCandidates(ctx context.Context, userID, key string) ([]db.GithubInstallation, error) {
	active, err := s.activeInstallations(ctx, userID)
	if err != nil {
		return nil, err
	}
	owner, _ := github.SplitRepoKey(key)
	var covering, others []db.GithubInstallation
	for _, l := range active {
		if !strings.EqualFold(l.AccountLogin, owner) {
			continue
		}
		if s.installationCovers(ctx, l, key) {
			covering = append(covering, l)
		} else {
			others = append(others, l)
		}
	}
	return append(covering, others...), nil
}

func (s *Service) installationCovers(ctx context.Context, l db.GithubInstallation, key string) bool {
	if l.RepositorySelection == "all" {
		return true
	}
	n, err := s.Q.GitHubInstallationHasRepo(ctx, db.GitHubInstallationHasRepoParams{InstallationID: l.InstallationID, Repo: key})
	return err == nil && n > 0
}

// gitCloneToken is the credential for cloning a github.com repository for
// the app owner: a short-lived token of one of the owner's own installations
// that covers the repository, narrowed to that repository with read access
// to contents. It is "" for anonymous access (public repositories), for other
// hosts and when the owner has nothing that fits. It never returns another
// user's installation.
func (s *Service) gitCloneToken(ctx context.Context, userID, repoURL string) string {
	key, ok := github.RepoKey(repoURL)
	if !ok {
		return ""
	}
	app, err := s.githubApp(ctx)
	if err != nil || app == nil {
		return ""
	}
	cands, err := s.installationCandidates(ctx, userID, key)
	if err != nil {
		return ""
	}
	for _, l := range cands {
		tok, err := s.installationToken(ctx, app, l.InstallationID, key)
		if err == nil {
			return tok
		}
		switch github.Status(err) {
		case 404:
			s.markInstallationGone(ctx, l.InstallationID)
		case 403:
			_, _ = s.Q.SetGitHubInstallationStatus(ctx, db.SetGitHubInstallationStatusParams{Status: "suspended", UpdatedAt: s.now(), InstallationID: l.InstallationID})
		case 422: // the installation does not include this repository
		default:
			s.Log.Warn("github: could not get an installation token", "installation", l.InstallationID, "err", err)
		}
	}
	return ""
}

// gitAccessHint is the "if it is private" part of the error for a repository
// bitploy cannot read, for this user.
func (s *Service) gitAccessHint(ctx context.Context, a Actor) string {
	settings := strings.TrimRight(s.Cfg.PublicURL, "/") + "/settings/github"
	app, _ := s.githubApp(ctx)
	switch {
	case app == nil && a.IsAdmin():
		return "GitHub is not set up on this bitploy yet. Create the GitHub App on the Admin page (" + strings.TrimRight(s.Cfg.PublicURL, "/") + "/admin), then connect GitHub in Settings > GitHub."
	case app == nil:
		return "GitHub is not set up on this bitploy yet, so private repositories cannot be deployed. Ask your bitploy admin to set it up on the Admin page. Public repositories work without it."
	}
	return "Open Settings > GitHub (" + settings + "), press Connect GitHub and give bitploy access to this repository."
}

// ---- status of a linked repository ----

// Push statuses.
const (
	pushReady        = "ready"
	pushOff          = "off"
	pushOtherHost    = "other_host"
	pushAppMissing   = "app_missing"
	pushNotConnected = "not_connected"
	pushNoAccess     = "no_access"
	pushReconnect    = "reconnect"
)

// gitSourceState describes the linked repository of an app and whether a push
// will deploy it. Only the database is read: no call to GitHub.
func (s *Service) gitSourceState(ctx context.Context, a Actor, r db.App) *GitSourceState {
	if !r.GitRepoUrl.Valid || r.GitRepoUrl.String == "" {
		return nil
	}
	v := &GitSourceState{
		RepoURL: r.GitRepoUrl.String, Branch: r.GitBranch.String, Subdir: r.GitSubdir.String,
		AutoDeploy: r.GitAutoDeploy != 0, LastPushAt: tsPtr(r.GitLastPushAt),
	}
	key, isGitHub := github.RepoKey(r.GitRepoUrl.String)
	if isGitHub {
		v.Repo = key
		v.RepoHTMLURL = s.githubWebURL() + "/" + strings.TrimSuffix(strings.Trim(urlPath(r.GitRepoUrl.String), "/"), ".git")
	}
	v.PushStatus, v.PushMessage = s.pushStatus(ctx, a, r, key, isGitHub)
	v.PushReady = v.PushStatus == pushReady
	return v
}

func urlPath(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.Index(raw, "/"); i >= 0 {
		return raw[i:]
	}
	return ""
}

func (s *Service) pushStatus(ctx context.Context, a Actor, r db.App, key string, isGitHub bool) (string, string) {
	branch := r.GitBranch.String
	switch {
	case !isGitHub:
		return pushOtherHost, "Pushes to this git host do not start a deploy. Deploy by hand after you push."
	case r.GitAutoDeploy == 0:
		return pushOff, "Auto-deploy is off. Pushes do not start a deploy."
	}
	if _, err := s.Q.GetGitHubApp(ctx); err != nil {
		if a.IsAdmin() {
			return pushAppMissing, "GitHub is not set up yet. Create the GitHub App on the Admin page to deploy on every push."
		}
		return pushAppMissing, "GitHub is not set up yet. Ask your bitploy admin to set it up to deploy on every push."
	}
	links, err := s.Q.ListGitHubInstallationsByUser(ctx, r.UserID)
	if err != nil {
		return pushNotConnected, "Connect GitHub to deploy on every push."
	}
	owner, _ := github.SplitRepoKey(key)
	var matching, inactive bool
	for _, l := range links {
		if !strings.EqualFold(l.AccountLogin, owner) {
			continue
		}
		if l.Status != "active" {
			inactive = true
			continue
		}
		matching = true
		if s.installationCovers(ctx, l, key) {
			return pushReady, "Deploys on every push to " + branch + "."
		}
	}
	switch {
	case matching:
		return pushNoAccess, "Bitploy cannot see " + key + " yet. Give it access to this repository on GitHub."
	case inactive:
		return pushReconnect, "GitHub access for " + owner + " was removed or paused. Connect GitHub again to deploy on every push."
	case len(links) == 0:
		return pushNotConnected, "Connect GitHub to deploy on every push."
	}
	return pushNotConnected, "Connect the GitHub account " + owner + " to deploy on every push."
}

// ---- the webhook ----

// HandleGitHubWebhook processes one delivery from GitHub. It checks the
// signature before it reads anything else, handles a repeated delivery id
// once, and only queues work (a deployment); it never builds inline.
// The returned text is for the response body and the logs.
func (s *Service) HandleGitHubWebhook(ctx context.Context, event, delivery, signature string, body []byte) (string, error) {
	app, err := s.githubApp(ctx)
	if err != nil {
		return "", err
	}
	if app == nil {
		return "", apperr.NotFound("github_app_missing", "GitHub is not set up on this bitploy.", "")
	}
	if !github.VerifySignature(app.WebhookSecret, body, signature) {
		return "", apperr.Unauthorized("github_signature_invalid", "The signature does not match.", "")
	}
	delivery = strings.TrimSpace(delivery)
	if delivery == "" || len(delivery) > 100 {
		return "", apperr.BadRequest("github_delivery_missing", "The delivery id is missing.", "")
	}
	n, err := s.Q.InsertGitHubDelivery(ctx, db.InsertGitHubDeliveryParams{DeliveryID: delivery, ReceivedAt: s.now()})
	if err != nil {
		return "", apperr.Internal(err)
	}
	if n == 0 {
		return "duplicate delivery, already handled", nil
	}
	var msg string
	switch event {
	case "ping":
		msg = "pong"
	case "push":
		msg, err = s.githubPush(ctx, body)
	case "installation":
		msg, err = s.githubInstallationEvent(ctx, body)
	case "installation_repositories":
		msg, err = s.githubRepositoriesEvent(ctx, app, body)
	default:
		msg = "ignored event " + event
	}
	if err != nil {
		// Forget the delivery so GitHub's "redeliver" (same id) is processed.
		_ = s.Q.DeleteGitHubDelivery(ctx, delivery)
		return "", err
	}
	return msg, nil
}

func (s *Service) githubPush(ctx context.Context, body []byte) (string, error) {
	var ev github.PushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", apperr.BadRequest("github_payload_invalid", "The push event could not be read.", "")
	}
	branch, ok := ev.Branch()
	if !ok {
		return "ignored: not a push to a branch, or the branch was deleted", nil
	}
	key, ok := github.RepoKey(ev.Repository.FullName)
	if !ok || ev.Installation.ID == 0 {
		return "ignored: no repository or installation in the event", nil
	}
	rows, err := s.Q.ListAppsWithGitHubSource(ctx)
	if err != nil {
		return "", apperr.Internal(err)
	}
	sha, message := ev.After, ""
	if ev.HeadCommit != nil {
		sha, message = ev.HeadCommit.ID, firstLine(ev.HeadCommit.Message)
	}
	var matched, queued int
	for _, row := range rows {
		if rk, ok := github.RepoKey(row.GitRepoUrl.String); !ok || rk != key || row.GitBranch.String != branch {
			continue
		}
		// Tenant isolation: only an installation that the app's owner linked
		// (and proved with GitHub) may act on the owner's apps.
		link, err := s.Q.GetGitHubInstallationLink(ctx, db.GetGitHubInstallationLinkParams{UserID: row.UserID, InstallationID: ev.Installation.ID})
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", apperr.Internal(err)
			}
			continue
		}
		// An installation only ever reports repositories of its own account.
		if owner, _ := github.SplitRepoKey(key); !strings.EqualFold(link.AccountLogin, owner) {
			continue
		}
		matched++
		if err := s.Q.SetAppGitLastPush(ctx, db.SetAppGitLastPushParams{GitLastPushAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: row.ID}); err != nil {
			return "", apperr.Internal(err)
		}
		if row.GitAutoDeploy == 0 || row.Status == "stopped" {
			continue
		}
		actor := Actor{UserID: row.UserID, Role: "user", Channel: ChannelGitHub, Scope: ScopeDeploy}
		_, err = s.queueDeployment(ctx, actor, row, queueSpec{kind: "git", ref: branch, commitSHA: sha, commitMessage: message})
		if err != nil {
			ae := apperr.As(err)
			s.Log.Warn("github: push did not start a deploy", "app", row.Slug, "code", ae.Code)
			s.Audit(ctx, actor, "github.push_not_deployed", "app", row.ID, map[string]any{"code": ae.Code, "commit": sha})
			continue
		}
		queued++
	}
	return "push handled: " + itoa64(int64(matched)) + " app(s) matched, " + itoa64(int64(queued)) + " deployment(s) queued", nil
}

func (s *Service) githubInstallationEvent(ctx context.Context, body []byte) (string, error) {
	var ev github.InstallationEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", apperr.BadRequest("github_payload_invalid", "The installation event could not be read.", "")
	}
	id := ev.Installation.ID
	status := ""
	switch ev.Action {
	case "deleted":
		status = "deleted"
	case "suspend":
		status = "suspended"
	case "unsuspend":
		status = "active"
	default:
		return "ignored installation action " + ev.Action, nil
	}
	if _, err := s.Q.SetGitHubInstallationStatus(ctx, db.SetGitHubInstallationStatusParams{Status: status, UpdatedAt: s.now(), InstallationID: id}); err != nil {
		return "", apperr.Internal(err)
	}
	if status == "deleted" {
		_ = s.Q.ClearGitHubInstallationRepos(ctx, id)
	}
	s.forgetInstallationCaches(id)
	return "installation " + status, nil
}

func (s *Service) githubRepositoriesEvent(ctx context.Context, app *ghApp, body []byte) (string, error) {
	var ev github.InstallationEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", apperr.BadRequest("github_payload_invalid", "The installation event could not be read.", "")
	}
	id := ev.Installation.ID
	cur, err := s.Q.GetAnyGitHubInstallationLink(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "ignored: installation not connected here", nil
	}
	if err != nil {
		return "", apperr.Internal(err)
	}
	selection := ev.RepositorySelection
	if selection == "" {
		selection = cur.RepositorySelection
	}
	if selection != "all" {
		selection = "selected"
	}
	now := s.now()
	err = s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.SetGitHubInstallationSelection(ctx, db.SetGitHubInstallationSelectionParams{RepositorySelection: selection, UpdatedAt: now, InstallationID: id}); err != nil {
			return err
		}
		if selection == "all" {
			return q.ClearGitHubInstallationRepos(ctx, id)
		}
		for _, r := range ev.RepositoriesAdded {
			if key, ok := github.RepoKey(r.FullName); ok {
				if err := q.AddGitHubInstallationRepo(ctx, db.AddGitHubInstallationRepoParams{InstallationID: id, Repo: key}); err != nil {
					return err
				}
			}
		}
		for _, r := range ev.RepositoriesRemoved {
			if key, ok := github.RepoKey(r.FullName); ok {
				if err := q.RemoveGitHubInstallationRepo(ctx, db.RemoveGitHubInstallationRepoParams{InstallationID: id, Repo: key}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", apperr.Internal(err)
	}
	s.forgetInstallationCaches(id)
	if selection == "selected" && cur.RepositorySelection == "all" {
		// The event lists only what changed, not everything that is shared
		// now: read the full list in the background.
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.syncInstallationRepos(bg, app, id, "selected"); err != nil {
				s.Log.Warn("github: could not read the repositories after a change", "installation", id, "err", err)
			}
		}()
	}
	return "repositories updated", nil
}
