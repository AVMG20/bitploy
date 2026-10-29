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

// OpenSignupCode is the invite code accepted when BITPLOY_SIGNUP=open.
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

func acctInviteInvalid(status int, why string) *apperr.Error {
	return apperr.New(status, "invite_invalid", why, "Ask the person who invited you for a new invite link.")
}

// AcceptInvite creates the user and logs them in. With BITPLOY_SIGNUP=open,
// code "open" is accepted for public signup.
func (s *Service) AcceptInvite(ctx context.Context, code string, in AcceptInviteInput, ip, userAgent string) (cookie string, info SessionInfo, err error) {
	var inv db.Invite
	open := code == OpenSignupCode && s.Cfg.Signup == "open"
	if !open {
		inv, err = s.Q.GetInviteByHash(ctx, store.HashToken(code))
		if errors.Is(err, sql.ErrNoRows) {
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
	emailIn := in.Email
	if inv.Email.Valid {
		if strings.TrimSpace(emailIn) == "" {
			emailIn = inv.Email.String
		} else if !strings.EqualFold(strings.TrimSpace(emailIn), inv.Email.String) {
			return "", SessionInfo{}, apperr.Forbidden("invite_email_mismatch",
				"This invite is for a different email address.", "Use the address the invite was sent to.")
		}
	}
	email, err := acctValidEmail(emailIn)
	if err != nil {
		return "", SessionInfo{}, err
	}
	name, err := acctValidName(in.Name)
	if err != nil {
		return "", SessionInfo{}, err
	}
	if err := acctValidPassword(in.Password); err != nil {
		return "", SessionInfo{}, err
	}
	role := "user"
	if !open {
		role = inv.Role
	}
	hash, herr := passwd.Hash(in.Password)
	if herr != nil {
		return "", SessionInfo{}, apperr.Internal(herr)
	}
	var user db.User
	terr := s.Store.Tx(ctx, func(q *db.Queries) error {
		u, e := s.insertUserHashed(ctx, q, acctNewUser{Email: email, Name: name, Role: role}, hash)
		if e != nil {
			return e
		}
		if !open {
			n, e := q.MarkInviteUsed(ctx, db.MarkInviteUsedParams{UsedAt: nullInt(s.now(), true), UsedBy: nullStr(u.ID), ID: inv.ID})
			if e != nil {
				return apperr.Internal(e)
			}
			if n == 0 {
				return acctInviteInvalid(410, "This invite link has already been used.")
			}
		}
		user = u
		return nil
	})
	if terr != nil {
		return "", SessionInfo{}, apperr.As(terr)
	}
	cookie, csrf, serr := s.newSession(ctx, user.ID, ip, userAgent)
	if serr != nil {
		return "", SessionInfo{}, serr
	}
	details := map[string]any{"email": email, "role": role, "open": open}
	if !open {
		details["invite"] = inv.ID
	}
	s.Audit(ctx, acctActorFor(user, ip), "user.signup", "user", user.ID, details)
	return cookie, SessionInfo{User: acctUserFromRow(user), CSRFToken: csrf}, nil
}
