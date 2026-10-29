// Package tokens creates and checks bitploy API tokens: "bp_" followed by 32
// random bytes in base62. Only the SHA-256 is stored, plus the first 8
// characters for display.
package tokens

import (
	"regexp"

	"github.com/arno/bitploy/internal/ids"
	"github.com/arno/bitploy/internal/store"
)

const (
	Prefix      = "bp_"
	DisplayLen  = 8
	MaxPerUser  = 20
	randomBytes = 32
)

var formatRe = regexp.MustCompile(`^bp_[A-Za-z0-9]{40,64}$`)

// New returns a fresh token value, its display prefix and the hash to store.
func New() (value, prefix, hash string) {
	value = Prefix + ids.Base62Bytes(randomBytes)
	return value, value[:DisplayLen], Hash(value)
}

// Hash is the hex SHA-256 stored in the database.
func Hash(value string) string { return store.HashToken(value) }

// WellFormed reports whether s looks like a bitploy token. It is a cheap
// check done before touching the database.
func WellFormed(s string) bool { return formatRe.MatchString(s) }
