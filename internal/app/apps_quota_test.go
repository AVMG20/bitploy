package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// apps_quota_exceeded names the user's apps, so an agent can ask the user
// which one to remove without calling list_apps first.
func TestAppsQuotaListsTheApps(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 2 WHERE id = ?`, e.user.UserID)
	deployed := e.mustApp(t, CreateAppInput{Slug: "my-blog"})
	if _, err := e.s.DeployFiles(ctx, e.user, deployed.Slug, staticFiles()); err != nil {
		t.Fatal(err)
	}
	e.mustApp(t, CreateAppInput{Slug: "empty-one"})

	_, err := e.s.CreateApp(ctx, e.user, CreateAppInput{})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "apps_quota_exceeded" {
		t.Fatalf("err = %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	for _, want := range []string{"You already have 2 apps", "my-blog (deploying, last deployed " + today + ")", "empty-one (new, never deployed)"} {
		if !strings.Contains(ae.Message, want) {
			t.Errorf("message lacks %q: %s", want, ae.Message)
		}
	}
	if !strings.Contains(ae.Hint, "Ask the user") || ae.Next != "delete_app" {
		t.Errorf("hint %q, next %q", ae.Hint, ae.Next)
	}
	apps, _ := ae.Details["apps"].([]map[string]any)
	if len(apps) != 2 {
		t.Fatalf("details = %v", ae.Details)
	}
	for _, a := range apps {
		switch a["slug"] {
		case "my-blog":
			if _, ok := a["last_deployed_at"].(time.Time); !ok || a["status"] != "deploying" {
				t.Errorf("deployed app: %v", a)
			}
		case "empty-one":
			if a["last_deployed_at"] != nil || a["status"] != "new" {
				t.Errorf("new app: %v", a)
			}
		default:
			t.Errorf("unexpected app %v", a)
		}
	}
}
