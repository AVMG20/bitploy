package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store/db"
)

func strp(s string) *string { return &s }

// liveApp makes an app that serves a container, as after a finished deploy.
func (e *depEnv) liveApp(t *testing.T) App {
	t.Helper()
	app := e.mustApp(t, CreateAppInput{})
	d, err := e.s.DeployFiles(context.Background(), e.user, app.Slug, filesWith("hello"))
	if err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE deployments SET state = 'running', container_name = ?, port = 8080 WHERE id = ?`, "bp-"+app.Slug+"-1", d.ID)
	e.exec(t, `UPDATE apps SET current_deployment_id = ?, status = 'running', desired_state = 'running' WHERE id = ?`, d.ID, app.ID)
	return app
}

func (e *depEnv) route(t *testing.T, slug string) string {
	t.Helper()
	b, err := e.s.Routes.Read(slug)
	if err != nil || b == nil {
		t.Fatalf("no route file for %s: %v", slug, err)
	}
	return string(b)
}

func TestPasswordProtectionOnOffAndRoute(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.liveApp(t)

	got, err := e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("blue-river-42")})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Protected || got.ProtectedUsername != "team" {
		t.Fatalf("app view: %+v", got)
	}
	route := e.route(t, app.Slug)
	if !strings.Contains(route, "basicAuth:") || !strings.Contains(route, "team:$2a$") {
		t.Fatalf("route lacks the login:\n%s", route)
	}
	if strings.Contains(route, "blue-river-42") {
		t.Fatal("the plain password is in the route file")
	}

	// The response and its JSON carry no hash and no password.
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "$2a$") || strings.Contains(string(b), "blue-river") || strings.Contains(string(b), "hash") {
		t.Fatalf("app JSON leaks the secret: %s", b)
	}
	// The password is stored as a bcrypt hash that matches, and nothing else holds it.
	var hash string
	if err := e.s.Store.DB.QueryRow(`SELECT protect_hash FROM apps WHERE id = ?`, app.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !proxy.CheckPassword(hash, "blue-river-42") || proxy.CheckPassword(hash, "blue-river-43") {
		t.Fatalf("stored hash does not check out: %s", hash)
	}
	var audit string
	if err := e.s.Store.DB.QueryRow(`SELECT group_concat(details) FROM audit_log WHERE action = 'app.protection_set'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, "blue-river") || strings.Contains(audit, "$2a$") {
		t.Fatalf("audit row leaks the secret: %s", audit)
	}

	// Username only: the password stays.
	if _, err = e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("anna")}); err != nil {
		t.Fatal(err)
	}
	if r := e.route(t, app.Slug); !strings.Contains(r, "anna:"+hash) {
		t.Fatalf("username change lost the password:\n%s", r)
	}
	// Password only: the username stays.
	if _, err = e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Password: strp("another-pass-1")}); err != nil {
		t.Fatal(err)
	}
	var user, hash2 string
	_ = e.s.Store.DB.QueryRow(`SELECT protect_username, protect_hash FROM apps WHERE id = ?`, app.ID).Scan(&user, &hash2)
	if user != "anna" || hash2 == hash || !proxy.CheckPassword(hash2, "another-pass-1") {
		t.Fatalf("password change: user=%s", user)
	}

	// Off: the prompt and the login are gone.
	got, err = e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if got.Protected || got.ProtectedUsername != "" {
		t.Fatalf("still protected: %+v", got)
	}
	if r := e.route(t, app.Slug); strings.Contains(r, "basicAuth") || strings.Contains(r, "anna") {
		t.Fatalf("route still has the login:\n%s", r)
	}
	_ = e.s.Store.DB.QueryRow(`SELECT protect_username, protect_hash FROM apps WHERE id = ?`, app.ID).Scan(&user, &hash2)
	if user != "" || hash2 != "" {
		t.Fatalf("login not forgotten: %q %q", user, hash2)
	}
	// Turning it on again needs a password again.
	if _, err = e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("anna")}); depCode(err) != "password_required" {
		t.Fatalf("want password_required, got %v", err)
	}
}

func TestPasswordProtectionValidation(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.liveApp(t)
	long := strings.Repeat("a", 73)
	for _, c := range []struct {
		name, code string
		in         ProtectionInput
	}{
		{"no username", "username_required", ProtectionInput{Enabled: true, Password: strp("longenough1")}},
		{"blank username", "username_required", ProtectionInput{Enabled: true, Username: strp("  "), Password: strp("longenough1")}},
		{"colon", "username_invalid", ProtectionInput{Enabled: true, Username: strp("a:b"), Password: strp("longenough1")}},
		{"template braces", "username_invalid", ProtectionInput{Enabled: true, Username: strp("{{x}}"), Password: strp("longenough1")}},
		{"space", "username_invalid", ProtectionInput{Enabled: true, Username: strp("a b"), Password: strp("longenough1")}},
		{"newline", "username_invalid", ProtectionInput{Enabled: true, Username: strp("a\nb"), Password: strp("longenough1")}},
		{"unicode", "username_invalid", ProtectionInput{Enabled: true, Username: strp("zoë"), Password: strp("longenough1")}},
		{"long username", "username_invalid", ProtectionInput{Enabled: true, Username: strp(strings.Repeat("a", 65)), Password: strp("longenough1")}},
		{"no password", "password_required", ProtectionInput{Enabled: true, Username: strp("team")}},
		{"empty password", "password_required", ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("")}},
		{"short password", "password_too_short", ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("1234567")}},
		{"long password", "password_too_long", ProtectionInput{Enabled: true, Username: strp("team"), Password: &long}},
		{"newline in password", "password_invalid", ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("abcd\nefgh")}},
	} {
		_, err := e.s.SetPasswordProtection(ctx, e.user, app.Slug, c.in)
		if depCode(err) != c.code {
			t.Errorf("%s: want %s, got %v", c.name, c.code, err)
		}
	}
	// Nothing was saved by the failures.
	if got, _ := e.s.GetApp(ctx, e.user, app.Slug); got.Protected {
		t.Fatal("a rejected request turned protection on")
	}
	// Exactly 72 bytes and exactly 8 characters are fine.
	if _, err := e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("a.b_c-d@e+f"), Password: strp(strings.Repeat("a", 72))}); err != nil {
		t.Fatalf("72 bytes: %v", err)
	}
	if _, err := e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("12345678")}); err != nil {
		t.Fatalf("8 characters: %v", err)
	}
}

func TestPasswordProtectionOwnershipScopeAndWorkers(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.liveApp(t)
	in := ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("longenough1")}

	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.SetPasswordProtection(ctx, bob, app.Slug, in); depCode(err) != "app_not_found" {
		t.Fatalf("another user: %v", err)
	}
	read := e.user
	read.Scope = ScopeRead
	if _, err := e.s.SetPasswordProtection(ctx, read, app.Slug, in); depCode(err) != "token_read_only" {
		t.Fatalf("read token: %v", err)
	}
	// An admin session cannot change someone else's app either.
	admin := e.addUser(t, "usr_admin", "admin@example.com", "admin")
	admin.Scope = ScopeSession
	if _, err := e.s.SetPasswordProtection(ctx, admin, app.Slug, in); depCode(err) != "app_not_found" {
		t.Fatalf("admin on foreign app: %v", err)
	}
	worker := e.mustApp(t, CreateAppInput{Type: "worker"})
	if _, err := e.s.SetPasswordProtection(ctx, e.user, worker.Slug, in); depCode(err) != "protection_needs_web_app" {
		t.Fatalf("worker: %v", err)
	}
	if got, _ := e.s.GetApp(ctx, e.user, app.Slug); got.Protected {
		t.Fatal("protected after only failed requests")
	}
}

// The login lives in the database, so every writer of the route file (swap,
// restart, the reconciler) puts it back.
func TestPasswordProtectionSurvivesRouteRewrites(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.liveApp(t)
	if _, err := e.s.SetPasswordProtection(ctx, e.user, app.Slug, ProtectionInput{Enabled: true, Username: strp("team"), Password: strp("longenough1")}); err != nil {
		t.Fatal(err)
	}
	want := e.route(t, app.Slug)

	// Reconciler: a lost or changed route file comes back with the login.
	if err := e.s.Routes.Remove(app.Slug); err != nil {
		t.Fatal(err)
	}
	e.s.reconcileRoutes(ctx)
	if got := e.route(t, app.Slug); got != want {
		t.Fatalf("reconciler did not restore the protected route:\n%s", got)
	}
	// A stale unprotected file (for example from before a crash) is repaired too.
	if err := e.s.Routes.Write(proxy.Route{Slug: app.Slug, Host: e.s.Cfg.AppHost(app.Slug), Upstream: "http://bp-" + app.Slug + "-1:8080"}); err != nil {
		t.Fatal(err)
	}
	e.s.reconcileRoutes(ctx)
	if got := e.route(t, app.Slug); got != want {
		t.Fatalf("reconciler left an open route:\n%s", got)
	}
	// The sync after a swap, start or restart.
	if err := e.s.SyncRoute(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.route(t, app.Slug); got != want {
		t.Fatal("SyncRoute lost the login")
	}
	// A candidate during a deploy never gets it; the public routers still do.
	e.s.setCandidate(app.ID, "http://bp-"+app.Slug+"-2:8080")
	if err := e.s.SyncRoute(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	r := e.route(t, app.Slug)
	if !strings.Contains(r, "candidate") || !strings.Contains(r, "basicAuth") {
		t.Fatalf("candidate + protected route:\n%s", r)
	}
	// A rename keeps it (the route is rebuilt for the new slug).
	d := e.s.desiredRoute(ctx, mustRow(t, e, app.ID))
	if d.Auth == nil || d.Auth.User != "team" {
		t.Fatalf("desiredRoute: %+v", d.Auth)
	}
}

func TestProtectedWithoutLoginFailsClosed(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.liveApp(t)
	e.exec(t, `UPDATE apps SET protect_enabled = 1 WHERE id = ?`, app.ID)
	d := e.s.desiredRoute(ctx, mustRow(t, e, app.ID))
	if d.Auth == nil || proxy.CheckPassword(d.Auth.Hash, "") || proxy.CheckPassword(d.Auth.Hash, "locked") {
		t.Fatalf("broken login must lock everyone out: %+v", d.Auth)
	}
}

func mustRow(t *testing.T, e *depEnv, id string) db.App {
	t.Helper()
	row, err := e.s.Q.GetApp(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
