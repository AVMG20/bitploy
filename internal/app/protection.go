package app

// Password protection: a browser login prompt (HTTP basic auth, done by
// Traefik) in front of an app's public addresses. The password is write-only:
// only a bcrypt hash is stored, and nothing returns it.

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const (
	maxProtectUsernameLen = 64
	minProtectPasswordLen = 8
)

// ProtectionInput turns password protection on, off, or changes the login.
type ProtectionInput struct {
	Enabled  bool    `json:"enabled" doc:"true puts a login prompt in front of the app, false removes it."`
	Username *string `json:"username,omitempty" doc:"The login name: 1 to 64 letters, digits and . _ - @ +. Needed when turning it on; leave out to keep the current one."`
	Password *string `json:"password,omitempty" doc:"8 to 72 characters. Needed when turning it on; leave out to keep the current one. It is never returned."`
}

// validProtectUsername allows only characters that are safe in a Traefik
// route file: Traefik reads every file in the folder as a Go template, so a
// name with braces could break the whole file. ":" would split the entry.
func validProtectUsername(u string) bool {
	if u == "" || len(u) > maxProtectUsernameLen {
		return false
	}
	for _, r := range u {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '@', r == '+':
		default:
			return false
		}
	}
	return true
}

func validProtectPassword(p string) *apperr.Error {
	switch {
	case utf8.RuneCountInString(p) < minProtectPasswordLen:
		return apperr.BadRequest("password_too_short", "The password is too short: use at least 8 characters.",
			"Use a few words and a number, like blue-river-lamp-42.")
	case len(p) > proxy.MaxPasswordBytes:
		return apperr.BadRequest("password_too_long", "The password is too long: use at most 72 characters.",
			"Accented letters and emoji count as more than one character.")
	case strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }), !utf8.ValidString(p):
		return apperr.BadRequest("password_invalid", "The password cannot contain line breaks or control characters.",
			"Use letters, digits and symbols.")
	}
	return nil
}

// SetPasswordProtection turns the login prompt on or off, or changes its
// username or password while it stays on. Turning it off forgets the login.
// With it on, a missing username or password keeps the current one. The route
// file is rewritten at once; Traefik picks it up within a second.
func (s *Service) SetPasswordProtection(ctx context.Context, a Actor, ref string, in ProtectionInput) (App, error) {
	row, err := s.LoadApp(ctx, a, ref, true)
	if err != nil {
		return App{}, err
	}
	user, hash := "", ""
	passwordChanged := false
	if in.Enabled {
		if row.Type != "web" {
			return App{}, apperr.Unprocessable("protection_needs_web_app", "A worker has no web address, so there is nothing to put a password on.",
				"Password protection is for web apps.")
		}
		user = row.ProtectUsername
		if in.Username != nil && strings.TrimSpace(*in.Username) != "" {
			user = strings.TrimSpace(*in.Username)
		}
		if user == "" {
			return App{}, apperr.BadRequest("username_required", "Choose a username to turn the password on.",
				"Use 1 to 64 letters, digits or . _ - @ +, for example team.").WithNext("set_password_protection")
		}
		if !validProtectUsername(user) {
			return App{}, apperr.BadRequest("username_invalid", "The username can only have letters, digits and . _ - @ + (1 to 64 characters, no spaces).",
				"Use something like team or anna.").WithNext("set_password_protection")
		}
		hash = row.ProtectHash
		if in.Password != nil && *in.Password != "" {
			if ae := validProtectPassword(*in.Password); ae != nil {
				return App{}, ae.WithNext("set_password_protection")
			}
			h, herr := proxy.HashPassword(*in.Password)
			if herr != nil {
				return App{}, apperr.Internal(herr)
			}
			hash, passwordChanged = h, true
		}
		if hash == "" {
			return App{}, apperr.BadRequest("password_required", "Choose a password to turn the protection on.",
				"Use at least 8 characters, for example three words and a number.").WithNext("set_password_protection")
		}
	}
	err = s.Q.SetAppProtection(ctx, db.SetAppProtectionParams{
		ProtectEnabled: b2i(in.Enabled), ProtectUsername: user, ProtectHash: hash, UpdatedAt: s.now(), ID: row.ID,
	})
	if err != nil {
		return App{}, apperr.Internal(err)
	}
	// The audit row never holds the password or its hash.
	s.Audit(ctx, a, "app.protection_set", "app", row.ID, map[string]any{
		"enabled": in.Enabled, "username": user, "password_changed": passwordChanged,
	})
	if err := s.SyncRoute(ctx, row.ID); err != nil {
		s.Log.Error("write route after password change; the reconciler will retry", "app", row.Slug, "err", err)
		hint := "Wait a minute and open the app again. Until then it may still ask for the old password."
		if in.Enabled {
			hint = "Wait a minute and open the app again. Until then it may still be open to everyone."
		}
		return App{}, apperr.New(500, "route_not_updated", "The setting is saved, but bitploy could not update the web server yet.", hint)
	}
	return s.GetApp(ctx, a, row.Slug)
}
