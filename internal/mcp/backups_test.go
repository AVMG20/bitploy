package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestBackupTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Notes", "slug": "notes-app", "volume": true}); res.IsError {
		t.Fatal(text(res))
	}
	res := call(t, cs, "list_backups", map[string]any{"app": "notes-app"})
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	if items, ok := m["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items = %v", m["items"])
	}
	if _, ok := m["restore"]; !ok {
		t.Errorf("list_backups must report the restore state: %v", m)
	}
	if !strings.Contains(strings.Join(nextSteps(t, "list_backups", res), " "), "create_backup") {
		t.Error("an empty list should point at create_backup")
	}
	// A read-only token may list.
	ro := e.connect(t, e.readToken)
	if res := call(t, ro, "list_backups", map[string]any{"app": "notes-app"}); res.IsError {
		t.Errorf("read-only list_backups: %s", text(res))
	}
	// There is no Docker in this test: the error still has the usual shape.
	assertErrorShape(t, "create_backup without docker", call(t, cs, "create_backup", map[string]any{"app": "notes-app"}))
	// Restoring is not a tool: it stays in the dashboard.
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if strings.Contains(tl.Name, "restore") {
			t.Errorf("tool %s: restoring a backup stays in the dashboard", tl.Name)
		}
	}
}
