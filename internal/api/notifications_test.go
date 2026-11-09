package api

import (
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestNotificationEndpoints(t *testing.T) {
	e := newEnv(t)
	e.login("me@example.nl", false)

	opts := decode[app.NotificationOptions](t, e.asSession("GET", "/api/v1/notifications/options", nil))
	if len(opts.Events) == 0 || len(opts.Types) != 2 || opts.VAPIDPublicKey == "" {
		t.Fatalf("options = %+v", opts)
	}

	hook := "https://discord.com/api/webhooks/112233445566778899/secret-token-abcdefghijklmnop"
	w := e.asSession("POST", "/api/v1/notification-channels", map[string]any{"type": "discord", "name": "#deploys", "config": map[string]any{"webhook_url": hook}})
	if w.Code != 201 || strings.Contains(w.Body.String(), "secret-token") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	c := decode[app.NotificationChannel](t, w)

	w = e.asSession("PATCH", "/api/v1/notification-channels/"+c.ID, map[string]any{"name": "#ops", "config": map[string]any{}})
	if w.Code != 200 || decode[app.NotificationChannel](t, w).Config["webhook_id"] != "112233445566778899" {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	list := decode[app.Page[app.NotificationChannel]](t, e.asSession("GET", "/api/v1/notification-channels", nil))
	if len(list.Items) != 1 || list.Items[0].Name != "#ops" {
		t.Fatalf("list = %+v", list)
	}

	// A token cannot manage channels: they hold secrets and reach people.
	tok := decode[app.TokenCreated](t, e.asSession("POST", "/api/v1/tokens", map[string]any{"name": "cli", "scope": "deploy"}))
	bearer := map[string]string{"Authorization": "Bearer " + tok.Value}
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/notifications/options"},
		{"GET", "/api/v1/notification-channels"},
		{"DELETE", "/api/v1/notification-channels/" + c.ID},
		{"POST", "/api/v1/notification-channels/" + c.ID + "/test"},
	} {
		if w := e.req(r.method, r.path, nil, bearer); w.Code != 403 {
			t.Errorf("token %s %s: %d %s", r.method, r.path, w.Code, w.Body)
		}
	}

	if w := e.asSession("DELETE", "/api/v1/notification-channels/"+c.ID, nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("DELETE", "/api/v1/notification-channels/"+c.ID, nil); w.Code != 404 {
		t.Errorf("delete again: %d %s", w.Code, w.Body)
	}
}
