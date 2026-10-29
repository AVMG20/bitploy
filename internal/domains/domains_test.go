package domains

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

var testRules = Rules{AppsDomain: "bitploy.nl", PublicHost: "bitploy.nl", EdgeHost: "edge.bitploy.nl"}

func TestValidate(t *testing.T) {
	tests := []struct {
		in       string
		want     string
		wantCode string
	}{
		{in: "www.example.com", want: "www.example.com"},
		{in: "  WWW.Example.COM. ", want: "www.example.com"},
		{in: "example.com", want: "example.com"},
		{in: "shop.example.co.uk", want: "shop.example.co.uk"},
		{in: "my-app.example.nl", want: "my-app.example.nl"},
		{in: "xn--bcher-kva.example", want: "", wantCode: "domain_invalid"}, // .example is not a public TLD
		{in: "", wantCode: "domain_invalid"},
		{in: "https://www.example.com", wantCode: "domain_invalid"},
		{in: "www.example.com/path", wantCode: "domain_invalid"},
		{in: "www.example.com:8080", wantCode: "domain_invalid"},
		{in: "user@example.com", wantCode: "domain_invalid"},
		{in: "*.example.com", wantCode: "domain_invalid"},
		{in: "203.0.113.10", wantCode: "domain_invalid"},
		{in: "localhost", wantCode: "domain_invalid"},
		{in: "nodots", wantCode: "domain_invalid"},
		{in: "a..example.com", wantCode: "domain_invalid"},
		{in: "-bad.example.com", wantCode: "domain_invalid"},
		{in: "bad-.example.com", wantCode: "domain_invalid"},
		{in: "under_score.example.com", wantCode: "domain_invalid"},
		{in: "bücher.example.com", wantCode: "domain_invalid"},
		{in: strings.Repeat("a", 64) + ".example.com", wantCode: "domain_invalid"},
		{in: "co.uk", wantCode: "domain_invalid"},
		{in: "host.internal", wantCode: "domain_invalid"},
		{in: "bitploy.nl", wantCode: "domain_reserved"},
		{in: "quiet-otter-3f9a.bitploy.nl", wantCode: "domain_reserved"},
		{in: "Deep.Sub.Bitploy.NL", wantCode: "domain_reserved"},
		{in: "notbitploy.nl", want: "notbitploy.nl"},
		{in: "evilbitploy.nl", want: "evilbitploy.nl"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Validate(tt.in, testRules)
			if tt.wantCode != "" {
				if err == nil || err.Code != tt.wantCode {
					t.Fatalf("Validate(%q) = %q, %v; want code %s", tt.in, got, err, tt.wantCode)
				}
				if err.Hint == "" {
					t.Errorf("error has no hint: %+v", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Validate(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestValidateReservedExternalPublicHost(t *testing.T) {
	r := Rules{AppsDomain: "apps.example.net", PublicHost: "console.example.net", EdgeHost: "edge.example.net"}
	for _, h := range []string{"console.example.net", "edge.example.net", "x.apps.example.net"} {
		if _, err := Validate(h, r); err == nil || err.Code != "domain_reserved" {
			t.Errorf("Validate(%q) = %v, want domain_reserved", h, err)
		}
	}
	if _, err := Validate("other.example.net", r); err != nil {
		t.Errorf("other.example.net should be allowed: %v", err)
	}
}

func TestIsApex(t *testing.T) {
	tests := map[string]bool{
		"example.com":         true,
		"www.example.com":     false,
		"example.co.uk":       true,
		"shop.example.co.uk":  false,
		"a.b.c.example.com":   false,
		"user.github.io":      true, // github.io is a private suffix: user.github.io is registrable
		"myapp.herokuapp.com": true,
	}
	for host, want := range tests {
		if got := IsApex(host); got != want {
			t.Errorf("IsApex(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestRecordsFor(t *testing.T) {
	both := Targets{EdgeHost: "edge.bitploy.nl", IPv4: "203.0.113.10", IPv6: "2001:db8::10"}
	tests := []struct {
		name  string
		host  string
		t     Targets
		want  []Record
		notes int
	}{
		{
			name: "subdomain gets a CNAME", host: "www.example.com", t: both,
			want: []Record{
				{"CNAME", "www.example.com", "edge.bitploy.nl"},
				{"TXT", "_bitploy-challenge.www.example.com", "tok"},
			},
			notes: 1,
		},
		{
			name: "apex gets A and AAAA", host: "example.com", t: both,
			want: []Record{
				{"A", "example.com", "203.0.113.10"},
				{"AAAA", "example.com", "2001:db8::10"},
				{"TXT", "_bitploy-challenge.example.com", "tok"},
			},
			notes: 2,
		},
		{
			name: "apex without IPv6", host: "example.nl", t: Targets{EdgeHost: "edge.bitploy.nl", IPv4: "203.0.113.10"},
			want: []Record{
				{"A", "example.nl", "203.0.113.10"},
				{"TXT", "_bitploy-challenge.example.nl", "tok"},
			},
			notes: 2,
		},
		{
			name: "apex without server IPs falls back to CNAME", host: "example.nl", t: Targets{EdgeHost: "edge.bitploy.nl"},
			want: []Record{
				{"CNAME", "example.nl", "edge.bitploy.nl"},
				{"TXT", "_bitploy-challenge.example.nl", "tok"},
			},
			notes: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, notes := RecordsFor(tt.host, "tok", tt.t)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("records = %v, want %v", got, tt.want)
			}
			if len(notes) != tt.notes {
				t.Errorf("got %d notes, want %d: %v", len(notes), tt.notes, notes)
			}
			if last := notes[len(notes)-1]; !strings.Contains(last, "DNS only (grey cloud)") {
				t.Errorf("missing Cloudflare note: %q", last)
			}
		})
	}
}

// fakeResolver answers from maps; missing names are NXDOMAIN.
type fakeResolver struct {
	cname map[string]string
	hosts map[string][]string
	txt   map[string][]string
	err   error // returned for every lookup when set
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (f *fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if c, ok := f.cname[host]; ok {
		return c + ".", nil
	}
	if _, ok := f.hosts[host]; ok {
		return host + ".", nil
	}
	return "", notFound(host)
}

func (f *fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.hosts[host]; ok {
		return v, nil
	}
	return nil, notFound(host)
}

func (f *fakeResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, notFound(name)
}

func TestCheck(t *testing.T) {
	const host, token, edge, ip4 = "www.example.com", "bitploy-abc123", "edge.bitploy.nl", "203.0.113.10"
	exp := Expect{Host: host, Token: token, EdgeHost: edge, IPv4: ip4}
	goodTXT := map[string][]string{"_bitploy-challenge.www.example.com": {token}}
	edgeHosts := map[string][]string{edge: {ip4}}
	merge := func(base map[string][]string, extra map[string][]string) map[string][]string {
		out := map[string][]string{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	tests := []struct {
		name        string
		r           *fakeResolver
		exp         Expect
		wantOK      bool
		wantProblem string
	}{
		{
			name:   "CNAME to the edge host and TXT token",
			r:      &fakeResolver{cname: map[string]string{host: edge}, hosts: merge(edgeHosts, map[string][]string{host: {ip4}}), txt: goodTXT},
			exp:    exp,
			wantOK: true,
		},
		{
			name:   "CNAME to edge passes even when edge IPs are unknown",
			r:      &fakeResolver{cname: map[string]string{host: edge}, txt: goodTXT},
			exp:    Expect{Host: host, Token: token, EdgeHost: edge},
			wantOK: true,
		},
		{
			name:   "chain ending at the server IP passes",
			r:      &fakeResolver{cname: map[string]string{host: "lb.example.org"}, hosts: merge(edgeHosts, map[string][]string{host: {ip4}}), txt: goodTXT},
			exp:    exp,
			wantOK: true,
		},
		{
			name:   "A record equal to the server IP",
			r:      &fakeResolver{hosts: merge(edgeHosts, map[string][]string{host: {ip4}}), txt: goodTXT},
			exp:    exp,
			wantOK: true,
		},
		{
			name:   "IPv6 server address matches in any notation",
			r:      &fakeResolver{hosts: map[string][]string{host: {"2001:0db8:0000:0000:0000:0000:0000:0010"}}, txt: goodTXT},
			exp:    Expect{Host: host, Token: token, EdgeHost: edge, IPv6: "2001:db8::10"},
			wantOK: true,
		},
		{
			name:        "CNAME to a parking page",
			r:           &fakeResolver{cname: map[string]string{host: "parking.godaddy.com"}, hosts: merge(edgeHosts, map[string][]string{host: {"198.51.100.7"}}), txt: goodTXT},
			exp:         exp,
			wantProblem: "CNAME points to parking.godaddy.com, expected edge.bitploy.nl",
		},
		{
			name:        "A record to another server",
			r:           &fakeResolver{hosts: merge(edgeHosts, map[string][]string{host: {"198.51.100.7"}}), txt: goodTXT},
			exp:         exp,
			wantProblem: "A record points to 198.51.100.7, expected 203.0.113.10",
		},
		{
			name:        "still behind the Cloudflare proxy",
			r:           &fakeResolver{hosts: merge(edgeHosts, map[string][]string{host: {"104.21.5.9"}}), txt: goodTXT},
			exp:         exp,
			wantProblem: "Cloudflare proxy (orange cloud)",
		},
		{
			name:        "no record at all",
			r:           &fakeResolver{hosts: edgeHosts, txt: goodTXT},
			exp:         exp,
			wantProblem: "No DNS record found for www.example.com; create a CNAME to edge.bitploy.nl",
		},
		{
			name:        "TXT missing",
			r:           &fakeResolver{cname: map[string]string{host: edge}, hosts: edgeHosts},
			exp:         exp,
			wantProblem: "TXT record _bitploy-challenge.www.example.com not found, expected bitploy-abc123",
		},
		{
			name:        "TXT has the wrong value",
			r:           &fakeResolver{cname: map[string]string{host: edge}, hosts: edgeHosts, txt: map[string][]string{"_bitploy-challenge.www.example.com": {"someone-else"}}},
			exp:         exp,
			wantProblem: `has value "someone-else", expected bitploy-abc123`,
		},
		{
			name:   "quoted TXT value is accepted",
			r:      &fakeResolver{cname: map[string]string{host: edge}, hosts: edgeHosts, txt: map[string][]string{"_bitploy-challenge.www.example.com": {`"` + token + `"`}}},
			exp:    exp,
			wantOK: true,
		},
		{
			name:        "both wrong gives both problems",
			r:           &fakeResolver{cname: map[string]string{host: "parking.godaddy.com"}, hosts: edgeHosts},
			exp:         exp,
			wantProblem: "CNAME points to parking.godaddy.com, expected edge.bitploy.nl. TXT record",
		},
		{
			name:        "resolver failure is reported",
			r:           &fakeResolver{err: &net.DNSError{Err: "i/o timeout", IsTimeout: true}},
			exp:         exp,
			wantProblem: "timed out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Check(context.Background(), []Named{{Name: "1.1.1.1", R: tt.r}}, tt.exp)
			if res.OK != tt.wantOK {
				t.Fatalf("OK = %v (%s), want %v", res.OK, res.Problem, tt.wantOK)
			}
			if tt.wantOK && res.Problem != "" {
				t.Errorf("unexpected problem %q", res.Problem)
			}
			if !tt.wantOK && !strings.Contains(res.Problem, tt.wantProblem) {
				t.Errorf("problem = %q, want it to contain %q", res.Problem, tt.wantProblem)
			}
		})
	}
}

func TestCheckRequiresEveryResolver(t *testing.T) {
	const host, token, edge = "www.example.com", "bitploy-abc", "edge.bitploy.nl"
	good := &fakeResolver{
		cname: map[string]string{host: edge},
		hosts: map[string][]string{edge: {"203.0.113.10"}},
		txt:   map[string][]string{"_bitploy-challenge." + host: {token}},
	}
	stale := &fakeResolver{hosts: map[string][]string{edge: {"203.0.113.10"}}}
	exp := Expect{Host: host, Token: token, EdgeHost: edge, IPv4: "203.0.113.10"}

	res := Check(context.Background(), []Named{{"1.1.1.1", good}, {"8.8.8.8", good}}, exp)
	if !res.OK {
		t.Fatalf("both resolvers agree but check failed: %s", res.Problem)
	}
	res = Check(context.Background(), []Named{{"1.1.1.1", good}, {"8.8.8.8", stale}}, exp)
	if res.OK {
		t.Fatal("check passed although 8.8.8.8 sees nothing")
	}
	if !strings.Contains(res.Problem, "as seen by 8.8.8.8") || !strings.Contains(res.Problem, "1.1.1.1 already sees") {
		t.Errorf("problem should name the resolvers: %q", res.Problem)
	}
}

func TestIsCloudflareIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"104.21.5.9": true, "172.67.1.1": true, "2606:4700::1": true,
		"203.0.113.10": false, "88.198.1.1": false, "not-an-ip": false,
	} {
		if got := IsCloudflareIP(ip); got != want {
			t.Errorf("IsCloudflareIP(%q) = %v, want %v", ip, got, want)
		}
	}
}
