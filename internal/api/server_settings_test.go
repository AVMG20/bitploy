package api

import (
	"testing"

	"github.com/AVMG20/bitploy/internal/app"
)

func TestServerSettingsEndpoints(t *testing.T) {
	// A user: the rules are public, the settings are not.
	e := newEnv(t)
	if w := e.req("GET", "/api/v1/signup-rules", nil, nil); w.Code != 200 {
		t.Fatalf("rules without login: %d %s", w.Code, w.Body)
	} else if r := decode[app.SignupRules](t, w); r.OpenSignup || !r.ProjectInviteSignup {
		t.Errorf("default rules = %+v", r)
	}
	e.login("me@example.nl", false)
	if w := e.asSession("GET", "/api/v1/admin/settings", nil); w.Code != 403 {
		t.Errorf("user reads settings: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("PATCH", "/api/v1/admin/settings", map[string]any{"signups": "open"}); w.Code != 403 {
		t.Errorf("user changes settings: %d %s", w.Code, w.Body)
	}
	if w := e.asSession("GET", "/api/v1/admin/project-invites", nil); w.Code != 403 {
		t.Errorf("user lists all project invites: %d %s", w.Code, w.Body)
	}

	// An admin.
	a := newEnv(t)
	a.login("admin@example.nl", true)
	w := a.asSession("GET", "/api/v1/admin/settings", nil)
	if w.Code != 200 {
		t.Fatalf("admin reads: %d %s", w.Code, w.Body)
	}
	if st := decode[app.ServerSettings](t, w); st.Signups != "any_invite" || st.NewAccountQuota.Apps != 10 {
		t.Errorf("settings = %+v", st)
	}
	w = a.asSession("PATCH", "/api/v1/admin/settings", map[string]any{
		"signups": "admin_invites", "project_invite_apps": false,
		"new_account_quota": map[string]any{"apps": 2, "memory_mb": 512, "cpu_millis": 500, "deploys_per_hour": 5, "domains_per_app": 1, "volume_mb": 2048},
	})
	if w.Code != 200 {
		t.Fatalf("admin changes: %d %s", w.Code, w.Body)
	}
	if st := decode[app.ServerSettings](t, w); st.Signups != "admin_invites" || st.ProjectInviteApps || st.NewAccountQuota.Apps != 2 {
		t.Errorf("after the change = %+v", st)
	}
	if w := a.asSession("PATCH", "/api/v1/admin/settings", map[string]any{"signups": "everyone"}); w.Code != 422 && w.Code != 400 {
		t.Errorf("unknown mode: %d %s", w.Code, w.Body)
	}
	if r := decode[app.SignupRules](t, a.req("GET", "/api/v1/signup-rules", nil, nil)); r.OpenSignup || r.ProjectInviteSignup {
		t.Errorf("rules = %+v", r)
	}
	w = a.req("POST", "/api/v1/invites/open/accept", map[string]any{"name": "N", "email": "n@example.nl", "password": "password-1234"}, map[string]string{"Origin": pub})
	if w.Code != 403 || decode[errResp](t, w).Error.Code != "signup_closed" {
		t.Errorf("open signup while closed: %d %s", w.Code, w.Body)
	}
	if w := a.asSession("DELETE", "/api/v1/admin/project-invites/pin_nope", nil); w.Code != 404 {
		t.Errorf("revoke unknown: %d %s", w.Code, w.Body)
	}
	if w := a.asSession("GET", "/api/v1/admin/project-invites", nil); w.Code != 200 {
		t.Errorf("admin lists project invites: %d %s", w.Code, w.Body)
	}
}
