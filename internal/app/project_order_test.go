package app

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestOrderProjects(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	clock := time.Unix(1_800_000_000, 0)
	m.s.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	mk := func(name string) string {
		t.Helper()
		p, err := m.s.CreateProject(ctx, m.alice, CreateProjectInput{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	a, b := mk("A"), mk("B")
	names := func(who Actor) []string {
		t.Helper()
		ps, err := m.s.ListProjects(ctx, who)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}
	if got := names(m.alice); !slices.Equal(got, []string{"B", "A", "Shop"}) {
		t.Fatalf("newest first = %v", got)
	}

	// The saved order; an unknown id and a repeat are skipped.
	if err := m.s.OrderProjects(ctx, m.alice, OrderProjectsInput{IDs: []string{m.shop.ID, "prj_gone", a, m.shop.ID, b}}); err != nil {
		t.Fatal(err)
	}
	if got := names(m.alice); !slices.Equal(got, []string{"Shop", "A", "B"}) {
		t.Fatalf("ordered = %v", got)
	}
	// A new project lands on top.
	mk("C")
	if got := names(m.alice); !slices.Equal(got, []string{"C", "Shop", "A", "B"}) {
		t.Fatalf("after new = %v", got)
	}

	// Bob's order is his own: Shop, shared with him, comes before his own
	// projects until he moves it, and alice's order stays.
	m.join(t, m.bob)
	if _, err := m.s.CreateProject(ctx, m.bob, CreateProjectInput{Name: "Bobs"}); err != nil {
		t.Fatal(err)
	}
	bobs := names(m.bob)
	if err := m.s.OrderProjects(ctx, m.bob, OrderProjectsInput{IDs: []string{a, m.shop.ID}}); err != nil {
		t.Fatal(err)
	}
	if got := names(m.bob); !slices.Equal(got, []string{"Bobs", "Shop"}) || !slices.Equal(bobs, []string{"Bobs", "Shop"}) {
		t.Fatalf("bob = %v (before %v)", got, bobs)
	}
	if got := names(m.alice); !slices.Equal(got, []string{"C", "Shop", "A", "B"}) {
		t.Fatalf("alice after bob = %v", got)
	}

	// A read-only token cannot change it.
	ro := m.alice
	ro.Scope = ScopeRead
	if err := m.s.OrderProjects(ctx, ro, OrderProjectsInput{IDs: []string{b}}); depCode(err) != "token_read_only" {
		t.Errorf("read-only: %v", err)
	}
}
