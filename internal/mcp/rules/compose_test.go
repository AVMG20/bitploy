package rules

import (
	"strings"
	"testing"
)

// Compose is the extra door: the guidance keeps SQLite in /data the default
// and never has the AI write a compose file just to deploy.
func TestComposeGuidance(t *testing.T) {
	ins := Instructions("https://<slug>.bitploy.nl", "bitploy.nl")
	for _, s := range []string{"SQLite in /data", "validate_compose", "Never write a compose file just to deploy."} {
		if !strings.Contains(ins, s) {
			t.Errorf("instructions lack %q", s)
		}
	}
	for _, s := range []string{"## Compose apps", "Never write a compose file just to deploy", "compose_ignored (the project has a compose file", "## Compose checks (validate_compose)", "set_per_service"} {
		if !strings.Contains(RuntimeContract, s) {
			t.Errorf("runtime contract lacks %q", s)
		}
	}
}
