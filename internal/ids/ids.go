// Package ids makes prefixed random IDs (usr_…, app_…, dep_…) and secrets.
package ids

import (
	"crypto/rand"
	"math"
	"math/big"
	"strings"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Prefixes used across bitploy.
const (
	User       = "usr"
	App        = "app"
	Deployment = "dep"
	Token      = "tok"
	Invite     = "inv"
	// ProjectInvite is an invite to someone else's project (project_invites).
	ProjectInvite = "pin"
	Upload        = "upl"
	Domain        = "dom"
	Backup        = "bak"
	AppDomain     = "apd"
)

// New returns prefix + "_" + 20 random base62 characters (~119 bits).
func New(prefix string) string { return prefix + "_" + Base62(20) }

// Base62 returns n random base62 characters.
func Base62(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(base62)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = base62[v.Int64()]
	}
	return string(b)
}

// Base62Bytes encodes nbytes of randomness as base62, left-padded with '0' to
// a fixed width (used for bp_ tokens: 32 bytes → always 43 characters).
func Base62Bytes(nbytes int) string {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	width := int(math.Ceil(float64(nbytes*8) / math.Log2(62)))
	s := new(big.Int).SetBytes(buf).Text(62)
	return strings.Repeat("0", width-len(s)) + s
}

// Hex returns n random lowercase hex characters.
func Hex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = hex[buf[i]%16]
	}
	return string(b)
}
