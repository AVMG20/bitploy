package app

// OWNER: auth agent. Users, sessions, invites, API tokens, GitHub token,
// admin user management, audit log reads. Signatures are a shared contract
// used by internal/api and internal/mcp; implement the bodies, keep the
// signatures. Split into more files freely (accounts_*.go).

import (
	"context"
	"time"

	"github.com/arno/bitploy/internal/apperr"
)

// ---- authentication (called by internal/auth middleware) ----

// Login checks email + password (rate limited: 5 failures / 15 min per email+IP)
// and creates a session. Returns the raw cookie value and the session info.
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	return "", SessionInfo{}, apperr.NotImplemented("login")
}

// Logout deletes the session behind the cookie value.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	return apperr.NotImplemented("logout")
}

// AuthenticateSession resolves a session cookie (sliding 14 d, absolute 30 d)
// into an actor with ScopeSession and Channel dashboard. Also returns the
// session's CSRF token for the CSRF check.
func (s *Service) AuthenticateSession(ctx context.Context, cookie, ip string) (Actor, string, error) {
	return Actor{}, "", apperr.Unauthorized("not_logged_in", "You are not logged in.", "Log in to the bitploy dashboard.")
}

// AuthenticateToken resolves a bp_ bearer token into an actor (channel set by
// the caller afterwards). Records last used time and IP. Revoked, expired or
// unknown tokens → 401 token_invalid. Suspended users → 403 account_suspended.
func (s *Service) AuthenticateToken(ctx context.Context, raw, ip string) (Actor, error) {
	return Actor{}, apperr.Unauthorized("token_invalid", "This API token is not valid.", "Create a new token in the bitploy dashboard under Tokens.")
}

// GetSession returns the current user and CSRF token (GET /api/v1/session).
func (s *Service) GetSession(ctx context.Context, a Actor, cookie string) (SessionInfo, error) {
	return SessionInfo{}, apperr.NotImplemented("session")
}

// ---- account ----

// Me is whoami: user, quota, usage (from Service.Usage), domains.
func (s *Service) Me(ctx context.Context, a Actor) (Me, error) {
	return Me{}, apperr.NotImplemented("whoami")
}

func (s *Service) UpdateProfile(ctx context.Context, a Actor, name string) (User, error) {
	return User{}, apperr.NotImplemented("profile")
}

// ChangePassword deletes all other sessions of the user.
func (s *Service) ChangePassword(ctx context.Context, a Actor, cookie, current, next string) error {
	return apperr.NotImplemented("change password")
}

func (s *Service) ListSessions(ctx context.Context, a Actor, cookie string) ([]Session, error) {
	return nil, apperr.NotImplemented("sessions")
}

// SignOutEverywhere deletes every session of the user except the current one.
func (s *Service) SignOutEverywhere(ctx context.Context, a Actor, cookie string) error {
	return apperr.NotImplemented("sign out everywhere")
}

// ---- invites & signup ----

func (s *Service) CreateInvite(ctx context.Context, a Actor, in CreateInviteInput) (Invite, error) {
	return Invite{}, apperr.NotImplemented("invites")
}

func (s *Service) ListInvites(ctx context.Context, a Actor) ([]Invite, error) {
	return nil, apperr.NotImplemented("invites")
}

func (s *Service) RevokeInvite(ctx context.Context, a Actor, id string) error {
	return apperr.NotImplemented("invites")
}

// AcceptInvite creates the user and logs them in. With BITPLOY_SIGNUP=open,
// code "open" is accepted for public signup.
func (s *Service) AcceptInvite(ctx context.Context, code string, in AcceptInviteInput, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	return "", SessionInfo{}, apperr.NotImplemented("invites")
}

// CreateUser is used by `bitploy admin create-user`.
func (s *Service) CreateUser(ctx context.Context, email, name, password string, admin bool) (User, error) {
	return User{}, apperr.NotImplemented("create user")
}

// ---- API tokens (session only) ----

func (s *Service) CreateToken(ctx context.Context, a Actor, in CreateTokenInput) (TokenCreated, error) {
	return TokenCreated{}, apperr.NotImplemented("tokens")
}

func (s *Service) ListTokens(ctx context.Context, a Actor) ([]Token, error) {
	return nil, apperr.NotImplemented("tokens")
}

func (s *Service) RevokeToken(ctx context.Context, a Actor, id string) error {
	return apperr.NotImplemented("tokens")
}

// ---- GitHub token ----

// SetGitHubToken validates with GitHub GET /user, stores it encrypted.
func (s *Service) SetGitHubToken(ctx context.Context, a Actor, token string) (GitHubStatus, error) {
	return GitHubStatus{}, apperr.NotImplemented("github token")
}

func (s *Service) DeleteGitHubToken(ctx context.Context, a Actor) error {
	return apperr.NotImplemented("github token")
}

func (s *Service) GetGitHubStatus(ctx context.Context, a Actor) (GitHubStatus, error) {
	return GitHubStatus{}, apperr.NotImplemented("github token")
}

// GitHubTokenFor returns the decrypted token for server-side cloning only.
// Never log it or return it to a client. Empty string when none is set.
func (s *Service) GitHubTokenFor(ctx context.Context, userID string) (string, error) {
	return "", nil
}

// ---- admin ----

func (s *Service) AdminListUsers(ctx context.Context, a Actor) ([]AdminUser, error) {
	return nil, apperr.NotImplemented("admin")
}

func (s *Service) AdminSetQuota(ctx context.Context, a Actor, userID string, q Quota) (AdminUser, error) {
	return AdminUser{}, apperr.NotImplemented("admin")
}

// AdminSetUserStatus suspends (stops their apps, revokes nothing) or reactivates a user.
func (s *Service) AdminSetUserStatus(ctx context.Context, a Actor, userID, status string) error {
	return apperr.NotImplemented("admin")
}

func (s *Service) AdminListAudit(ctx context.Context, a Actor, limit int, before time.Time) ([]AuditEntry, error) {
	return nil, apperr.NotImplemented("admin")
}
