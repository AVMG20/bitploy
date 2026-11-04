package domains

import (
	"strings"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// AppDomainRules are what an extra app domain must not clash with.
type AppDomainRules struct {
	Existing   []string // the default app domain and the extra ones already added
	PublicHost string   // host of BITPLOY_PUBLIC_URL
	EdgeHost   string
}

// NormalizeAppDomain turns what an admin typed (*.example.com, example.com.,
// https://example.com/) into example.com.
func NormalizeAppDomain(in string) string {
	h := strings.ToLower(strings.TrimSpace(in))
	for _, p := range []string{"https://", "http://"} {
		h = strings.TrimPrefix(h, p)
	}
	h = strings.TrimSuffix(h, "/")
	h = strings.TrimPrefix(h, "*.")
	return strings.TrimSuffix(h, ".")
}

// ValidateAppDomain checks a domain an admin wants apps to live under
// (<slug>.<domain>) and returns its normalized form. It must be a valid
// hostname that anyone could own, not bitploy's own dashboard or edge host,
// and must not overlap a domain that is already in use.
func ValidateAppDomain(in string, rules AppDomainRules) (string, *apperr.Error) {
	h := NormalizeAppDomain(in)
	bad := func(msg, hint string) (string, *apperr.Error) {
		return "", apperr.BadRequest("app_domain_invalid", msg, hint)
	}
	if h == "" {
		return bad("The domain is empty.", "Type a domain such as example.com.")
	}
	if strings.Contains(h, "*") {
		return bad(quote(in)+" has a * in the middle.", "Type only the domain, like example.com. Apps get <name>.example.com.")
	}
	if h == rules.PublicHost {
		return bad(quote(h)+" is the address of the bitploy dashboard.", "Use a domain that is only for apps, like apps.example.com or example.com when the dashboard lives elsewhere.")
	}
	if rules.EdgeHost != "" && h == rules.EdgeHost {
		return bad(quote(h)+" is bitploy's edge host for custom domains.", "Use another domain.")
	}
	for _, d := range rules.Existing {
		d = strings.ToLower(d)
		switch {
		case d == h:
			return "", apperr.Conflict("app_domain_exists", quote(h)+" is already an app domain.", "It is in the list already.")
		case strings.HasSuffix(h, "."+d):
			return bad(quote(h)+" lies below the app domain "+d+".", "Use a domain that is not part of "+d+".")
		case strings.HasSuffix(d, "."+h):
			return bad("The app domain "+d+" lies below "+quote(h)+".", "Use a domain that does not contain "+d+".")
		}
	}
	// The same hostname rules as a custom domain (characters, length, a real
	// top-level domain), without bitploy's own reservations.
	if _, err := Validate(h, Rules{}); err != nil {
		return bad(strings.ReplaceAll(err.Message, "a custom domain", "an app domain"), err.Hint)
	}
	return h, nil
}
