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

// Every deploy error code an AI can meet has a fix in the rulebook, and
// removed features are answered instead of left out.
func TestRulesCoverNewCodes(t *testing.T) {
	for _, s := range []string{
		"- before_deploy_failed / before_deploy_timeout:", "- after_deploy_failed", "- nothing_to_build",
		"project_move_unsupported", "compose_image_variable", "Only the code goes back",
	} {
		if !strings.Contains(RuntimeContract, s) {
			t.Errorf("runtime contract lacks %q", s)
		}
	}
	if strings.Contains(RuntimeContract, "chosen in the dashboard") {
		t.Error("build_mode auto is described as dashboard-only")
	}
}

func TestInstructionsUseConfiguredDomain(t *testing.T) {
	got := Instructions("http://<slug>.localhost:8000", "localhost")
	if !strings.Contains(got, "http://<slug>.localhost:8000.") || strings.Contains(got, "bitploy.nl") {
		t.Errorf("got:\n%s", got)
	}
}
