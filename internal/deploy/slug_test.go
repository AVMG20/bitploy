package deploy

import (
	"regexp"
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		slug     string
		wantCode string
	}{
		{"my-app", ""},
		{"abc", ""},
		{"quiet-otter-3f9a", ""},
		{strings.Repeat("a", 40), ""},
		{"a1b", ""},
		{"ab", "slug_invalid"},
		{"", "slug_invalid"},
		{strings.Repeat("a", 41), "slug_invalid"},
		{"My-App", "slug_invalid"},
		{"my_app", "slug_invalid"},
		{"my.app", "slug_invalid"},
		{"api.myapp", "slug_invalid"},
		{"my app", "slug_invalid"},
		{"-abc", "slug_invalid"},
		{"abc-", "slug_invalid"},
		{"emoji-😀", "slug_invalid"},
		{"www", "slug_reserved"},
		{"api", "slug_reserved"},
		{"mcp", "slug_reserved"},
		{"admin", "slug_reserved"},
		{"dashboard", "slug_reserved"},
		{"docs", "slug_reserved"},
		{"status", "slug_reserved"},
		{"mail", "slug_reserved"},
		{"edge", "slug_reserved"},
		{"custom", "slug_reserved"},
		{"cdn", "slug_reserved"},
		{"static", "slug_reserved"},
		{"assets", "slug_reserved"},
		{"auth", "slug_reserved"},
		{"login", "slug_reserved"},
		{"bitploy", "slug_reserved"},
		{"traefik", "slug_reserved"},
		{"app", "slug_reserved"},
		{"bp-foo", "slug_reserved"},
		{"bp-", "slug_invalid"},
		{"bp-test-x", "slug_reserved"},
		{"bpfoo", ""},
		{"apps", ""},
	}
	for _, tc := range tests {
		err := ValidateSlug(tc.slug)
		got := ""
		if err != nil {
			got = err.Code
			if err.Hint == "" || err.Message == "" {
				t.Errorf("%q: error needs message and hint", tc.slug)
			}
		}
		if got != tc.wantCode {
			t.Errorf("ValidateSlug(%q) = %q, want %q", tc.slug, got, tc.wantCode)
		}
	}
}

func TestGenerateSlug(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := GenerateSlug()
		if !re.MatchString(s) {
			t.Fatalf("slug %q does not match adjective-noun-xxxx", s)
		}
		if err := ValidateSlug(s); err != nil {
			t.Fatalf("generated slug %q is invalid: %v", s, err)
		}
		seen[s] = true
	}
	if len(seen) < 150 {
		t.Errorf("only %d distinct slugs in 200 tries", len(seen))
	}
}
