package domains

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Resolver is the slice of *net.Resolver the checks use; tests fake it.
type Resolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// Named is a resolver with the label shown in messages.
type Named struct {
	Name string
	R    Resolver
}

// PublicResolvers queries Cloudflare (1.1.1.1) and Google (8.8.8.8) directly,
// ignoring the server's own resolver and its caches.
func PublicResolvers() []Named {
	return []Named{
		{Name: "1.1.1.1", R: resolverFor("1.1.1.1:53")},
		{Name: "8.8.8.8", R: resolverFor("8.8.8.8:53")},
	}
}

func resolverFor(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// Expect is what the DNS must show for one domain.
type Expect struct {
	Host     string
	Token    string
	EdgeHost string
	IPv4     string // server addresses; either may be empty
	IPv6     string
}

// Result is the outcome of a DNS check.
type Result struct {
	OK      bool
	TXTOK   bool
	RouteOK bool
	Problem string // readable, empty when OK
}

// Check resolves the domain through every resolver. It passes only when all
// resolvers see the TXT token and a route to this server: a CNAME ending at
// the edge host, or an address equal to the server's IP.
func Check(ctx context.Context, resolvers []Named, e Expect) Result {
	e.Host, e.EdgeHost = Normalize(e.Host), Normalize(e.EdgeHost)
	type failure struct{ resolver, problem string }
	var fails []failure
	var passing []string
	res := Result{TXTOK: true, RouteOK: true}
	for _, nr := range resolvers {
		txtProblem := checkTXT(ctx, nr.R, e)
		routeProblem := checkRoute(ctx, nr.R, e)
		if txtProblem != "" {
			res.TXTOK = false
		}
		if routeProblem != "" {
			res.RouteOK = false
		}
		if txtProblem != "" || routeProblem != "" {
			fails = append(fails, failure{nr.Name, strings.Join(nonEmpty(routeProblem, txtProblem), ". ")})
		} else {
			passing = append(passing, nr.Name)
		}
	}
	if len(fails) == 0 {
		res.OK = true
		return res
	}
	res.Problem = fails[0].problem
	if len(passing) > 0 {
		res.Problem += fmt.Sprintf(" (as seen by %s; %s already sees the right records, so this may just be DNS spreading)",
			fails[0].resolver, strings.Join(passing, " and "))
	}
	return res
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func checkTXT(ctx context.Context, r Resolver, e Expect) string {
	name := ChallengePrefix + e.Host
	vals, err := r.LookupTXT(ctx, name)
	if err != nil && !isNotFound(err) {
		return "Could not look up the TXT record " + name + ": " + shortErr(err)
	}
	for _, v := range vals {
		if strings.Trim(strings.TrimSpace(v), `"`) == e.Token {
			return ""
		}
	}
	if len(vals) == 0 {
		return fmt.Sprintf("TXT record %s not found, expected %s", name, e.Token)
	}
	return fmt.Sprintf("TXT record %s has value %q, expected %s", name, clip(vals[0], 80), e.Token)
}

func checkRoute(ctx context.Context, r Resolver, e Expect) string {
	cname, cerr := r.LookupCNAME(ctx, e.Host)
	cname = Normalize(cname)
	hasCNAME := cerr == nil && cname != "" && cname != e.Host
	if hasCNAME && cname == e.EdgeHost {
		return ""
	}

	ips, ierr := r.LookupHost(ctx, e.Host)
	want := serverIPs(e)
	if edgeIPs, err := r.LookupHost(ctx, e.EdgeHost); err == nil && e.EdgeHost != "" {
		want = append(want, edgeIPs...)
	}
	for _, ip := range ips {
		for _, w := range want {
			if sameIP(ip, w) {
				return "" // an A/AAAA record equal to the server, or a chain that ends there
			}
		}
	}

	expected := e.EdgeHost
	if len(serverIPs(e)) > 0 {
		expected = strings.Join(serverIPs(e), " or ")
	}
	switch {
	case hasCNAME:
		if cf := firstCloudflare(ips); cf != "" {
			return fmt.Sprintf("CNAME points to %s, which is behind the Cloudflare proxy (orange cloud); set the record to DNS only (grey cloud) and point it to %s", cname, e.EdgeHost)
		}
		return fmt.Sprintf("CNAME points to %s, expected %s", cname, e.EdgeHost)
	case len(ips) > 0:
		if cf := firstCloudflare(ips); cf != "" {
			return fmt.Sprintf("%s resolves to %s, which is the Cloudflare proxy (orange cloud); set the record to DNS only (grey cloud)", e.Host, cf)
		}
		return fmt.Sprintf("A record points to %s, expected %s", strings.Join(ips, ", "), expected)
	case ierr != nil && !isNotFound(ierr):
		return "Could not look up " + e.Host + ": " + shortErr(ierr)
	default:
		return fmt.Sprintf("No DNS record found for %s; create a CNAME to %s", e.Host, e.EdgeHost)
	}
}

func serverIPs(e Expect) []string {
	var out []string
	if e.IPv4 != "" {
		out = append(out, e.IPv4)
	}
	if e.IPv6 != "" {
		out = append(out, e.IPv6)
	}
	return out
}

func sameIP(a, b string) bool {
	pa, pb := net.ParseIP(a), net.ParseIP(b)
	return pa != nil && pb != nil && pa.Equal(pb)
}

func firstCloudflare(ips []string) string {
	for _, ip := range ips {
		if IsCloudflareIP(ip) {
			return ip
		}
	}
	return ""
}

func isNotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

func shortErr(err error) string {
	var de *net.DNSError
	if errors.As(err, &de) {
		if de.IsTimeout {
			return "timed out"
		}
		return clip(de.Err, 80)
	}
	return clip(err.Error(), 80)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
