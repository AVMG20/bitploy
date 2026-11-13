package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/store/db"
)

// setSignups switches who can make an account, as an admin would.
func setSignups(t *testing.T, s *Service, mode string) {
	t.Helper()
	if _, err := s.Q.GetServerSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`UPDATE server_settings SET signups = ? WHERE id = 1`, mode); err != nil {
		t.Fatal(err)
	}
}

func auditCount(t *testing.T, s *Service, action, like string) int {
	t.Helper()
	var n int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ? AND details LIKE ?`, action, "%"+like+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func inviteCode(url string) string { return url[strings.LastIndex(url, "/")+1:] }

func TestServerSettingsDefaultsAndAdminOnly(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	mustUser(t, s, "user@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	uc, _ := login(t, s, "user@example.nl")
	admin, user := sessionActor(t, s, ac), sessionActor(t, s, uc)

	st, err := s.GetServerSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	want := NewAccountQuota{Apps: 10, MemoryMB: 2048, CPUMillis: 2000, DomainsPerApp: 5, VolumeMB: 25600}
	if st.Signups != SignupAnyInvite || !st.ProjectInviteApps || st.NewAccountQuota != want || st.UpdatedAt != nil || st.Server.CPUMillis < 1000 {
		t.Fatalf("defaults = %+v", st)
	}

	if _, err := s.GetServerSettings(ctx, user); code(err) != "admin_only" {
		t.Errorf("user reads settings: %v", err)
	}
	open := SignupOpen
	if _, err := s.UpdateServerSettings(ctx, user, UpdateServerSettingsInput{Signups: &open}); err == nil {
		t.Error("a user changed the settings")
	}
	if got, _ := s.GetSignupRules(ctx); got.OpenSignup || !got.ProjectInviteSignup {
		t.Errorf("rules after the user's try = %+v", got)
	}

	bad := "everyone"
	if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{Signups: &bad}); code(err) != "invalid_signups" {
		t.Errorf("unknown mode: %v", err)
	}
	if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{}); code(err) != "nothing_to_change" {
		t.Errorf("empty patch: %v", err)
	}
	for name, q := range map[string]NewAccountQuota{
		"negative":        {Apps: -1, MemoryMB: 1, CPUMillis: 1, VolumeMB: 1000},
		"tiny storage":    {Apps: 1, MemoryMB: 1, CPUMillis: 1, VolumeMB: 0},
		"more cpu":        {Apps: 1, MemoryMB: 1, CPUMillis: st.Server.CPUMillis + 1, VolumeMB: 1000},
		"more than int32": {Apps: 1 << 40, MemoryMB: 1, CPUMillis: 1, VolumeMB: 1000},
	} {
		if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{NewAccountQuota: &q}); code(err) != "invalid_quota" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if st.Server.MemoryMB > 0 {
		q := want
		q.MemoryMB = st.Server.MemoryMB + 1
		if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{NewAccountQuota: &q}); code(err) != "invalid_quota" {
			t.Errorf("more memory than the server: %v", err)
		}
	}

	off := false
	got, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{Signups: &open, ProjectInviteApps: &off})
	if err != nil || got.Signups != SignupOpen || got.ProjectInviteApps || got.UpdatedAt == nil {
		t.Fatalf("update = %+v, %v", got, err)
	}
	if again, _ := s.GetServerSettings(ctx, admin); again.Signups != SignupOpen || again.ProjectInviteApps {
		t.Errorf("not stored: %+v", again)
	}
	if rules, _ := s.GetSignupRules(ctx); !rules.OpenSignup || !rules.ProjectInviteSignup {
		t.Errorf("rules = %+v", rules)
	}
	if n := auditCount(t, s, "admin.settings_update", `"signups":{"from":"any_invite","to":"open"}`); n != 1 {
		t.Errorf("%d audit entries for the signups change", n)
	}
	if n := auditCount(t, s, "admin.settings_update", `"project_invite_apps":{"from":true,"to":false}`); n != 1 {
		t.Errorf("%d audit entries for project_invite_apps", n)
	}
	// Saving the same again changes nothing and logs nothing.
	if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{Signups: &open}); err != nil {
		t.Fatal(err)
	}
	if n := auditCount(t, s, "admin.settings_update", ""); n != 1 {
		t.Errorf("%d audit entries after a save without changes", n)
	}
}

func TestSignupModes(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	ac, _ := login(t, s, "admin@example.nl")
	admin := sessionActor(t, s, ac)
	form := func(email string) AcceptInviteInput {
		return AcceptInviteInput{Name: "N", Email: email, Password: "password-1234"}
	}
	adminInvite := func() string {
		inv, err := s.CreateInvite(ctx, admin, CreateInviteInput{})
		if err != nil {
			t.Fatal(err)
		}
		return inviteCode(inv.URL)
	}

	for i, mode := range []string{SignupAdminInvites, SignupAnyInvite, SignupOpen} {
		setSignups(t, s, mode)
		ip := "198.51.100." + string(rune('1'+i))
		_, _, err := s.AcceptInvite(ctx, OpenSignupCode, form(mode+"-open@example.nl"), ip, "ua")
		if mode == SignupOpen && err != nil {
			t.Errorf("%s: open signup: %v", mode, err)
		}
		if mode != SignupOpen && code(err) != "signup_closed" {
			t.Errorf("%s: open signup: %v", mode, err)
		}
		// An admin invite works in every mode.
		if _, info, err := s.AcceptInvite(ctx, adminInvite(), form(mode+"-inv@example.nl"), ip, "ua"); err != nil || info.User.Role != "user" {
			t.Errorf("%s: admin invite: %+v %v", mode, info, err)
		}
	}
	var via, by string
	_ = s.Store.DB.QueryRow(`SELECT joined_via, joined_by FROM users WHERE email = 'any_invite-inv@example.nl'`).Scan(&via, &by)
	if via != "admin_invite" || by != admin.UserID {
		t.Errorf("admin invite joined = %q by %q", via, by)
	}
	_ = s.Store.DB.QueryRow(`SELECT joined_via FROM users WHERE email = 'open-open@example.nl'`).Scan(&via)
	if via != "open" {
		t.Errorf("open signup joined = %q", via)
	}
	_ = s.Store.DB.QueryRow(`SELECT joined_via FROM users WHERE email = 'admin@example.nl'`).Scan(&via)
	if via != "setup" {
		t.Errorf("create-user joined = %q", via)
	}
}

func TestNewAccountLimits(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	before := mustUser(t, s, "before@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	admin := sessionActor(t, s, ac)

	q := NewAccountQuota{Apps: 3, MemoryMB: 1024, CPUMillis: 1000, DomainsPerApp: 2, VolumeMB: 4096}
	if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{NewAccountQuota: &q}); err != nil {
		t.Fatal(err)
	}
	if n := auditCount(t, s, "admin.settings_update", `"to":{"apps":3`); n != 1 {
		t.Errorf("%d audit entries for the limits", n)
	}
	inv, err := s.CreateInvite(ctx, admin, CreateInviteInput{})
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := s.AcceptInvite(ctx, inviteCode(inv.URL), AcceptInviteInput{Name: "New", Email: "new@example.nl", Password: "password-1234"}, "198.51.100.9", "ua")
	if err != nil {
		t.Fatal(err)
	}
	users, err := s.AdminListUsers(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		switch u.ID {
		case info.User.ID:
			if got := (NewAccountQuota{u.Quota.Apps, u.Quota.MemoryMB, u.Quota.CPUMillis, u.Quota.DomainsPerApp, u.Quota.VolumeMB}); got != q {
				t.Errorf("new account's limits = %+v", got)
			}
			if u.Joined == nil || u.Joined.Via != "admin_invite" || u.Joined.By == nil || u.Joined.By.ID != admin.UserID {
				t.Errorf("new account joined = %+v", u.Joined)
			}
		case before.ID:
			if u.Quota.Apps != 10 || u.Quota.MemoryMB != 2048 || u.Quota.VolumeMB != 25600 {
				t.Errorf("an existing account's limits changed: %+v", u.Quota)
			}
		}
	}
	// An account from before joined_via was recorded shows nothing.
	if _, err := s.Store.DB.Exec(`UPDATE users SET joined_via = '' WHERE id = ?`, before.ID); err != nil {
		t.Fatal(err)
	}
	users, _ = s.AdminListUsers(ctx, admin)
	for _, u := range users {
		if u.ID == before.ID && u.Joined != nil {
			t.Errorf("unrecorded origin = %+v", u.Joined)
		}
	}
}

func TestProjectInviteSignupFollowsSettings(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s

	// Only admin invites make accounts: the link still works for an account.
	setSignups(t, s, SignupAdminInvites)
	c := m.invite(t, "")
	info, err := s.GetProjectInvite(ctx, c)
	if err != nil || info.Signup {
		t.Fatalf("invite info = %+v, %v", info, err)
	}
	if _, _, err := s.SignupProjectInvite(ctx, c, AcceptInviteInput{Name: "Erin", Email: "erin@example.com", Password: "correct horse battery"}, "198.51.100.1", "ua"); depCode(err) != "project_invite_signup_off" {
		t.Errorf("signup with admin invites only: %v", err)
	}
	var n int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email = 'erin@example.com'`).Scan(&n)
	if n != 0 {
		t.Error("the account was made anyway")
	}
	if _, err := s.AcceptProjectInvite(ctx, m.bob, c); err != nil {
		t.Errorf("an existing account joins: %v", err)
	}
	// A wrong code still counts and still says the link is wrong.
	if _, _, err := s.SignupProjectInvite(ctx, "nope", AcceptInviteInput{}, "198.51.100.1", "ua"); depCode(err) != "invite_invalid" {
		t.Errorf("wrong code: %v", err)
	}

	// Project invites make accounts, without apps of their own.
	setSignups(t, s, SignupAnyInvite)
	m.exec(t, `UPDATE server_settings SET project_invite_apps = 0`)
	c = m.invite(t, "")
	if info, _ := s.GetProjectInvite(ctx, c); !info.Signup {
		t.Errorf("invite info = %+v", info)
	}
	_, out, err := s.SignupProjectInvite(ctx, c, AcceptInviteInput{Name: "Erin", Email: "erin@example.com", Password: "correct horse battery"}, "198.51.100.2", "ua")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Q.GetUser(ctx, out.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.QuotaApps != 0 || u.QuotaMemoryMb != 0 || u.QuotaCpuMillis != 0 || u.QuotaVolumeMb != 25600 {
		t.Errorf("project invite account's limits = apps %d mem %d cpu %d vol %d", u.QuotaApps, u.QuotaMemoryMb, u.QuotaCpuMillis, u.QuotaVolumeMb)
	}
	if u.JoinedVia != "project_invite" || u.JoinedBy != "usr_alice" || u.JoinedProject != m.shop.ID {
		t.Errorf("joined = %q by %q project %q", u.JoinedVia, u.JoinedBy, u.JoinedProject)
	}
	erin := Actor{UserID: u.ID, Role: "user", Channel: ChannelDashboard, Scope: ScopeSession}

	// No apps of their own, with a plain refusal...
	_, err = s.CreateApp(ctx, erin, CreateAppInput{Name: "Mine", Slug: "erin-mine"})
	if depCode(err) != "apps_quota_exceeded" || !strings.Contains(err.Error(), "can't create apps of its own") {
		t.Errorf("own app: %v", err)
	}
	// ...and no empty project either (it could only bring in more accounts)...
	if _, err := s.CreateProject(ctx, erin, CreateProjectInput{Name: "Mine"}); depCode(err) != "apps_quota_exceeded" {
		t.Errorf("own project: %v", err)
	}
	// ...but the shared project works: its apps are alice's.
	made, err := s.CreateApp(ctx, erin, CreateAppInput{Name: "Admin", Slug: "shop-admin", Project: m.shop.ID})
	if err != nil || made.OwnerID != "usr_alice" {
		t.Fatalf("app in the shared project = %+v, %v", made, err)
	}
	if _, err := s.UpdateApp(ctx, erin, "shop", UpdateAppInput{MemoryMB: i64p(1024)}); err != nil {
		t.Errorf("more memory for a shared app: %v", err)
	}
	if _, err := s.DeployFiles(ctx, erin, "shop", staticFiles()); err != nil {
		t.Errorf("deploy in the shared project: %v", err)
	}
	if _, err := s.SetEnv(ctx, erin, "shop", map[string]string{"A": "b"}, nil); err != nil {
		t.Errorf("variables in the shared project: %v", err)
	}

	// Admin > People says how they joined; the project's name follows it.
	admin := session(m.addUser(t, "usr_admin", "admin@example.com", "admin"))
	users, err := s.AdminListUsers(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, au := range users {
		if au.ID != u.ID {
			continue
		}
		j := au.Joined
		if j == nil || j.Via != "project_invite" || j.By == nil || j.By.ID != "usr_alice" || j.Project == nil || j.Project.Name != "Shop" {
			t.Errorf("joined = %+v", j)
		}
	}

	// With project_invite_apps on, a project invite account gets the defaults.
	m.exec(t, `UPDATE server_settings SET project_invite_apps = 1`)
	_, out, err = s.SignupProjectInvite(ctx, m.invite(t, ""), AcceptInviteInput{Name: "Finn", Email: "finn@example.com", Password: "correct horse battery"}, "198.51.100.3", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := s.Q.GetUser(ctx, out.User.ID); f.QuotaApps != 10 || f.QuotaMemoryMb != 2048 || f.QuotaCpuMillis != 2000 {
		t.Errorf("finn's limits = %d apps %d MB %d CPU", f.QuotaApps, f.QuotaMemoryMb, f.QuotaCpuMillis)
	}
}

func TestAdminProjectInvites(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	admin := session(m.addUser(t, "usr_admin", "admin@example.com", "admin"))
	m.invite(t, "dana@example.com")
	used := m.invite(t, "")
	if _, err := s.AcceptProjectInvite(ctx, m.bob, used); err != nil {
		t.Fatal(err)
	}

	if _, err := s.AdminListProjectInvites(ctx, m.bob); err == nil {
		t.Error("a user lists every project invite")
	}
	list, err := s.AdminListProjectInvites(ctx, admin)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	inv := list[0]
	if inv.Project.ID != m.shop.ID || inv.Project.Name != "Shop" || inv.InvitedBy == nil || inv.InvitedBy.ID != "usr_alice" || inv.Email == nil || *inv.Email != "dana@example.com" {
		t.Errorf("invite = %+v", inv)
	}

	if err := s.AdminRevokeProjectInvite(ctx, m.bob, inv.ID); err == nil {
		t.Error("a user revoked someone's invite")
	}
	if err := s.AdminRevokeProjectInvite(ctx, admin, inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminRevokeProjectInvite(ctx, admin, inv.ID); depCode(err) != "invite_not_found" {
		t.Errorf("revoked twice: %v", err)
	}
	if n := auditCount(t, s, "admin.project_invite_revoke", inv.ID); n != 1 {
		t.Errorf("%d audit entries for the revoke", n)
	}
	if pending, _ := s.ListProjectInvites(ctx, m.alice, m.shop.ID); len(pending) != 0 {
		t.Errorf("alice still sees %d invites", len(pending))
	}
	if list, _ := s.AdminListProjectInvites(ctx, admin); len(list) != 0 {
		t.Errorf("after the revoke: %+v", list)
	}
}

func i64p(v int64) *int64 { return &v }

// The signup mode is checked again in the signup's transaction: a change made
// while the password was hashed still stops the account, and the try is not
// counted against the IP.
func TestSignupModeCheckedInTransaction(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	req := func(email string, allowed func(db.ServerSetting) error) error {
		_, err := s.signup(ctx, signupReq{in: AcceptInviteInput{Name: "N", Email: email, Password: "password-1234"},
			role: "user", sigKey: "signup|203.0.113.9", via: joinedOpen, allowed: allowed})
		return err
	}
	for i := 0; i < 6; i++ {
		if err := req(fmt.Sprintf("n%d@example.nl", i), openSignupAllowed); code(err) != "signup_closed" {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	var n int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email LIKE 'n%@example.nl'`).Scan(&n)
	if n != 0 {
		t.Errorf("%d accounts made while closed", n)
	}
	setSignups(t, s, SignupOpen)
	if err := req("ok@example.nl", openSignupAllowed); err != nil {
		t.Errorf("open: %v", err)
	}
}
