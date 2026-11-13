package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth"
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

type resetCode struct {
	Code string `path:"code" doc:"The code from the reset link (https://<bitploy>/reset/<code>)"`
}

type resetRequest struct {
	Password string `json:"password" doc:"The new password, at least 10 characters"`
}

type profileRequest struct {
	Name  string `json:"name,omitempty" doc:"Display name, 1 to 80 characters"`
	Email string `json:"email,omitempty" doc:"New login email address; needs current_password"`
	// CurrentPassword is required when the email changes, not for a name-only change.
	CurrentPassword string `json:"current_password,omitempty" doc:"Your current password; required when email changes"`
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password" doc:"At least 10 characters. All other sessions are signed out."`
}

func (a *API) accountRoutes() {
	reg(a, public, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/auth/login", Tags: []string{"Auth"},
		Summary: "Log in", Description: "Sets the session cookie. Limits (429 with Retry-After): 5 failed tries per 15 minutes per email and IP, 30 attempts per 15 minutes per IP, 20 failures per hour per email.",
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
		OperationID: "getSignupRules", Method: http.MethodGet, Path: "/signup-rules", Tags: []string{"Auth"},
		Summary:     "Who can make an account here",
		Description: "No login needed. open_signup: anyone can make one on /invite/open. project_invite_signup: a project invite can also create an account. An admin sets both in Admin > Signups.",
	}, func(ctx context.Context, _ app.Actor, _ *struct{}) (*Out[app.SignupRules], error) {
		return okOrNil(a.svc.GetSignupRules(ctx))
	})

	reg(a, public, huma.Operation{
		OperationID: "acceptInvite", Method: http.MethodPost, Path: "/invites/{code}/accept", Tags: []string{"Auth"},
		Summary: "Finish signup from an invite", Description: "Creates the account and logs in. The code `open` is open signup, accepted while Admin > Signups lets anyone make an account (else 403 signup_closed). At most 5 attempts per hour per IP. With open signup an address that is already registered gets the generic `signup_failed` (409).",
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

	reg(a, public, huma.Operation{
		OperationID: "getPasswordReset", Method: http.MethodGet, Path: "/password-resets/{code}", Tags: []string{"Auth"},
		Summary:     "Whose account a password reset link is for",
		Description: "The name and email of the account, so the person can check it is theirs. No login needed: the code is the credential. 404 or 410 reset_link_invalid (wrong, used or expired), 403 account_suspended. Wrong codes count towards 5 per hour per IP (429).",
	}, func(ctx context.Context, _ app.Actor, in *resetCode) (*Out[app.PasswordResetInfo], error) {
		return okOrNil(a.svc.GetPasswordReset(ctx, in.Code, metaFrom(ctx).IP))
	})

	reg(a, public, huma.Operation{
		OperationID: "usePasswordReset", Method: http.MethodPost, Path: "/password-resets/{code}/use", Tags: []string{"Auth"},
		Summary: "Set a new password with a reset link",
		Description: "Sets the password (at least 10 characters), uses up the link, signs out every session of the account (API tokens keep working) and logs in. " +
			"404 or 410 reset_link_invalid, 403 account_suspended (the link does not reactivate an account). At most 5 attempts per hour per IP.",
	}, func(ctx context.Context, _ app.Actor, in *struct {
		Code string `path:"code" doc:"The code from the reset link (https://<bitploy>/reset/<code>)"`
		Body resetRequest
	}) (*sessionOut, error) {
		m := metaFrom(ctx)
		cookie, info, err := a.svc.UsePasswordReset(ctx, in.Code, in.Body.Password, m.IP, m.UserAgent)
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
		Summary: "Update profile", Description: "Name and/or email. Changing the email needs current_password.",
	}, func(ctx context.Context, act app.Actor, in *struct{ Body profileRequest }) (*Out[app.User], error) {
		if in.Body.Name == "" && in.Body.Email == "" {
			return nil, apperr.BadRequest("nothing_to_change", "Send a name and/or an email.", "Example: {\"name\": \"Ada\"}.")
		}
		if in.Body.Name != "" {
			if err := app.ValidateName(in.Body.Name); err != nil {
				return nil, err
			}
		}
		var u app.User
		var err error
		// Email first: if the password is wrong nothing else is changed.
		if in.Body.Email != "" {
			if u, err = a.svc.UpdateEmail(ctx, act, in.Body.Email, in.Body.CurrentPassword); err != nil {
				return nil, err
			}
		}
		if in.Body.Name != "" {
			if u, err = a.svc.UpdateProfile(ctx, act, in.Body.Name); err != nil {
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

	reg(a, anyAuth, huma.Operation{
		OperationID: "listActivity", Method: http.MethodGet, Path: "/activity", Tags: []string{"Account"},
		Summary: "Activity on your apps, projects and account, newest first (list_activity)",
		Description: "What happened to the apps and projects you own or share, and to your account: by you, the people you share a project with, an admin or bitploy itself. " +
			"Other people show by name only when they share the project; IP addresses only on what you did yourself. Kept 90 days.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		PageParams
		App string `query:"app" doc:"Only the activity of this app (slug or ID)."`
	}) (*Out[app.Page[app.ActivityEntry]], error) {
		return okOrNil(a.svc.ListActivity(ctx, act, in.App, pageLimit(in.Limit), in.Cursor))
	})
}
