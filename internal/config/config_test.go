package config

import "testing"

func TestAppURLOnDomain(t *testing.T) {
	c := &Config{AppsDomain: "bitploy.nl", AppsScheme: "https"}
	for _, tc := range []struct{ domain, url, host string }{
		{"", "https://shop.bitploy.nl", "shop.bitploy.nl"},
		{"example.com", "https://shop.example.com", "shop.example.com"},
	} {
		if got := c.AppURLOn("shop", tc.domain); got != tc.url {
			t.Errorf("AppURLOn(%q) = %q, want %q", tc.domain, got, tc.url)
		}
		if got := c.AppHostOn("shop", tc.domain); got != tc.host {
			t.Errorf("AppHostOn(%q) = %q, want %q", tc.domain, got, tc.host)
		}
	}
	if c.AppURL("shop") != "https://shop.bitploy.nl" || c.AppHost("shop") != "shop.bitploy.nl" {
		t.Error("AppURL and AppHost must use the default domain")
	}
	dev := &Config{AppsDomain: "localhost", AppsScheme: "http", AppsPort: "8000"}
	if got := dev.AppURLOn("shop", "example.test"); got != "http://shop.example.test:8000" {
		t.Errorf("dev URL = %q", got)
	}
}

func TestParsePortRange(t *testing.T) {
	for _, tc := range []struct {
		in       string
		from, to int
		ok       bool
	}{
		{"", 0, 0, true},
		{"off", 0, 0, true},
		{"30000-30099", 30000, 30099, true},
		{" 40000 - 40000 ", 40000, 40000, true},
		{"40000", 40000, 40000, true},
		{"30100-30000", 0, 0, false},
		{"80-90", 0, 0, false},
		{"30000-70000", 0, 0, false},
		{"20000-29999", 0, 0, false}, // 10000 ports
		{"8000-8100", 0, 0, false},   // holds 8080 and 8081
		{"a-b", 0, 0, false},
	} {
		from, to, err := ParsePortRange(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParsePortRange(%q) error = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && (from != tc.from || to != tc.to) {
			t.Errorf("ParsePortRange(%q) = %d-%d, want %d-%d", tc.in, from, to, tc.from, tc.to)
		}
	}
}

func TestLoadPublicPorts(t *testing.T) {
	t.Setenv("BITPLOY_SECRET_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("BITPLOY_PUBLIC_PORTS", "")
	t.Setenv("BITPLOY_PUBLIC_PORTS_HOST", "")
	t.Setenv("BITPLOY_PUBLIC_PORTS_PER_USER", "")
	t.Setenv("BITPLOY_SERVER_IPV4", "")
	t.Setenv("BITPLOY_EDGE_HOST", "")
	t.Setenv("BITPLOY_APPS_DOMAIN", "bitploy.test")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicPortsOn() || c.PublicPortsPerUser != DefaultPublicPortsPerUser || c.PublicPortsHost != "edge.bitploy.test" {
		t.Fatalf("defaults: on=%v per user=%d host=%q", c.PublicPortsOn(), c.PublicPortsPerUser, c.PublicPortsHost)
	}
	t.Setenv("BITPLOY_PUBLIC_PORTS", "30000-30009")
	t.Setenv("BITPLOY_SERVER_IPV4", "203.0.113.10")
	t.Setenv("BITPLOY_PUBLIC_PORTS_PER_USER", "1")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	}
	if !c.PublicPortsOn() || c.PublicPortsFrom != 30000 || c.PublicPortsTo != 30009 || c.PublicPortsPerUser != 1 || c.PublicPortsHost != "203.0.113.10" {
		t.Fatalf("set: %+v", c)
	}
	t.Setenv("BITPLOY_PUBLIC_PORTS", "1-2")
	if _, err := Load(); err == nil {
		t.Fatal("a bad range must fail")
	}
}
