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
