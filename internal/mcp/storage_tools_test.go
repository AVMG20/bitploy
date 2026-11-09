package mcp

import (
	"strings"
	"testing"
)

func TestStorageTools(t *testing.T) {
	e := newEnv(t)
	cs := e.connect(t, e.deployToken)
	if res := call(t, cs, "create_app", map[string]any{"name": "Notes", "slug": "notes-app", "volume": true}); res.IsError {
		t.Fatal(text(res))
	}
	e.exec(t, `INSERT INTO app_backups (id, app_id, kind, status, size_bytes, created_at)
		SELECT 'bak_old', id, 'scheduled', 'ready', 3145728, 1700000000 FROM apps WHERE slug = 'notes-app'`)

	res := call(t, cs, "get_storage", nil)
	if res.IsError {
		t.Fatal(text(res))
	}
	m := structured(t, res)
	apps, _ := m["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("apps = %v", m["apps"])
	}
	row := apps[0].(map[string]any)
	backups, _ := row["backups"].([]any)
	if row["slug"] != "notes-app" || len(backups) != 1 || backups[0].(map[string]any)["size_mb"] != float64(3) {
		t.Errorf("row = %v", row)
	}
	if !strings.Contains(strings.Join(nextSteps(t, "get_storage", res), " "), "admin") {
		t.Error("get_storage should say to ask the admin when deleting is not enough")
	}

	// A read-only token may look but not delete.
	ro := e.connect(t, e.readToken)
	if res := call(t, ro, "get_storage", nil); res.IsError {
		t.Errorf("read-only get_storage: %s", text(res))
	}
	if res := call(t, ro, "delete_backup", map[string]any{"app": "notes-app", "id": "bak_old"}); !res.IsError {
		t.Error("a read token must not delete a backup")
	}

	res = call(t, cs, "delete_backup", map[string]any{"app": "notes-app", "id": "bak_old"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if !strings.Contains(text(res), "3.0 MB freed") {
		t.Errorf("summary = %s", text(res))
	}
	assertErrorShape(t, "delete_backup again", call(t, cs, "delete_backup", map[string]any{"app": "notes-app", "id": "bak_old"}))

	// Never deployed: nothing to edit, and the reason says why.
	res = call(t, cs, "get_dockerfile", map[string]any{"app": "notes-app"})
	if res.IsError {
		t.Fatal(text(res))
	}
	if d := structured(t, res); d["editable"] != false || d["reason"] == "" {
		t.Errorf("get_dockerfile = %v", d)
	}
	assertErrorShape(t, "set_dockerfile without source", call(t, cs, "set_dockerfile", map[string]any{"app": "notes-app", "content": "FROM nginx:1.27\n"}))
	// There is no Docker in this test: the error still has the usual shape.
	assertErrorShape(t, "list_files without docker", call(t, cs, "list_files", map[string]any{"app": "notes-app"}))
}

func TestInstructionsMentionNotifications(t *testing.T) {
	e := newEnv(t)
	if !strings.Contains(Instructions(e.svc), "Settings > Notifications") {
		t.Error("the instructions should say where notifications are turned on")
	}
}
