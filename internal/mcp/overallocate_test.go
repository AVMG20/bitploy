package mcp

import (
	"strings"
	"testing"
)

// whoami and the rules say when memory and CPU are not checked for the user,
// so an agent does not hold to quota numbers that do not apply.
func TestWhoamiTellsAboutOverallocate(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "whoami", nil); strings.Contains(text(res), "overallocate") {
		t.Fatalf("whoami without the switch: %s", text(res))
	}
	if _, err := e.svc.Store.DB.Exec(`UPDATE users SET overallocate = 1`); err != nil {
		t.Fatal(err)
	}
	if res := call(t, cs, "whoami", nil); !strings.Contains(text(res), "not checked for this user (overallocate is on)") {
		t.Errorf("whoami: %s", text(res))
	}
	res := call(t, cs, "get_deploy_rules", nil)
	if rules, _ := structured(t, res)["rules"].(string); !strings.Contains(rules, "Overallocate is on for this user") {
		t.Errorf("rules: %s", rules)
	}
}
