package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/arno/bitploy/internal/app"
	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/auth"
)

type loginRequest struct {
	Email    string `json:"email" doc:"Account email"`
	Password string `json:"password" doc:"Account password"`
}

type sessionOut struct {
	SetCookie string `header:"Set-Cookie"`
	Body      app.SessionInfo
}

type acceptRequest struct {
	Name     string `json:"name" doc:"Display name"`
	Email    string `json:"email,omitempty" doc:"Email address; optional when the invite is locked to one address"`
	Password string `json:"password" doc:"At least 10 characters"`
}

type profileRequest struct {
	Name  string `json:"name,omitempty" doc:"Display name, 1 to 80 characters"`
	Email string `json:"email,omitempty" doc:"New login email address"`
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password" doc:"At least 10 characters. All other sessions are signed out."`
}

type githubRequest struct {
	Token string `json:"token" doc:"Fine-grained GitHub personal access token with read-only Contents and Metadata. Never returned."`
}

func (a *API) accountRoutes() {
	reg(a, public, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/auth/login", Tags: []string{"Auth"},
		Summary: "Log in", Description: "Sets the session cookie. Failed logins are limited to 5 per 15 minutes per email and IP (429 with Retry-After).",
	}, func(ctx context.Context, _ app.Actor, in *struct{ Body loginRequest }) (*sessionOut, error) {
		m := metaFrom(ctx)
		cookie, info, err := a.svc.Login(ctx, in.Body.Email, in.Body.Password, m.IP, m.UserAgent)
		if err != nil {
			return nil, err
		}
		return &sessionOut{SetCookie: auth.NewSessionCookie(a.cfg, cookie).String(), Body: info}, nil
	})

	reg(a, public, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout", Tags: []string{"Auth"},
		Summary: "Log out", Description: "Deletes the current session and the cookie.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ app.Actor, _ *struct{}) (*struct {
		SetCookie string `header:"Set-Cookie"`
	}, error) {
		if err := a.svc.Logout(ctx, auth.SessionCookieFrom(ctx)); err != nil {
			return nil, err
		}
		return &struct {
			SetCookie string `header:"Set-Cookie"`
		}{SetCookie: auth.ExpiredSessionCookie(a.cfg).String()}, nil
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "getSession", Method: http.MethodGet, Path: "/session", Tags: []string{"Auth"},
		Summary: "Current user and CSRF token", Description: "Session only. Send the csrf_token as X-CSRF-Token on every change.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.SessionInfo], error) {
		return okOrNil(a.svc.GetSession(ctx, act, auth.SessionCookieFrom(ctx)))
	})

	reg(a, public, huma.Operation{
		OperationID: "acceptInvite", Method: http.MethodPost, Path: "/invites/{code}/accept", Tags: []string{"Auth"},
		Summary: "Finish signup from an invite", Description: "Creates the account and logs in. With BITPLOY_SIGNUP=open the code `open` is accepted.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, _ app.Actor, in *struct {
		Code string `path:"code"`
		Body acceptRequest
	}) (*sessionOut, error) {
		m := metaFrom(ctx)
		cookie, info, err := a.svc.AcceptInvite(ctx, in.Code, app.AcceptInviteInput{
			Name: in.Body.Name, Email: in.Body.Email, Password: in.Body.Password,
		}, m.IP, m.UserAgent)
		if err != nil {
			return nil, err
		}
		return &sessionOut{SetCookie: auth.NewSessionCookie(a.cfg, cookie).String(), Body: info}, nil
	})

	reg(a, anyAuth, huma.Operation{
		OperationID: "getMe", Method: http.MethodGet, Path: "/me", Tags: []string{"Account"},
		Summary: "Who am I (whoami)", Description: "User, quotas, usage, base domain and the scope of the credential used.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.Me], error) {
		return okOrNil(a.svc.Me(ctx, act))
	})

	reg(a, anyAuth, huma.Operation{
		OperationID: "getUsage", Method: http.MethodGet, Path: "/usage", Tags: []string{"Account"},
		Summary: "Usage totals", Description: "Apps, memory reserved, build minutes, running hours and deployments this month, disk used.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.Usage], error) {
		return okOrNil(a.svc.Usage(ctx, act.UserID))
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "updateMe", Method: http.MethodPatch, Path: "/me", Tags: []string{"Account"},
		Summary: "Update profile",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body profileRequest }) (*Out[app.User], error) {
		if in.Body.Name == "" && in.Body.Email == "" {
			return nil, apperr.BadRequest("nothing_to_change", "Send a name and/or an email.", "Example: {\"name\": \"Ada\"}.")
		}
		var u app.User
		var err error
		if in.Body.Name != "" {
			if u, err = a.svc.UpdateProfile(ctx, act, in.Body.Name); err != nil {
				return nil, err
			}
		}
		if in.Body.Email != "" {
			if u, err = a.svc.UpdateEmail(ctx, act, in.Body.Email); err != nil {
				return nil, err
			}
		}
		return &Out[app.User]{Body: u}, nil
	})

	reg(a, sessionOnly, huma.Operation{
		OperationID: "changePassword", Method: http.MethodPut, Path: "/me/password", Tags: []string{"Account"},
		Summary: "Change password", Description: "Signs out every other session.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body passwordRequest }) (*Empty, error) {
		if err := a.svc.ChangePassword(ctx, act, auth.SessionCookieFrom(ctx), in.Body.CurrentPassword, in.Body.NewPassword); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- sessions ----
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listSessions", Method: http.MethodGet, Path: "/sessions", Tags: []string{"Account"},
		Summary: "List active dashboard sessions",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.Session]], error) {
		items, err := a.svc.ListSessions(ctx, act, auth.SessionCookieFrom(ctx))
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "revokeSession", Method: http.MethodDelete, Path: "/sessions/{id}", Tags: []string{"Account"},
		Summary: "Sign out one session", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id"`
	}) (*Empty, error) {
		if err := a.svc.RevokeSession(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "revokeOtherSessions", Method: http.MethodPost, Path: "/sessions/revoke-others", Tags: []string{"Account"},
		Summary: "Sign out everywhere else", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Empty, error) {
		if err := a.svc.SignOutEverywhere(ctx, act, auth.SessionCookieFrom(ctx)); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- API tokens (session only: a token can never create or list tokens) ----
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listTokens", Method: http.MethodGet, Path: "/tokens", Tags: []string{"Tokens"},
		Summary: "List API tokens", Description: "Session only. The token value is never shown again after creation.",
	}, func(ctx context.Context, act app.Actor, in *struct{ PageParams }) (*Out[app.Page[app.Token]], error) {
		items, err := a.svc.ListTokens(ctx, act)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "createToken", Method: http.MethodPost, Path: "/tokens", Tags: []string{"Tokens"},
		Summary: "Create an API token", Description: "Session only. Returns the token value once. Max 20 active tokens per user.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct{ Body app.CreateTokenInput }) (*Out[app.TokenCreated], error) {
		return okOrNil(a.svc.CreateToken(ctx, act, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "revokeToken", Method: http.MethodDelete, Path: "/tokens/{id}", Tags: []string{"Tokens"},
		Summary: "Revoke an API token", Description: "Takes effect immediately.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ID string `path:"id"`
	}) (*Empty, error) {
		if err := a.svc.RevokeToken(ctx, act, in.ID); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	// ---- GitHub token ----
	reg(a, anyAuth, huma.Operation{
		OperationID: "getGitHubToken", Method: http.MethodGet, Path: "/me/github-token", Tags: []string{"Account"},
		Summary: "GitHub token status", Description: "Connected as which GitHub login, and when it expires. The token itself is never returned.",
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Out[app.GitHubStatus], error) {
		return okOrNil(a.svc.GetGitHubStatus(ctx, act))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "setGitHubToken", Method: http.MethodPut, Path: "/me/github-token", Tags: []string{"Account"},
		Summary: "Store the GitHub token", Description: "Validated against GitHub GET /user, then stored encrypted.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body githubRequest }) (*Out[app.GitHubStatus], error) {
		return okOrNil(a.svc.SetGitHubToken(ctx, act, in.Body.Token))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "deleteGitHubToken", Method: http.MethodDelete, Path: "/me/github-token", Tags: []string{"Account"},
		Summary: "Remove the GitHub token", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, _ *struct{}) (*Empty, error) {
		if err := a.svc.DeleteGitHubToken(ctx, act); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
}
