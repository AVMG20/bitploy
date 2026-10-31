package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
	"github.com/AVMG20/bitploy/internal/github"
)

type githubConnectRequest struct {
	Mode string `json:"mode,omitempty" enum:"install,authorize" doc:"install (default): choose an account and repositories on GitHub. authorize: only sign in, to link accounts where the app is already installed."`
}

type autoDeployRequest struct {
	AutoDeploy bool `json:"auto_deploy" doc:"Deploy on every push to the branch"`
}

func (a *API) githubRoutes() {
	// ---- the user's GitHub connection ----
	reg(a, sessionOnly, huma.Operation{
		OperationID: "getGitHub", Method: http.MethodGet, Path: "/github", Tags: []string{"GitHub"},
		Summary: "GitHub connection", Description: "Whether the GitHub App is set up on this bitploy, and the GitHub accounts you connected.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.GitHubOverview], error) {
		return okOrNil(a.svc.GitHubOverview(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "connectGitHub", Method: http.MethodPost, Path: "/github/connect", Tags: []string{"GitHub"},
		Summary: "Start connecting a GitHub account", Description: "Returns the GitHub address to send the browser to. GitHub sends it back to Settings > GitHub with a code, which goes to POST /github/link.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body *githubConnectRequest }) (*Out[app.GitHubRedirect], error) {
		mode := ""
		if in.Body != nil {
			mode = in.Body.Mode
		}
		return okOrNil(a.svc.GitHubConnectURL(ctx, act, mode))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "linkGitHub", Method: http.MethodPost, Path: "/github/link", Tags: []string{"GitHub"},
		Summary: "Finish connecting a GitHub account", Description: "Exchanges the OAuth code and links only installations GitHub itself shows to the signed-in GitHub user.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.GitHubLinkInput }) (*Out[app.GitHubOverview], error) {
		return okOrNil(a.svc.GitHubLink(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "unlinkGitHub", Method: http.MethodDelete, Path: "/github/installations/{id}", Tags: []string{"GitHub"},
		Summary: "Forget a connected GitHub account", Description: "Only bitploy forgets it. Uninstall the app on GitHub to remove the access there.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID int64 `path:"id"`
	}) (*Empty, error) {
		if err := a.svc.GitHubUnlink(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listGitHubRepositories", Method: http.MethodGet, Path: "/github/repositories", Tags: []string{"GitHub"},
		Summary: "Repositories of your connected accounts", Description: "Newest push first, at most 100.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Query string `query:"query" doc:"Only repositories whose name contains this text"`
	}) (*Out[app.Page[app.GitHubRepo]], error) {
		items, err := a.svc.GitHubRepositories(ctx, act, in.Query)
		if err != nil {
			return nil, err
		}
		return &Out[app.Page[app.GitHubRepo]]{Body: app.Page[app.GitHubRepo]{Items: items}}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listGitHubBranches", Method: http.MethodGet, Path: "/github/branches", Tags: []string{"GitHub"},
		Summary: "Branches of a repository", Description: "For a repository of one of your connected accounts.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		Repo string `query:"repo" doc:"owner/name"`
	}) (*Out[app.GitHubBranches], error) {
		return okOrNil(a.svc.GitHubBranches(ctx, act, in.Repo))
	})

	// ---- admin: the GitHub App ----
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminGetGitHubApp", Method: http.MethodGet, Path: "/admin/github", Tags: []string{"Admin"},
		Summary: "The GitHub App of this bitploy", Description: "Admin only.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.GitHubAppInfo], error) {
		return okOrNil(a.svc.GitHubAppDetails(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminStartGitHubApp", Method: http.MethodPost, Path: "/admin/github/manifest", Tags: []string{"Admin"},
		Summary: "Start creating the GitHub App", Description: "Admin only. Returns the manifest to post to GitHub (field manifest, to post_url).",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.GitHubManifestInput }) (*Out[app.GitHubManifest], error) {
		return okOrNil(a.svc.GitHubStartManifest(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminFinishGitHubApp", Method: http.MethodPost, Path: "/admin/github/manifest/complete", Tags: []string{"Admin"},
		Summary: "Finish creating the GitHub App", Description: "Admin only. Exchanges the code GitHub sent back and stores the app's credentials encrypted.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.GitHubManifestResult }) (*Out[app.GitHubAppInfo], error) {
		return okOrNil(a.svc.GitHubFinishManifest(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "adminDisconnectGitHubApp", Method: http.MethodDelete, Path: "/admin/github", Tags: []string{"Admin"},
		Summary: "Forget the GitHub App", Description: "Admin only. Removes the credentials and every connected account here. Delete the app on GitHub yourself.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Empty, error) {
		if err := a.svc.GitHubDisconnectApp(ctx, act); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- the repository of an app ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "clearGitSource", Method: http.MethodDelete, Path: "/apps/{app}/git-source", Tags: []string{"Deployments"},
		Summary: "Unlink the repository", Description: "The app takes uploads again. What runs now keeps running.",
	}, func(ctx context.Context, act app.Actor, in *AppRef) (*Out[app.App], error) {
		return okOrNil(a.svc.ClearGitSource(ctx, act, in.App))
	})
	reg(a, anyAuth, huma.Operation{
		OperationID: "setGitAutoDeploy", Method: http.MethodPatch, Path: "/apps/{app}/git-source", Tags: []string{"Deployments"},
		Summary: "Turn auto-deploy on or off", Description: "Deploy on every push to the linked branch (GitHub repositories only).",
	}, func(ctx context.Context, act app.Actor, in *struct {
		AppRef
		Body autoDeployRequest
	}) (*Out[app.App], error) {
		return okOrNil(a.svc.SetGitAutoDeploy(ctx, act, in.App, in.Body.AutoDeploy))
	})
}

// githubWebhook is POST /api/v1/github/webhook. GitHub calls it, so it sits
// outside session and token auth: the HMAC signature is the credential. The
// body is read once, capped, and checked before anything is parsed.
func (a *API) githubWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, github.MaxWebhookBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			auth.WriteError(w, apperr.New(http.StatusRequestEntityTooLarge, "github_payload_too_large", "The event is larger than 5 MB.", ""))
			return
		}
		auth.WriteError(w, apperr.BadRequest("github_payload_invalid", "The event could not be read.", ""))
		return
	}
	msg, err := a.svc.HandleGitHubWebhook(r.Context(), r.Header.Get("X-GitHub-Event"), r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-Hub-Signature-256"), body)
	if err != nil {
		auth.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": msg})
}
