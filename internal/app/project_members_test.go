package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// membersEnv: alice owns the project Shop with the app shop, bob is invited,
// carol is a stranger. Every actor is a dashboard session unless a test
// says otherwise.
type membersEnv struct {
	*depEnv
	alice, bob, carol Actor
	shop              Project
	app               App
}

func session(a Actor) Actor {
	a.Channel, a.Scope = ChannelDashboard, ScopeSession
	return a
}

func newMembersEnv(t *testing.T) *membersEnv {
	t.Helper()
	e := depTestSvc(t)
	ctx := context.Background()
	m := &membersEnv{depEnv: e, alice: session(e.user)}
	m.bob = session(e.addUser(t, "usr_bob", "bob@example.com", "user"))
	m.carol = session(e.addUser(t, "usr_carol", "carol@example.com", "user"))
	var err error
	if m.shop, err = e.s.CreateProject(ctx, m.alice, CreateProjectInput{Name: "Shop"}); err != nil {
		t.Fatal(err)
	}
	m.app = e.mustApp(t, CreateAppInput{Name: "Shop", Slug: "shop", Project: m.shop.ID})
	if _, err := e.s.SetEnv(ctx, m.alice, "shop", map[string]string{"GREETING": "hello"}, nil); err != nil {
		t.Fatal(err)
	}
	return m
}

// invite makes an invite of alice's project and returns its code.
func (m *membersEnv) invite(t *testing.T, email string) string {
	t.Helper()
	inv, err := m.s.CreateProjectInvite(context.Background(), m.alice, m.shop.ID, CreateProjectInviteInput{Email: email})
	if err != nil {
		t.Fatalf("CreateProjectInvite: %v", err)
	}
	i := strings.LastIndex(inv.URL, "/join/")
	if !strings.HasPrefix(inv.URL, "https://bitploy.test/join/") || i < 0 || !strings.HasPrefix(inv.ID, "pin_") {
		t.Fatalf("invite = %+v", inv)
	}
	return inv.URL[i+len("/join/"):]
}

func (m *membersEnv) join(t *testing.T, a Actor) {
	t.Helper()
	p, err := m.s.AcceptProjectInvite(context.Background(), a, m.invite(t, ""))
	if err != nil || p.ID != m.shop.ID || p.Role != "member" || !p.Shared {
		t.Fatalf("accept = %+v, %v", p, err)
	}
}

// strangerLocked checks that the actor can neither see nor change alice's
// project or its app.
func (m *membersEnv) strangerLocked(t *testing.T, who string, a Actor) {
	t.Helper()
	ctx := context.Background()
	s := m.s
	check := func(what string, err error, want string) {
		t.Helper()
		if depCode(err) != want {
			t.Errorf("%s: %s: got %v, want %s", who, what, err, want)
		}
	}
	_, err := s.GetProject(ctx, a, m.shop.ID)
	check("get project", err, "project_not_found")
	_, err = s.GetProject(ctx, a, "Shop")
	check("get project by name", err, "project_not_found")
	_, err = s.GetApp(ctx, a, "shop")
	check("get app", err, "app_not_found")
	_, err = s.GetApp(ctx, a, m.app.ID)
	check("get app by id", err, "app_not_found")
	_, err = s.ListEnv(ctx, a, "shop")
	check("list env", err, "app_not_found")
	_, err = s.SetEnv(ctx, a, "shop", map[string]string{"X": "1"}, nil)
	check("set env", err, "app_not_found")
	_, err = s.UpdateApp(ctx, a, "shop", UpdateAppInput{Name: strp("Mine")})
	check("update app", err, "app_not_found")
	_, err = s.RestartApp(ctx, a, "shop")
	check("restart", err, "app_not_found")
	_, err = s.StopApp(ctx, a, "shop")
	check("stop", err, "app_not_found")
	check("delete app", s.DeleteApp(ctx, a, "shop", "shop"), "app_not_found")
	_, err = s.ListDeployments(ctx, a, "shop", 10, "")
	check("deployments", err, "app_not_found")
	_, err = s.CreateApp(ctx, a, CreateAppInput{Name: "x", Slug: "x-" + strings.TrimPrefix(a.UserID, "usr_"), Project: m.shop.ID})
	check("create app in project", err, "project_not_found")
	_, err = s.AddService(ctx, a, m.shop.ID, AddServiceInput{Template: "postgres"})
	check("add service", err, "project_not_found")
	_, err = s.UpdateProject(ctx, a, m.shop.ID, UpdateProjectInput{Name: strp("Mine")})
	check("rename project", err, "project_not_found")
	check("delete project", s.DeleteProject(ctx, a, m.shop.ID, "Shop", true), "project_not_found")
	_, err = s.ListProjectMembers(ctx, a, m.shop.ID)
	check("list members", err, "project_not_found")
	manage := "project_not_found"
	if a.Scope != ScopeSession {
		manage = "session_required" // tokens never manage members, whose project ever
	}
	_, err = s.CreateProjectInvite(ctx, a, m.shop.ID, CreateProjectInviteInput{})
	check("create invite", err, manage)
	_, err = s.ListProjectInvites(ctx, a, m.shop.ID)
	check("list invites", err, manage)
	check("remove member", s.RemoveProjectMember(ctx, a, m.shop.ID, "usr_bob"), manage)
	check("leave", s.RemoveProjectMember(ctx, a, m.shop.ID, "me"), manage)

	ps, err := s.ListProjects(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.ID == m.shop.ID {
			t.Errorf("%s: list_projects shows alice's project", who)
		}
	}
	apps, err := s.ListApps(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, ap := range apps {
		if ap.ID == m.app.ID {
			t.Errorf("%s: list_apps shows alice's app", who)
		}
	}
}

func TestProjectMembersStrangerSeesNothing(t *testing.T) {
	m := newMembersEnv(t)
	m.strangerLocked(t, "carol", m.carol)
	tok := m.carol
	tok.Channel, tok.Scope = ChannelAPI, ScopeDeploy
	m.strangerLocked(t, "carol's token", tok)

	// A member of another project of alice's is a stranger to this one.
	ctx := context.Background()
	other, err := m.s.CreateProject(ctx, m.alice, CreateProjectInput{Name: "Blog"})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := m.s.CreateProjectInvite(ctx, m.alice, other.ID, CreateProjectInviteInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.s.AcceptProjectInvite(ctx, m.carol, inv.URL[strings.LastIndex(inv.URL, "/")+1:]); err != nil {
		t.Fatal(err)
	}
	m.strangerLocked(t, "carol in Blog", m.carol)
}

func TestProjectMemberCanUseTheProject(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	m.join(t, m.bob)

	// Listed, marked shared, with the owner.
	ps, err := s.ListProjects(ctx, m.bob)
	if err != nil || len(ps) != 1 {
		t.Fatalf("bob's projects = %d, %v", len(ps), err)
	}
	if p := ps[0]; p.ID != m.shop.ID || !p.Shared || p.Role != "member" || p.Owner.ID != "usr_alice" || p.Owner.Email == "" || len(p.Apps) != 1 || !p.Apps[0].Shared {
		t.Errorf("shared project = %+v", p)
	}
	own, _ := s.GetProject(ctx, m.alice, m.shop.ID)
	if own.Shared || own.Role != "owner" || own.MemberCount != 1 || own.Apps[0].Shared {
		t.Errorf("alice's view = role %q shared %v members %d", own.Role, own.Shared, own.MemberCount)
	}
	if _, err := s.GetProject(ctx, m.bob, "shop"); err != nil {
		t.Errorf("bob finds the project by name: %v", err)
	}
	apps, err := s.ListApps(ctx, m.bob)
	if err != nil || len(apps) != 1 || apps[0].ID != m.app.ID || !apps[0].Shared || apps[0].OwnerID != "usr_alice" {
		t.Fatalf("bob's apps = %+v, %v", apps, err)
	}

	// Variables with their values, changes, settings.
	vars, err := s.ListEnv(ctx, m.bob, "shop")
	if err != nil || len(vars) != 1 || vars[0].Value == nil || *vars[0].Value != "hello" {
		t.Fatalf("bob's env = %+v, %v", vars, err)
	}
	if _, err := s.SetEnv(ctx, m.bob, "shop", map[string]string{"GREETING": "hi"}, nil); err != nil {
		t.Errorf("bob sets env: %v", err)
	}
	if _, err := s.UpdateApp(ctx, m.bob, "shop", UpdateAppInput{Name: strp("Shop front")}); err != nil {
		t.Errorf("bob renames the app: %v", err)
	}
	if _, err := s.ListDeployments(ctx, m.bob, "shop", 10, ""); err != nil {
		t.Errorf("bob lists deployments: %v", err)
	}
	if _, err := s.ListProjectMembers(ctx, m.bob, m.shop.ID); err != nil {
		t.Errorf("bob lists members: %v", err)
	}

	// A new app in the shared project is alice's: her quota, created_by bob.
	made, err := s.CreateApp(ctx, m.bob, CreateAppInput{Name: "Admin", Slug: "shop-admin", Project: m.shop.ID})
	if err != nil {
		t.Fatalf("bob creates an app in the project: %v", err)
	}
	if made.OwnerID != "usr_alice" || !made.Shared || made.Project.ID != m.shop.ID {
		t.Errorf("made = owner %q shared %v project %+v", made.OwnerID, made.Shared, made.Project)
	}
	var by string
	_ = s.Store.DB.QueryRow(`SELECT created_by FROM apps WHERE id = ?`, made.ID).Scan(&by)
	if by != "usr_bob" {
		t.Errorf("created_by = %q", by)
	}
	if u, _ := s.Usage(ctx, "usr_bob"); u.Apps != 0 {
		t.Errorf("bob's usage counts %d apps", u.Apps)
	}
	if u, _ := s.Usage(ctx, "usr_alice"); u.Apps != 2 {
		t.Errorf("alice's usage counts %d apps, want 2", u.Apps)
	}
	// The owner's app quota applies, not the member's.
	m.exec(t, `UPDATE users SET quota_apps = 2 WHERE id = 'usr_alice'`)
	if _, err := s.CreateApp(ctx, m.bob, CreateAppInput{Name: "x", Slug: "shop-x", Project: m.shop.ID}); depCode(err) != "quota_apps" && depCode(err) != "apps_quota" && !strings.Contains(depCode(err), "quota") {
		t.Errorf("past alice's quota: %v", err)
	}
	m.exec(t, `UPDATE users SET quota_apps = 10 WHERE id = 'usr_alice'`)
	// A suspended owner: nothing new goes into the project.
	m.exec(t, `UPDATE users SET status = 'suspended' WHERE id = 'usr_alice'`)
	if _, err := s.CreateApp(ctx, m.bob, CreateAppInput{Name: "x", Slug: "shop-y", Project: m.shop.ID}); depCode(err) != "project_owner_suspended" {
		t.Errorf("suspended owner: %v", err)
	}
	m.exec(t, `UPDATE users SET status = 'active' WHERE id = 'usr_alice'`)

	// Bob's own project stays his own; alice does not see it.
	mine, err := s.CreateProject(ctx, m.bob, CreateProjectInput{Name: "Bob's"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(ctx, m.alice, mine.ID); depCode(err) != "project_not_found" {
		t.Errorf("alice sees bob's project: %v", err)
	}

	// Deleting apps is up to the owner, but the member may take back an app
	// he made that never ran (project_owner_only_test.go has the rest).
	if err := s.DeleteApp(ctx, m.bob, "shop-admin", "shop-admin"); err != nil {
		t.Errorf("bob deletes the app he made: %v", err)
	}
}

func TestProjectMemberCannotManage(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	m.join(t, m.bob)
	m.join(t, m.carol)

	owner := func(what string, err error) {
		t.Helper()
		if depCode(err) != "project_owner_only" {
			t.Errorf("%s: %v, want project_owner_only", what, err)
		}
	}
	_, err := s.UpdateProject(ctx, m.bob, m.shop.ID, UpdateProjectInput{Name: strp("Bob's shop")})
	owner("rename", err)
	owner("delete", s.DeleteProject(ctx, m.bob, m.shop.ID, "Shop", true))
	_, err = s.CreateProjectInvite(ctx, m.bob, m.shop.ID, CreateProjectInviteInput{})
	owner("invite", err)
	_, err = s.ListProjectInvites(ctx, m.bob, m.shop.ID)
	owner("list invites", err)
	owner("revoke invite", s.RevokeProjectInvite(ctx, m.bob, m.shop.ID, "pin_x"))
	owner("remove carol", s.RemoveProjectMember(ctx, m.bob, m.shop.ID, "usr_carol"))
	if err := s.RemoveProjectMember(ctx, m.bob, m.shop.ID, "usr_alice"); depCode(err) != "project_owner_only" {
		t.Errorf("bob removes alice: %v", err)
	}
	if _, err := s.UpdateApp(ctx, m.bob, "shop", UpdateAppInput{Project: strp("prj_other")}); depCode(err) != "project_move_unsupported" {
		t.Errorf("move: %v", err)
	}
	if got, _ := s.GetProject(ctx, m.alice, m.shop.ID); got.Name != "Shop" || len(got.Apps) != 1 {
		t.Errorf("project changed: %+v", got)
	}
	if err := s.RemoveProjectMember(ctx, m.alice, m.shop.ID, "me"); depCode(err) != "owner_cannot_leave" {
		t.Errorf("alice leaves: %v", err)
	}

	// Tokens never change who is in a project, of any scope.
	for _, scope := range []Scope{ScopeDeploy, ScopeRead} {
		tok := Actor{UserID: "usr_alice", Role: "user", Channel: ChannelAPI, Scope: scope}
		if _, err := s.CreateProjectInvite(ctx, tok, m.shop.ID, CreateProjectInviteInput{}); depCode(err) != "session_required" {
			t.Errorf("%s token invites: %v", scope, err)
		}
		if err := s.RemoveProjectMember(ctx, tok, m.shop.ID, "usr_bob"); depCode(err) != "session_required" {
			t.Errorf("%s token removes: %v", scope, err)
		}
		if _, err := s.ListProjectInvites(ctx, tok, m.shop.ID); depCode(err) != "session_required" {
			t.Errorf("%s token lists invites: %v", scope, err)
		}
	}
	code := m.invite(t, "")
	tok := Actor{UserID: "usr_dave", Role: "user", Channel: ChannelAPI, Scope: ScopeDeploy}
	m.addUser(t, "usr_dave", "dave@example.com", "user")
	if _, err := s.AcceptProjectInvite(ctx, tok, code); depCode(err) != "session_required" {
		t.Errorf("token accepts: %v", err)
	}
}

func TestProjectMemberTokensFollowScope(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	m.join(t, m.bob)
	read := Actor{UserID: "usr_bob", Role: "user", Channel: ChannelMCP, Scope: ScopeRead}
	if _, err := m.s.GetApp(ctx, read, "shop"); err != nil {
		t.Errorf("read token gets the shared app: %v", err)
	}
	if _, err := m.s.SetEnv(ctx, read, "shop", map[string]string{"X": "1"}, nil); depCode(err) != "token_read_only" {
		t.Errorf("read token sets env: %v", err)
	}
	if _, err := m.s.CreateApp(ctx, read, CreateAppInput{Name: "x", Slug: "shop-r", Project: m.shop.ID}); depCode(err) != "token_read_only" {
		t.Errorf("read token creates: %v", err)
	}
	deploy := read
	deploy.Scope = ScopeDeploy
	if _, err := m.s.SetEnv(ctx, deploy, "shop", map[string]string{"X": "1"}, nil); err != nil {
		t.Errorf("deploy token sets env: %v", err)
	}
	if _, err := m.s.UpdateProject(ctx, deploy, m.shop.ID, UpdateProjectInput{Name: strp("x")}); depCode(err) != "project_owner_only" {
		t.Errorf("deploy token renames: %v", err)
	}
}

func TestProjectMemberRemovedLosesAccess(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	m.join(t, m.bob)
	m.join(t, m.carol)
	tok := Actor{UserID: "usr_bob", Role: "user", Channel: ChannelAPI, Scope: ScopeDeploy}
	if _, err := s.GetApp(ctx, tok, "shop"); err != nil {
		t.Fatalf("before: %v", err)
	}
	if err := s.RemoveProjectMember(ctx, m.alice, m.shop.ID, "usr_bob"); err != nil {
		t.Fatal(err)
	}
	m.strangerLocked(t, "removed bob", m.bob)
	m.strangerLocked(t, "removed bob's token", tok)
	if err := s.RemoveProjectMember(ctx, m.alice, m.shop.ID, "usr_bob"); depCode(err) != "member_not_found" {
		t.Errorf("remove twice: %v", err)
	}

	// Carol leaves on her own.
	if err := s.RemoveProjectMember(ctx, m.carol, m.shop.ID, "me"); err != nil {
		t.Fatalf("carol leaves: %v", err)
	}
	m.strangerLocked(t, "carol after leaving", m.carol)
	members, _ := s.ListProjectMembers(ctx, m.alice, m.shop.ID)
	if len(members) != 1 || members[0].Role != "owner" || !members[0].You {
		t.Errorf("members = %+v", members)
	}
	// The apps stay.
	if got, _ := s.GetProject(ctx, m.alice, m.shop.ID); len(got.Apps) != 1 {
		t.Errorf("apps after removal: %d", len(got.Apps))
	}
	// Deleting the project takes the memberships with it.
	m.join(t, m.bob)
	if err := s.DeleteProject(ctx, m.alice, m.shop.ID, "Shop", true); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM project_members`).Scan(&n)
	if n != 0 {
		t.Errorf("%d memberships left after delete", n)
	}
}

func TestProjectInvites(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s

	code := m.invite(t, "")
	info, err := s.GetProjectInvite(ctx, code)
	if err != nil || info.ProjectName != "Shop" || info.InvitedBy != "usr_alice" || info.Email != nil || info.Role != "member" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := s.AcceptProjectInvite(ctx, m.alice, code); depCode(err) != "invite_own_project" {
		t.Errorf("owner accepts: %v", err)
	}
	if _, err := s.AcceptProjectInvite(ctx, m.bob, code); err != nil {
		t.Fatal(err)
	}
	// Single use.
	if _, err := s.AcceptProjectInvite(ctx, m.carol, code); depCode(err) != "invite_invalid" {
		t.Errorf("second use: %v", err)
	}
	if _, err := s.GetProjectInvite(ctx, code); depCode(err) != "invite_invalid" {
		t.Errorf("info after use: %v", err)
	}
	if _, err := s.GetProjectInvite(ctx, "nope"); depCode(err) != "invite_invalid" {
		t.Errorf("unknown code: %v", err)
	}
	// A member who opens another link keeps the link unused.
	again := m.invite(t, "")
	if p, err := s.AcceptProjectInvite(ctx, m.bob, again); err != nil || p.ID != m.shop.ID {
		t.Errorf("member accepts again: %v", err)
	}
	if _, err := s.GetProjectInvite(ctx, again); err != nil {
		t.Errorf("the second link was used up: %v", err)
	}

	// Locked to an address.
	locked := m.invite(t, "Carol@Example.com")
	if _, err := s.AcceptProjectInvite(ctx, session(m.addUser(t, "usr_dave", "dave@example.com", "user")), locked); depCode(err) != "invite_email_mismatch" {
		t.Errorf("wrong account: %v", err)
	}
	if _, err := s.AcceptProjectInvite(ctx, m.carol, locked); err != nil {
		t.Errorf("carol accepts her invite: %v", err)
	}
	if _, err := s.CreateProjectInvite(ctx, m.alice, m.shop.ID, CreateProjectInviteInput{Email: "bob@example.com"}); depCode(err) != "already_member" {
		t.Errorf("invite a member: %v", err)
	}
	if _, err := s.CreateProjectInvite(ctx, m.alice, m.shop.ID, CreateProjectInviteInput{Email: "alice@example.com"}); depCode(err) != "invite_self" {
		t.Errorf("invite the owner: %v", err)
	}
	if _, err := s.CreateProjectInvite(ctx, m.alice, m.shop.ID, CreateProjectInviteInput{Role: "owner"}); depCode(err) != "invalid_role" {
		t.Errorf("owner role: %v", err)
	}

	// Pending list, revoke.
	pending, err := s.ListProjectInvites(ctx, m.alice, m.shop.ID)
	if err != nil || len(pending) != 1 || pending[0].URL != "" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := s.RevokeProjectInvite(ctx, m.alice, m.shop.ID, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptProjectInvite(ctx, session(Actor{UserID: "usr_dave", Role: "user"}), again); depCode(err) != "invite_invalid" {
		t.Errorf("revoked invite: %v", err)
	}
	if err := s.RevokeProjectInvite(ctx, m.alice, m.shop.ID, pending[0].ID); depCode(err) != "invite_not_found" {
		t.Errorf("revoke twice: %v", err)
	}

	// Expired after 7 days.
	late := m.invite(t, "")
	real := s.Now
	s.Now = func() time.Time { return real().Add(7*24*time.Hour + time.Minute) }
	if _, err := s.AcceptProjectInvite(ctx, session(Actor{UserID: "usr_dave", Role: "user"}), late); depCode(err) != "invite_invalid" {
		t.Errorf("expired: %v", err)
	}
	s.Now = real

	// An invite keeps an auto project (made for one app) when its app goes.
	solo := m.mustApp(t, CreateAppInput{Name: "Solo", Slug: "solo"})
	if _, err := s.CreateProjectInvite(ctx, m.alice, solo.Project.ID, CreateProjectInviteInput{}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, m.alice, "solo", "solo"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(ctx, m.alice, solo.Project.ID); err != nil {
		t.Errorf("the shared auto project went with its app: %v", err)
	}
}

func TestSignupThroughProjectInvite(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	if st, err := s.serverSettings(ctx, s.Q); err != nil || st.Signups != SignupAnyInvite {
		t.Fatalf("signups = %q, %v", st.Signups, err)
	}
	locked := m.invite(t, "erin@example.com")
	if _, _, err := s.SignupProjectInvite(ctx, locked, AcceptInviteInput{Name: "Erin", Email: "other@example.com", Password: "correct horse battery"}, "198.51.100.1", "ua"); depCode(err) != "invite_email_mismatch" {
		t.Errorf("other address: %v", err)
	}
	cookie, out, err := s.SignupProjectInvite(ctx, locked, AcceptInviteInput{Name: "Erin", Password: "correct horse battery"}, "198.51.100.1", "ua")
	if err != nil || cookie == "" || out.User.Email != "erin@example.com" || out.User.Role != "user" || out.Project.ID != m.shop.ID || out.CSRFToken == "" {
		t.Fatalf("signup = %+v, %v", out, err)
	}
	erin := Actor{UserID: out.User.ID, Role: "user", Channel: ChannelDashboard, Scope: ScopeSession}
	if _, err := s.GetApp(ctx, erin, "shop"); err != nil {
		t.Errorf("erin sees the shared app: %v", err)
	}
	if _, _, err := s.SignupProjectInvite(ctx, locked, AcceptInviteInput{Name: "Erin", Password: "correct horse battery"}, "198.51.100.1", "ua"); depCode(err) != "invite_invalid" {
		t.Errorf("used invite: %v", err)
	}
	// An address with an account: log in and accept instead.
	open := m.invite(t, "")
	if _, _, err := s.SignupProjectInvite(ctx, open, AcceptInviteInput{Name: "Bob", Email: "bob@example.com", Password: "correct horse battery"}, "198.51.100.2", "ua"); depCode(err) != "email_taken" {
		t.Errorf("existing address: %v", err)
	}
	if _, err := s.GetProjectInvite(ctx, open); err != nil {
		t.Errorf("a failed signup used the invite: %v", err)
	}
	var n int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = 'bob@example.com'`).Scan(&n)
	if n != 1 {
		t.Errorf("%d bobs", n)
	}
}

func TestProjectAdminReadsButIsNoMember(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	admin := session(m.addUser(t, "usr_admin", "admin@example.com", "admin"))
	p, err := m.s.GetProject(ctx, admin, m.shop.ID)
	if err != nil || p.Role != "admin" || p.Shared {
		t.Fatalf("admin view = %+v, %v", p, err)
	}
	vars, err := m.s.ListEnv(ctx, admin, "shop")
	if err != nil || len(vars) != 1 || vars[0].Value != nil {
		t.Errorf("admin sees values: %+v, %v", vars, err)
	}
	if _, err := m.s.UpdateProject(ctx, admin, m.shop.ID, UpdateProjectInput{Name: strp("x")}); depCode(err) != "project_not_found" {
		t.Errorf("admin renames: %v", err)
	}
	if _, err := m.s.CreateProjectInvite(ctx, admin, m.shop.ID, CreateProjectInviteInput{}); depCode(err) != "project_not_found" {
		t.Errorf("admin invites: %v", err)
	}
	if err := m.s.RemoveProjectMember(ctx, admin, m.shop.ID, "me"); depCode(err) != "project_not_found" {
		t.Errorf("admin leaves: %v", err)
	}
	tok := admin
	tok.Channel, tok.Scope = ChannelAPI, ScopeRead
	if _, err := m.s.GetProject(ctx, tok, m.shop.ID); depCode(err) != "project_not_found" {
		t.Errorf("admin token reads: %v", err)
	}
}

func TestProjectMembersHearAppEvents(t *testing.T) {
	m := newMembersEnv(t)
	m.join(t, m.bob)
	bob := m.s.Events().Subscribe("usr_bob")
	defer bob.Close()
	carol := m.s.Events().Subscribe("usr_carol")
	defer carol.Close()
	m.s.publishApp(context.Background(), m.app.ID)
	m.s.PublishAppChange(context.Background(), m.alice, "shop", "", "")
	if got := bob.Drain(); len(got) == 0 || got[0].App != "shop" {
		t.Errorf("bob's events = %+v", got)
	}
	if got := carol.Drain(); len(got) != 0 {
		t.Errorf("carol hears about alice's app: %+v", got)
	}
}

func TestGitCheckUser(t *testing.T) {
	owner := Actor{UserID: "usr_o"}
	member := Actor{UserID: "usr_m"}
	linked := db.App{UserID: "usr_o"}
	linked.GitRepoUrl.String, linked.GitRepoUrl.Valid = "https://github.com/acme/shop.git", true
	none := db.App{UserID: "usr_o"}
	cases := []struct {
		name string
		a    Actor
		row  db.App
		repo string
		want string
	}{
		{"owner links anything", owner, none, "https://github.com/acme/secret", "usr_o"},
		{"member keeps the linked repo", member, linked, "https://github.com/Acme/Shop", "usr_o"},
		{"member links another repo", member, linked, "https://github.com/acme/secret", "usr_m"},
		{"member links a first repo", member, none, "https://github.com/acme/secret", "usr_m"},
		{"system", System, none, "https://github.com/acme/secret", "usr_o"},
	}
	for _, c := range cases {
		if got := gitCheckUser(c.a, c.row, c.repo); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
