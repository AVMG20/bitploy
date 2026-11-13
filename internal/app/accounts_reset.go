package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth/passwd"
	"github.com/AVMG20/bitploy/internal/ids"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

// passwordResetTTL is how long a password reset link works.
const passwordResetTTL = 24 * time.Hour

// There is no email: an admin makes a reset link (Admin > People, or
// `bitploy admin reset-link` when the only admin forgot their own password)
// and sends it to the person. Like an invite link the code is the credential;
// only its hash is stored, it works once, and a newer link for the same user
// replaces the older ones.

func resetLinkInvalid(status int) *apperr.Error {
	return apperr.New(status, "reset_link_invalid",
		"This password reset link does not work: it is wrong, was already used, or has expired.",
		"Ask an admin of this server for a new link.")
}

// CreatePasswordReset makes a one-time link for the user to pick a new
// password (admin, session; also for the admin's own account). Older unused
// links of the user stop working.
func (s *Service) CreatePasswordReset(ctx context.Context, a Actor, userID string) (PasswordReset, error) {
	if err := RequireAdmin(a); err != nil {
		return PasswordReset{}, err
	}
	u, err := s.Q.GetUser(ctx, strings.TrimSpace(userID))
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordReset{}, acctUserNotFound()
	} else if err != nil {
		return PasswordReset{}, apperr.Internal(err)
	}
	code := ids.Base62(32)
	id := ids.New(ids.PasswordReset)
	now := s.Now()
	exp := now.Add(passwordResetTTL)
	if err := s.Store.Tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteUnusedPasswordResetsOfUser(ctx, u.ID); err != nil {
			return err
		}
		return q.InsertPasswordReset(ctx, db.InsertPasswordResetParams{
			ID: id, UserID: u.ID, CodeHash: store.HashToken(code), CreatedBy: nullStr(a.UserID),
			ExpiresAt: exp.Unix(), CreatedAt: now.Unix(),
		})
	}); err != nil {
		return PasswordReset{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "admin.password_reset_link", "user", u.ID, map[string]any{"reset": id, "email": u.Email})
	return PasswordReset{
		ID: id, User: UserBrief{ID: u.ID, Name: u.Name, Email: u.Email}, ExpiresAt: exp.UTC(),
		URL: s.Cfg.PublicURL + "/reset/" + code,
	}, nil
}

// passwordResetByCode finds a reset link that still works, and its user.
// Unknown codes count against the caller's IP (5 per hour, like invite
// codes), so links cannot be guessed; a suspended user gets account_suspended.
func (s *Service) passwordResetByCode(ctx context.Context, code, ip string) (db.PasswordReset, db.User, error) {
	lim := s.limiter()
	key := "reset|" + ip
	if wait, full := lim.full(key, s.now(), signupMax, signupWindow); full {
		return db.PasswordReset{}, db.User{}, tooManyResets(wait)
	}
	code = strings.TrimSpace(code)
	r, err := s.Q.GetPasswordResetByHash(ctx, store.HashToken(code))
	if errors.Is(err, sql.ErrNoRows) || code == "" {
		lim.add(key, s.now())
		return db.PasswordReset{}, db.User{}, resetLinkInvalid(404)
	}
	if err != nil {
		return db.PasswordReset{}, db.User{}, apperr.Internal(err)
	}
	if r.UsedAt.Valid || r.ExpiresAt <= s.now() {
		return db.PasswordReset{}, db.User{}, resetLinkInvalid(410)
	}
	u, err := s.Q.GetUser(ctx, r.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.PasswordReset{}, db.User{}, resetLinkInvalid(404)
	}
	if err != nil {
		return db.PasswordReset{}, db.User{}, apperr.Internal(err)
	}
	if u.Status == "suspended" {
		return db.PasswordReset{}, db.User{}, acctSuspended()
	}
	return r, u, nil
}

func tooManyResets(wait int64) *apperr.Error {
	mins := (wait + 59) / 60
	if mins < 1 {
		mins = 1
	}
	return apperr.TooMany("too_many_attempts",
		"Too many password reset attempts from this network.",
		"Wait about "+acctItoa(mins)+" minute(s) and try again.").WithDetail("retry_after_seconds", wait)
}

// GetPasswordReset says whose account a reset link is for, without using it.
// The code is the credential: whoever has the link sees the name and email.
func (s *Service) GetPasswordReset(ctx context.Context, code, ip string) (PasswordResetInfo, error) {
	r, u, err := s.passwordResetByCode(ctx, code, ip)
	if err != nil {
		return PasswordResetInfo{}, err
	}
	return PasswordResetInfo{Name: u.Name, Email: u.Email, ExpiresAt: ts(r.ExpiresAt)}, nil
}

// UsePasswordReset sets the new password, marks the link used, signs out
// every session of the user (API tokens stay) and logs them in, like an
// invite signup does. A suspended user is refused and stays suspended.
func (s *Service) UsePasswordReset(ctx context.Context, code, password, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	r, u, err := s.passwordResetByCode(ctx, code, ip)
	if err != nil {
		return "", SessionInfo{}, err
	}
	if err := acctValidPassword(password); err != nil {
		return "", SessionInfo{}, err
	}
	// A real use counts too, like a signup does.
	lim := s.limiter()
	key := "reset|" + ip
	if wait, ok := lim.reserve(key, s.now(), signupMax, signupWindow); !ok {
		return "", SessionInfo{}, tooManyResets(wait)
	}
	hash, herr := passwd.Hash(password)
	if herr != nil {
		lim.refund(key)
		return "", SessionInfo{}, acctHashErr(herr)
	}
	if terr := s.Store.Tx(ctx, func(q *db.Queries) error {
		now := s.now()
		n, e := q.MarkPasswordResetUsed(ctx, db.MarkPasswordResetUsedParams{UsedAt: nullInt(now, true), ID: r.ID, ExpiresAt: now})
		if e != nil {
			return apperr.Internal(e)
		}
		if n == 0 {
			return resetLinkInvalid(410)
		}
		// Suspended while the password was hashed?
		cur, e := q.GetUser(ctx, u.ID)
		if e != nil {
			return apperr.Internal(e)
		}
		if cur.Status == "suspended" {
			return acctSuspended()
		}
		if e := q.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{PasswordHash: hash, UpdatedAt: now, ID: u.ID}); e != nil {
			return apperr.Internal(e)
		}
		if e := q.DeleteSessionsOfUser(ctx, u.ID); e != nil {
			return apperr.Internal(e)
		}
		if e := q.DeleteUnusedPasswordResetsOfUser(ctx, u.ID); e != nil {
			return apperr.Internal(e)
		}
		return nil
	}); terr != nil {
		return "", SessionInfo{}, apperr.As(terr)
	}
	s.Audit(ctx, acctActorFor(u, ip), "user.password_reset", "user", u.ID, map[string]any{"reset": r.ID})
	cookie, csrf, serr := s.newSession(ctx, u.ID, ip, userAgent)
	if serr != nil {
		return "", SessionInfo{}, serr
	}
	return cookie, SessionInfo{User: acctUserFromRow(u), CSRFToken: csrf}, nil
}

// PasswordResetForEmail is CreatePasswordReset by email address, for
// `bitploy admin reset-link` (run on the server, as the system).
func (s *Service) PasswordResetForEmail(ctx context.Context, a Actor, email string) (PasswordReset, error) {
	if err := RequireAdmin(a); err != nil {
		return PasswordReset{}, err
	}
	u, err := s.Q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordReset{}, apperr.NotFound("user_not_found", "There is no user with that email address.",
			"Check the address; bitploy admin create-user makes a new account.")
	} else if err != nil {
		return PasswordReset{}, apperr.Internal(err)
	}
	return s.CreatePasswordReset(ctx, a, u.ID)
}
