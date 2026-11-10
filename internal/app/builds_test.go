package app

import (
	"context"
	"testing"
)

func TestMaxParallelBuildsSetting(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	mustUser(t, s, "user@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	uc, _ := login(t, s, "user@example.nl")
	admin, user := sessionActor(t, s, ac), sessionActor(t, s, uc)

	st, err := s.GetServerSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if st.MaxParallelBuilds != s.defaultParallelBuilds() || st.DefaultMaxParallelBuilds != s.defaultParallelBuilds() {
		t.Fatalf("default = %d (default %d)", st.MaxParallelBuilds, st.DefaultMaxParallelBuilds)
	}

	four := 4
	if _, err := s.UpdateServerSettings(ctx, user, UpdateServerSettingsInput{MaxParallelBuilds: &four}); code(err) != "admin_only" {
		t.Errorf("a user changed the build slots: %v", err)
	}
	for _, bad := range []int{0, -1, MaxParallelBuildsLimit + 1} {
		v := bad
		if _, err := s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{MaxParallelBuilds: &v}); code(err) != "invalid_max_parallel_builds" {
			t.Errorf("%d: %v", bad, err)
		}
	}
	st, err = s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{MaxParallelBuilds: &four})
	if err != nil {
		t.Fatal(err)
	}
	if st.MaxParallelBuilds != 4 || s.maxParallelBuilds(ctx) != 4 {
		t.Errorf("after save = %d, worker reads %d", st.MaxParallelBuilds, s.maxParallelBuilds(ctx))
	}
	if n := auditCount(t, s, "admin.settings_update", "max_parallel_builds"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// Other settings keep the slots.
	open := SignupOpen
	if st, _ = s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{Signups: &open}); st.MaxParallelBuilds != 4 {
		t.Errorf("signups save reset the slots to %d", st.MaxParallelBuilds)
	}
}

func TestBuildSlotLimit(t *testing.T) {
	e := depTestSvc(t)
	if !e.s.tryBuildSlot(2) || !e.s.tryBuildSlot(2) {
		t.Fatal("two slots of two not taken")
	}
	if e.s.tryBuildSlot(2) {
		t.Fatal("a third slot was taken with a limit of 2")
	}
	// Lowered while two run: nothing new starts until both are done.
	e.s.releaseBuildSlot()
	if e.s.tryBuildSlot(1) {
		t.Fatal("a slot was taken over the lowered limit")
	}
	e.s.releaseBuildSlot()
	if e.s.buildSlotsUsed() != 0 || !e.s.tryBuildSlot(1) {
		t.Fatalf("used = %d", e.s.buildSlotsUsed())
	}
	e.s.releaseBuildSlot()
	e.s.releaseBuildSlot() // an extra release does not go below zero
	if e.s.buildSlotsUsed() != 0 {
		t.Fatalf("used = %d", e.s.buildSlotsUsed())
	}
}

func TestListBuilds(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a1 := e.mustApp(t, CreateAppInput{Name: "One"})
	a2 := e.mustApp(t, CreateAppInput{Name: "Two"})
	e.insertDep(t, a1, "dep_run", 1, "building")
	e.insertDep(t, a1, "dep_wait", 2, "queued")
	e.insertDep(t, a2, "dep_wait2", 1, "queued")
	e.insertDep(t, a2, "dep_done", 2, "running")
	e.exec(t, `UPDATE deployments SET queued_at = queued_at + 1 WHERE id = 'dep_wait2'`)

	b, err := e.s.ListBuilds(ctx, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Running) != 1 || b.Running[0].ID != "dep_run" || b.Running[0].App.Slug != a1.Slug || b.Running[0].App.Name != "One" {
		t.Errorf("running = %+v", b.Running)
	}
	if len(b.Queued) != 2 || b.Queued[0].ID != "dep_wait" || b.Queued[0].Position != 1 || b.Queued[1].ID != "dep_wait2" || b.Queued[1].Position != 2 {
		t.Errorf("queued = %+v", b.Queued)
	}
	if b.Slots.Max != 2 || b.Slots.Queued != 2 || b.Slots.Used != 0 {
		t.Errorf("slots = %+v", b.Slots)
	}

	other := e.user
	other.UserID = "usr_other"
	if b, err := e.s.ListBuilds(ctx, other); err != nil || len(b.Running)+len(b.Queued) != 0 || b.Slots.Queued != 2 {
		t.Errorf("someone else sees %+v, %v", b, err)
	}
}
