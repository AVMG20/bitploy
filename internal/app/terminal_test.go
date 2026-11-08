package app

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/deploy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

type stubTerminals struct {
	running map[string]bool
	openErr error
}

func (s stubTerminals) Running(_ context.Context, name string) (bool, error) {
	return s.running[name], nil
}

func (s stubTerminals) Open(context.Context, string, int, int) (TerminalConn, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return &stubTermConn{}, nil
}

type stubTermConn struct{ closed int }

func (*stubTermConn) Read([]byte) (int, error)               { return 0, io.EOF }
func (*stubTermConn) Write(p []byte) (int, error)            { return len(p), nil }
func (*stubTermConn) Resize(context.Context, int, int) error { return nil }
func (*stubTermConn) Exit(context.Context) (int, bool)       { return 0, true }
func (c *stubTermConn) Close() error                         { c.closed++; return nil }

// liveOn gives the app a live deployment of kind, run by container bp-<slug>-1.
func liveOn(t *testing.T, s *Service, appID, slug, kind string) {
	t.Helper()
	ctx := context.Background()
	dep := "dep_term_" + slug
	if err := s.Q.InsertDeployment(ctx, db.InsertDeploymentParams{ID: dep, AppID: appID, Seq: 1, SourceKind: "upload", Channel: "dashboard", BuildKind: kind, QueuedAt: s.now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.Q.SetDeploymentContainer(ctx, db.SetDeploymentContainerParams{ContainerName: sql.NullString{String: "bp-" + slug + "-1", Valid: true}, ID: dep}); err != nil {
		t.Fatal(err)
	}
	if err := s.Q.SetAppCurrentDeployment(ctx, db.SetAppCurrentDeploymentParams{CurrentDeploymentID: sql.NullString{String: dep, Valid: true}, UpdatedAt: s.now(), ID: appID}); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalAccess(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	liveOn(t, m.s, m.app.ID, "shop", "dockerfile")
	m.s.UseTerminalBackend(stubTerminals{running: map[string]bool{"bp-shop-1": true}})

	// The owner opens one; it says which app.
	tm, err := m.s.OpenTerminal(ctx, m.alice, "shop", TerminalOptions{})
	if err != nil || tm.App != "shop" {
		t.Fatalf("owner: %+v, %v", tm, err)
	}
	tm.Close(TerminalClosed)
	tm.Close(TerminalClosed) // only once
	if c := tm.conn.(*stubTermConn).closed; c != 1 {
		t.Errorf("shell closed %d times", c)
	}

	// A stranger and an API token cannot; an admin cannot on someone else's app.
	if _, err := m.s.OpenTerminal(ctx, m.carol, "shop", TerminalOptions{}); depCode(err) != "app_not_found" {
		t.Errorf("stranger: %v", err)
	}
	token := m.alice
	token.Channel, token.Scope, token.TokenID = ChannelAPI, ScopeDeploy, "tok_x"
	if _, err := m.s.OpenTerminal(ctx, token, "shop", TerminalOptions{}); depCode(err) != "session_required" {
		t.Errorf("token: %v", err)
	}
	mcp := token
	mcp.Channel = ChannelMCP
	if _, err := m.s.OpenTerminal(ctx, mcp, "shop", TerminalOptions{}); depCode(err) != "session_required" {
		t.Errorf("mcp: %v", err)
	}
	admin := session(m.addUser(t, "usr_root", "root@example.com", "admin"))
	if _, err := m.s.OpenTerminal(ctx, admin, "shop", TerminalOptions{}); depCode(err) != "app_not_found" {
		t.Errorf("admin: %v", err)
	}

	// A member of the project can, and loses it when removed.
	m.join(t, m.bob)
	tm, err = m.s.OpenTerminal(ctx, m.bob, "shop", TerminalOptions{})
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if why := tm.Check(ctx); why != "" {
		t.Errorf("member check: %q", why)
	}
	if err := m.s.RemoveProjectMember(ctx, m.alice, m.shop.ID, m.bob.UserID); err != nil {
		t.Fatal(err)
	}
	if why := tm.Check(ctx); why != TerminalNoAccess {
		t.Errorf("removed member check: %q", why)
	}
	tm.Close(TerminalNoAccess)

	// Opening and closing are audited with the container, nothing typed.
	rows, err := m.s.Store.DB.Query(`SELECT action, details FROM audit_log WHERE action LIKE 'app.terminal_%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action, details string
		_ = rows.Scan(&action, &details)
		if !strings.Contains(details, `"container":"bp-shop-1"`) {
			t.Errorf("%s without the container: %s", action, details)
		}
		got = append(got, action)
	}
	if strings.Join(got, ",") != "app.terminal_open,app.terminal_close,app.terminal_open,app.terminal_close" {
		t.Errorf("audit = %v", got)
	}
}

func TestTerminalNeedsARunningContainer(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	m.s.UseTerminalBackend(stubTerminals{running: map[string]bool{"bp-site-1": true}})

	// Never deployed.
	if _, err := m.s.OpenTerminal(ctx, m.alice, "shop", TerminalOptions{}); depCode(err) != "app_not_running" {
		t.Errorf("never deployed: %v", err)
	}
	// Deployed, but the container is gone.
	liveOn(t, m.s, m.app.ID, "shop", "dockerfile")
	if _, err := m.s.OpenTerminal(ctx, m.alice, "shop", TerminalOptions{}); depCode(err) != "app_not_running" {
		t.Errorf("no container: %v", err)
	}
	// A static site bitploy serves has no terminal, even while it runs.
	site := m.mustApp(t, CreateAppInput{Name: "Site", Slug: "site", Project: m.shop.ID})
	liveOn(t, m.s, site.ID, "site", deploy.BuildKindStatic)
	if _, err := m.s.OpenTerminal(ctx, m.alice, "site", TerminalOptions{}); depCode(err) != "terminal_static" {
		t.Errorf("static site: %v", err)
	}
}

func TestTerminalWithoutAShell(t *testing.T) {
	m := newMembersEnv(t)
	ctx := context.Background()
	liveOn(t, m.s, m.app.ID, "shop", "dockerfile")
	m.s.UseTerminalBackend(stubTerminals{running: map[string]bool{"bp-shop-1": true}, openErr: errNoShell()})
	if _, err := m.s.OpenTerminal(ctx, m.alice, "shop", TerminalOptions{}); depCode(err) != "terminal_no_shell" {
		t.Fatalf("no shell: %v", err)
	}
	// The slot is free again.
	m.s.UseTerminalBackend(stubTerminals{running: map[string]bool{"bp-shop-1": true}})
	for i := 0; i < MaxTerminalsPerUser; i++ {
		tm, err := m.s.OpenTerminal(ctx, m.alice, "shop", TerminalOptions{})
		if err != nil {
			t.Fatalf("terminal %d: %v", i, err)
		}
		defer tm.Close(TerminalClosed)
	}
}

func TestTerminalSizeIsClamped(t *testing.T) {
	for _, tc := range []struct{ cols, rows, wantCols, wantRows int }{
		{0, 0, 80, 24},
		{-3, 10, 80, 10},
		{120, 40, 120, 40},
		{10000, 10000, maxTerminalCols, maxTerminalRows},
	} {
		if c, r := ClampTerminalSize(tc.cols, tc.rows); c != tc.wantCols || r != tc.wantRows {
			t.Errorf("ClampTerminalSize(%d, %d) = %d, %d", tc.cols, tc.rows, c, r)
		}
	}
}
