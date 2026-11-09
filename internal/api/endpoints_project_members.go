package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AVMG20/bitploy/internal/app"
	"github.com/AVMG20/bitploy/internal/auth"
)

type projectSignupOut struct {
	SetCookie string `header:"Set-Cookie"`
	Body      app.ProjectSignup
}

type projectInviteCode struct {
	Code string `path:"code" doc:"The code from the invite link (https://<bitploy>/join/<code>)"`
}

// projectMemberRoutes registers the members and invite links of a project
// (docs/decisions.md, "Project members"). Changing who is in a project needs
// a dashboard session; listing the members works with any token.
func (a *API) projectMemberRoutes() {
	reg(a, anyAuth, huma.Operation{
		OperationID: "listProjectMembers", Method: http.MethodGet, Path: "/projects/{id}/members", Tags: []string{"Projects"},
		Summary:     "List the members of a project",
		Description: "The owner first, then the users the project is shared with. Any scope; the owner and every member may list them.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		PageParams
	}) (*Out[app.Page[app.ProjectMember]], error) {
		items, err := a.svc.ListProjectMembers(ctx, act, in.ID)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "removeProjectMember", Method: http.MethodDelete, Path: "/projects/{id}/members/{user}", Tags: []string{"Projects"},
		Summary: "Remove a member, or leave a project",
		Description: "The owner removes any member; a member removes only themselves (user `me` or their own id) to leave. Access ends at once; the apps stay. " +
			"Session only. 403 project_owner_only, 404 member_not_found, 409 owner_cannot_leave.",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		User string `path:"user" doc:"The member's user id, or me"`
	}) (*Empty, error) {
		if err := a.svc.RemoveProjectMember(ctx, act, in.ID, in.User); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "listProjectInvites", Method: http.MethodGet, Path: "/projects/{id}/invites", Tags: []string{"Projects"},
		Summary:     "List the open invites of a project",
		Description: "Owner only, session only. Invites that are used or expired are left out; the links themselves are never shown again.",
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		PageParams
	}) (*Out[app.Page[app.ProjectInvite]], error) {
		items, err := a.svc.ListProjectInvites(ctx, act, in.ID)
		return pageOut(items, err, in.PageParams)
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "createProjectInvite", Method: http.MethodPost, Path: "/projects/{id}/invites", Tags: []string{"Projects"},
		Summary: "Invite someone to a project",
		Description: "Owner only, session only. A single-use link, valid 7 days, returned once as url (bitploy sends no email). " +
			"With email only the account with that address, or a signup with it, can accept. The link also lets a new account sign up, unless Admin > Signups allows only admin invites to make accounts. " +
			"403 project_invites_limit (20 open), project_members_limit (50), 429 project_invites_rate_limited (50 a day), 409 already_member.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		Body app.CreateProjectInviteInput
	}) (*Out[app.ProjectInvite], error) {
		return okOrNil(a.svc.CreateProjectInvite(ctx, act, in.ID, in.Body))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "revokeProjectInvite", Method: http.MethodDelete, Path: "/projects/{id}/invites/{invite}", Tags: []string{"Projects"},
		Summary: "Revoke an open invite", Description: "Owner only, session only. 404 invite_not_found.", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, act app.Actor, in *struct {
		ProjectRef
		Invite string `path:"invite" doc:"The invite's id (pin_...)"`
	}) (*Empty, error) {
		if err := a.svc.RevokeProjectInvite(ctx, act, in.ID, in.Invite); err != nil {
			return nil, err
		}
		return &Empty{}, nil
	})

	reg(a, public, huma.Operation{
		OperationID: "getProjectInvite", Method: http.MethodGet, Path: "/project-invites/{code}", Tags: []string{"Auth"},
		Summary:     "What a project invite link is for",
		Description: "The project's name, who invited, the address the invite is locked to, and whether someone without an account can make one with it (signup). No login needed: the code is the credential. 404 or 410 invite_invalid.",
	}, func(ctx context.Context, _ app.Actor, in *projectInviteCode) (*Out[app.ProjectInviteInfo], error) {
		return okOrNil(a.svc.GetProjectInvite(ctx, in.Code))
	})
	reg(a, sessionOnly, huma.Operation{
		OperationID: "acceptProjectInvite", Method: http.MethodPost, Path: "/project-invites/{code}/accept", Tags: []string{"Projects"},
		Summary: "Join a project",
		Description: "Adds the signed-in user to the invite's project and returns it. Session only. " +
			"403 invite_email_mismatch (the invite is for another address), 409 invite_own_project, 404 or 410 invite_invalid. Already a member: the project, and the invite stays unused.",
	}, func(ctx context.Context, act app.Actor, in *projectInviteCode) (*Out[app.Project], error) {
		return okOrNil(a.svc.AcceptProjectInvite(ctx, act, in.Code))
	})
	reg(a, public, huma.Operation{
		OperationID: "signupProjectInvite", Method: http.MethodPost, Path: "/project-invites/{code}/signup", Tags: []string{"Auth"},
		Summary: "Sign up through a project invite",
		Description: "Creates the account, adds it to the project and logs in, unless Admin > Signups allows only admin invites to make accounts (403 project_invite_signup_off; signup is false in GET /project-invites/{code}). " +
			"The account gets the limits for new accounts, or no apps, memory or CPU of its own when the admin turned that off. " +
			"At most 5 attempts per hour per IP, shared with POST /invites/{code}/accept. 409 email_taken: log in and accept instead.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, _ app.Actor, in *struct {
		Code string `path:"code" doc:"The code from the invite link (https://<bitploy>/join/<code>)"`
		Body acceptRequest
	}) (*projectSignupOut, error) {
		m := metaFrom(ctx)
		cookie, out, err := a.svc.SignupProjectInvite(ctx, in.Code, app.AcceptInviteInput{
			Name: in.Body.Name, Email: in.Body.Email, Password: in.Body.Password,
		}, m.IP, m.UserAgent)
		if err != nil {
			return nil, err
		}
		return &projectSignupOut{SetCookie: auth.NewSessionCookie(a.cfg, cookie).String(), Body: out}, nil
	})
}
