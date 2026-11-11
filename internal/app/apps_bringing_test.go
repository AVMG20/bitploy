package app

import (
	"context"
	"testing"
)

// While restart_app replaces the containers, the app reads "restarting"
// everywhere (its page, the list, the projects); afterwards its own status again.
func TestBringingStatus(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	e.s.markBringing(ctx, app.ID, "restarting")
	got, err := e.s.GetApp(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "restarting" {
		t.Fatalf("during: %q", got.Status)
	}
	list, err := e.s.ListApps(ctx, e.user)
	if err != nil || len(list) != 1 || list[0].Status != "restarting" {
		t.Fatalf("list during: %v %+v", err, list)
	}

	e.s.unmarkBringing(ctx, app.ID)
	got, err = e.s.GetApp(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != app.Status {
		t.Fatalf("after: %q, want %q", got.Status, app.Status)
	}
}
