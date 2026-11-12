package app

import (
	"context"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/notify"
)

func fieldValue(m notify.Message, name string) string {
	for _, f := range m.Fields {
		if f.Name == name {
			return f.Value
		}
	}
	return ""
}

func TestAdminNotifications(t *testing.T) {
	m := newMembersEnv(t)
	withRecorder(m.s)
	ctx := context.Background()
	ada := session(m.addUser(t, "usr_ada", "ada@example.com", "admin"))

	admin := m.channel(t, ada, "ops")
	for _, e := range []string{EventDiskLow, EventServerUpdated, EventAccountCreated} {
		if !subscribed(admin.Events, []string{e}) {
			t.Errorf("an admin's new channel lacks %s: %v", e, admin.Events)
		}
	}
	user := m.channel(t, m.alice, "alice")
	if subscribed(user.Events, []string{EventServerUpdated, EventAccountCreated}) {
		t.Errorf("a user's new channel has admin events: %v", user.Events)
	}
	// Even a user who picks them gets nothing.
	m.channel(t, m.alice, "alice server", EventServerUpdated, EventAccountCreated)
	lastVersion := func() string {
		st, err := m.s.serverSettings(ctx, m.s.Q)
		if err != nil {
			t.Fatal(err)
		}
		return st.LastVersion
	}

	// A development build is never announced or stored.
	m.s.Version = "dev"
	m.s.NotifyVersion(ctx)
	if got := queued(m.s); len(got) != 0 || lastVersion() != "" {
		t.Fatalf("dev build: queued %v, stored %q", keysOf(got), lastVersion())
	}

	m.s.Version = "v1.0.0"
	m.s.NotifyVersion(ctx)
	got := queued(m.s)
	if len(got) != 1 || got[admin.ID].Event != EventServerUpdated || !strings.Contains(got[admin.ID].Title, "v1.0.0") ||
		fieldValue(got[admin.ID], "Was") != "" || lastVersion() != "v1.0.0" {
		t.Fatalf("first version: queued %v %+v, stored %q", keysOf(got), got[admin.ID], lastVersion())
	}
	m.s.NotifyVersion(ctx) // a restart on the same version
	if got := queued(m.s); len(got) != 0 {
		t.Fatalf("same version announced again: %v", keysOf(got))
	}
	m.s.Version = "v1.1.0"
	m.s.NotifyVersion(ctx)
	got = queued(m.s)
	if len(got) != 1 || fieldValue(got[admin.ID], "Version") != "v1.1.0" || fieldValue(got[admin.ID], "Was") != "v1.0.0" {
		t.Fatalf("update: queued %v %+v", keysOf(got), got[admin.ID])
	}

	// A signup with a project invite.
	code := m.invite(t, "erin@example.com")
	if _, _, err := m.s.SignupProjectInvite(ctx, code, AcceptInviteInput{Name: "Erin", Password: "correct horse battery"}, "198.51.100.1", "ua"); err != nil {
		t.Fatal(err)
	}
	got = queued(m.s)
	msg := got[admin.ID]
	if len(got) != 1 || msg.Event != EventAccountCreated || msg.Title != "New account: Erin" ||
		fieldValue(msg, "Email") != "erin@example.com" || fieldValue(msg, "Joined with") != "Project invite to Shop from usr_alice" {
		t.Fatalf("signup: queued %v %+v", keysOf(got), msg)
	}

	// Turning the event off stops it.
	none := []string{EventDiskLow}
	if _, err := m.s.UpdateNotificationChannel(ctx, ada, admin.ID, UpdateNotificationChannelInput{Events: &none}); err != nil {
		t.Fatal(err)
	}
	m.s.Version = "v1.2.0"
	m.s.NotifyVersion(ctx)
	if got := queued(m.s); len(got) != 0 {
		t.Fatalf("unsubscribed channel got %v", keysOf(got))
	}
}
