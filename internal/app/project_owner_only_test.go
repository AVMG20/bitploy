package app

import (
	"context"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// addMember makes the user a member of the app's project, as an accepted
// project invite does.
func (e *depEnv) addMember(t *testing.T, appID, userID string) {
	t.Helper()
	e.exec(t, `INSERT INTO project_members (project_id, user_id, role, created_at)
		SELECT project_id, ?, 'member', 1 FROM apps WHERE id = ?`, userID, appID)
}

// ownerOnly checks the error a member gets for what only the owner may do.
func ownerOnly(t *testing.T, what string, err error) {
	t.Helper()
	ae := apperr.As(err)
	if err == nil || ae.Code != "owner_only" || ae.HTTPStatus != 403 {
		t.Fatalf("%s: %v, want 403 owner_only", what, err)
	}
	if !strings.Contains(ae.Message, "Only the project owner (usr_alice) can") || ae.Next != "get_app" {
		t.Errorf("%s: message %q next %q", what, ae.Message, ae.Next)
	}
}

func TestMemberCannotDeleteApps(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	s := m.s
	m.join(t, m.bob)
	db, err := s.CreateApp(ctx, m.alice, CreateAppInput{Name: "Db", Slug: "shop-db", Project: m.shop.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.exec(t, `UPDATE apps SET template = 'postgres' WHERE id = ?`, db.ID) // a database service

	for _, who := range []Actor{m.bob, {UserID: "usr_bob", Role: "user", Channel: ChannelAPI, Scope: ScopeDeploy}, {UserID: "usr_bob", Role: "user", Channel: ChannelMCP, Scope: ScopeDeploy}} {
		ownerOnly(t, "bob deletes the app", s.DeleteApp(ctx, who, "shop", "shop"))
		ownerOnly(t, "bob deletes the database", s.DeleteApp(ctx, who, "shop-db", "shop-db"))
	}
	// Owner-only comes before the confirmation, so it is not a guessing game.
	ownerOnly(t, "bob with a wrong confirmation", s.DeleteApp(ctx, m.bob, "shop", "nope"))
	// Bob may take back an app he made as long as it never ran (the
	// dashboard does when the first deploy of a new app cannot start).
	made, err := s.CreateApp(ctx, m.bob, CreateAppInput{Name: "Mine", Slug: "shop-bob", Project: m.shop.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteApp(ctx, m.bob, made.Slug, made.Slug); err != nil {
		t.Fatalf("bob takes back a new app: %v", err)
	}
	// Once it ran, it holds the owner's data: the owner's call.
	made, err = s.CreateApp(ctx, m.bob, CreateAppInput{Name: "Mine", Slug: "shop-bob2", Project: m.shop.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.exec(t, `UPDATE apps SET current_deployment_id = 'dep_ran' WHERE id = ?`, made.ID)
	ownerOnly(t, "bob deletes the app he made after it ran", s.DeleteApp(ctx, m.bob, made.Slug, made.Slug))
	// And never an app someone else made, even before it ran.
	ownerOnly(t, "bob deletes alice's new app", s.DeleteApp(ctx, m.bob, "shop-db", "shop-db"))
	if got, _ := s.GetProject(ctx, m.alice, m.shop.ID); len(got.Apps) != 3 {
		t.Fatalf("apps left = %d", len(got.Apps))
	}
	// A stranger still does not learn the app exists.
	if err := s.DeleteApp(ctx, m.carol, "shop", "shop"); depCode(err) != "app_not_found" {
		t.Errorf("carol: %v", err)
	}
	// The owner may.
	for _, slug := range []string{"shop-db", "shop-bob2"} {
		if err := s.DeleteApp(ctx, m.alice, slug, slug); err != nil {
			t.Fatalf("alice deletes %s: %v", slug, err)
		}
	}
}

func TestMemberCannotDeleteOrReplaceFiles(t *testing.T) {
	e, fv := newFilesEnv(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	e.addMember(t, e.app.ID, "usr_bob")

	ownerOnly(t, "bob deletes a file", e.s.DeleteFile(ctx, bob, e.app.Slug, "config.json"))
	ownerOnly(t, "bob deletes a folder", e.s.DeleteFile(ctx, bob, e.app.Slug, "data"))
	if fv.kind("config.json") != "file" || fv.kind("data/app.db") != "file" {
		t.Fatal("bob deleted something")
	}
	put := func(a Actor, dir, name, body string, ow bool) error {
		_, err := e.s.UploadFile(ctx, a, e.app.Slug, dir, name, ow, strings.NewReader(body), int64(len(body)))
		return err
	}
	ownerOnly(t, "bob replaces a file", put(bob, "data", "app.db", "gone", true))
	if string(fv.tree["data/app.db"]) != "sqlite" {
		t.Fatal("bob replaced app.db")
	}
	if err := put(bob, "data", "app.db", "gone", false); depCode(err) != "file_exists" {
		t.Errorf("bob uploads over a file without overwrite: %v", err)
	}
	// New files and folders are fine, with or without overwrite.
	if err := put(bob, "data", "new.txt", "hi", false); err != nil {
		t.Fatalf("bob adds a file: %v", err)
	}
	if err := put(bob, "data", "other.txt", "hi", true); err != nil {
		t.Fatalf("bob adds a file with overwrite=true: %v", err)
	}
	if _, err := e.s.MakeFolder(ctx, bob, e.app.Slug, "", "uploads"); err != nil {
		t.Fatalf("bob makes a folder: %v", err)
	}
	if _, err := e.s.ListFiles(ctx, bob, e.app.Slug, "data"); err != nil {
		t.Fatalf("bob lists files: %v", err)
	}

	// The owner may do all of it.
	if err := put(e.user, "data", "app.db", "new", true); err != nil || string(fv.tree["data/app.db"]) != "new" {
		t.Fatalf("alice replaces app.db: %v", err)
	}
	if err := e.s.DeleteFile(ctx, e.user, e.app.Slug, "config.json"); err != nil || fv.kind("config.json") != "" {
		t.Fatalf("alice deletes a file: %v", err)
	}
}

func TestMemberCannotDeleteOrRestoreBackups(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	e.addMember(t, e.app.ID, "usr_bob")
	b := e.makeBackup(t)

	ownerOnly(t, "bob deletes a backup", e.s.DeleteBackup(ctx, bob, e.app.Slug, b.ID))
	_, err := e.s.RestoreBackup(ctx, bob, e.app.Slug, b.ID)
	ownerOnly(t, "bob restores a backup", err)
	if l := e.list(t); len(l.Items) != 1 || l.Restore != nil {
		t.Fatalf("after bob: %+v", l)
	}
	// Turning /data off would delete the files and the backup.
	off, yes := false, true
	_, err = e.s.UpdateApp(ctx, bob, e.app.Slug, UpdateAppInput{Volume: &off, DeleteData: &yes})
	ownerOnly(t, "bob turns /data off with delete_data", err)
	_, err = e.s.UpdateApp(ctx, bob, e.app.Slug, UpdateAppInput{Volume: &off})
	ownerOnly(t, "bob turns /data off", err)
	if row, _ := e.s.Q.GetApp(ctx, e.app.ID); row.VolumeEnabled == 0 {
		t.Fatal("bob turned /data off")
	}

	// Making, listing and downloading backups stay open to members.
	if _, err := e.s.CreateBackup(ctx, bob, e.app.Slug); err != nil {
		t.Fatalf("bob backs up: %v", err)
	}
	e.wait()
	if _, err := e.s.ListBackups(ctx, bob, e.app.Slug); err != nil {
		t.Fatalf("bob lists backups: %v", err)
	}
	f, _, err := e.s.OpenBackup(ctx, bob, e.app.Slug, b.ID)
	if err != nil {
		t.Fatalf("bob downloads a backup: %v", err)
	}
	_ = f.Close()

	// The owner may restore and delete.
	if _, err := e.s.RestoreBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatalf("alice restores: %v", err)
	}
	if r := e.waitRestore(t); r.State != "done" {
		t.Fatalf("restore = %+v", r)
	}
	if err := e.s.DeleteBackup(ctx, e.user, e.app.Slug, b.ID); err != nil {
		t.Fatalf("alice deletes: %v", err)
	}
}

// A member may still turn off a /data that holds nothing: nothing is lost.
func TestMemberTurnsOffEmptyVolume(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	e.addMember(t, e.app.ID, "usr_bob")
	e.vol.files = map[string]string{}
	off := false
	if _, err := e.s.UpdateApp(ctx, bob, e.app.Slug, UpdateAppInput{Volume: &off}); err != nil {
		t.Fatalf("bob turns an empty /data off: %v", err)
	}
}
