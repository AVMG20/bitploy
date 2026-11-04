package domains

import "testing"

func TestValidateAppDomain(t *testing.T) {
	rules := AppDomainRules{
		Existing:   []string{"bitploy.nl", "apps.example.org"},
		PublicHost: "console.example.net",
		EdgeHost:   "edge.bitploy.nl",
	}
	ok := map[string]string{
		"example.com":            "example.com",
		"*.Example.COM.":         "example.com",
		" https://example.com/ ": "example.com",
		"apps.example.net":       "apps.example.net",
		"example.net":            "example.net", // the dashboard below it is handled per slug
	}
	for in, want := range ok {
		got, err := ValidateAppDomain(in, rules)
		if err != nil || got != want {
			t.Errorf("ValidateAppDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":                    "app_domain_invalid",
		"bitploy.nl":          "app_domain_exists",
		"BITPLOY.NL":          "app_domain_exists",
		"x.bitploy.nl":        "app_domain_invalid", // below an app domain
		"example.org":         "app_domain_invalid", // above an app domain
		"console.example.net": "app_domain_invalid", // the dashboard
		"edge.bitploy.nl":     "app_domain_invalid",
		"exa mple.com":        "app_domain_invalid",
		"localhost":           "app_domain_invalid",
		"co.uk":               "app_domain_invalid", // a public suffix
		"10.0.0.1":            "app_domain_invalid",
		"a.*.example.com":     "app_domain_invalid",
		"-bad.example.com":    "app_domain_invalid",
		"example.com:8443":    "app_domain_invalid",
	}
	for in, code := range bad {
		if _, err := ValidateAppDomain(in, rules); err == nil || err.Code != code {
			t.Errorf("ValidateAppDomain(%q) = %v, want %s", in, err, code)
		} else if err.Hint == "" {
			t.Errorf("ValidateAppDomain(%q): no hint", in)
		}
	}
}

func TestCustomDomainsCannotUseAnyAppDomain(t *testing.T) {
	r := Rules{AppsDomains: []string{"bitploy.nl", "example.com"}}
	for _, h := range []string{"shop.bitploy.nl", "example.com", "www.example.com"} {
		if _, err := Validate(h, r); err == nil || err.Code != "domain_reserved" {
			t.Errorf("Validate(%q) = %v, want domain_reserved", h, err)
		}
	}
	if _, err := Validate("www.example.org", r); err != nil {
		t.Errorf("www.example.org: %v", err)
	}
}
