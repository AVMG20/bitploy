package mcp

import "strings"

// secretNameParts are the parts of a variable name that make set_env hide
// its value when hidden is not passed. The dashboard uses the same list in
// web/src/lib/secretName.ts: keep both identical.
var secretNameParts = []string{"SECRET", "TOKEN", "KEY", "PASSWORD"}

// looksSecretName reports a name that holds a secret by the look of it,
// like STRIPE_SECRET_KEY or github_token (any case).
func looksSecretName(name string) bool {
	u := strings.ToUpper(name)
	for _, p := range secretNameParts {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}
