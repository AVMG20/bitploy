// Package domains holds the rules and DNS checks for custom domains: which
// hostnames are allowed, which DNS records the user must create, and whether
// those records are live. It has no database access; internal/app drives it.
package domains

import (
	"net"
	"strings"

	"golang.org/x/net/publicsuffix"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// ChallengePrefix is the label under which the TXT token is published.
const ChallengePrefix = "_bitploy-challenge."

// Rules says which hostnames bitploy owns and so cannot be added as custom domains.
type Rules struct {
	AppsDomain string // bitploy.nl: it and everything under it is off limits
	PublicHost string // host of BITPLOY_PUBLIC_URL
	EdgeHost   string // edge.bitploy.nl
}

// Normalize lower-cases a hostname and strips spaces and a trailing dot.
func Normalize(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

// Validate checks a hostname and returns its normalized form. Errors are
// user-facing (*apperr.Error).
func Validate(host string, rules Rules) (string, *apperr.Error) {
	h := Normalize(host)
	bad := func(msg, hint string) (string, *apperr.Error) {
		return "", apperr.BadRequest("domain_invalid", msg, hint).WithNext("add_domain")
	}
	if h == "" {
		return bad("The domain is empty.", "Pass a hostname such as www.example.com.")
	}
	if strings.ContainsAny(h, "/?#@: ") {
		return bad(quote(host)+" is not a plain hostname.",
			"Give only the hostname, like www.example.com: no https://, no path, no port.")
	}
	if strings.Contains(h, "*") {
		return bad("Wildcard domains are not supported.", "Add each hostname on its own, like www.example.com.")
	}
	if net.ParseIP(h) != nil {
		return bad("An IP address cannot be a custom domain.", "Use a hostname you own, like www.example.com.")
	}
	if len(h) > 253 {
		return bad("The domain is longer than 253 characters.", "Use a shorter hostname.")
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return bad(quote(h)+" has no top-level domain.", "Use a full hostname like www.example.com.")
	}
	for _, l := range labels {
		if l == "" {
			return bad(quote(h)+" has an empty part between dots.", "Remove the double dot.")
		}
		if len(l) > 63 {
			return bad("A part of the domain is longer than 63 characters.", "Use a shorter hostname.")
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return bad(quote(h)+" has a part that starts or ends with a dash.", "Hostname parts cannot begin or end with '-'.")
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			case c >= 0x80:
				return bad(quote(h)+" has non-ASCII characters.",
					"Use the punycode form (xn--...) of the domain; your registrar shows it.")
			default:
				return bad(quote(h)+" has a character that is not allowed in a hostname ("+string(rune(c))+").",
					"Use only a-z, 0-9 and '-' in each part.")
			}
		}
	}

	if rules.AppsDomain != "" && (h == rules.AppsDomain || strings.HasSuffix(h, "."+rules.AppsDomain)) {
		return "", apperr.BadRequest("domain_reserved",
			quote(h)+" is part of bitploy's own domain "+rules.AppsDomain+".",
			"Every app already has its own address on "+rules.AppsDomain+". Add a domain you own instead.")
	}
	if h == rules.PublicHost || (rules.EdgeHost != "" && h == rules.EdgeHost) {
		return "", apperr.BadRequest("domain_reserved", quote(h)+" is used by bitploy itself.",
			"Add a domain you own instead.")
	}

	suffix, icann := publicsuffix.PublicSuffix(h)
	if suffix == h {
		return bad(quote(h)+" is a public suffix, not a domain anyone can own.",
			"Add a name under it, like www."+h+" or myapp."+h+".")
	}
	if !icann && !strings.Contains(suffix, ".") {
		return bad(quote(h)+" ends in an unknown top-level domain (."+suffix+").",
			"Use a domain with a public top-level domain such as .com or .nl.")
	}
	return h, nil
}

// IsApex reports whether the hostname is a registrable domain itself
// (example.com, example.co.uk) rather than a name below one (www.example.com).
// DNS does not allow a CNAME at the apex, so apex domains get A/AAAA records.
// This uses the public suffix list, so shop.example.com counts as a
// subdomain and example.co.uk as an apex.
func IsApex(host string) bool {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(Normalize(host))
	return err == nil && etld1 == Normalize(host)
}

// Targets are the server addresses shown in DNS instructions.
type Targets struct {
	EdgeHost string
	IPv4     string
	IPv6     string
}

// Record is one DNS record the user creates.
type Record struct {
	Type  string // CNAME | A | AAAA | TXT
	Name  string
	Value string
}

// CloudflareNote is shown with every domain.
const CloudflareNote = "If this domain is on Cloudflare, set the CNAME to DNS only (grey cloud). " +
	"A proxied (orange cloud) record points at Cloudflare instead of this server, so the certificate cannot be issued."

// RecordsFor lists the records to create: CNAME to the edge host (or A/AAAA
// to the server IPs for an apex domain) plus the TXT challenge.
func RecordsFor(host, token string, t Targets) (records []Record, notes []string) {
	if IsApex(host) && (t.IPv4 != "" || t.IPv6 != "") {
		if t.IPv4 != "" {
			records = append(records, Record{Type: "A", Name: host, Value: t.IPv4})
		}
		if t.IPv6 != "" {
			records = append(records, Record{Type: "AAAA", Name: host, Value: t.IPv6})
		}
		notes = append(notes, host+" is a root domain, and DNS does not allow a CNAME there, so it needs A records. "+
			"If your DNS provider offers CNAME flattening or ALIAS records, pointing them at "+t.EdgeHost+" works too.")
	} else {
		records = append(records, Record{Type: "CNAME", Name: host, Value: t.EdgeHost})
		if IsApex(host) {
			notes = append(notes, host+" is a root domain: use CNAME flattening or an ALIAS record if your DNS provider offers it, "+
				"or ask the bitploy admin for the server IP address.")
		}
	}
	records = append(records, Record{Type: "TXT", Name: ChallengePrefix + host, Value: token})
	notes = append(notes, CloudflareNote)
	return records, notes
}

func quote(s string) string { return "\"" + s + "\"" }
