package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInstanceIDIsStablePerDatabase(t *testing.T) {
	ctx := context.Background()
	open := func(name string) *Store {
		st, err := Open(filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return st
	}
	a, b := open("a.db"), open("b.db")
	ida, err := a.InstanceID(ctx)
	if err != nil || len(ida) != 16 {
		t.Fatalf("id = %q, %v", ida, err)
	}
	if again, _ := a.InstanceID(ctx); again != ida {
		t.Errorf("id changed: %q -> %q", ida, again)
	}
	if idb, _ := b.InstanceID(ctx); idb == ida {
		t.Error("two databases must not share an instance id")
	}
}
