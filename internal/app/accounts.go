package app

// OWNER: auth agent. Users, sessions, invites, API tokens, GitHub token,
// admin user management, audit log reads. Signatures are a shared contract
// used by internal/api and internal/mcp. This file holds authentication,
// sessions and the account itself; see accounts_*.go for the rest.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth/passwd"
	"github.com/AVMG20/bitploy/internal/auth/tokens"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	sessionSliding  = 14 * 24 * time.Hour
	sessionAbsolute = 30 * 24 * time.Hour
	// sessions are only written back at most this often to spare the database.
	sessionTouchEvery = 60 // seconds
	tokenTouchEvery   = 60 // seconds
)

// ---- authentication (called by internal/auth middleware) ----

// Login checks email + password and creates a session. Returns the raw cookie
// value and the session info.
//
// Limits (in memory, per process): 5 failures / 15 min per email+IP, 30
// attempts / 15 min per IP over all emails, 20 failures / hour per email over
// all IPs. An attempt is reserved atomically BEFORE the password is checked, so
// a burst of parallel requests cannot get more tries than the limit; the
// reservations on the IP and the email are given back on a correct password.
func (s *Service) Login(ctx context.Context, email, password, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return "", SessionInfo{}, apperr.BadRequest("credentials_missing",
			"Email and password are both required.", "Send both fields: {\"email\": \"...\", \"password\": \"...\"}.")
	}
	// No stored email is longer than 254 bytes and no accepted password longer
	// than passwd.MaxLength, so such a login can only fail. Answer at once:
	// the email goes into limiter keys and the audit log, and a request body
	// of a megabyte per attempt would otherwise pile up in memory and the database.
	if len(email) > 254 || len(password) > passwd.MaxLength {
		return "", SessionInfo{}, apperr.Unauthorized("invalid_credentials",
			"The email or password is wrong.",
			"Check both and try again. After 5 failed tries you have to wait 15 minutes.")
	}
	lim := s.limiter()
	now := s.now()
	keyEmailIP, keyIP, keyEmail := "login|"+email+"|"+ip, "loginip|"+ip, "loginemail|"+email
	if wait, ok := lim.reserve(keyIP, now, loginIPMax, loginIPWindow); !ok {
		return "", SessionInfo{}, tooManyLogins(wait)
	}
	if wait, ok := lim.reserve(keyEmailIP, now, loginMaxFailures, loginWindow); !ok {
		lim.refund(keyIP)
		return "", SessionInfo{}, tooManyLogins(wait)
	}
	if wait, ok := lim.reserve(keyEmail, now, loginEmailMax, loginEmailWindow); !ok {
		lim.refund(keyIP)
		lim.refund(keyEmailIP)
		return "", SessionInfo{}, tooManyLogins(wait)
	}
	giveBack := func() { // the attempt was not a failure
		lim.refund(keyIP)
		lim.refund(keyEmail)
		lim.refund(keyEmailIP)
	}
	u, err := s.Q.GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		if _, verr := passwd.Verify(password, passwd.Dummy()); errors.Is(verr, passwd.ErrBusy) { // equalise timing
			giveBack()
			return "", SessionInfo{}, acctBusy(verr)
		}
		return "", SessionInfo{}, s.loginFailed(ctx, "", email, ip)
	}
	if err != nil {
		giveBack()
		return "", SessionInfo{}, apperr.Internal(err)
	}
	ok, verr := passwd.Verify(password, u.PasswordHash)
	if errors.Is(verr, passwd.ErrBusy) {
		giveBack()
		return "", SessionInfo{}, acctBusy(verr)
	}
	if verr != nil {
		s.Log.Error("stored password hash is invalid", "user", u.ID, "err", verr)
	}
	if !ok {
		return "", SessionInfo{}, s.loginFailed(ctx, u.ID, email, ip)
	}
	lim.reset(keyEmailIP)
	lim.refund(keyIP)
	lim.refund(keyEmail)
	if u.Status == "suspended" {
		return "", SessionInfo{}, acctSuspended()
	}
	cookie, csrf, err := s.newSession(ctx, u.ID, ip, userAgent)
	if err != nil {
		return "", SessionInfo{}, err
	}
	s.Audit(ctx, acctActorFor(u, ip), "auth.login", "user", u.ID, nil)
	return cookie, SessionInfo{User: acctUserFromRow(u), CSRFToken: csrf}, nil
}

// acctBusy maps passwd.ErrBusy (argon2 queue full) to 429 with Retry-After.
func acctBusy(err error) *apperr.Error {
	return apperr.TooMany("too_many_requests",
		"bitploy is busy checking passwords right now.", "Try again in a few seconds.").
		WithDetail("retry_after_seconds", int64(5)).WithCause(err)
}

// acctHashErr turns an error from passwd.Hash into an apperr.
func acctHashErr(err error) *apperr.Error {
	if errors.Is(err, passwd.ErrBusy) {
		return acctBusy(err)
	}
	return apperr.Internal(err)
}

func (s *Service) loginFailed(ctx context.Context, userID, email, ip string) error {
	s.Audit(ctx, Actor{UserID: userID, Channel: ChannelDashboard, IP: ip}, "auth.login_failed", "user", userID, map[string]any{"email": email})
	return apperr.Unauthorized("invalid_credentials",
		"The email or password is wrong.",
		"Check both and try again. After 5 failed tries you have to wait 15 minutes.")
}

func tooManyLogins(wait int64) *apperr.Error {
	mins := (wait + 59) / 60
	if mins < 1 {
		mins = 1
	}
	return apperr.TooMany("too_many_attempts",
		"Too many login attempts for this email or from this network.",
		"Wait about "+acctItoa(mins)+" minute(s) and try again.").WithDetail("retry_after_seconds", wait)
}

func acctSuspended() *apperr.Error {
	return apperr.Forbidden("account_suspended",
		"This account is suspended.", "Ask a bitploy admin to reactivate it.")
}

func (s *Service) newSession(ctx context.Context, userID, ip, userAgent string) (cookie, csrf string, err error) {
	cookie, err = acctRandomToken()
	if err != nil {
		return "", "", apperr.Internal(err)
	}
	csrf, err = acctRandomToken()
	if err != nil {
		return "", "", apperr.Internal(err)
	}
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	now := s.Now()
	err = s.Q.InsertSession(ctx, db.InsertSessionParams{
		IDHash:            store.HashToken(cookie),
		UserID:            userID,
		CsrfToken:         csrf,
		Ip:                ip,
		UserAgent:         userAgent,
		CreatedAt:         now.Unix(),
		LastSeenAt:        now.Unix(),
		ExpiresAt:         now.Add(sessionSliding).Unix(),
		AbsoluteExpiresAt: now.Add(sessionAbsolute).Unix(),
	})
	if err != nil {
		return "", "", apperr.Internal(err)
	}
	return cookie, csrf, nil
}

// acctRandomToken is 32 random bytes, URL-safe base64 (43 characters).
func acctRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Logout deletes the session behind the cookie value.
func (s *Service) Logout(ctx context.Context, cookie string) error {
	if cookie == "" {
		return nil
	}
	h := store.HashToken(cookie)
	sess, err := s.Q.GetSessionByHash(ctx, h)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.Q.DeleteSessionByHash(ctx, h); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, Actor{UserID: sess.UserID, Channel: ChannelDashboard, Scope: ScopeSession, IP: sess.Ip}, "auth.logout", "session", acctShortID(h), nil)
	return nil
}

func acctNotLoggedIn() *apperr.Error {
	return apperr.Unauthorized("not_logged_in", "You are not logged in.", "Log in to the bitploy dashboard.")
}

// AuthenticateSession resolves a session cookie (sliding 14 d, absolute 30 d)
// into an actor with ScopeSession and Channel dashboard. Also returns the
// session's CSRF token for the CSRF check.
func (s *Service) AuthenticateSession(ctx context.Context, cookie, ip string) (Actor, string, error) {
	if cookie == "" {
		return Actor{}, "", acctNotLoggedIn()
	}
	h := store.HashToken(cookie)
	sess, err := s.Q.GetSessionByHash(ctx, h)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, "", acctNotLoggedIn()
	}
	if err != nil {
		return Actor{}, "", apperr.Internal(err)
	}
	now := s.now()
	if sess.ExpiresAt <= now || sess.AbsoluteExpiresAt <= now {
		_ = s.Q.DeleteSessionByHash(ctx, h)
		return Actor{}, "", apperr.Unauthorized("session_expired", "Your session has expired.", "Log in again.")
	}
	u, err := s.Q.GetUser(ctx, sess.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, "", acctNotLoggedIn()
	}
	if err != nil {
		return Actor{}, "", apperr.Internal(err)
	}
	if u.Status == "suspended" {
		return Actor{}, "", acctSuspended()
	}
	if now-sess.LastSeenAt >= sessionTouchEvery {
		exp := now + int64(sessionSliding/time.Second)
		if exp > sess.AbsoluteExpiresAt {
			exp = sess.AbsoluteExpiresAt
		}
		if err := s.Q.TouchSession(ctx, db.TouchSessionParams{LastSeenAt: now, ExpiresAt: exp, IDHash: h}); err != nil {
			s.Log.Warn("session touch failed", "err", err)
		}
	}
	return acctActorFor(u, ip), sess.CsrfToken, nil
}

func acctActorFor(u db.User, ip string) Actor {
	return Actor{UserID: u.ID, Role: u.Role, Channel: ChannelDashboard, Scope: ScopeSession, IP: ip}
}

// AuthenticateToken resolves a bp_ bearer token into an actor (channel set by
// the caller afterwards). Records last used time and IP. Revoked, expired or
// unknown tokens -> 401 token_invalid. Suspended users -> 403 account_suspended.
func (s *Service) AuthenticateToken(ctx context.Context, raw, ip string) (Actor, error) {
	invalid := apperr.Unauthorized("token_invalid", "This API token is not valid.",
		"It may be revoked, expired or mistyped. Create a new token in the bitploy dashboard under Tokens.")
	if !tokens.WellFormed(raw) {
		return Actor{}, invalid
	}
	t, err := s.Q.GetTokenByHash(ctx, tokens.Hash(raw))
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, invalid
	}
	if err != nil {
		return Actor{}, apperr.Internal(err)
	}
	now := s.now()
	if t.RevokedAt.Valid || (t.ExpiresAt.Valid && t.ExpiresAt.Int64 <= now) {
		return Actor{}, invalid
	}
	u, err := s.Q.GetUser(ctx, t.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, invalid
	}
	if err != nil {
		return Actor{}, apperr.Internal(err)
	}
	if u.Status == "suspended" {
		return Actor{}, acctSuspended()
	}
	if !t.LastUsedAt.Valid || now-t.LastUsedAt.Int64 >= tokenTouchEvery || t.LastUsedIp.String != ip {
		if err := s.Q.TouchToken(ctx, db.TouchTokenParams{LastUsedAt: nullInt(now, true), LastUsedIp: nullStr(ip), ID: t.ID}); err != nil {
			s.Log.Warn("token touch failed", "err", err)
		}
	}
	return Actor{
		UserID:  u.ID,
		Role:    u.Role,
		Channel: ChannelAPI,
		Scope:   Scope(t.Scope),
		TokenID: t.ID,
		IP:      ip,
	}, nil
}

// GetSession returns the current user and CSRF token (GET /api/v1/session).
func (s *Service) GetSession(ctx context.Context, a Actor, cookie string) (SessionInfo, error) {
	if err := RequireSession(a); err != nil {
		return SessionInfo{}, err
	}
	sess, err := s.Q.GetSessionByHash(ctx, store.HashToken(cookie))
	if err != nil || sess.UserID != a.UserID {
		return SessionInfo{}, acctNotLoggedIn()
	}
	u, err := s.Q.GetUser(ctx, a.UserID)
	if err != nil {
		return SessionInfo{}, acctNotLoggedIn()
	}
	return SessionInfo{User: acctUserFromRow(u), CSRFToken: sess.CsrfToken}, nil
}

// ---- account ----

// Me is whoami: user, quota, usage (from Service.Usage), domains.
func (s *Service) Me(ctx context.Context, a Actor) (Me, error) {
	u, err := s.Q.GetUser(ctx, a.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Me{}, acctNotLoggedIn()
	}
	if err != nil {
		return Me{}, apperr.Internal(err)
	}
	usage, err := s.Usage(ctx, a.UserID)
	if err != nil {
		return Me{}, apperr.As(err)
	}
	return Me{
		User:           acctUserFromRow(u),
		Quota:          acctQuotaFromRow(u),
		Usage:          usage,
		AppsDomain:     s.Cfg.AppsDomain,
		AppURLTemplate: s.Cfg.AppURL("{slug}"),
		AppDomains:     s.appDomainChoices(ctx, a, u),
		PublicURL:      s.Cfg.PublicURL,
		Scope:          a.Scope,
	}, nil
}

func (s *Service) UpdateProfile(ctx context.Context, a Actor, name string) (User, error) {
	if err := RequireSession(a); err != nil {
		return User{}, err
	}
	name, err := acctValidName(name)
	if err != nil {
		return User{}, err
	}
	if err := s.Q.UpdateUserName(ctx, db.UpdateUserNameParams{Name: name, UpdatedAt: s.now(), ID: a.UserID}); err != nil {
		return User{}, apperr.Internal(err)
	}
	u, err := s.Q.GetUser(ctx, a.UserID)
	if err != nil {
		return User{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "user.update", "user", a.UserID, map[string]any{"name": name})
	return acctUserFromRow(u), nil
}

// ChangePassword deletes all other sessions of the user.
func (s *Service) ChangePassword(ctx context.Context, a Actor, cookie, current, next string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	lim := s.limiter()
	key := "pw|" + a.UserID
	// Reserve the attempt before verifying so parallel guesses cannot exceed the limit.
	if wait, ok := lim.reserve(key, s.now(), loginMaxFailures, loginWindow); !ok {
		return tooManyLogins(wait)
	}
	u, err := s.Q.GetUser(ctx, a.UserID)
	if err != nil {
		return apperr.Internal(err)
	}
	ok, verr := passwd.Verify(current, u.PasswordHash)
	if errors.Is(verr, passwd.ErrBusy) {
		lim.refund(key)
		return acctBusy(verr)
	}
	if !ok {
		return apperr.Unauthorized("invalid_credentials", "The current password is wrong.", "Type your current password again.")
	}
	lim.reset(key)
	if err := acctValidPassword(next); err != nil {
		return err
	}
	hash, err := passwd.Hash(next)
	if err != nil {
		return acctHashErr(err)
	}
	if err := s.Q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: hash, UpdatedAt: s.now(), ID: a.UserID}); err != nil {
		return apperr.Internal(err)
	}
	if err := s.Q.DeleteOtherSessionsOfUser(ctx, db.DeleteOtherSessionsOfUserParams{UserID: a.UserID, IDHash: store.HashToken(cookie)}); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "user.password_change", "user", a.UserID, nil)
	return nil
}

func (s *Service) ListSessions(ctx context.Context, a Actor, cookie string) ([]Session, error) {
	if err := RequireSession(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListSessionsOfUser(ctx, a.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	cur := store.HashToken(cookie)
	now := s.now()
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		if r.ExpiresAt <= now || r.AbsoluteExpiresAt <= now {
			continue
		}
		out = append(out, Session{
			ID: acctShortID(r.IDHash), Current: r.IDHash == cur, IP: r.Ip, UserAgent: r.UserAgent,
			CreatedAt: ts(r.CreatedAt), LastSeenAt: ts(r.LastSeenAt),
		})
	}
	return out, nil
}

// RevokeSession signs out one session of the user (by the id from ListSessions).
func (s *Service) RevokeSession(ctx context.Context, a Actor, id string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	n, err := s.Q.DeleteSessionByPrefix(ctx, db.DeleteSessionByPrefixParams{UserID: a.UserID, IDHash: id})
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return apperr.NotFound("session_not_found", "There is no such session.", "List your sessions with GET /api/v1/sessions.")
	}
	s.Audit(ctx, a, "session.revoke", "session", id, nil)
	return nil
}

// SignOutEverywhere deletes every session of the user except the current one.
func (s *Service) SignOutEverywhere(ctx context.Context, a Actor, cookie string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	if err := s.Q.DeleteOtherSessionsOfUser(ctx, db.DeleteOtherSessionsOfUserParams{UserID: a.UserID, IDHash: store.HashToken(cookie)}); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "session.revoke_others", "user", a.UserID, nil)
	return nil
}

// CreateUser is used by `bitploy admin create-user`. The account gets the
// limits for new accounts (server settings).
func (s *Service) CreateUser(ctx context.Context, email, name, password string, admin bool) (User, error) {
	email, err := acctValidEmail(email)
	if err != nil {
		return User{}, err
	}
	name, err = acctValidName(name)
	if err != nil {
		return User{}, err
	}
	if err := acctValidPassword(password); err != nil {
		return User{}, err
	}
	role := "user"
	if admin {
		role = "admin"
	}
	st, err := s.serverSettings(ctx, s.Q)
	if err != nil {
		return User{}, err
	}
	u, err := s.insertUser(ctx, acctNewUser{Email: email, Name: name, Password: password, Role: role,
		Quota: newAccountQuota(st, joinedSetup), JoinedVia: joinedSetup})
	if err != nil {
		return User{}, err
	}
	s.Audit(ctx, Actor{Channel: ChannelSystem}, "user.create", "user", u.ID, map[string]any{"email": email, "role": role})
	return acctUserFromRow(u), nil
}

// insertUser hashes the password and inserts the user, optionally inside tx.
func (s *Service) insertUser(ctx context.Context, p acctNewUser) (db.User, error) {
	hash, err := passwd.Hash(p.Password)
	if err != nil {
		return db.User{}, acctHashErr(err)
	}
	return s.insertUserHashed(ctx, s.Q, p, hash)
}

func (s *Service) insertUserHashed(ctx context.Context, q *db.Queries, p acctNewUser, hash string) (db.User, error) {
	id := ids.New(ids.User)
	now := s.now()
	err := q.InsertUser(ctx, db.InsertUserParams{
		ID: id, Email: p.Email, Name: p.Name, PasswordHash: hash, Role: p.Role,
		QuotaApps: p.Quota.Apps, QuotaMemoryMb: p.Quota.MemoryMB, QuotaCpuMillis: p.Quota.CPUMillis,
		QuotaDomainsPerApp: p.Quota.DomainsPerApp, QuotaVolumeMb: p.Quota.VolumeMB,
		JoinedVia: p.JoinedVia, JoinedBy: p.JoinedBy, JoinedProject: p.JoinedProject,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		if acctIsUnique(err) {
			return db.User{}, apperr.Conflict("email_taken", "An account with this email already exists.",
				"Log in instead, or use a different email address.")
		}
		return db.User{}, apperr.Internal(err)
	}
	u, err := q.GetUser(ctx, id)
	if err != nil {
		return db.User{}, apperr.Internal(err)
	}
	return u, nil
}

// ---- background upkeep ----

// RunAccountsMaintenance prunes expired sessions, invites, password reset
// links, idempotency keys
// and audit rows older than 90 days. It returns when ctx ends.
func (s *Service) RunAccountsMaintenance(ctx context.Context) {
	run := func() {
		start := s.Now()
		now := start.Unix()
		// One failing delete does not stop the others; the Jobs tab shows what failed.
		err := errors.Join(
			s.Q.DeleteExpiredSessions(ctx, db.DeleteExpiredSessionsParams{ExpiresAt: now, AbsoluteExpiresAt: now}),
			s.Q.DeleteOldInvites(ctx, db.DeleteOldInvitesParams{ExpiresAt: now - 30*86400, UsedAt: nullInt(now-30*86400, true)}),
			s.Q.DeleteOldProjectInvites(ctx, db.DeleteOldProjectInvitesParams{ExpiresAt: now - 30*86400, UsedAt: nullInt(now-30*86400, true)}),
			s.Q.DeleteOldPasswordResets(ctx, db.DeleteOldPasswordResetsParams{ExpiresAt: now - 30*86400, UsedAt: nullInt(now-30*86400, true)}),
			s.Q.DeleteOldIdempotencyKeys(ctx, now-86400),
			s.Q.DeleteOldAudit(ctx, now-90*86400),
			s.Q.PruneGitHubDeliveries(ctx, now-7*86400),
		)
		s.jobRan("accounts_cleanup", start, err, "")
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// ---- login / signup rate limits (in memory) ----

const (
	loginMaxFailures = 5
	loginWindow      = 15 * 60 // seconds; per email+IP
	loginIPMax       = 30
	loginIPWindow    = 15 * 60 // per IP, all emails
	loginEmailMax    = 20
	loginEmailWindow = 60 * 60 // per email, all IPs
	signupMax        = 5
	signupWindow     = 60 * 60 // per IP

	// limiterKeep is the longest window; older entries are dropped.
	limiterKeep = 60 * 60
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]int64
}

var limiters sync.Map // *Service -> *loginLimiter

func (s *Service) limiter() *loginLimiter {
	if l, ok := limiters.Load(s); ok {
		return l.(*loginLimiter)
	}
	l, _ := limiters.LoadOrStore(s, &loginLimiter{fails: map[string][]int64{}})
	return l.(*loginLimiter)
}

// recent returns the entries of key younger than window (and forgets entries
// older than limiterKeep). The caller holds l.mu.
func (l *loginLimiter) recent(key string, now, window int64) []int64 {
	var keep, inWindow []int64
	for _, t := range l.fails[key] {
		if now-t < limiterKeep {
			keep = append(keep, t)
			if now-t < window {
				inWindow = append(inWindow, t)
			}
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = keep
	}
	return inWindow
}

// reserve atomically counts one attempt for key unless max attempts already
// happened within window; then it says for how many more seconds key is blocked.
func (l *loginLimiter) reserve(key string, now int64, max int, window int64) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.recent(key, now, window)
	if len(f) >= max {
		return f[0] + window - now, false
	}
	if len(l.fails) > 10000 { // bound memory under a spray of random emails
		for k := range l.fails {
			l.recent(k, now, limiterKeep)
		}
	}
	l.fails[key] = append(l.fails[key], now)
	return 0, true
}

// refund gives back the newest reservation of key.
func (l *loginLimiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if f := l.fails[key]; len(f) > 1 {
		l.fails[key] = f[:len(f)-1]
	} else {
		delete(l.fails, key)
	}
}

// full reports whether key already holds max entries within window, without counting.
func (l *loginLimiter) full(key string, now int64, max int, window int64) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.recent(key, now, window)
	if len(f) < max {
		return 0, false
	}
	return f[0] + window - now, true
}

// add counts one attempt without checking.
func (l *loginLimiter) add(key string, now int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[key] = append(l.fails[key], now)
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// UpdateEmail changes the login email of the signed-in user. The current
// password is required: a stolen session cookie alone must not be enough to
// take over the account (password reset by email would follow the new address).
func (s *Service) UpdateEmail(ctx context.Context, a Actor, email, currentPassword string) (User, error) {
	if err := RequireSession(a); err != nil {
		return User{}, err
	}
	email, err := acctValidEmail(email)
	if err != nil {
		return User{}, err
	}
	old, err := s.Q.GetUser(ctx, a.UserID)
	if err != nil {
		return User{}, apperr.Internal(err)
	}
	if strings.EqualFold(old.Email, email) {
		return acctUserFromRow(old), nil
	}
	if currentPassword == "" {
		return User{}, apperr.BadRequest("current_password_required",
			"Changing the email address needs your current password.",
			"Send {\"email\": \"...\", \"current_password\": \"...\"}.")
	}
	lim := s.limiter()
	key := "pw|" + a.UserID // shared with ChangePassword: one budget for password guesses
	if wait, ok := lim.reserve(key, s.now(), loginMaxFailures, loginWindow); !ok {
		return User{}, tooManyLogins(wait)
	}
	ok, verr := passwd.Verify(currentPassword, old.PasswordHash)
	if errors.Is(verr, passwd.ErrBusy) {
		lim.refund(key)
		return User{}, acctBusy(verr)
	}
	if !ok {
		return User{}, apperr.Unauthorized("invalid_credentials", "The current password is wrong.", "Type your current password again.")
	}
	lim.reset(key)
	if err := s.Q.UpdateUserEmail(ctx, db.UpdateUserEmailParams{Email: email, UpdatedAt: s.now(), ID: a.UserID}); err != nil {
		if acctIsUnique(err) {
			return User{}, apperr.Conflict("email_taken", "An account with this email already exists.", "Use a different email address.")
		}
		return User{}, apperr.Internal(err)
	}
	u, err := s.Q.GetUser(ctx, a.UserID)
	if err != nil {
		return User{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "user.email_change", "user", a.UserID, map[string]any{"from": old.Email, "to": email})
	return acctUserFromRow(u), nil
}
