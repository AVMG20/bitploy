package app

// Push = deploy: the webhook, the rules that decide which apps a push may
// start, and the clone credential for GitHub repositories.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/github"
	"github.com/AVMG20/bitploy/internal/notify"
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
		if !strings.EqualFold(l.AccountLogin, owner) || !s.userMayUse(ctx, l, key) {
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

// installationCovers reports whether the link gives its user the
// repository: the installation includes it, and for a limited link the user
// could open it on GitHub when they linked.
func (s *Service) installationCovers(ctx context.Context, l db.GithubInstallation, key string) bool {
	if !s.userMayUse(ctx, l, key) {
		return false
	}
	if l.RepositorySelection == "all" {
		return true
	}
	n, err := s.Q.GitHubInstallationHasRepo(ctx, db.GitHubInstallationHasRepoParams{InstallationID: l.InstallationID, Repo: key})
	return err == nil && n > 0
}

// userMayUse is the per-user half of installationCovers. A link by the owner
// of the GitHub account may use every repository of the installation; a
// limited link (organization member, collaborator) only those its user could
// open on GitHub. Without this, anyone who can see an organization's
// installation could deploy, and so read, its private repositories.
func (s *Service) userMayUse(ctx context.Context, l db.GithubInstallation, key string) bool {
	if l.Limited == 0 {
		return true
	}
	n, err := s.Q.GitHubUserHasRepo(ctx, db.GitHubUserHasRepoParams{UserID: l.UserID, InstallationID: l.InstallationID, Repo: key})
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
	return "Open Settings > GitHub (" + settings + "), press Connect GitHub and give bitploy access to this repository. Already connected? Give access on GitHub, then press Sync from GitHub there."
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
	pushNoWebhook    = "no_webhook"
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
	ghRow, err := s.Q.GetGitHubApp(ctx)
	if err != nil {
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
			// Access problems come first: they also stop a deploy by hand.
			if len(ghRow.WebhookSecretEnc) == 0 {
				return pushNoWebhook, "GitHub cannot reach this bitploy, so pushes do not start a deploy. Deploy by hand after you push."
			}
			return pushReady, "Deploys on every push to " + branch + "."
		}
	}
	switch {
	case matching:
		return pushNoAccess, "bitploy cannot use " + key + " yet. Give it access on GitHub, then press Sync from GitHub in Settings > GitHub."
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
	redelivery := false
	if n == 0 {
		// A push that could not queue every app it matched is handled again
		// when GitHub redelivers it (once per redelivery).
		retry, err := s.Q.ClaimGitHubDeliveryRetry(ctx, delivery)
		if err != nil {
			return "", apperr.Internal(err)
		}
		if retry == 0 {
			return "duplicate delivery, already handled", nil
		}
		redelivery = true
	}
	var msg string
	switch event {
	case "ping":
		msg = "pong"
	case "push":
		var refused bool
		msg, refused, err = s.githubPush(ctx, body, redelivery)
		if err == nil && refused {
			_ = s.Q.MarkGitHubDeliveryRetry(ctx, delivery)
		}
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

// githubPush queues a deployment for every app the push may start. refused
// says that at least one of them could not be queued (deploy limit, disk,
// storage): its users are told, and GitHub's "Redeliver" of this delivery is
// handled again. On such a redelivery, an app whose newest deployment already
// is this commit is not queued a second time.
func (s *Service) githubPush(ctx context.Context, body []byte, redelivery bool) (msg string, refused bool, err error) {
	var ev github.PushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", false, apperr.BadRequest("github_payload_invalid", "The push event could not be read.", "")
	}
	branch, ok := ev.Branch()
	if !ok {
		return "ignored: not a push to a branch, or the branch was deleted", false, nil
	}
	key, ok := github.RepoKey(ev.Repository.FullName)
	if !ok || ev.Installation.ID == 0 {
		return "ignored: no repository or installation in the event", false, nil
	}
	rows, err := s.Q.ListAppsWithGitHubSource(ctx)
	if err != nil {
		return "", false, apperr.Internal(err)
	}
	sha, message := ev.After, ""
	if ev.HeadCommit != nil {
		sha, message = ev.HeadCommit.ID, firstLine(ev.HeadCommit.Message)
	}
	repoID := ev.Repository.ID
	var matched, queued int
	for _, row := range rows {
		rk, ok := github.RepoKey(row.GitRepoUrl.String)
		if !ok || row.GitBranch.String != branch {
			continue
		}
		// An app that knows its repository's id follows it through a rename or
		// a transfer; the others match by name until their first push.
		byID := repoID != 0 && row.GithubRepoID.Valid
		if (byID && row.GithubRepoID.Int64 != repoID) || (!byID && rk != key) {
			continue
		}
		// Tenant isolation: only an installation that the app's owner linked
		// (and proved with GitHub) may act on the owner's apps. The checks
		// below use the repository's current name (key).
		link, err := s.Q.GetGitHubInstallationLink(ctx, db.GetGitHubInstallationLinkParams{UserID: row.UserID, InstallationID: ev.Installation.ID})
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", false, apperr.Internal(err)
			}
			continue
		}
		// An installation only ever reports repositories of its own account.
		if owner, _ := github.SplitRepoKey(key); !strings.EqualFold(link.AccountLogin, owner) {
			continue
		}
		// A paused or removed installation, or a repository this user may not
		// use through it, does not deploy.
		if link.Status != "active" || !s.userMayUse(ctx, link, key) {
			continue
		}
		matched++
		actor := Actor{UserID: row.UserID, Role: "user", Channel: ChannelGitHub, Scope: ScopeDeploy}
		switch {
		case byID && rk != key:
			// Renamed or transferred on GitHub: keep the app's link up to date.
			newURL := "https://github.com/" + ev.Repository.FullName
			if err := s.Q.RenameAppGitHubRepo(ctx, db.RenameAppGitHubRepoParams{GitRepoUrl: sql.NullString{String: newURL, Valid: true}, UpdatedAt: s.now(), ID: row.ID}); err != nil {
				return "", false, apperr.Internal(err)
			}
			s.Audit(ctx, actor, "app.git_repo_renamed", "app", row.ID, map[string]any{"from": row.GitRepoUrl.String, "to": newURL, "github_repo_id": repoID})
			row.GitRepoUrl.String = newURL
		case !row.GithubRepoID.Valid && repoID != 0:
			if err := s.Q.SetAppGitHubRepoID(ctx, db.SetAppGitHubRepoIDParams{GithubRepoID: sql.NullInt64{Int64: repoID, Valid: true}, ID: row.ID}); err != nil {
				return "", false, apperr.Internal(err)
			}
		}
		if err := s.Q.SetAppGitLastPush(ctx, db.SetAppGitLastPushParams{GitLastPushAt: sql.NullInt64{Int64: s.now(), Valid: true}, ID: row.ID}); err != nil {
			return "", false, apperr.Internal(err)
		}
		if row.GitAutoDeploy == 0 || row.Status == "stopped" {
			continue
		}
		if redelivery && sha != "" {
			if last, err := s.Q.NewestDeploymentCommit(ctx, row.ID); err == nil && last == sha {
				continue // queued when this push came in first
			}
		}
		_, err = s.queueDeployment(ctx, actor, row, queueSpec{kind: "git", ref: branch, commitSHA: sha, commitMessage: message})
		if err != nil {
			ae := apperr.As(err)
			s.Log.Warn("github: push did not start a deploy", "app", row.Slug, "code", ae.Code)
			s.Audit(ctx, actor, "github.push_not_deployed", "app", row.ID, map[string]any{"code": ae.Code, "commit": sha})
			s.notifyPushNotDeployed(ctx, row, branch, sha, message, ae)
			refused = true
			continue
		}
		queued++
	}
	return "push handled: " + itoa64(int64(matched)) + " app(s) matched, " + itoa64(int64(queued)) + " deployment(s) queued", refused, nil
}

// notifyPushNotDeployed tells the app's users that a push could not be
// queued. It goes to the channels of deployment.failed: either way the commit
// did not go live.
func (s *Service) notifyPushNotDeployed(ctx context.Context, app db.App, branch, sha, message string, ae *apperr.Error) {
	src := "Git, branch " + branch + ", commit " + shortSHA(sha)
	if message != "" {
		src += ": " + message
	}
	hint := "Then deploy the app by hand, push again, or press Redeliver on this delivery in the GitHub App's settings."
	if ae.Hint != "" {
		hint = ae.Hint + " " + hint
	}
	m := notify.Message{
		Event: EventDeploymentFailed, Severity: notify.Emergency, Tone: notify.ToneFailure, Time: s.Now(), Tag: app.ID,
		Title: fmt.Sprintf("A push to %s did not deploy %s", branch, app.Name),
		Body:  apperr.Redact(ae.Message), Hint: apperr.Redact(hint), URL: s.appPage(app, "deployments"),
		Fields: append(s.appFields(ctx, app), notify.Field{Name: "Source", Value: src}),
	}
	s.notifyApp(ctx, app, m, EventDeploymentFailed)
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
		_ = s.Q.ClearGitHubUserReposOfInstallation(ctx, id)
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
