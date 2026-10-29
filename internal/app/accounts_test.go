package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/config"
	"github.com/arno/bitploy/internal/store"
)

type acctClock struct{ now atomic.Int64 }

func (c *acctClock) Now() time.Time          { return time.Unix(c.now.Load(), 0).UTC() }
func (c *acctClock) Advance(d time.Duration) { c.now.Add(int64(d / time.Second)) }

// acctTestSvc builds a Service on a temp-dir SQLite database.
func acctTestSvc(t *testing.T) (*Service, *acctClock) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		PublicURL: "https://bitploy.test", AppsDomain: "bitploy.test", AppsScheme: "https",
		DataDir: dir, Signup: "invite", Dev: false,
	}
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	crypto, err := store.NewCrypto([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg, st, crypto, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clk := &acctClock{}
	clk.now.Store(time.Now().Unix())
	svc.Now = clk.Now
	return svc, clk
}

func mustUser(t *testing.T, s *Service, email string, admin bool) User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), email, "Test "+email, "password-1234", admin)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func login(t *testing.T, s *Service, email string) (string, SessionInfo) {
	t.Helper()
	c, info, err := s.Login(context.Background(), email, "password-1234", "203.0.113.5", "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	return c, info
}

func sessionActor(t *testing.T, s *Service, cookie string) Actor {
	t.Helper()
	a, _, err := s.AuthenticateSession(context.Background(), cookie, "203.0.113.5")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func code(err error) string {
	if err == nil {
		return ""
	}
	return apperr.As(err).Code
}

func TestCreateUserValidation(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "dup@example.nl", false)
	tests := []struct {
		name, email, uname, pw string
		wantCode               string
	}{
		{"ok", "ok@example.nl", "Ok", "long-enough-pw", ""},
		{"short password", "a@example.nl", "A", "123456789", "password_too_short"},
		{"exactly ten", "b@example.nl", "B", "1234567890", ""},
		{"bad email", "not-an-email", "A", "long-enough-pw", "invalid_email"},
		{"email without dot", "a@localhost", "A", "long-enough-pw", "invalid_email"},
		{"empty name", "c@example.nl", "  ", "long-enough-pw", "invalid_name"},
		{"duplicate email, other case", "DUP@example.nl", "A", "long-enough-pw", "email_taken"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.CreateUser(ctx, tt.email, tt.uname, tt.pw, false)
			if got := code(err); got != tt.wantCode {
				t.Fatalf("code = %q, want %q (err %v)", got, tt.wantCode, err)
			}
		})
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	s, clk := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)

	for i := 0; i < 5; i++ {
		_, _, err := s.Login(ctx, "me@example.nl", "wrong-password", "198.51.100.1", "ua")
		if code(err) != "invalid_credentials" {
			t.Fatalf("attempt %d: code %q", i, code(err))
		}
	}
	// Sixth try is blocked, even with the right password.
	_, _, err := s.Login(ctx, "me@example.nl", "password-1234", "198.51.100.1", "ua")
	e := apperr.As(err)
	if e.Code != "too_many_attempts" || e.HTTPStatus != 429 {
		t.Fatalf("want 429 too_many_attempts, got %v", err)
	}
	if _, ok := e.Details["retry_after_seconds"]; !ok {
		t.Fatal("429 should carry retry_after_seconds")
	}
	// Another IP and another email are unaffected.
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "198.51.100.2", "ua"); err != nil {
		t.Fatalf("other IP should work: %v", err)
	}
	// Unknown emails are limited too, and fail like a wrong password.
	for i := 0; i < 5; i++ {
		_, _, err := s.Login(ctx, "ghost@example.nl", "x", "198.51.100.1", "ua")
		if code(err) != "invalid_credentials" {
			t.Fatalf("ghost attempt %d: %q", i, code(err))
		}
	}
	if code(func() error { _, _, e := s.Login(ctx, "ghost@example.nl", "x", "198.51.100.1", "ua"); return e }()) != "too_many_attempts" {
		t.Fatal("unknown email should be rate limited as well")
	}
	// After 15 minutes the block lifts.
	clk.Advance(16 * time.Minute)
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "198.51.100.1", "ua"); err != nil {
		t.Fatalf("login after the window: %v", err)
	}
}

func TestLoginSuccessResetsFailures(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			s.Login(ctx, "me@example.nl", "nope-nope-nope", "198.51.100.1", "ua")
		}
		if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "198.51.100.1", "ua"); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, clk := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	cookie, info := login(t, s, "me@example.nl")
	if len(cookie) < 40 || info.CSRFToken == "" || info.CSRFToken == cookie {
		t.Fatalf("cookie %q csrf %q", cookie, info.CSRFToken)
	}
	// Only the hash is stored.
	var n int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id_hash = ?`, cookie).Scan(&n)
	if n != 0 {
		t.Fatal("raw cookie value stored in the database")
	}

	a, csrf, err := s.AuthenticateSession(ctx, cookie, "203.0.113.5")
	if err != nil || csrf != info.CSRFToken || a.Scope != ScopeSession || a.Channel != ChannelDashboard {
		t.Fatalf("authenticate: %+v csrf=%q err=%v", a, csrf, err)
	}
	if _, _, err := s.AuthenticateSession(ctx, "garbage", "1.2.3.4"); code(err) != "not_logged_in" {
		t.Fatalf("garbage cookie: %v", err)
	}

	// Sliding: active use every 10 days keeps it alive past 14 days.
	for i := 0; i < 2; i++ {
		clk.Advance(10 * 24 * time.Hour)
		if _, _, err := s.AuthenticateSession(ctx, cookie, "203.0.113.5"); err != nil {
			t.Fatalf("sliding step %d: %v", i, err)
		}
	}
	// Absolute: day 30 is the hard limit, however active.
	clk.Advance(10 * 24 * time.Hour)
	if _, _, err := s.AuthenticateSession(ctx, cookie, "203.0.113.5"); code(err) != "session_expired" {
		t.Fatalf("absolute limit: %v", err)
	}

	// Idle for 14 days expires.
	cookie2, _ := login(t, s, "me@example.nl")
	clk.Advance(15 * 24 * time.Hour)
	if _, _, err := s.AuthenticateSession(ctx, cookie2, "203.0.113.5"); code(err) != "session_expired" {
		t.Fatalf("idle expiry: %v", err)
	}

	// Logout deletes the row.
	cookie3, _ := login(t, s, "me@example.nl")
	if err := s.Logout(ctx, cookie3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateSession(ctx, cookie3, "x"); code(err) != "not_logged_in" {
		t.Fatalf("after logout: %v", err)
	}
}

func TestChangePasswordKeepsOnlyCurrentSession(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	c1, _ := login(t, s, "me@example.nl")
	c2, _ := login(t, s, "me@example.nl")
	a := sessionActor(t, s, c1)

	if err := s.ChangePassword(ctx, a, c1, "wrong-current", "new-password-1"); code(err) != "invalid_credentials" {
		t.Fatalf("wrong current: %v", err)
	}
	if err := s.ChangePassword(ctx, a, c1, "password-1234", "short"); code(err) != "password_too_short" {
		t.Fatalf("short new: %v", err)
	}
	if err := s.ChangePassword(ctx, a, c1, "password-1234", "new-password-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateSession(ctx, c2, "x"); err == nil {
		t.Fatal("other session survived a password change")
	}
	if _, _, err := s.AuthenticateSession(ctx, c1, "x"); err != nil {
		t.Fatalf("current session was deleted: %v", err)
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "9.9.9.9", "ua"); err == nil {
		t.Fatal("old password still works")
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "new-password-1", "9.9.9.9", "ua"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsListRevokeAndSignOutEverywhere(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	c1, _ := login(t, s, "me@example.nl")
	c2, _ := login(t, s, "me@example.nl")
	c3, _ := login(t, s, "me@example.nl")
	a := sessionActor(t, s, c1)
	list, err := s.ListSessions(ctx, a, c1)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %v %v", list, err)
	}
	cur := 0
	var other string
	for _, x := range list {
		if x.Current {
			cur++
		} else if other == "" {
			other = x.ID
		}
	}
	if cur != 1 || len(other) != 12 {
		t.Fatalf("current=%d other=%q", cur, other)
	}
	if err := s.RevokeSession(ctx, a, other); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, a, other); code(err) != "session_not_found" {
		t.Fatalf("revoking twice: %v", err)
	}
	if err := s.SignOutEverywhere(ctx, a, c1); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{c2, c3} {
		if _, _, err := s.AuthenticateSession(ctx, c, "x"); err == nil {
			t.Fatal("session survived sign-out-everywhere")
		}
	}
	if _, _, err := s.AuthenticateSession(ctx, c1, "x"); err != nil {
		t.Fatalf("current session lost: %v", err)
	}
	// A token actor cannot touch sessions.
	tokenActor := Actor{UserID: a.UserID, Scope: ScopeDeploy, Channel: ChannelAPI}
	if _, err := s.ListSessions(ctx, tokenActor, c1); code(err) != "session_required" {
		t.Fatalf("token listing sessions: %v", err)
	}
}

func TestTokensLifecycle(t *testing.T) {
	s, clk := acctTestSvc(t)
	ctx := context.Background()
	u := mustUser(t, s, "me@example.nl", false)
	c, _ := login(t, s, "me@example.nl")
	sess := sessionActor(t, s, c)

	tc, err := s.CreateToken(ctx, sess, CreateTokenInput{Name: "laptop", Scope: ScopeDeploy, ExpiresInDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tc.Value, "bp_") || tc.Prefix != tc.Value[:8] || tc.ExpiresAt == nil {
		t.Fatalf("created token: %+v", tc)
	}
	var stored int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE hash = ? AND hash != ?`, store.HashToken(tc.Value), tc.Value).Scan(&stored)
	if stored != 1 {
		t.Fatal("token hash not stored (or raw value stored)")
	}

	a, err := s.AuthenticateToken(ctx, tc.Value, "198.51.100.9")
	if err != nil || a.UserID != u.ID || a.Scope != ScopeDeploy || a.TokenID != tc.ID || a.Channel != ChannelAPI {
		t.Fatalf("authenticate: %+v %v", a, err)
	}
	list, _ := s.ListTokens(ctx, sess)
	if len(list) != 1 || list[0].LastUsedAt == nil || list[0].LastUsedIP == nil || *list[0].LastUsedIP != "198.51.100.9" {
		t.Fatalf("last used not tracked: %+v", list)
	}

	// Tokens can never create or list tokens.
	if _, err := s.CreateToken(ctx, a, CreateTokenInput{Name: "x", Scope: ScopeRead}); code(err) != "session_required" {
		t.Fatalf("token creating token: %v", err)
	}
	if _, err := s.ListTokens(ctx, a); code(err) != "session_required" {
		t.Fatalf("token listing tokens: %v", err)
	}
	if err := s.RevokeToken(ctx, a, tc.ID); code(err) != "session_required" {
		t.Fatalf("token revoking tokens: %v", err)
	}

	// Revoke takes effect on the very next request.
	if err := s.RevokeToken(ctx, sess, tc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(ctx, tc.Value, "x"); apperr.As(err).HTTPStatus != 401 || code(err) != "token_invalid" {
		t.Fatalf("revoked token: %v", err)
	}
	if err := s.RevokeToken(ctx, sess, tc.ID); err != nil {
		t.Fatalf("revoking twice should be fine: %v", err)
	}
	if err := s.RevokeToken(ctx, sess, "tok_doesnotexist"); code(err) != "token_not_found" {
		t.Fatalf("unknown token: %v", err)
	}

	// Expiry.
	short, _ := s.CreateToken(ctx, sess, CreateTokenInput{Name: "short", Scope: ScopeRead, ExpiresInDays: 30})
	clk.Advance(31 * 24 * time.Hour)
	if _, err := s.AuthenticateToken(ctx, short.Value, "x"); code(err) != "token_invalid" {
		t.Fatalf("expired token: %v", err)
	}

	// Garbage.
	for _, raw := range []string{"", "bp_", "bp_tooshort", "Bearer", strings.Repeat("x", 50)} {
		if _, err := s.AuthenticateToken(ctx, raw, "x"); code(err) != "token_invalid" {
			t.Fatalf("garbage %q: %v", raw, err)
		}
	}
}

func TestTokenValidationAndLimit(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	c, _ := login(t, s, "me@example.nl")
	sess := sessionActor(t, s, c)
	bad := []struct {
		name string
		in   CreateTokenInput
		code string
	}{
		{"no name", CreateTokenInput{Scope: ScopeRead}, "invalid_token_name"},
		{"bad scope", CreateTokenInput{Name: "x", Scope: "admin"}, "invalid_scope"},
		{"session scope", CreateTokenInput{Name: "x", Scope: ScopeSession}, "invalid_scope"},
		{"bad expiry", CreateTokenInput{Name: "x", Scope: ScopeRead, ExpiresInDays: 7}, "invalid_expiry"},
	}
	for _, tt := range bad {
		if _, err := s.CreateToken(ctx, sess, tt.in); code(err) != tt.code {
			t.Errorf("%s: code %q, want %q", tt.name, code(err), tt.code)
		}
	}
	var first TokenCreated
	for i := 0; i < 20; i++ {
		tc, err := s.CreateToken(ctx, sess, CreateTokenInput{Name: "t", Scope: ScopeRead, ExpiresInDays: 90})
		if err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
		if i == 0 {
			first = tc
		}
	}
	if _, err := s.CreateToken(ctx, sess, CreateTokenInput{Name: "t21", Scope: ScopeRead}); code(err) != "token_limit" {
		t.Fatalf("21st token: %v", err)
	}
	// Revoking one frees a slot.
	if err := s.RevokeToken(ctx, sess, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken(ctx, sess, CreateTokenInput{Name: "t21", Scope: ScopeRead}); err != nil {
		t.Fatalf("after revoke: %v", err)
	}
}

func TestSuspendedUserCannotAuthenticate(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	admin := mustUser(t, s, "admin@example.nl", true)
	u := mustUser(t, s, "me@example.nl", false)
	c, _ := login(t, s, "me@example.nl")
	sess := sessionActor(t, s, c)
	tc, _ := s.CreateToken(ctx, sess, CreateTokenInput{Name: "t", Scope: ScopeDeploy})
	ac, _ := login(t, s, "admin@example.nl")
	adminActor := sessionActor(t, s, ac)

	if err := s.AdminSetUserStatus(ctx, adminActor, admin.ID, "suspended"); code(err) != "cannot_suspend_self" {
		t.Fatalf("self suspend: %v", err)
	}
	// StopAllForUser may still be a stub in this tree; the status must change either way.
	_ = s.AdminSetUserStatus(ctx, adminActor, u.ID, "suspended")
	if _, err := s.AuthenticateToken(ctx, tc.Value, "x"); code(err) != "account_suspended" {
		t.Fatalf("suspended token: %v", err)
	}
	if _, _, err := s.AuthenticateSession(ctx, c, "x"); code(err) != "account_suspended" {
		t.Fatalf("suspended session: %v", err)
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "1.1.1.1", "ua"); code(err) != "account_suspended" {
		t.Fatalf("suspended login: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, adminActor, u.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateToken(ctx, tc.Value, "x"); err != nil {
		t.Fatalf("after reactivation: %v", err)
	}
	// Non-admins cannot.
	if err := s.AdminSetUserStatus(ctx, sess, admin.ID, "suspended"); code(err) != "admin_only" {
		t.Fatalf("non-admin: %v", err)
	}
	if err := s.AdminSetUserStatus(ctx, adminActor, u.ID, "banana"); code(err) != "invalid_status" {
		t.Fatalf("bad status: %v", err)
	}
}

func TestInvites(t *testing.T) {
	s, clk := acctTestSvc(t)
	ctx := context.Background()
	admin := mustUser(t, s, "admin@example.nl", true)
	user := mustUser(t, s, "user@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	adminActor := sessionActor(t, s, ac)
	uc, _ := login(t, s, "user@example.nl")
	userActor := sessionActor(t, s, uc)
	_ = admin
	_ = user

	if _, err := s.CreateInvite(ctx, userActor, CreateInviteInput{}); code(err) != "admin_only" {
		t.Fatalf("user creating invite: %v", err)
	}
	inv, err := s.CreateInvite(ctx, adminActor, CreateInviteInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(inv.URL, "https://bitploy.test/invite/") || inv.ExpiresAt.Sub(inv.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("invite: %+v", inv)
	}
	invCode := strings.TrimPrefix(inv.URL, "https://bitploy.test/invite/")
	list, _ := s.ListInvites(ctx, adminActor)
	if len(list) != 1 || list[0].URL != "" {
		t.Fatalf("list must not leak the code: %+v", list)
	}

	in := AcceptInviteInput{Name: "New Person", Email: "new@example.nl", Password: "password-1234"}
	if _, _, err := s.AcceptInvite(ctx, "wrongcode", in, "1.1.1.1", "ua"); code(err) != "invite_invalid" {
		t.Fatalf("wrong code: %v", err)
	}
	if _, _, err := s.AcceptInvite(ctx, invCode, AcceptInviteInput{Name: "N", Email: "new@example.nl", Password: "short"}, "1.1.1.1", "ua"); code(err) != "password_too_short" {
		t.Fatalf("short password: %v", err)
	}
	cookie, info, err := s.AcceptInvite(ctx, invCode, in, "1.1.1.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if info.User.Role != "user" || info.User.Email != "new@example.nl" || cookie == "" || info.CSRFToken == "" {
		t.Fatalf("accepted: %+v", info)
	}
	// Single use.
	in2 := AcceptInviteInput{Name: "Other", Email: "other@example.nl", Password: "password-1234"}
	if _, _, err := s.AcceptInvite(ctx, invCode, in2, "1.1.1.1", "ua"); apperr.As(err).HTTPStatus != 410 || code(err) != "invite_invalid" {
		t.Fatalf("reuse: %v", err)
	}
	if _, _, err := s.Login(ctx, "other@example.nl", "password-1234", "1.1.1.1", "ua"); err == nil {
		t.Fatal("second signup created a user")
	}

	// Expiry after 7 days.
	inv2, _ := s.CreateInvite(ctx, adminActor, CreateInviteInput{})
	code2 := strings.TrimPrefix(inv2.URL, "https://bitploy.test/invite/")
	clk.Advance(7*24*time.Hour + time.Minute)
	if _, _, err := s.AcceptInvite(ctx, code2, in2, "1.1.1.1", "ua"); apperr.As(err).HTTPStatus != 410 {
		t.Fatalf("expired: %v", err)
	}

	// Locked to an email, with an admin role.
	inv3, _ := s.CreateInvite(ctx, adminActor, CreateInviteInput{Email: "Locked@Example.nl", Role: "admin"})
	code3 := strings.TrimPrefix(inv3.URL, "https://bitploy.test/invite/")
	if _, _, err := s.AcceptInvite(ctx, code3, AcceptInviteInput{Name: "X", Email: "someone-else@example.nl", Password: "password-1234"}, "1.1.1.1", "ua"); code(err) != "invite_email_mismatch" {
		t.Fatalf("mismatch: %v", err)
	}
	_, info3, err := s.AcceptInvite(ctx, code3, AcceptInviteInput{Name: "X", Password: "password-1234"}, "1.1.1.1", "ua")
	if err != nil || info3.User.Email != "locked@example.nl" || info3.User.Role != "admin" {
		t.Fatalf("locked invite: %+v %v", info3, err)
	}

	// Existing email is refused and the invite stays usable.
	inv4, _ := s.CreateInvite(ctx, adminActor, CreateInviteInput{})
	code4 := strings.TrimPrefix(inv4.URL, "https://bitploy.test/invite/")
	if _, _, err := s.AcceptInvite(ctx, code4, AcceptInviteInput{Name: "X", Email: "user@example.nl", Password: "password-1234"}, "1.1.1.1", "ua"); code(err) != "email_taken" {
		t.Fatalf("taken: %v", err)
	}
	if _, _, err := s.AcceptInvite(ctx, code4, AcceptInviteInput{Name: "X", Email: "fresh@example.nl", Password: "password-1234"}, "1.1.1.1", "ua"); err != nil {
		t.Fatalf("invite should survive a failed attempt: %v", err)
	}

	// Revoke.
	inv5, _ := s.CreateInvite(ctx, adminActor, CreateInviteInput{})
	if err := s.RevokeInvite(ctx, adminActor, inv5.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptInvite(ctx, strings.TrimPrefix(inv5.URL, "https://bitploy.test/invite/"), in2, "1.1.1.1", "ua"); code(err) != "invite_invalid" {
		t.Fatalf("revoked: %v", err)
	}
}

func TestOpenSignup(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	in := AcceptInviteInput{Name: "Open", Email: "open@example.nl", Password: "password-1234"}
	if _, _, err := s.AcceptInvite(ctx, "open", in, "1.1.1.1", "ua"); code(err) != "invite_invalid" {
		t.Fatalf("open code in invite mode: %v", err)
	}
	s.Cfg.Signup = "open"
	_, info, err := s.AcceptInvite(ctx, "open", in, "1.1.1.1", "ua")
	if err != nil || info.User.Role != "user" {
		t.Fatalf("open signup: %+v %v", info, err)
	}
	in.Email = "open2@example.nl"
	if _, _, err := s.AcceptInvite(ctx, "open", in, "1.1.1.1", "ua"); err != nil {
		t.Fatalf("open signup is reusable: %v", err)
	}
}

func TestGitHubToken(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	u := mustUser(t, s, "me@example.nl", false)
	c, _ := login(t, s, "me@example.nl")
	sess := sessionActor(t, s, c)

	const secret = "github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz"
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("github-authentication-token-expiration", "2030-01-02 03:04:05 UTC")
		w.Write([]byte(`{"login":"octocat"}`))
	}))
	defer gh.Close()
	old := acctGithubAPI
	acctGithubAPI = gh.URL
	defer func() { acctGithubAPI = old }()

	if st, _ := s.GetGitHubStatus(ctx, sess); st.Connected {
		t.Fatal("connected before setting")
	}
	if tok, err := s.GitHubTokenFor(ctx, u.ID); tok != "" || err != nil {
		t.Fatalf("token before set: %q %v", tok, err)
	}
	if _, err := s.SetGitHubToken(ctx, sess, "github_pat_wrongwrongwrongwrongwrong"); code(err) != "github_token_invalid" {
		t.Fatalf("bad token: %v", err)
	}
	st, err := s.SetGitHubToken(ctx, sess, secret)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected || st.Login != "octocat" || st.ExpiresAt == nil || st.ExpiresAt.Year() != 2030 {
		t.Fatalf("status: %+v", st)
	}
	// Stored encrypted, bound to the user, never in the clear.
	var enc []byte
	s.Store.DB.QueryRow(`SELECT github_token_enc FROM users WHERE id = ?`, u.ID).Scan(&enc)
	if len(enc) == 0 || strings.Contains(string(enc), "github_pat") {
		t.Fatal("token not encrypted at rest")
	}
	if _, err := s.Crypto.Open(enc, "github:"+u.ID); err != nil {
		t.Fatalf("not sealed with github:<userID>: %v", err)
	}
	if tok, err := s.GitHubTokenFor(ctx, u.ID); tok != secret || err != nil {
		t.Fatalf("GitHubTokenFor: %q %v", tok, err)
	}
	me, _ := s.Me(ctx, sess)
	if me.User.GitHubLogin == nil || *me.User.GitHubLogin != "octocat" {
		t.Fatalf("me: %+v", me.User)
	}
	// A token actor cannot set or delete it.
	tokenActor := Actor{UserID: u.ID, Scope: ScopeDeploy, Channel: ChannelAPI}
	if _, err := s.SetGitHubToken(ctx, tokenActor, secret); code(err) != "session_required" {
		t.Fatalf("token actor: %v", err)
	}
	if err := s.DeleteGitHubToken(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.GetGitHubStatus(ctx, sess); st.Connected {
		t.Fatal("still connected after delete")
	}
	if tok, _ := s.GitHubTokenFor(ctx, u.ID); tok != "" {
		t.Fatal("token survived delete")
	}
	// The secret is nowhere in the audit log.
	var leaked int
	s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE details LIKE '%github_pat%'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatal("GitHub token leaked into the audit log")
	}
}

func TestAdminQuotaUsersAndAudit(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	u := mustUser(t, s, "user@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	admin := sessionActor(t, s, ac)
	uc, _ := login(t, s, "user@example.nl")
	user := sessionActor(t, s, uc)

	users, err := s.AdminListUsers(ctx, admin)
	if err != nil || len(users) != 2 {
		t.Fatalf("users: %v %v", users, err)
	}
	if _, err := s.AdminListUsers(ctx, user); code(err) != "admin_only" {
		t.Fatalf("non admin: %v", err)
	}
	// Admin rights need a session: a token never counts as admin.
	adminToken := Actor{UserID: admin.UserID, Role: "admin", Scope: ScopeDeploy, Channel: ChannelAPI}
	if _, err := s.AdminListUsers(ctx, adminToken); code(err) != "admin_only" {
		t.Fatalf("admin token: %v", err)
	}
	q := Quota{Apps: 3, MemoryMB: 1024, CPUMillis: 1000, DeploysPerHour: 5, DomainsPerApp: 2, VolumeMB: 100}
	au, err := s.AdminSetQuota(ctx, admin, u.ID, q)
	if err != nil || au.Quota != q {
		t.Fatalf("set quota: %+v %v", au, err)
	}
	me, _ := s.Me(ctx, user)
	if me.Quota != q {
		t.Fatalf("me quota: %+v", me.Quota)
	}
	if _, err := s.AdminSetQuota(ctx, admin, u.ID, Quota{Apps: -1}); code(err) != "invalid_quota" {
		t.Fatalf("negative: %v", err)
	}
	if _, err := s.AdminSetQuota(ctx, admin, "usr_nope", q); code(err) != "user_not_found" {
		t.Fatalf("unknown user: %v", err)
	}

	entries, err := s.AdminListAuditPage(ctx, admin, 100, 0)
	if err != nil || len(entries) == 0 {
		t.Fatalf("audit: %v %v", entries, err)
	}
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
	}
	for _, want := range []string{"user.create", "auth.login", "admin.quota_set"} {
		if !actions[want] {
			t.Errorf("audit log has no %q entry; got %v", want, actions)
		}
	}
	// Paging by id.
	page1, _ := s.AdminListAuditPage(ctx, admin, 2, 0)
	page2, _ := s.AdminListAuditPage(ctx, admin, 2, page1[len(page1)-1].ID)
	if len(page1) != 2 || len(page2) == 0 || page2[0].ID >= page1[1].ID {
		t.Fatalf("paging: %v / %v", page1, page2)
	}
	if _, err := s.AdminListAudit(ctx, user, 10, time.Time{}); code(err) != "admin_only" {
		t.Fatalf("audit non admin: %v", err)
	}
}

func TestAuditNeverContainsSecrets(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	c, _ := login(t, s, "me@example.nl")
	sess := sessionActor(t, s, c)
	tc, _ := s.CreateToken(ctx, sess, CreateTokenInput{Name: "t", Scope: ScopeRead})
	s.Login(ctx, "me@example.nl", "totally-wrong-password", "1.1.1.1", "ua")
	rows, err := s.Store.DB.Query(`SELECT details FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		rows.Scan(&d)
		if strings.Contains(d, tc.Value) || strings.Contains(d, "password-1234") || strings.Contains(d, "totally-wrong") {
			t.Fatalf("secret in audit details: %s", d)
		}
	}
}
