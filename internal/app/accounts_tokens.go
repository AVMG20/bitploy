package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/arno/bitploy/internal/apperr"
	"github.com/arno/bitploy/internal/auth/tokens"
	"github.com/arno/bitploy/internal/ids"
	"github.com/arno/bitploy/internal/store/db"
)

// ---- API tokens (session only) ----

func (s *Service) CreateToken(ctx context.Context, a Actor, in CreateTokenInput) (TokenCreated, error) {
	if err := RequireSession(a); err != nil {
		return TokenCreated{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return TokenCreated{}, apperr.BadRequest("invalid_token_name", "The token name must be 1 to 60 characters.",
			"Give the token a short name such as \"laptop\" or \"claude-code\".")
	}
	if in.Scope != ScopeRead && in.Scope != ScopeDeploy {
		return TokenCreated{}, apperr.BadRequest("invalid_scope", "The scope must be \"read\" or \"deploy\".",
			"Use \"read\" for list/get/logs/metrics only, or \"deploy\" for everything except tokens and account settings.")
	}
	now := s.Now()
	var expires sql.NullInt64
	switch in.ExpiresInDays {
	case 0:
	case 30, 90:
		expires = nullInt(now.Add(time.Duration(in.ExpiresInDays)*24*time.Hour).Unix(), true)
	default:
		return TokenCreated{}, apperr.BadRequest("invalid_expiry", "expires_in_days must be 0 (never), 30 or 90.",
			"Pick one of those three values.")
	}
	n, err := s.Q.CountActiveTokensOfUser(ctx, db.CountActiveTokensOfUserParams{UserID: a.UserID, ExpiresAt: nullInt(now.Unix(), true)})
	if err != nil {
		return TokenCreated{}, apperr.Internal(err)
	}
	if n >= tokens.MaxPerUser {
		return TokenCreated{}, apperr.Conflict("token_limit",
			"You already have 20 active API tokens, which is the maximum.",
			"Revoke a token you no longer use in the dashboard under Tokens, then try again.")
	}
	value, prefix, hash := tokens.New()
	id := ids.New(ids.Token)
	if err := s.Q.InsertToken(ctx, db.InsertTokenParams{
		ID: id, UserID: a.UserID, Name: name, Prefix: prefix, Hash: hash,
		Scope: string(in.Scope), ExpiresAt: expires, CreatedAt: now.Unix(),
	}); err != nil {
		return TokenCreated{}, apperr.Internal(err)
	}
	s.Audit(ctx, a, "token.create", "token", id, map[string]any{"name": name, "scope": string(in.Scope), "prefix": prefix})
	row, err := s.Q.GetTokenOfUser(ctx, db.GetTokenOfUserParams{ID: id, UserID: a.UserID})
	if err != nil {
		return TokenCreated{}, apperr.Internal(err)
	}
	return TokenCreated{Token: acctTokenFromRow(row), Value: value}, nil
}

func (s *Service) ListTokens(ctx context.Context, a Actor) ([]Token, error) {
	if err := RequireSession(a); err != nil {
		return nil, err
	}
	rows, err := s.Q.ListTokensOfUser(ctx, a.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	out := make([]Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, acctTokenFromRow(r))
	}
	return out, nil
}

// RevokeToken takes effect at once: the next request with the token gets 401.
func (s *Service) RevokeToken(ctx context.Context, a Actor, id string) error {
	if err := RequireSession(a); err != nil {
		return err
	}
	row, err := s.Q.GetTokenOfUser(ctx, db.GetTokenOfUserParams{ID: id, UserID: a.UserID})
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound("token_not_found", "There is no token with that ID in your account.", "List your tokens with GET /api/v1/tokens.")
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if row.RevokedAt.Valid {
		return nil
	}
	if _, err := s.Q.RevokeTokenRow(ctx, db.RevokeTokenRowParams{RevokedAt: nullInt(s.now(), true), ID: id, UserID: a.UserID}); err != nil {
		return apperr.Internal(err)
	}
	s.Audit(ctx, a, "token.revoke", "token", id, map[string]any{"name": row.Name})
	return nil
}

func acctTokenFromRow(r db.ApiToken) Token {
	t := Token{
		ID: r.ID, Name: r.Name, Prefix: r.Prefix, Scope: Scope(r.Scope), CreatedAt: ts(r.CreatedAt),
		ExpiresAt: tsPtr(r.ExpiresAt), LastUsedAt: tsPtr(r.LastUsedAt), LastUsedIP: strPtr(r.LastUsedIp),
		RevokedAt: tsPtr(r.RevokedAt),
	}
	return t
}
