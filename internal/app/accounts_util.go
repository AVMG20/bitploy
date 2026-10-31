package app

import (
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/auth/passwd"
	"github.com/AVMG20/bitploy/internal/store/db"
)

type acctNewUser struct {
	Email, Name, Password, Role string
}

func acctItoa(n int64) string { return strconv.FormatInt(n, 10) }

// acctShortID is the 12-character display and revoke id of a session.
func acctShortID(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func acctIsUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func acctUserFromRow(u db.User) User {
	out := User{
		ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Status: u.Status,
		CreatedAt: ts(u.CreatedAt),
	}
	return out
}

func acctQuotaFromRow(u db.User) Quota {
	return Quota{
		Apps: u.QuotaApps, MemoryMB: u.QuotaMemoryMb, CPUMillis: u.QuotaCpuMillis,
		DeploysPerHour: u.QuotaDeploysPerHour, DomainsPerApp: u.QuotaDomainsPerApp, VolumeMB: u.QuotaVolumeMb,
	}
}

func acctValidEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	bad := apperr.BadRequest("invalid_email", "That does not look like an email address.", "Use an address like name@example.nl.")
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " <>,;\"") {
		return "", bad
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", bad
	}
	at := strings.LastIndex(s, "@")
	if at < 1 || !strings.Contains(s[at+1:], ".") {
		return "", bad
	}
	return s, nil
}

func acctValidName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > 80 {
		return "", apperr.BadRequest("invalid_name", "The name must be 1 to 80 characters.", "Pick a shorter or non-empty name.")
	}
	return s, nil
}

func acctValidPassword(p string) error {
	if utf8.RuneCountInString(p) < passwd.MinLength {
		return apperr.BadRequest("password_too_short",
			"The password must be at least "+strconv.Itoa(passwd.MinLength)+" characters.",
			"Use a longer password, for example three or four random words.")
	}
	if len(p) > passwd.MaxLength {
		return apperr.BadRequest("password_too_long", "The password is too long.", "Use at most 1024 bytes.")
	}
	return nil
}
