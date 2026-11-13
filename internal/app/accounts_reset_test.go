package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

func resetCodeOf(t *testing.T, r PasswordReset) string {
	t.Helper()
	c, ok := strings.CutPrefix(r.URL, "https://bitploy.test/reset/")
	if !ok || c == "" {
		t.Fatalf("reset url %q", r.URL)
	}
	return c
}

func TestPasswordResetLink(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	u := mustUser(t, s, "me@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	admin := sessionActor(t, s, ac)
	c1, _ := login(t, s, "me@example.nl")
	me := sessionActor(t, s, c1)
	tok, err := s.CreateToken(ctx, me, CreateTokenInput{Name: "t", Scope: ScopeDeploy})
	if err != nil {
		t.Fatal(err)
	}

	// Only admins, with a session.
	if _, err := s.CreatePasswordReset(ctx, me, u.ID); code(err) != "admin_only" {
		t.Fatalf("non-admin: %v", err)
	}
	tokenAdmin := admin
	tokenAdmin.Scope = ScopeDeploy
	if _, err := s.CreatePasswordReset(ctx, tokenAdmin, u.ID); code(err) != "admin_only" {
		t.Fatalf("admin with a token: %v", err)
	}
	if _, err := s.CreatePasswordReset(ctx, admin, "usr_nope"); code(err) != "user_not_found" {
		t.Fatalf("unknown user: %v", err)
	}
	r, err := s.CreatePasswordReset(ctx, admin, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.User.Email != "me@example.nl" || !strings.HasPrefix(r.ID, "prs_") {
		t.Fatalf("reset = %+v", r)
	}
	rc := resetCodeOf(t, r)
	var stored int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM password_resets WHERE code_hash = ?`, rc).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("the raw code is stored (%d, %v)", stored, err)
	}

	info, err := s.GetPasswordReset(ctx, rc, "10.0.0.1")
	if err != nil || info.Email != "me@example.nl" || info.Name != u.Name {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := s.GetPasswordReset(ctx, "wrong", "10.0.0.1"); code(err) != "reset_link_invalid" || apperr.As(err).HTTPStatus != 404 {
		t.Fatalf("wrong code: %v", err)
	}
	if _, _, err := s.UsePasswordReset(ctx, rc, "short", "10.0.0.1", "ua"); code(err) != "password_too_short" {
		t.Fatalf("short password: %v", err)
	}

	cookie, sess, err := s.UsePasswordReset(ctx, rc, "brand-new-pass-1", "10.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if sess.User.ID != u.ID || cookie == "" {
		t.Fatalf("session = %+v", sess)
	}
	if _, _, err := s.AuthenticateSession(ctx, cookie, "x"); err != nil {
		t.Fatalf("not logged in after the reset: %v", err)
	}
	if _, _, err := s.AuthenticateSession(ctx, c1, "x"); err == nil {
		t.Fatal("an old session survived the reset")
	}
	if _, err := s.AuthenticateToken(ctx, tok.Value, "x"); err != nil {
		t.Fatalf("the API token stopped working: %v", err)
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "9.9.9.9", "ua"); err == nil {
		t.Fatal("the old password still works")
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "brand-new-pass-1", "9.9.9.9", "ua"); err != nil {
		t.Fatal(err)
	}

	// Once only.
	if _, _, err := s.UsePasswordReset(ctx, rc, "another-pass-12", "10.0.0.1", "ua"); code(err) != "reset_link_invalid" || apperr.As(err).HTTPStatus != 410 {
		t.Fatalf("second use: %v", err)
	}
	if _, err := s.GetPasswordReset(ctx, rc, "10.0.0.1"); code(err) != "reset_link_invalid" {
		t.Fatalf("info of a used link: %v", err)
	}

	// The audit log names the link, never the code.
	entries, _, err := s.AdminListAuditPage(ctx, admin, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
		if strings.Contains(fmt.Sprint(e.Details), rc) {
			t.Fatalf("audit %s holds the code", e.Action)
		}
	}
	if !seen["admin.password_reset_link"] || !seen["user.password_reset"] {
		t.Fatalf("audit actions = %v", seen)
	}
}

func TestPasswordResetExpiresAndIsReplaced(t *testing.T) {
	s, clk := acctTestSvc(t)
	ctx := context.Background()
	admin := mustUser(t, s, "admin@example.nl", true)
	ac, _ := login(t, s, "admin@example.nl")
	adminActor := sessionActor(t, s, ac)

	// A newer link replaces the older unused one.
	old, err := s.CreatePasswordReset(ctx, adminActor, admin.ID) // for yourself is fine
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.CreatePasswordReset(ctx, adminActor, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UsePasswordReset(ctx, resetCodeOf(t, old), "brand-new-pass-1", "10.0.0.2", "ua"); code(err) != "reset_link_invalid" {
		t.Fatalf("older link: %v", err)
	}
	if _, err := s.GetPasswordReset(ctx, resetCodeOf(t, fresh), "10.0.0.2"); err != nil {
		t.Fatalf("newer link: %v", err)
	}

	// 24 hours.
	clk.Advance(24*time.Hour + time.Second)
	if _, _, err := s.UsePasswordReset(ctx, resetCodeOf(t, fresh), "brand-new-pass-1", "10.0.0.2", "ua"); code(err) != "reset_link_invalid" || apperr.As(err).HTTPStatus != 410 {
		t.Fatalf("expired link: %v", err)
	}
	if _, _, err := s.Login(ctx, "admin@example.nl", "password-1234", "9.9.9.9", "ua"); err != nil {
		t.Fatalf("the password changed: %v", err)
	}

	// The command line makes them by email, as the system.
	sys := System
	sys.Scope = ScopeSession
	r, err := s.PasswordResetForEmail(ctx, sys, " Admin@Example.nl ")
	if err != nil || r.User.ID != admin.ID {
		t.Fatalf("by email: %+v, %v", r, err)
	}
	if _, err := s.PasswordResetForEmail(ctx, sys, "nobody@example.nl"); code(err) != "user_not_found" {
		t.Fatalf("unknown email: %v", err)
	}
}

func TestPasswordResetSuspendedUser(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	u := mustUser(t, s, "me@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	admin := sessionActor(t, s, ac)
	r, err := s.CreatePasswordReset(ctx, admin, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.AdminSetUserStatus(ctx, admin, u.ID, "suspended")
	rc := resetCodeOf(t, r)
	if _, err := s.GetPasswordReset(ctx, rc, "10.0.0.3"); code(err) != "account_suspended" {
		t.Fatalf("info: %v", err)
	}
	if _, _, err := s.UsePasswordReset(ctx, rc, "brand-new-pass-1", "10.0.0.3", "ua"); code(err) != "account_suspended" {
		t.Fatalf("use: %v", err)
	}
	got, _ := s.Q.GetUser(ctx, u.ID)
	if got.Status != "suspended" {
		t.Fatalf("status = %s", got.Status)
	}
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "9.9.9.9", "ua"); code(err) != "account_suspended" {
		t.Fatalf("login: %v", err)
	}
	// Still unused: it works once the account is active again (within the 24 hours).
	if err := s.AdminSetUserStatus(ctx, admin, u.ID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UsePasswordReset(ctx, rc, "brand-new-pass-1", "10.0.0.3", "ua"); err != nil {
		t.Fatalf("after reactivation: %v", err)
	}
}

func TestPasswordResetWrongCodesAreLimited(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	u := mustUser(t, s, "me@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	r, err := s.CreatePasswordReset(ctx, sessionActor(t, s, ac), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < signupMax; i++ {
		if _, err := s.GetPasswordReset(ctx, fmt.Sprintf("guess-%d", i), "198.51.100.9"); code(err) != "reset_link_invalid" {
			t.Fatalf("guess %d: %v", i, err)
		}
	}
	if _, err := s.GetPasswordReset(ctx, resetCodeOf(t, r), "198.51.100.9"); apperr.As(err).HTTPStatus != 429 {
		t.Fatalf("after 5 wrong codes: %v", err)
	}
	if _, err := s.GetPasswordReset(ctx, resetCodeOf(t, r), "198.51.100.10"); err != nil {
		t.Fatalf("another network: %v", err)
	}
}
