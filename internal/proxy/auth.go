package proxy

import "golang.org/x/crypto/bcrypt"

// BcryptCost is the cost of the password hashes in route files.
//
// Traefik v3.7's basicAuth checks the hash on every request that carries an
// Authorization header, which a browser sends for every page, image, script
// and stylesheet. It has no cache: the only shortcut is a singleflight that
// merges concurrent checks of the same user and password. So the cost is paid
// per request, on the same CPU that serves every other app. Cost 8 takes
// roughly 10 to 20 ms; the default 10 is four times that. See docs/decisions.md.
const BcryptCost = 8

// MaxPasswordBytes is bcrypt's limit: it ignores everything after 72 bytes.
const MaxPasswordBytes = 72

// NeverMatchHash is a well-formed hash that no password matches. It stands in
// when an app says it is protected but its login is missing.
const NeverMatchHash = "$2a$08$....................................................."

// HashPassword returns the bcrypt hash Traefik's basicAuth understands
// ("$2a$08$..."). The result never contains ":" or "{", so it is safe in the
// "user:hash" entry and in a route file that Traefik reads as a Go template.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(b), err
}

// CheckPassword reports whether password matches a hash made by HashPassword.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
