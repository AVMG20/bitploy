package app

import (
	"context"
	"strings"
	"testing"
)

// activityOf lists everything actor a sees on their Activity page.
func activityOf(t *testing.T, s *Service, a Actor, app string) []ActivityEntry {
	t.Helper()
	var out []ActivityEntry
	cursor := ""
	for {
		page, err := s.ListActivity(context.Background(), a, app, 3, cursor) // small pages: the cursor is used too
		if err != nil {
			t.Fatalf("ListActivity(%s): %v", a.UserID, err)
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" {
			return out
		}
		cursor = page.NextCursor
	}
}

func findActivity(list []ActivityEntry, action string, match func(ActivityEntry) bool) *ActivityEntry {
	for i := range list {
		if list[i].Action == action && (match == nil || match(list[i])) {
			return &list[i]
		}
	}
	return nil
}

func withIP(a Actor, ip string) Actor {
	a.IP = ip
	return a
}

// The Activity page shows a user what happened to their own apps, projects
// and account, and nothing of anyone else's: no other users' IPs, emails or
// admin work.
func TestActivityScoping(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	alice := withIP(m.alice, "198.51.100.1")
	bob := withIP(m.bob, "198.51.100.2")
	carol := withIP(m.carol, "198.51.100.3")
	admin := withIP(session(m.addUser(t, "usr_dave", "dave@example.com", "admin")), "198.51.100.4")
	for id, name := range map[string]string{"usr_alice": "Alice", "usr_bob": "Bob", "usr_carol": "Carol", "usr_dave": "Dave"} {
		m.exec(t, `UPDATE users SET name = ? WHERE id = ?`, name, id)
	}

	m.join(t, m.bob)
	if _, err := s.CreateProjectInvite(ctx, alice, m.shop.ID, CreateProjectInviteInput{Email: "friend@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateApp(ctx, alice, "shop", UpdateAppInput{Name: strp("Shop front")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnv(ctx, bob, "shop", map[string]string{"FROM_BOB": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StopApp(ctx, admin, "shop"); err != nil {
		t.Fatal(err)
	}
	// carol's own app: nobody else sees it.
	if _, err := s.CreateApp(ctx, carol, CreateAppInput{Name: "Secret", Slug: "carol-secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnv(ctx, carol, "carol-secret", map[string]string{"X": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	// alice's own app outside the shared project, then deleted.
	if _, err := s.CreateApp(ctx, alice, CreateAppInput{Name: "Temp", Slug: "temp"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, alice, "temp", "temp"); err != nil {
		t.Fatal(err)
	}
	// Account rows: about alice, and about carol.
	if _, err := s.AdminSetQuota(ctx, admin, "usr_alice", QuotaInput{Apps: 7, MemoryMB: 2048, CPUMillis: 2000, DomainsPerApp: 3, VolumeMB: 5120}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePasswordReset(ctx, admin, "usr_alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminSetQuota(ctx, admin, "usr_carol", QuotaInput{Apps: 1, MemoryMB: 512, CPUMillis: 500, DomainsPerApp: 1, VolumeMB: 1024}); err != nil {
		t.Fatal(err)
	}
	// Server rows.
	s.Audit(ctx, admin, "admin.settings_update", "server", "settings", map[string]any{"signups": "open"})
	s.Audit(ctx, System, "server.builds_paused", "server", "disk", nil)

	aliceSees := activityOf(t, s, alice, "")
	bobSees := activityOf(t, s, bob, "")
	carolSees := activityOf(t, s, carol, "")
	adminSees := activityOf(t, s, admin, "")

	for who, list := range map[string][]ActivityEntry{"alice": aliceSees, "bob": bobSees, "carol": carolSees, "admin": adminSees} {
		for _, e := range list {
			if strings.HasPrefix(e.Action, "server.") || e.Action == "admin.settings_update" {
				t.Errorf("%s sees server row %s", who, e.Action)
			}
			if e.IP != "" && e.Actor != actorYou {
				t.Errorf("%s sees the IP of %s on %s", who, e.ActorName, e.Action)
			}
			for k, v := range e.Details {
				if sv, ok := v.(string); ok && strings.HasPrefix(sv, "usr_") || strings.HasSuffix(k, "_id") {
					t.Errorf("%s sees an id in %s details: %s=%v", who, e.Action, k, v)
				}
			}
		}
	}
	for who, list := range map[string][]ActivityEntry{"alice": aliceSees, "bob": bobSees, "admin": adminSees} {
		for _, e := range list {
			if e.App != nil && e.App.Slug == "carol-secret" || e.Action == "admin.quota_set" && e.TargetID == "usr_carol" {
				t.Errorf("%s sees carol's %s", who, e.Action)
			}
		}
	}

	// alice: her own rows with her IP, bob's and the admin's on her app without theirs.
	if e := findActivity(aliceSees, "app.update", nil); e == nil || e.Actor != actorYou || e.IP != "198.51.100.1" || e.App == nil || e.App.Slug != "shop" || e.App.Gone {
		t.Errorf("alice's own update = %+v", e)
	}
	if e := findActivity(aliceSees, "env.set", func(e ActivityEntry) bool { return e.Actor != actorYou }); e == nil || e.Actor != actorPerson || e.ActorName != "Bob" || e.IP != "" {
		t.Errorf("bob's change as alice sees it = %+v", e)
	}
	if e := findActivity(aliceSees, "app.stop", nil); e == nil || e.Actor != actorAdmin || e.ActorName != "An admin" || e.IP != "" {
		t.Errorf("the admin's stop as alice sees it = %+v", e)
	}
	if e := findActivity(aliceSees, "project.member_add", nil); e == nil || e.Details["member"] != "Bob" || e.Project == nil || e.Project.Name != "Shop" {
		t.Errorf("bob joining as alice sees it = %+v", e)
	}
	if e := findActivity(aliceSees, "project.invite_create", func(e ActivityEntry) bool { return e.Details["email"] != nil }); e == nil || e.Details["email"] != "friend@example.com" {
		t.Errorf("alice's own invite keeps the email she typed: %+v", e)
	}
	if e := findActivity(aliceSees, "admin.quota_set", nil); e == nil || e.Actor != actorAdmin || e.TargetID != "usr_alice" || e.IP != "" {
		t.Errorf("alice's new limits = %+v", e)
	}
	if e := findActivity(aliceSees, "admin.password_reset_link", nil); e == nil || e.Details["email"] != "alice@example.com" {
		t.Errorf("the reset link for alice = %+v", e)
	}
	// The deleted app: still hers, by the slug it had.
	if e := findActivity(aliceSees, "app.delete", nil); e == nil || e.App == nil || !e.App.Gone || e.App.Slug != "temp" || e.App.ID != "" {
		t.Errorf("the deleted app = %+v", e)
	}
	if e := findActivity(aliceSees, "app.create", func(e ActivityEntry) bool { return e.App != nil && e.App.Slug == "temp" }); e == nil || !e.App.Gone {
		t.Errorf("creating the deleted app = %+v", e)
	}

	// bob, a member: the shared app's rows, alice by name and without her IP.
	if e := findActivity(bobSees, "app.update", nil); e == nil || e.Actor != actorPerson || e.ActorName != "Alice" || e.IP != "" || e.App == nil || e.App.Slug != "shop" {
		t.Errorf("alice's update as bob sees it = %+v", e)
	}
	if e := findActivity(bobSees, "env.set", func(e ActivityEntry) bool { return e.Actor == actorYou }); e == nil || e.IP != "198.51.100.2" {
		t.Errorf("bob's own change = %+v", e)
	}
	if e := findActivity(bobSees, "app.stop", nil); e == nil || e.Actor != actorAdmin {
		t.Errorf("the admin's stop as bob sees it = %+v", e)
	}
	if e := findActivity(bobSees, "project.invite_create", func(e ActivityEntry) bool { return e.Details["email"] != nil }); e != nil {
		t.Errorf("bob sees the email of alice's invite: %+v", e)
	}
	for _, e := range bobSees {
		if e.App != nil && e.App.Slug == "temp" || e.TargetType == "user" {
			t.Errorf("bob sees alice's own %s", e.Action)
		}
	}

	// carol: her own app only.
	if len(carolSees) == 0 {
		t.Fatal("carol sees nothing of her own")
	}
	for _, e := range carolSees {
		if e.App != nil && e.App.Slug == "shop" || e.Project != nil && e.Project.ID == m.shop.ID {
			t.Errorf("carol sees the shop's %s", e.Action)
		}
	}
	if e := findActivity(carolSees, "admin.quota_set", nil); e == nil || e.Actor != actorAdmin {
		t.Errorf("carol's new limits = %+v", e)
	}

	// The admin sees what they did to alice's app, but not their admin work.
	if e := findActivity(adminSees, "app.stop", nil); e == nil || e.Actor != actorYou || e.IP != "198.51.100.4" {
		t.Errorf("the admin's own stop = %+v", e)
	}
	for _, e := range adminSees {
		if strings.HasPrefix(e.Action, "admin.") {
			t.Errorf("the admin sees their admin work %s on their own page", e.Action)
		}
	}

	// One app: only its rows, and only for someone who may open it.
	shopOnly := activityOf(t, s, bob, "shop")
	if len(shopOnly) == 0 {
		t.Fatal("no rows for the shop")
	}
	for _, e := range shopOnly {
		if e.App == nil || e.App.Slug != "shop" {
			t.Errorf("app filter let through %+v", e)
		}
	}
	if _, err := s.ListActivity(ctx, carol, "shop", 10, ""); depCode(err) != "app_not_found" {
		t.Errorf("carol filtering on the shop: %v", err)
	}
	if _, err := s.ListActivity(ctx, alice, "", 10, "nope"); depCode(err) != "invalid_cursor" {
		t.Errorf("bad cursor: %v", err)
	}

	// A member who leaves no longer sees the project's rows, only their own.
	if err := s.RemoveProjectMember(ctx, alice, m.shop.ID, "usr_bob"); err != nil {
		t.Fatal(err)
	}
	for _, e := range activityOf(t, s, bob, "") {
		if e.Actor != actorYou {
			t.Errorf("bob still sees %s by %s after leaving", e.Action, e.ActorName)
		}
	}
}

// Rows about a deployment find their app through the deployment.
func TestActivityDeploymentRows(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	m.join(t, m.bob)
	d, err := m.s.DeployFiles(ctx, m.bob, "shop", staticFiles())
	if err != nil {
		t.Fatal(err)
	}
	e := findActivity(activityOf(t, m.s, m.alice, ""), "deployment.queue", nil)
	if e == nil || e.TargetID != d.ID || e.App == nil || e.App.Slug != "shop" || e.Actor != actorPerson {
		t.Fatalf("the deploy bob started, as alice sees it = %+v", e)
	}
	if findActivity(activityOf(t, m.s, m.carol, ""), "deployment.queue", nil) != nil {
		t.Error("carol sees the deploy")
	}
}
