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

const inviteTTL = 7 * 24 * time.Hour

// OpenSignupCode is the invite code of open signup (/invite/open), accepted
// while Admin > Signups lets anyone make an account (signups open).
const OpenSignupCode = "open"

func (s *Service) CreateInvite(ctx context.Context, a Actor, in CreateInviteInput) (Invite, error) {
	if err := RequireAdmin(a); err != nil {
		return Invite{}, err
	}
	role := in.Role
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "admin" {
		return Invite{}, apperr.BadRequest("invalid_role", "The role must be \"user\" or \"admin\".", "Use one of those two values.")
	}
	var email sql.NullString
	if strings.TrimSpace(in.Email) != "" {
		e, err := acctValidEmail(in.Email)
		if err != nil {
			return Invite{}, err
		}
		email = nullStr(e)
	}
	code := ids.Base62(32)
	id := ids.New(ids.Invite)
	now := s.Now()
	exp := now.Add(inviteTTL)
	if err := s.Q.InsertInvite(ctx, db.InsertInviteParams{
		ID: id, CodeHash: store.HashToken(code), Email: email, Role: role,
		CreatedBy: nullStr(a.UserID), ExpiresAt: exp.Unix(), CreatedAt: now.Unix(),
	}); err != nil {
		return Invite{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "invite.create", "invite", id, map[string]any{"role": role, "email": email.String})
	inv := Invite{ID: id, Email: strPtr(email), Role: role, CreatedAt: now.UTC(), ExpiresAt: exp.UTC(), URL: s.Cfg.PublicURL + "/invite/" + code}
	return inv, nil
}

func (s *Service) ListInvites(ctx context.Context, a Actor) ([]Invite, error) {
	if err := RequireAdmin(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListInvitesNewestFirst(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]Invite, 0, len(rows))
	for _, r := range rows {
		out = append(out, Invite{
			ID: r.ID, Email: strPtr(r.Email), Role: r.Role, CreatedAt: ts(r.CreatedAt),
			ExpiresAt: ts(r.ExpiresAt), UsedAt: tsPtr(r.UsedAt),
		})
	}
	return out, nil
}

func (s *Service) RevokeInvite(ctx context.Context, a Actor, id string) error {
	if err := RequireAdmin(a); err != nil {
		return err
	}
	n, err := s.Q.DeleteUnusedInvite(ctx, id)
	if err != nil {
		return apperr.Internal(err)
	}
	if n == 0 {
		return apperr.NotFound("invite_not_found", "There is no unused invite with that ID.",
			"Used invites cannot be revoked. List invites with GET /api/v1/admin/invites.")
	}
	s.Audit(ctx, a, "invite.revoke", "invite", id, nil)
	return nil
}

// signupReq is one new account from an invite link: the form, the address
// the invite is locked to ("" for none), the role, the rate limit key of the
// caller's IP, how the account joins (via, the invite's maker, the project of
// a project invite) and what else the same transaction does (mark the invite
// used).
type signupReq struct {
	in          AcceptInviteInput
	lockedEmail string
	role        string
	sigKey      string
	via         string
	by, project string
	inTx        func(q *db.Queries, u db.User) error
}

// signup checks the form, spends one of the 5 signups per hour of the IP and
// creates the user together with inTx, with the limits for new accounts
// (server settings, read in the same transaction). The caller has checked the invite (and
// counted a wrong code against sigKey) and logs the user in afterwards. A
// user invite (AcceptInvite) and a project invite (SignupProjectInvite) both
// sign up here.
func (s *Service) signup(ctx context.Context, r signupReq) (db.User, error) {
	in := r.in
	emailIn := in.Email
	if r.lockedEmail != "" {
		if strings.TrimSpace(emailIn) == "" {
			emailIn = r.lockedEmail
		} else if !strings.EqualFold(strings.TrimSpace(emailIn), r.lockedEmail) {
			return db.User{}, apperr.Forbidden("invite_email_mismatch",
				"This invite is for a different email address.", "Use the address the invite was sent to.")
		}
	}
	email, err := acctValidEmail(emailIn)
	if err != nil {
		return db.User{}, err
	}
	name, err := acctValidName(in.Name)
	if err != nil {
		return db.User{}, err
	}
	if err := acctValidPassword(in.Password); err != nil {
		return db.User{}, err
	}
	lim := s.limiter()
	if wait, ok := lim.reserve(r.sigKey, s.now(), signupMax, signupWindow); !ok {
		return db.User{}, tooManySignups(wait)
	}
	hash, herr := passwd.Hash(in.Password)
	if herr != nil {
		lim.refund(r.sigKey)
		return db.User{}, acctHashErr(herr)
	}
	var user db.User
	terr := s.Store.Tx(ctx, func(q *db.Queries) error {
		st, e := s.serverSettings(ctx, q)
		if e != nil {
			return e
		}
		u, e := s.insertUserHashed(ctx, q, acctNewUser{Email: email, Name: name, Role: r.role,
			Quota: newAccountQuota(st, r.via), JoinedVia: r.via, JoinedBy: r.by, JoinedProject: r.project}, hash)
		if e != nil {
			return e
		}
		if r.inTx != nil {
			if e := r.inTx(q, u); e != nil {
				return e
			}
		}
		user = u
		return nil
	})
	if terr != nil {
		return db.User{}, terr
	}
	return user, nil
}

func tooManySignups(wait int64) *apperr.Error {
	mins := (wait + 59) / 60
	if mins < 1 {
		mins = 1
	}
	return apperr.TooMany("too_many_attempts",
		"Too many signup attempts from this network.",
		"Wait about "+acctItoa(mins)+" minute(s) and try again.").WithDetail("retry_after_seconds", wait)
}

func acctInviteInvalid(status int, why string) *apperr.Error {
	return apperr.New(status, "invite_invalid", why, "Ask the person who invited you for a new invite link.")
}

func signupClosed() *apperr.Error {
	return apperr.Forbidden("signup_closed", "This server does not let people make an account on their own.",
		"Ask an admin of this server for an invite link.")
}

// AcceptInvite creates the user and logs them in. Code "open" is open
// signup, accepted while the server settings let anyone make an account.
func (s *Service) AcceptInvite(ctx context.Context, code string, in AcceptInviteInput, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	var inv db.Invite
	open := code == OpenSignupCode
	if open {
		st, err := s.serverSettings(ctx, s.Q)
		if err != nil {
			return "", SessionInfo{}, err
		}
		if st.Signups != SignupOpen {
			return "", SessionInfo{}, signupClosed()
		}
	}
	// Signup and invite acceptance: 5 attempts per hour per IP. Wrong invite
	// codes and real signups count; plain validation errors (bad email, short
	// password) do not, so a typo is not punished.
	lim := s.limiter()
	sigKey := "signup|" + ip
	if wait, full := lim.full(sigKey, s.now(), signupMax, signupWindow); full {
		return "", SessionInfo{}, tooManySignups(wait)
	}
	if !open {
		inv, err = s.Q.GetInviteByHash(ctx, store.HashToken(code))
		if errors.Is(err, sql.ErrNoRows) {
			lim.add(sigKey, s.now())
			return "", SessionInfo{}, acctInviteInvalid(404, "This invite link is not valid.")
		}
		if err != nil {
			return "", SessionInfo{}, apperr.Internal(err)
		}
		if inv.UsedAt.Valid {
			return "", SessionInfo{}, acctInviteInvalid(410, "This invite link has already been used.")
		}
		if inv.ExpiresAt <= s.now() {
			return "", SessionInfo{}, acctInviteInvalid(410, "This invite link has expired.")
		}
	}
	role, via := "user", joinedOpen
	if !open {
		role, via = inv.Role, joinedAdminInvite
	}
	user, err := s.signup(ctx, signupReq{in: in, lockedEmail: inv.Email.String, role: role, sigKey: sigKey,
		via: via, by: inv.CreatedBy.String,
		inTx: func(q *db.Queries, u db.User) error {
			if open {
				return nil
			}
			n, e := q.MarkInviteUsed(ctx, db.MarkInviteUsedParams{UsedAt: nullInt(s.now(), true), UsedBy: nullStr(u.ID), ID: inv.ID})
			if e != nil {
				return apperr.Internal(e)
			}
			if n == 0 {
				return acctInviteInvalid(410, "This invite link has already been used.")
			}
			return nil
		}})
	if err != nil {
		e := apperr.As(err)
		if open && e.Code == "email_taken" {
			// Without email verification an open signup cannot hide that an
			// address is registered, but it should not confirm it either.
			// (Enumeration is further capped by the 5 per hour per IP limit.)
			return "", SessionInfo{}, apperr.Conflict("signup_failed",
				"The account could not be created with these details.",
				"If you already have an account, log in instead; otherwise try a different email address.")
		}
		return "", SessionInfo{}, e
	}
	cookie, csrf, serr := s.newSession(ctx, user.ID, ip, userAgent)
	if serr != nil {
		return "", SessionInfo{}, serr
	}
	details := map[string]any{"email": user.Email, "role": role, "open": open}
	if !open {
		details["invite"] = inv.ID
	}
	s.Audit(ctx, acctActorFor(user, ip), "user.signup", "user", user.ID, details)
	return cookie, SessionInfo{User: acctUserFromRow(user), CSRFToken: csrf}, nil
}
