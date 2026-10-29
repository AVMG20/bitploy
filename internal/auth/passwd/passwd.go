// Package passwd hashes and verifies passwords with argon2id.
//
// Parameters follow the OWASP minimum (m=19456 KiB, t=2, p=1). Hashes are
// stored as PHC strings, so the parameters can be raised later and old hashes
// still verify.
package passwd

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

const (
	MemoryKiB uint32 = 19456
	Time      uint32 = 2
	Threads   uint8  = 1
	KeyLen           = 32
	SaltLen          = 16

	// MinLength is the shortest password bitploy accepts.
	MinLength = 10
	// MaxLength keeps absurd request bodies from burning CPU.
	MaxLength = 1024
)

// Each hash needs ~19 MiB; cap how many run at once so a burst of logins
// cannot exhaust memory on the small server.
var sem = make(chan struct{}, 4)

// MaxWaiting is how many hashes may queue behind the running ones. Beyond that
// Hash and Verify fail fast with ErrBusy instead of piling up goroutines (each
// holding a request, a body and a database handle) during a login flood.
var MaxWaiting int32 = 32

var waiting atomic.Int32

var ErrInvalidHash = errors.New("invalid password hash")

// ErrBusy means too many password hashes are already waiting; try again shortly.
var ErrBusy = errors.New("too many password checks in progress")

// acquire takes a hashing slot, queueing up to MaxWaiting callers.
func acquire(queueLimit bool) error {
	select {
	case sem <- struct{}{}:
		return nil
	default:
	}
	if queueLimit && waiting.Add(1) > MaxWaiting {
		waiting.Add(-1)
		return ErrBusy
	}
	sem <- struct{}{}
	if queueLimit {
		waiting.Add(-1)
	}
	return nil
}

// Hash returns a PHC-formatted argon2id hash of the password.
func Hash(password string) (string, error) { return hash(password, true) }

func hash(password string, queueLimit bool) (string, error) {
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	if err := acquire(queueLimit); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, Time, MemoryKiB, Threads, KeyLen)
	<-sem
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, MemoryKiB, Time, Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether password matches the PHC hash, in constant time.
func Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrInvalidHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, ErrInvalidHash
	}
	// Refuse parameters that would let a corrupt row exhaust memory.
	if m == 0 || m > 1<<20 || t == 0 || t > 20 || p == 0 {
		return false, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrInvalidHash
	}
	if err := acquire(true); err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	<-sem
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummy     string
)

// Dummy returns a valid hash used to spend the same time on unknown emails as
// on known ones, so login timing does not reveal which addresses have accounts.
func Dummy() string {
	dummyOnce.Do(func() { dummy, _ = hash("bitploy-dummy-password", false) })
	return dummy
}
