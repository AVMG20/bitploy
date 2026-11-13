package app

import (
	"context"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/store/db"
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
	first := e.s.tryBuildSlot("dep_1", "usr_a", 2)
	second := e.s.tryBuildSlot("dep_2", "usr_b", 2)
	if !first || !second {
		t.Fatal("two slots of two not taken")
	}
	if e.s.tryBuildSlot("dep_3", "usr_a", 2) {
		t.Fatal("a third slot was taken with a limit of 2")
	}
	if h := e.s.buildSlotHolders(); h["usr_a"] != 1 || h["usr_b"] != 1 {
		t.Fatalf("holders = %v", h)
	}
	// Lowered while two run: nothing new starts until both are done.
	e.s.releaseBuildSlot("dep_1")
	if e.s.tryBuildSlot("dep_3", "usr_a", 1) {
		t.Fatal("a slot was taken over the lowered limit")
	}
	e.s.releaseBuildSlot("dep_2")
	if e.s.buildSlotsUsed() != 0 || !e.s.tryBuildSlot("dep_3", "usr_a", 1) {
		t.Fatalf("used = %d", e.s.buildSlotsUsed())
	}
	if e.s.tryBuildSlot("dep_3", "usr_a", 5) {
		t.Fatal("one deployment took two slots")
	}
	e.s.releaseBuildSlot("dep_3")
	e.s.releaseBuildSlot("dep_3") // an extra release does not go below zero
	if e.s.buildSlotsUsed() != 0 {
		t.Fatalf("used = %d", e.s.buildSlotsUsed())
	}
}

func qd(id, app, owner, kind string) db.ListQueuedDeploymentsRow {
	return db.ListQueuedDeploymentsRow{ID: id, AppID: app, OwnerID: owner, SourceKind: kind}
}

func orderIDs(rows []db.ListQueuedDeploymentsRow) string {
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return strings.Join(ids, ",")
}

// The line stays first come, first served, except that someone whose build
// already runs waits while someone else's build could start.
func TestFairBuildOrder(t *testing.T) {
	idle := func(string) bool { return false }
	cases := []struct {
		name string
		line []db.ListQueuedDeploymentsRow
		held map[string]int
		busy func(string) bool
		want string
	}{
		{"plain FIFO between people with nothing running",
			[]db.ListQueuedDeploymentsRow{qd("a1", "app_a1", "ann", "files"), qd("b1", "app_b1", "bob", "git")}, nil, idle, "a1,b1"},
		{"one person alone may use every slot",
			[]db.ListQueuedDeploymentsRow{qd("a1", "app_a1", "ann", "files"), qd("a2", "app_a2", "ann", "files"), qd("a3", "app_a3", "ann", "files")},
			map[string]int{"ann": 2}, idle, "a1,a2,a3"},
		{"someone with a build running waits for someone with none",
			[]db.ListQueuedDeploymentsRow{qd("a2", "app_a2", "ann", "files"), qd("a3", "app_a3", "ann", "files"), qd("b1", "app_b1", "bob", "files")},
			map[string]int{"ann": 1}, idle, "b1,a2,a3"},
		{"several each: they take turns, oldest first",
			[]db.ListQueuedDeploymentsRow{qd("a1", "app_a1", "ann", "files"), qd("a2", "app_a2", "ann", "files"), qd("b1", "app_b1", "bob", "files"), qd("b2", "app_b2", "bob", "files")},
			nil, idle, "a1,b1,a2,b2"},
		{"both running one: FIFO, no one is stuck",
			[]db.ListQueuedDeploymentsRow{qd("b2", "app_b2", "bob", "files"), qd("a2", "app_a2", "ann", "files")},
			map[string]int{"ann": 1, "bob": 1}, idle, "b2,a2"},
		{"a build whose app still deploys does not count as waiting",
			[]db.ListQueuedDeploymentsRow{qd("a2", "app_a2", "ann", "files"), qd("b1", "app_b1", "bob", "files")},
			map[string]int{"ann": 1}, func(app string) bool { return app == "app_b1" }, "a2"},
		{"rollbacks need no slot and are left out",
			[]db.ListQueuedDeploymentsRow{qd("b1", "app_b1", "bob", "rollback"), qd("a2", "app_a2", "ann", "files")},
			map[string]int{"ann": 1}, idle, "a2"},
	}
	for _, c := range cases {
		if got := orderIDs(fairBuildOrder(c.line, c.held, c.busy)); got != c.want {
			t.Errorf("%s: order %s, want %s", c.name, got, c.want)
		}
	}
}

// The worker gives the free slot to the other person's build, not to a
// second build of the one whose build already runs, and positions follow.
func TestDispatchIsFairAcrossOwners(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	a1 := e.mustApp(t, CreateAppInput{Name: "A1"})
	a2 := e.mustApp(t, CreateAppInput{Name: "A2"})
	b1, err := e.s.CreateApp(ctx, bob, CreateAppInput{Name: "B1"})
	if err != nil {
		t.Fatal(err)
	}
	// Alice builds a1 and holds one of the two slots; a2 (hers) was queued before b1 (Bob's).
	e.insertDep(t, a1, "dep_a1", 1, "building")
	if !e.s.tryBuildSlot("dep_a1", e.user.UserID, 2) {
		t.Fatal("slot")
	}
	e.insertDep(t, a2, "dep_a2", 1, "queued")
	e.insertDep(t, b1, "dep_b1", 1, "queued")
	e.exec(t, `UPDATE deployments SET queued_at = queued_at + 5 WHERE id = 'dep_b1'`)

	b, err := e.s.ListBuilds(ctx, bob)
	if err != nil || len(b.Queued) != 1 || b.Queued[0].ID != "dep_b1" || b.Queued[0].Position != 1 {
		t.Fatalf("bob's place: %+v %v", b.Queued, err)
	}
	if b, _ := e.s.ListBuilds(ctx, e.user); len(b.Queued) != 1 || b.Queued[0].ID != "dep_a2" || b.Queued[0].Position != 2 {
		t.Fatalf("alice's place: %+v", b.Queued)
	}

	rows, err := e.s.Q.ListQueuedDeployments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := fairBuildOrder(rows, e.s.buildSlotHolders(), e.s.appBusy)
	if len(next) != 2 || next[0].ID != "dep_b1" || next[0].OwnerID != "usr_bob" {
		t.Fatalf("next = %+v", next)
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

// Someone else sees only their own build, at its place in the fair line of
// every build that waits for a slot, whoever's it is.
func TestListBuildsPlaceAcrossAccounts(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	mine := e.mustApp(t, CreateAppInput{Name: "Mine"})
	busy := e.mustApp(t, CreateAppInput{Name: "Busy"})
	e.insertDep(t, mine, "dep_a", 1, "queued")
	e.insertDep(t, mine, "dep_b", 2, "queued")
	e.insertDep(t, busy, "dep_busy", 1, "queued")
	e.insertDep(t, mine, "dep_back", 3, "queued")
	e.exec(t, `UPDATE deployments SET source_kind = 'rollback' WHERE id = 'dep_back'`)
	if !e.s.tryBusy(busy.ID, "dep_busy_run", func() {}) {
		t.Fatal("busy app taken")
	}
	defer e.s.clearBusy(busy.ID, "dep_busy_run")

	other := e.addUser(t, "usr_bob", "bob@example.nl", "user")
	e.user = other
	theirs := e.mustApp(t, CreateAppInput{Name: "Theirs"})
	e.insertDep(t, theirs, "dep_theirs", 1, "queued")
	e.exec(t, `UPDATE deployments SET queued_at = queued_at + 5 WHERE id = 'dep_theirs'`)

	b, err := e.s.ListBuilds(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Running) != 0 || len(b.Queued) != 1 || b.Queued[0].ID != "dep_theirs" {
		t.Fatalf("bob sees %+v", b)
	}
	// dep_a (the oldest) goes first; then Bob's, as Alice already has one
	// ahead, then her dep_b. dep_busy waits for its app, the rollback takes none.
	if b.Queued[0].Position != 2 {
		t.Errorf("bob's place = %d, want 2", b.Queued[0].Position)
	}
	if b.Slots.Queued != 5 {
		t.Errorf("server queue = %d", b.Slots.Queued)
	}
}

func TestBuilderLimitsSetting(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "admin@example.nl", true)
	mustUser(t, s, "user@example.nl", false)
	ac, _ := login(t, s, "admin@example.nl")
	uc, _ := login(t, s, "user@example.nl")
	admin, user := sessionActor(t, s, ac), sessionActor(t, s, uc)

	// Unset: the env defaults (2 CPUs, 6g), and the builder keeps its own.
	st, err := s.GetServerSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if st.BuilderCPUMillis != 2000 || st.BuilderMemoryMB != 6144 || st.DefaultBuilderCPUMillis != 2000 || st.DefaultBuilderMemoryMB != 6144 {
		t.Fatalf("defaults: %+v", st)
	}
	if c, m := s.builderDriverLimits(ctx); c != "" || m != "" {
		t.Errorf("unset limits give the builder %q %q", c, m)
	}

	one, gb := int64(1000), int64(1024)
	if _, err := s.UpdateServerSettings(ctx, user, UpdateServerSettingsInput{BuilderCPUMillis: &one}); code(err) != "admin_only" {
		t.Errorf("a user changed the builder: %v", err)
	}
	for _, bad := range []UpdateServerSettingsInput{
		{BuilderCPUMillis: ptr(int64(100))},
		{BuilderCPUMillis: ptr(int64(1 << 40))},
		{BuilderMemoryMB: ptr(int64(256))},
		{BuilderMemoryMB: ptr(int64(1 << 40))},
	} {
		if _, err := s.UpdateServerSettings(ctx, admin, bad); code(err) != "invalid_builder_limits" {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	// One of the two: the other stays.
	if st, err = s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{BuilderCPUMillis: ptr(int64(1500))}); err != nil {
		t.Fatal(err)
	}
	if st.BuilderCPUMillis != 1500 || st.BuilderMemoryMB != 6144 {
		t.Errorf("cpu only: %d %d", st.BuilderCPUMillis, st.BuilderMemoryMB)
	}
	if st, err = s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{BuilderMemoryMB: &gb}); err != nil {
		t.Fatal(err)
	}
	if st.BuilderCPUMillis != 1500 || st.BuilderMemoryMB != 1024 {
		t.Errorf("memory only: %d %d", st.BuilderCPUMillis, st.BuilderMemoryMB)
	}
	if c, m := s.builderDriverLimits(ctx); c != "1.5" || m != "1024m" {
		t.Errorf("builder gets %q %q", c, m)
	}
	if n := auditCount(t, s, "admin.settings_update", "builder_memory_mb"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// Other settings keep them.
	if st, _ = s.UpdateServerSettings(ctx, admin, UpdateServerSettingsInput{MaxParallelBuilds: ptr(3)}); st.BuilderCPUMillis != 1500 || st.BuilderMemoryMB != 1024 {
		t.Errorf("slots save changed the builder: %d %d", st.BuilderCPUMillis, st.BuilderMemoryMB)
	}
}

func TestBuilderLimitParsing(t *testing.T) {
	for in, want := range map[string]int64{"2": 2000, "1.5": 1500, "0.25": 250, "x": 0, "0": 0} {
		if got := config.BuilderCPUMillis(in); got != want {
			t.Errorf("BuilderCPUMillis(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]int64{"6g": 6144, "3GB": 3072, "2048m": 2048, "1048576k": 1024, "1073741824": 1024, "lots": 0} {
		if got := config.BuilderMemoryMB(in); got != want {
			t.Errorf("BuilderMemoryMB(%q) = %d, want %d", in, got, want)
		}
	}
}
