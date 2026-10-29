package app

import (
	"context"
	"testing"
)

// The admin override (read every app) is for dashboard sessions only: a leaked
// admin API token, whatever its scope, sees just the admin's own apps.
func TestAdminOverrideNeedsSession(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")

	if _, err := e.svc.LoadApp(ctx, e.admin, "quiet-otter-3f9a", false); err != nil {
		t.Fatalf("admin session should read any app: %v", err)
	}
	for _, scope := range []Scope{ScopeRead, ScopeDeploy} {
		tok := e.admin
		tok.Scope, tok.Channel, tok.TokenID = scope, ChannelAPI, "tok_x"
		_, err := e.svc.LoadApp(ctx, tok, "quiet-otter-3f9a", false)
		wantCode(t, err, "app_not_found")
		_, err = e.svc.ListEnv(ctx, tok, "app_1")
		wantCode(t, err, "app_not_found")
		if scope == ScopeDeploy {
			_, err = e.svc.StopApp(ctx, tok, "quiet-otter-3f9a")
			wantCode(t, err, "app_not_found")
		}
	}
}
