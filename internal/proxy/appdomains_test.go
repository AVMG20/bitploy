package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCatchAllCoversEveryAppDomain(t *testing.T) {
	// One domain: the rule is unchanged from before extra domains existed.
	if got := CatchAllRule([]string{"bitploy.nl"}); got != "HostRegexp(`^[a-z0-9-]+\\.bitploy\\.nl$`)" {
		t.Fatalf("single domain rule = %s", got)
	}
	w := &Writer{Dir: t.TempDir(), EntryPoint: "websecure", TLS: true}
	if err := w.WriteCatchAll([]string{"bitploy.nl", "Example.COM", "apps.example.org"}, "http://bitploy:8080"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(w.Dir, "_catchall.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	rt := f.HTTP.Routers["bitploy-catchall"]
	if rt.Priority != 1 || rt.TLS == nil || rt.Service != "bitploy-catchall" {
		t.Fatalf("router = %+v", rt)
	}
	m := regexp.MustCompile("`(.*)`").FindStringSubmatch(rt.Rule)
	if m == nil {
		t.Fatalf("rule = %s", rt.Rule)
	}
	re := regexp.MustCompile(m[1])
	for host, want := range map[string]bool{
		"shop.bitploy.nl":       true,
		"shop.example.com":      true,
		"shop.apps.example.org": true,
		"a.b.example.com":       false, // one label only
		"example.com":           false,
		"shop.example.org":      false,
		"shopxexample.com":      false, // the dot is literal
		"shop.example.com.evil": false,
	} {
		if got := re.MatchString(host); got != want {
			t.Errorf("%s matches = %v, want %v (rule %s)", host, got, want, rt.Rule)
		}
	}
}

func TestWriteCerts(t *testing.T) {
	w := &Writer{Dir: t.TempDir(), TraefikDir: "/dynamic"}
	certs := []Cert{
		{Domain: "example.com", Cert: "CERT-A\n", Key: "KEY-A\n"},
		{Domain: "other.org", Cert: "CERT-B\n", Key: "KEY-B\n"},
		{Domain: "../evil", Cert: "x", Key: "y"}, // never a path outside the folder
		{Domain: "nocert.net"},                   // no certificate: not listed
	}
	if err := w.WriteCerts(certs); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"example.com.crt", "example.com.key", "other.org.crt", "other.org.key"} {
		st, err := os.Stat(filepath.Join(w.Dir, "certs", f))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", f, st.Mode().Perm())
		}
	}
	b, err := os.ReadFile(filepath.Join(w.Dir, "_certs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var conf struct {
		TLS struct {
			Certificates []struct {
				CertFile string `yaml:"certFile"`
				KeyFile  string `yaml:"keyFile"`
			} `yaml:"certificates"`
		} `yaml:"tls"`
	}
	if err := yaml.Unmarshal(b, &conf); err != nil {
		t.Fatal(err)
	}
	if len(conf.TLS.Certificates) != 2 || conf.TLS.Certificates[0].CertFile != "/dynamic/certs/example.com.crt" ||
		conf.TLS.Certificates[1].KeyFile != "/dynamic/certs/other.org.key" {
		t.Fatalf("_certs.yml:\n%s", b)
	}
	if strings.Contains(string(b), "KEY-A") {
		t.Fatal("the key must not be in the YAML")
	}
	// A route file listing must not see the certificates.
	if got, _ := w.List(); len(got) != 0 {
		t.Fatalf("List = %v", got)
	}

	// The next write drops the files of a domain that is gone.
	if err := w.WriteCerts(certs[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, "certs", "other.org.key")); !os.IsNotExist(err) {
		t.Fatalf("stale key still there: %v", err)
	}
	if err := w.WriteCerts(nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(w.Dir, "_certs.yml"))
	if strings.Contains(string(b), "example.com") {
		t.Fatalf("_certs.yml after removing all:\n%s", b)
	}
}
