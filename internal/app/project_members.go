package app

// Project members (docs/decisions.md, "Project members"): the owner of a
// project shares it with other users through single-use invite links. A
// member sees the project and its apps and may do on them what the owner's
// own deploy token may (deploy, variables, logs, domains, restarts, backups,
// files, new apps and services in the project); the owner alone renames and
// deletes the project and manages its members. Every app in a project stays
// the owner's: it counts against the owner's quotas, whoever made it.
//
// Membership is read from the database on every check (mayUseApp,
// loadProject), so a removed member loses access with their next request.
// Managing members needs a dashboard session: a leaked API token must not
// be able to let someone else in.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	roleOwner  = "owner"
	roleMember = "member"

	projectInviteTTL = 7 * 24 * time.Hour
	// maxProjectMembers is the number of members a project can have besides
	// its owner; maxPendingProjectInvites the open invites of one project.
	maxProjectMembers        = 50
	maxPendingProjectInvites = 20
	// maxProjectInvitesPerDay caps the invites one user makes in 24 hours:
	// an invite can also let a new account sign up (server settings).
	maxProjectInvitesPerDay = 50
)

// isProjectMember reports whether userID is a member of the project (the
// owner is not a member). A failed lookup counts as no.
func (s *Service) isProjectMember(ctx context.Context, projectID, userID string) bool {
	if projectID == "" || userID == "" {
		return false
	}
	n, err := s.Q.IsProjectMember(ctx, db.IsProjectMemberParams{ProjectID: projectID, UserID: userID})
	return err == nil && n > 0
}

// projectRole is the actor's part in the project: owner, member or "".
func (s *Service) projectRole(ctx context.Context, a Actor, p db.Project) string {
	switch {
	case a.UserID == "":
		return ""
	case p.UserID == a.UserID:
		return roleOwner
	case s.isProjectMember(ctx, p.ID, a.UserID):
		return roleMember
	}
	return ""
}

// appAudience are the users who hear about changes to an app: its owner and
// the members of its project.
func (s *Service) appAudience(ctx context.Context, row db.App) []string {
	out := []string{row.UserID}
	if row.ProjectID == "" {
		return out
	}
	memberIDs, err := s.Q.ListProjectMemberIDs(ctx, row.ProjectID)
	if err != nil {
		return out
	}
	for _, id := range memberIDs {
		if id != row.UserID {
			out = append(out, id)
		}
	}
	return out
}

func projectOwnerOnly() *apperr.Error {
	return apperr.Forbidden("project_owner_only", "Only the owner of this project can do this.",
		"The project was shared with you: you can deploy and change its apps, but renaming or deleting the project and managing its members is up to its owner (get_project shows who that is).").WithNext("get_project")
}

func projectOwnerSuspended() *apperr.Error {
	return apperr.Forbidden("project_owner_suspended", "The owner of this project is suspended, so nothing can be added to it.",
		"Ask a bitploy admin, or the project's owner.")
}

// projectInviteNoSignup refuses a signup through a project invite while only
// admin invites make accounts.
func projectInviteNoSignup() *apperr.Error {
	return apperr.Forbidden("project_invite_signup_off",
		"You need an account on this server first: this invite only works for people who already have one.",
		"Log in if you have an account. Otherwise ask an admin of this server for an invite link, make your account, then open this link again.")
}

func projectInviteInvalid(status int, why string) *apperr.Error {
	return apperr.New(status, "invite_invalid", why, "Ask the person who invited you for a new invite link.")
}

func (s *Service) projectInviteURL(code string) string {
	return strings.TrimRight(s.Cfg.PublicURL, "/") + "/join/" + code
}

// loadProjectToManage loads a project for its owner, with a dashboard session.
func (s *Service) loadProjectToManage(ctx context.Context, a Actor, ref string) (db.Project, error) {
	if err := RequireSession(a); err != nil {
		return db.Project{}, err
	}
	return s.loadProject(ctx, a, ref, projectManage)
}

// ---- members ----

// ListProjectMembers returns the owner and the members of a project. Anyone
// who can see the project can see who else can.
func (s *Service) ListProjectMembers(ctx context.Context, a Actor, ref string) ([]ProjectMember, error) {
	p, err := s.loadProject(ctx, a, ref, projectRead)
	if err != nil {
		return nil, err
	}
	out := []ProjectMember{}
	if u, err := s.Q.GetUser(ctx, p.UserID); err == nil {
		out = append(out, ProjectMember{UserID: u.ID, Name: u.Name, Email: u.Email, Role: roleOwner,
			CreatedAt: ts(p.CreatedAt), You: u.ID == a.UserID})
	}
	rows, err := s.Q.ListProjectMembers(ctx, p.ID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	for _, r := range rows {
		out = append(out, ProjectMember{UserID: r.UserID, Name: r.Name, Email: r.Email, Role: r.Role,
			InvitedBy: strPtr(r.InvitedBy), CreatedAt: ts(r.CreatedAt), You: r.UserID == a.UserID})
	}
	return out, nil
}

// RemoveProjectMember takes a member out of a project: the owner removes
// anyone, a member removes only themselves (leave). Their access ends at
// once; the apps stay as they are.
func (s *Service) RemoveProjectMember(ctx context.Context, a Actor, ref, userID string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	userID = strings.TrimSpace(userID)
	if userID == "me" {
		userID = a.UserID
	}
	need := projectManage
	if userID == a.UserID {
		need = projectRead // leaving needs only access
	}
	p, err := s.loadProject(ctx, a, ref, need)
	if err != nil {
		return err
	}
	if userID == p.UserID {
		return apperr.Conflict("owner_cannot_leave", "The owner cannot leave their own project.",
			"Delete the project instead, or keep it.")
	}
	if userID == a.UserID && s.projectRole(ctx, a, p) != roleMember {
		return projectNotFound(ref) // an admin looking: not a member
	}
	n, err := s.Q.DeleteProjectMember(ctx, db.DeleteProjectMemberParams{ProjectID: p.ID, UserID: userID})
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return apperr.NotFound("member_not_found", "That user is not a member of this project.",
			"List the members with GET /api/v1/projects/{id}/members.")
	}
	action := "project.member_remove"
	if userID == a.UserID {
		action = "project.leave"
	}
	s.Audit(ctx, a, action, "project", p.ID, map[string]any{"user": userID})
	return nil
}

// ---- invites ----

// CreateProjectInvite makes a single-use invite link to the project (owner,
// dashboard session). With an email only the account with that address, or
// a signup with it, can accept. bitploy sends no email: the owner passes the
// link on. The project stays when its last app goes, like a project made on
// purpose.
func (s *Service) CreateProjectInvite(ctx context.Context, a Actor, ref string, in CreateProjectInviteInput) (ProjectInvite, error) {
	p, err := s.loadProjectToManage(ctx, a, ref)
	if err != nil {
		return ProjectInvite{}, err
	}
	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = roleMember
	}
	if role != roleMember {
		return ProjectInvite{}, apperr.BadRequest("invalid_role", "The role of a project invite must be \"member\".", "Leave role out, or use member.")
	}
	var email sql.NullString
	if strings.TrimSpace(in.Email) != "" {
		e, err := acctValidEmail(in.Email)
		if err != nil {
			return ProjectInvite{}, err
		}
		if u, err := s.Q.GetUserByEmail(ctx, e); err == nil {
			if u.ID == p.UserID {
				return ProjectInvite{}, apperr.BadRequest("invite_self", "That is the address of the project's owner.",
					"Invite someone else.")
			}
			if s.isProjectMember(ctx, p.ID, u.ID) {
				return ProjectInvite{}, apperr.Conflict("already_member", "The user with that address is already a member of this project.",
					"There is nothing to do.")
			}
		}
		email = nullStr(e)
	}
	now := s.Now()
	pending, err := s.Q.CountPendingProjectInvites(ctx, db.CountPendingProjectInvitesParams{ProjectID: p.ID, ExpiresAt: now.Unix()})
	if err != nil {
		return ProjectInvite{}, apperr.Internal(err)
	}
	if pending >= maxPendingProjectInvites {
		return ProjectInvite{}, apperr.Forbidden("project_invites_limit",
			fmt.Sprintf("This project already has %d open invites, the maximum.", pending),
			"Revoke the invites nobody will use, then try again.")
	}
	members, err := s.Q.CountProjectMembers(ctx, p.ID)
	if err != nil {
		return ProjectInvite{}, apperr.Internal(err)
	}
	if members >= maxProjectMembers {
		return ProjectInvite{}, membersLimit(members)
	}
	made, err := s.Q.CountProjectInvitesByCreatorSince(ctx, db.CountProjectInvitesByCreatorSinceParams{
		CreatedBy: nullStr(a.UserID), CreatedAt: now.Add(-24 * time.Hour).Unix(),
	})
	if err != nil {
		return ProjectInvite{}, apperr.Internal(err)
	}
	if made >= maxProjectInvitesPerDay {
		return ProjectInvite{}, apperr.TooMany("project_invites_rate_limited",
			fmt.Sprintf("You made %d project invites in the last 24 hours, the maximum.", made),
			"Try again tomorrow.")
	}
	code := ids.Base62(32)
	id := ids.New(ids.ProjectInvite)
	exp := now.Add(projectInviteTTL)
	if err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.InsertProjectInvite(ctx, db.InsertProjectInviteParams{
			ID: id, CodeHash: store.HashToken(code), ProjectID: p.ID, Email: email, Role: role,
			CreatedBy: nullStr(a.UserID), ExpiresAt: exp.Unix(), CreatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		// A shared project is one on purpose: it stays when its last app goes.
		return q.SetProjectManual(ctx, db.SetProjectManualParams{UpdatedAt: now.Unix(), ID: p.ID})
	}); err != nil {
		return ProjectInvite{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "project.invite_create", "project", p.ID, map[string]any{"invite": id, "role": role, "email": email.String})
	return ProjectInvite{ID: id, Email: strPtr(email), Role: role, CreatedAt: now.UTC(), ExpiresAt: exp.UTC(), URL: s.projectInviteURL(code)}, nil
}

func membersLimit(n int64) *apperr.Error {
	return apperr.Forbidden("project_members_limit",
		fmt.Sprintf("This project already has %d members, the maximum.", n),
		"Remove a member who no longer needs access first.")
}

// ListProjectInvites returns the open invites of a project (owner, session).
func (s *Service) ListProjectInvites(ctx context.Context, a Actor, ref string) ([]ProjectInvite, error) {
	p, err := s.loadProjectToManage(ctx, a, ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.Q.ListPendingProjectInvites(ctx, db.ListPendingProjectInvitesParams{ProjectID: p.ID, ExpiresAt: s.now()})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]ProjectInvite, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProjectInvite{ID: r.ID, Email: strPtr(r.Email), Role: r.Role, CreatedAt: ts(r.CreatedAt), ExpiresAt: ts(r.ExpiresAt)})
	}
	return out, nil
}

// RevokeProjectInvite deletes an unused invite of the project (owner, session).
func (s *Service) RevokeProjectInvite(ctx context.Context, a Actor, ref, id string) error {
	p, err := s.loadProjectToManage(ctx, a, ref)
	if err != nil {
		return err
	}
	n, err := s.Q.DeleteUnusedProjectInvite(ctx, db.DeleteUnusedProjectInviteParams{ID: strings.TrimSpace(id), ProjectID: p.ID})
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return apperr.NotFound("invite_not_found", "This project has no unused invite with that id.",
			"Used invites cannot be revoked; remove the member instead. List the invites with GET /api/v1/projects/{id}/invites.")
	}
	s.Audit(ctx, a, "project.invite_revoke", "project", p.ID, map[string]any{"invite": id})
	return nil
}

// projectInviteByCode finds an invite that can still be accepted, and its project.
func (s *Service) projectInviteByCode(ctx context.Context, code string) (db.ProjectInvite, db.Project, error) {
	inv, err := s.Q.GetProjectInviteByHash(ctx, store.HashToken(strings.TrimSpace(code)))
	if errors.Is(err, sql.ErrNoRows) {
		return db.ProjectInvite{}, db.Project{}, projectInviteInvalid(404, "This invite link is not valid.")
	}
	if err != nil {
		return db.ProjectInvite{}, db.Project{}, apperr.Internal(err)
	}
	if inv.UsedAt.Valid {
		return db.ProjectInvite{}, db.Project{}, projectInviteInvalid(410, "This invite link has already been used.")
	}
	if inv.ExpiresAt <= s.now() {
		return db.ProjectInvite{}, db.Project{}, projectInviteInvalid(410, "This invite link has expired.")
	}
	p, err := s.Q.GetProject(ctx, inv.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ProjectInvite{}, db.Project{}, projectInviteInvalid(404, "This invite link is not valid.")
	}
	if err != nil {
		return db.ProjectInvite{}, db.Project{}, apperr.Internal(err)
	}
	return inv, p, nil
}

// GetProjectInvite says what an invite link is for, without accepting it.
// The code is the credential: whoever has the link may see the project's
// name and who invited them (nothing else).
func (s *Service) GetProjectInvite(ctx context.Context, code string) (ProjectInviteInfo, error) {
	inv, p, err := s.projectInviteByCode(ctx, code)
	if err != nil {
		return ProjectInviteInfo{}, err
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return ProjectInviteInfo{}, err
	}
	out := ProjectInviteInfo{ProjectName: p.Name, Email: strPtr(inv.Email), Role: inv.Role, ExpiresAt: ts(inv.ExpiresAt),
		Signup: st.Signups != SignupAdminInvites}
	if inv.CreatedBy.Valid {
		if u, err := s.Q.GetUser(ctx, inv.CreatedBy.String); err == nil {
			out.InvitedBy = u.Name
		}
	}
	return out, nil
}

// addMember marks the invite used and adds the user to its project, in q.
func (s *Service) addMember(ctx context.Context, q *db.Queries, inv db.ProjectInvite, userID string) error {
	n, err := q.CountProjectMembers(ctx, inv.ProjectID)
	if err != nil {
		return apperr.Internal(err)
	}
	if n >= maxProjectMembers {
		return membersLimit(n)
	}
	now := s.now()
	used, err := q.MarkProjectInviteUsed(ctx, db.MarkProjectInviteUsedParams{UsedAt: nullInt(now, true), UsedBy: nullStr(userID), ID: inv.ID, ExpiresAt: now})
	if err != nil {
		return apperr.Internal(err)
	}
	if used == 0 {
		return projectInviteInvalid(410, "This invite link has already been used.")
	}
	if err := q.InsertProjectMember(ctx, db.InsertProjectMemberParams{
		ProjectID: inv.ProjectID, UserID: userID, Role: inv.Role, InvitedBy: inv.CreatedBy, CreatedAt: now,
	}); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// AcceptProjectInvite adds the signed-in user to the invite's project and
// returns it. An invite locked to an address only works for the account
// with that address. A user who is already in the project gets it back and
// the invite stays unused.
func (s *Service) AcceptProjectInvite(ctx context.Context, a Actor, code string) (Project, error) {
	if err := RequireSession(a); err != nil {
		return Project{}, err
	}
	inv, p, err := s.projectInviteByCode(ctx, code)
	if err != nil {
		return Project{}, err
	}
	u, err := s.activeUser(ctx, a.UserID)
	if err != nil {
		return Project{}, err
	}
	switch s.projectRole(ctx, a, p) {
	case roleOwner:
		return Project{}, apperr.Conflict("invite_own_project", "This is an invite to your own project.",
			"Send the link to the person you want to invite; it works once.")
	case roleMember:
		return s.projectView(ctx, a, p)
	}
	if inv.Email.Valid && !strings.EqualFold(inv.Email.String, u.Email) {
		return Project{}, apperr.Forbidden("invite_email_mismatch",
			"This invite is for a different email address.",
			"Log in with the account of "+inv.Email.String+", or ask for an invite for "+u.Email+".")
	}
	if err := s.Store.Tx(ctx, func(q *db.Queries) error { return s.addMember(ctx, q, inv, u.ID) }); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return s.projectView(ctx, a, p) // joined in a parallel request
		}
		return Project{}, apperr.As(err)
	}
	s.Audit(ctx, a, "project.member_add", "project", p.ID, map[string]any{"invite": inv.ID, "user": u.ID})
	return s.projectView(ctx, a, p)
}

// SignupProjectInvite creates an account from a project invite and adds it
// to the project, in one go, then logs it in. It works unless the server
// settings let only admin invites make accounts (signups admin_invites), and
// shares the user invite's limit of 5 signups per hour per IP. The account
// is a normal user with the limits for new accounts, or none of its own
// when project_invite_apps is off.
func (s *Service) SignupProjectInvite(ctx context.Context, code string, in AcceptInviteInput, ip, userAgent string) (string, ProjectSignup, error) {
	lim := s.limiter()
	sigKey := "signup|" + ip
	if wait, full := lim.full(sigKey, s.now(), signupMax, signupWindow); full {
		return "", ProjectSignup{}, tooManySignups(wait)
	}
	inv, p, err := s.projectInviteByCode(ctx, code)
	if err != nil {
		if ae := apperr.As(err); ae.HTTPStatus == 404 {
			lim.add(sigKey, s.now()) // a wrong code counts, like for a user invite
		}
		return "", ProjectSignup{}, err
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return "", ProjectSignup{}, err
	}
	if st.Signups == SignupAdminInvites {
		return "", ProjectSignup{}, projectInviteNoSignup()
	}
	user, err := s.signup(ctx, signupReq{in: in, lockedEmail: inv.Email.String, role: "user", sigKey: sigKey,
		via: joinedProjectInvite, by: inv.CreatedBy.String, project: p.ID,
		inTx: func(q *db.Queries, u db.User) error { return s.addMember(ctx, q, inv, u.ID) }})
	if err != nil {
		return "", ProjectSignup{}, apperr.As(err)
	}
	cookie, csrf, err := s.newSession(ctx, user.ID, ip, userAgent)
	if err != nil {
		return "", ProjectSignup{}, err
	}
	act := acctActorFor(user, ip)
	s.Audit(ctx, act, "user.signup", "user", user.ID, map[string]any{"email": user.Email, "role": "user", "project_invite": inv.ID})
	s.Audit(ctx, act, "project.member_add", "project", p.ID, map[string]any{"invite": inv.ID, "user": user.ID})
	return cookie, ProjectSignup{User: acctUserFromRow(user), CSRFToken: csrf, Project: ProjectRef{ID: p.ID, Name: p.Name}}, nil
}
