package rules

import (
	"os"
	"strings"
	"testing"
)

// The instructions must be exactly the block in SPEC.md.
func TestInstructionsMatchSpec(t *testing.T) {
	spec, err := os.ReadFile("../../../SPEC.md")
	if err != nil {
		t.Skip("SPEC.md not found")
	}
	s := string(spec)
	start := strings.Index(s, "**Server instructions**")
	if start < 0 {
		t.Skip("no instructions block in SPEC.md")
	}
	open := strings.Index(s[start:], "```markdown\n")
	body := s[start+open+len("```markdown\n"):]
	body = body[:strings.Index(body, "\n```")]
	if got := Instructions("https://<slug>.bitploy.nl", "bitploy.nl"); strings.TrimSpace(got) != strings.TrimSpace(body) {
		t.Errorf("instructions.md differs from SPEC.md:\n--- got\n%s\n--- want\n%s", got, body)
	}
}

func TestInstructionsUseConfiguredDomain(t *testing.T) {
	got := Instructions("http://<slug>.localhost:8000", "localhost")
	if !strings.Contains(got, "http://<slug>.localhost:8000.") || strings.Contains(got, "bitploy.nl") {
		t.Errorf("got:\n%s", got)
	}
}
