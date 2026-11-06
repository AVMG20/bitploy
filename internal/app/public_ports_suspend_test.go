package app

import (
	"context"
	"strings"
	"testing"
)

// Suspending a user closes their public ports; reactivating opens none.
func TestSuspendClosesPublicPorts(t *testing.T) {
	e, fp, pg := pubEnv(t)
	ctx := context.Background()
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true, CloseAfter: strp("never")}); err != nil {
		t.Fatal(err)
	}
	admin := e.addUser(t, "usr_root", "root@example.com", "admin")
	admin.Scope = ScopeSession
	if err := e.s.AdminSetUserStatus(ctx, admin, e.user.UserID, "suspended"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("the forwarder stayed after the suspension")
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT count(*) FROM public_ports`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d public port rows after the suspension", n)
	}
	if ds := auditDetails(t, e, "app.public_port_closed"); len(ds) != 1 || !strings.Contains(ds[0], `"reason":"user_suspended"`) {
		t.Fatalf("suspend audit = %v", ds)
	}
	if err := e.s.AdminSetUserStatus(ctx, admin, e.user.UserID, "active"); err != nil {
		t.Fatal(err)
	}
	e.s.reconcilePublicPorts(ctx)
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("reactivating opened the port again")
	}
}
