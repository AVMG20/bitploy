package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/proxy"
	"github.com/AVMG20/bitploy/internal/store"
	"github.com/AVMG20/bitploy/internal/store/db"
)

const depStaticDockerfile = "FROM nginxinc/nginx-unprivileged:1.27-alpine\nCOPY . /usr/share/nginx/html\nEXPOSE 8080\nUSER nginx\n"

type depEnv struct {
	s    *Service
	user Actor
	dir  string
}

func depTestSvc(t *testing.T) *depEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		PublicURL: "https://bitploy.test", AppsDomain: "bitploy.test", AppsScheme: "https",
		DataDir: dir, Signup: "invite", MaxParallelBuilds: 2,
		TraefikContainer: "bitploy-traefik", TraefikInternal: "http://127.0.0.1:1",
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	crypto, err := store.NewCrypto([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	routes := &proxy.Writer{Dir: cfg.RoutesDir(), EntryPoint: "websecure", TLS: true}
	s := New(cfg, st, crypto, nil, routes, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &depEnv{s: s, dir: dir}
	e.user = e.addUser(t, "usr_alice", "alice@example.com", "user")
	return e
}

func (e *depEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.s.Store.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (e *depEnv) addUser(t *testing.T, id, email, role string) Actor {
	t.Helper()
	now := time.Now().Unix()
	e.exec(t, `INSERT INTO users (id, email, name, password_hash, role, quota_volume_mb, created_at, updated_at) VALUES (?, ?, ?, 'x', ?, 25600, ?, ?)`, id, email, id, role, now, now)
	return Actor{UserID: id, Role: role, Channel: ChannelAPI, Scope: ScopeDeploy}
}

func depCode(err error) string {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	if err == nil {
		return ""
	}
	return "non-apperr: " + err.Error()
}

func (e *depEnv) mustApp(t *testing.T, in CreateAppInput) App {
	t.Helper()
	a, err := e.s.CreateApp(context.Background(), e.user, in)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return a
}

func staticFiles() []FileInput {
	return []FileInput{
		{Path: "Dockerfile", Content: depStaticDockerfile},
		{Path: "index.html", Content: "<h1>hello</h1>"},
	}
}

func TestCreateAppDefaults(t *testing.T) {
	e := depTestSvc(t)
	app := e.mustApp(t, CreateAppInput{Name: "My site"})
	if !regexp.MustCompile(`^[a-z]+-[a-z]+-[0-9a-f]{4}$`).MatchString(app.Slug) {
		t.Errorf("slug %q is not adjective-noun-xxxx", app.Slug)
	}
	if app.URL != "https://"+app.Slug+".bitploy.test" {
		t.Errorf("url %q", app.URL)
	}
	if app.Type != "web" || app.MemoryMB != 512 || app.CPUMillis != 500 || app.HealthPath != "/" || app.DockerfilePath != "Dockerfile" || app.ContextDir != "." {
		t.Errorf("defaults: %+v", app)
	}
	if app.Status != "new" || app.DesiredState != "running" || app.Domains == nil || app.Current != nil || app.Name != "My site" {
		t.Errorf("state: %+v", app)
	}
	w := e.mustApp(t, CreateAppInput{Type: "worker"})
	if w.URL != "" || w.Name != w.Slug {
		t.Errorf("worker: url=%q name=%q", w.URL, w.Name)
	}
	list, err := e.s.ListApps(context.Background(), e.user)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if _, err := e.s.CreateApp(context.Background(), e.user, CreateAppInput{Type: "cron"}); depCode(err) != "app_type_invalid" {
		t.Errorf("type: %v", err)
	}
	port := int64(70000)
	if _, err := e.s.CreateApp(context.Background(), e.user, CreateAppInput{Port: &port}); depCode(err) != "port_invalid" {
		t.Errorf("port: %v", err)
	}
}

func TestCreateAppSlugRules(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")

	app := e.mustApp(t, CreateAppInput{Slug: "  My-App  "})
	if app.Slug != "my-app" {
		t.Fatalf("slug %q should be trimmed and lower-cased", app.Slug)
	}
	for slug, want := range map[string]string{
		"my-app": "slug_taken", "ab": "slug_invalid", "has_underscore": "slug_invalid", "a.b.c": "slug_invalid",
		"www": "slug_reserved", "bp-hello": "slug_reserved", "mcp": "slug_reserved",
	} {
		if _, err := e.s.CreateApp(ctx, bob, CreateAppInput{Slug: slug}); depCode(err) != want {
			t.Errorf("slug %q: got %q, want %q", slug, depCode(err), want)
		}
	}

	// Deleting reserves the slug for its owner for 30 days.
	if err := e.s.DeleteApp(ctx, e.user, "my-app", "my-app"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateApp(ctx, bob, CreateAppInput{Slug: "my-app"}); depCode(err) != "slug_taken" {
		t.Errorf("another user must not get a reserved slug: %v", err)
	}
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: "my-app"}); err != nil {
		t.Errorf("the owner can take the slug back: %v", err)
	}
	// After 30 days the reservation is gone.
	if err := e.s.DeleteApp(ctx, e.user, "my-app", "my-app"); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE slug_reservations SET expires_at = ? WHERE slug = 'my-app'`, time.Now().Add(-time.Hour).Unix())
	if _, err := e.s.CreateApp(ctx, bob, CreateAppInput{Slug: "my-app"}); err != nil {
		t.Errorf("expired reservation must free the slug: %v", err)
	}
}

func TestQuotas(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 3, quota_memory_mb = 1024, quota_cpu_millis = 1200 WHERE id = ?`, e.user.UserID)
	// Creating apps reserves nothing: three 512 MB apps fit a 1024 MB quota until they run.
	first := e.mustApp(t, CreateAppInput{})
	second := e.mustApp(t, CreateAppInput{})
	third := e.mustApp(t, CreateAppInput{})
	// An app bigger than the whole quota can never run.
	huge := int64(2000)
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{MemoryMB: &huge}); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("bigger than the quota: %v", err)
	}
	bigCPU := int64(5000)
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{CPUMillis: &bigCPU}); depCode(err) != "cpu_quota_exceeded" {
		t.Errorf("cpu bigger than the quota: %v", err)
	}
	// App count: 3 of 3.
	small := int64(64)
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{MemoryMB: &small}); depCode(err) != "apps_quota_exceeded" {
		t.Errorf("count: %v", err)
	}

	// Two apps run (desired state running and a deployment): 1024 of 1024 MB reserved.
	e.exec(t, `UPDATE apps SET desired_state = 'running', current_deployment_id = 'dep_x' WHERE id IN (?, ?)`, first.ID, second.ID)
	// The first deploy of the third app is where the quota bites.
	if _, err := e.s.DeployFiles(ctx, e.user, third.Slug, staticFiles()); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("first deploy over the memory quota: %v", err)
	}
	// Freeing memory with update_app makes room; raising a running app past the quota fails.
	half := int64(256)
	if _, err := e.s.UpdateApp(ctx, e.user, second.Slug, UpdateAppInput{MemoryMB: &half}); err != nil {
		t.Fatal(err)
	}
	tooMuch := int64(900)
	if _, err := e.s.UpdateApp(ctx, e.user, second.Slug, UpdateAppInput{MemoryMB: &tooMuch}); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("raise: %v", err)
	}
	// 512 + 256 running; the third app (512) now exceeds by 256 ...
	if _, err := e.s.DeployFiles(ctx, e.user, third.Slug, staticFiles()); depCode(err) != "memory_quota_exceeded" {
		t.Errorf("still too big: %v", err)
	}
	// ... unless it is smaller.
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{MemoryMB: &half}); err != nil {
		t.Fatal(err)
	}
	cpu := int64(900)
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{CPUMillis: &cpu}); err != nil {
		t.Fatal(err) // not running: only the "fits at all" check
	}
	if _, err := e.s.DeployFiles(ctx, e.user, third.Slug, staticFiles()); depCode(err) != "cpu_quota_exceeded" {
		t.Errorf("cpu at first deploy: %v", err)
	}
	lowCPU := int64(100)
	if _, err := e.s.UpdateApp(ctx, e.user, third.Slug, UpdateAppInput{CPUMillis: &lowCPU}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DeployFiles(ctx, e.user, third.Slug, staticFiles()); err != nil {
		t.Errorf("third app fits now: %v", err)
	}
	// A stopped app does not count: stopping the first makes room for a bigger third.
	e.exec(t, `UPDATE apps SET desired_state = 'stopped' WHERE id = ?`, first.ID)
	u, err := e.s.Usage(ctx, e.user.UserID)
	if err != nil || u.MemoryReservedMB != 256 {
		t.Errorf("reserved after stopping = %+v, %v", u, err)
	}
	bad := int64(10)
	if _, err := e.s.UpdateApp(ctx, e.user, second.Slug, UpdateAppInput{MemoryMB: &bad}); depCode(err) != "memory_invalid" {
		t.Errorf("tiny memory: %v", err)
	}
}

func TestPermissions(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	admin := e.addUser(t, "usr_root", "root@example.com", "admin")
	admin.Scope = ScopeSession

	if _, err := e.s.GetApp(ctx, bob, app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user sees app: %v", err)
	}
	if _, err := e.s.GetApp(ctx, admin, app.Slug); err != nil {
		t.Errorf("admin reads any app: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.CreateApp(ctx, ro, CreateAppInput{}); depCode(err) != "token_read_only" {
		t.Errorf("read token creates app: %v", err)
	}
	if _, err := e.s.GetApp(ctx, ro, app.Slug); err != nil {
		t.Errorf("read token reads: %v", err)
	}
	if _, err := e.s.StopApp(ctx, ro, app.Slug); depCode(err) != "token_read_only" {
		t.Errorf("read token stops: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, admin, app.Slug, UpdateAppInput{}); depCode(err) != "app_not_found" {
		t.Errorf("admin edits someone else's app: %v", err)
	}
	if err := e.s.DeleteApp(ctx, admin, app.Slug, app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("admin deletes someone else's app: %v", err)
	}
	if _, err := e.s.StopApp(ctx, admin, app.Slug); err != nil {
		t.Errorf("admin stops any app: %v", err)
	}
	all, err := e.s.ListAllApps(ctx, admin)
	if err != nil || len(all) != 1 {
		t.Errorf("ListAllApps: %v %d", err, len(all))
	}
	if _, err := e.s.ListAllApps(ctx, e.user); depCode(err) != "admin_only" {
		t.Errorf("ListAllApps as user: %v", err)
	}
	// Suspended users cannot create or deploy.
	e.exec(t, `UPDATE users SET status = 'suspended' WHERE id = ?`, e.user.UserID)
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{}); depCode(err) != "account_suspended" {
		t.Errorf("suspended create: %v", err)
	}
	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles()); depCode(err) != "account_suspended" {
		t.Errorf("suspended deploy: %v", err)
	}
}

func TestUpdateAppFields(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	port, hp, vol, dp, cd, name := int64(3000), "/healthz", true, "docker/Dockerfile", "backend", "New name"
	got, err := e.s.UpdateApp(ctx, e.user, app.ID, UpdateAppInput{Port: &port, HealthPath: &hp, Volume: &vol, DockerfilePath: &dp, ContextDir: &cd, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if got.Port == nil || *got.Port != 3000 || got.HealthPath != "/healthz" || !got.Volume || got.DockerfilePath != "docker/Dockerfile" || got.ContextDir != "backend" || got.Name != "New name" {
		t.Fatalf("%+v", got)
	}
	zero := int64(0)
	got, _ = e.s.UpdateApp(ctx, e.user, app.Slug, UpdateAppInput{Port: &zero})
	if got.Port != nil {
		t.Errorf("port 0 should clear the port: %v", *got.Port)
	}
	for field, in := range map[string]UpdateAppInput{
		"health_path_invalid":     {HealthPath: ptr("healthz")},
		"dockerfile_path_invalid": {DockerfilePath: ptr("../Dockerfile")},
		"context_dir_invalid":     {ContextDir: ptr("../x")},
		"app_name_invalid":        {Name: ptr("   ")},
		"app_type_invalid":        {Type: ptr("db")},
	} {
		if _, err := e.s.UpdateApp(ctx, e.user, app.Slug, in); depCode(err) != field {
			t.Errorf("want %s, got %v", field, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestStorageAllowance(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_volume_mb = 10240 WHERE id = ?`, e.user.UserID)

	a := e.mustApp(t, CreateAppInput{Slug: "vol-a", Volume: true})
	if a.VolumeMB != 5120 {
		t.Fatalf("default volume_mb = %d", a.VolumeMB)
	}
	// A second 5 GB volume fits exactly; a third does not.
	e.mustApp(t, CreateAppInput{Slug: "vol-b", Volume: true})
	if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: "vol-c", Volume: true, VolumeMB: ptr(int64(100))}); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("third volume: %v", err)
	}
	// Without a volume, nothing is reserved; turning it on is checked.
	c := e.mustApp(t, CreateAppInput{Slug: "vol-c"})
	if _, err := e.s.UpdateApp(ctx, e.user, c.Slug, UpdateAppInput{Volume: ptr(true)}); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("turn on volume: %v", err)
	}
	// Lowering one limit frees room for the other.
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{VolumeMB: ptr(int64(1024))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, c.Slug, UpdateAppInput{Volume: ptr(true), VolumeMB: ptr(int64(4096))}); err != nil {
		t.Errorf("fits now: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{VolumeMB: ptr(int64(2048))}); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("raise over allowance: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{VolumeMB: ptr(int64(10))}); depCode(err) != "volume_mb_invalid" {
		t.Errorf("too small: %v", err)
	}
	// The limit cannot go below what /data already holds.
	e.exec(t, `INSERT INTO app_disk (app_id, image_bytes, volume_bytes, measured_at) VALUES (?, 0, ?, 1)`, a.ID, int64(900)<<20)
	if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{VolumeMB: ptr(int64(512))}); depCode(err) != "volume_limit_below_usage" {
		t.Errorf("below usage: %v", err)
	}
	got, err := e.s.GetApp(ctx, e.user, a.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.VolumeUsedMB == nil || *got.VolumeUsedMB != 900 {
		t.Errorf("volume_used_mb = %v", got.VolumeUsedMB)
	}
	u, err := e.s.Usage(ctx, e.user.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if u.VolumeReservedMB != 1024+5120+4096 || u.VolumeUsedMB != 900 {
		t.Errorf("usage = %d MB reserved, %v MB used", u.VolumeReservedMB, u.VolumeUsedMB)
	}

	// The allowance is full (10 GB of /data limits): no new builds, until the
	// limits shrink. App files count too.
	if _, err := e.s.DeployFiles(ctx, e.user, a.Slug, staticFiles()); depCode(err) != "storage_full" {
		t.Errorf("deploy with full storage: %v", err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, c.Slug, UpdateAppInput{VolumeMB: ptr(int64(2048))}); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE app_disk SET image_bytes = ? WHERE app_id = ?`, int64(1500)<<20, a.ID)
	if _, err := e.s.UpdateApp(ctx, e.user, c.Slug, UpdateAppInput{VolumeMB: ptr(int64(3072))}); depCode(err) != "storage_quota_exceeded" {
		t.Errorf("raise with app files counted: %v", err)
	}
	if _, err := e.s.DeployFiles(ctx, e.user, a.Slug, staticFiles()); err != nil {
		t.Errorf("deploy with room: %v", err)
	}
}

func TestRenameApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	app := e.mustApp(t, CreateAppInput{Slug: "old-name"})
	// Pretend it is live: a running deployment and a route file.
	e.seedRunning(t, app, 1)
	if err := e.s.SyncRoute(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.s.Routes.Read("old-name"); b == nil {
		t.Fatal("route file missing before rename")
	}

	got, err := e.s.UpdateApp(ctx, e.user, "old-name", UpdateAppInput{Slug: ptr("new-name")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "new-name" || got.URL != "https://new-name.bitploy.test" {
		t.Fatalf("%+v", got)
	}
	if b, _ := e.s.Routes.Read("old-name"); b != nil {
		t.Error("old route file must be removed")
	}
	if got.Current == nil || got.Current.Image == nil || *got.Current.Image != "bitploy/new-name:1" {
		t.Errorf("deployment image not renamed: %+v", got.Current)
	}
	var cn string
	_ = e.s.Store.DB.QueryRow(`SELECT container_name FROM deployments WHERE app_id = ?`, app.ID).Scan(&cn)
	if cn != "bp-new-name-1" {
		t.Errorf("container name %q", cn)
	}
	// Old slug stays reserved for the owner.
	if _, err := e.s.CreateApp(ctx, bob, CreateAppInput{Slug: "old-name"}); depCode(err) != "slug_taken" {
		t.Errorf("old slug must be reserved: %v", err)
	}
	if _, err := e.s.GetApp(ctx, e.user, "old-name"); depCode(err) != "app_not_found" {
		t.Errorf("old slug still resolves: %v", err)
	}
	// Bad targets.
	for slug, want := range map[string]string{"www": "slug_reserved", "x": "slug_invalid"} {
		if _, err := e.s.UpdateApp(ctx, e.user, "new-name", UpdateAppInput{Slug: ptr(slug)}); depCode(err) != want {
			t.Errorf("rename to %q: %v", slug, err)
		}
	}
	// An app with a volume can be renamed; its volume keeps its name, and a new
	// app on the old slug gets a volume name of its own.
	vol := e.mustApp(t, CreateAppInput{Slug: "has-volume", Volume: true})
	if _, err := e.s.UpdateApp(ctx, e.user, vol.Slug, UpdateAppInput{Slug: ptr("other-name")}); err != nil {
		t.Fatalf("volume rename: %v", err)
	}
	volName := func(id string) (n string) {
		_ = e.s.Store.DB.QueryRow(`SELECT volume_name FROM apps WHERE id = ?`, id).Scan(&n)
		return n
	}
	if n := volName(vol.ID); n != "bp-has-volume-data" {
		t.Errorf("renamed app volume = %q", n)
	}
	again := e.mustApp(t, CreateAppInput{Slug: "has-volume"})
	if n := volName(again.ID); n != "bp-"+again.ID+"-data" {
		t.Errorf("new app on old slug got volume %q", n)
	}
	// Busy apps cannot be renamed.
	busy := e.mustApp(t, CreateAppInput{Slug: "busy-app"})
	if _, err := e.s.DeployFiles(ctx, e.user, busy.Slug, staticFiles()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, busy.Slug, UpdateAppInput{Slug: ptr("busy-renamed")}); depCode(err) != "app_busy" {
		t.Errorf("busy rename: %v", err)
	}
}

// seedRunning inserts a running deployment as current for an app.
func (e *depEnv) seedRunning(t *testing.T, app App, seq int64) string {
	t.Helper()
	id := "dep_seed" + app.Slug + string(rune('0'+seq))
	now := time.Now().Unix()
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, image, container_name, port, queued_at, started_at, finished_at)
		VALUES (?, ?, ?, 'running', 'files', 'api', ?, ?, 8080, ?, ?, ?)`,
		id, app.ID, seq, "bitploy/"+app.Slug+":1", "bp-"+app.Slug+"-1", now, now, now)
	e.exec(t, `UPDATE apps SET current_deployment_id = ?, status = 'running', deploy_seq = ? WHERE id = ?`, id, seq, app.ID)
	return id
}

func TestEnvVars(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	vars, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"DATABASE_URL": "postgres://user:hunter2@db/x", "API_KEY": "s3cret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 || vars[0].Name != "API_KEY" || vars[0].Masked == "" || strings.Contains(vars[0].Masked, "s3cret") {
		t.Fatalf("%+v", vars)
	}
	// Ciphertext at rest, bound to app and name.
	var enc []byte
	_ = e.s.Store.DB.QueryRow(`SELECT value_enc FROM env_vars WHERE app_id = ? AND name = 'API_KEY'`, app.ID).Scan(&enc)
	if bytes.Contains(enc, []byte("s3cret")) {
		t.Fatal("value stored in plain text")
	}
	if plain, err := e.s.Crypto.Open(enc, "env:"+app.ID+":API_KEY"); err != nil || string(plain) != "s3cret" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := e.s.Crypto.Open(enc, "env:"+app.ID+":OTHER"); err == nil {
		t.Fatal("ciphertext must be bound to the variable name")
	}

	rowApp, _ := e.s.Q.GetApp(ctx, app.ID)
	env, ae := e.s.containerEnv(ctx, rowApp, 8080)
	if ae != nil {
		t.Fatal(ae)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"API_KEY=s3cret", "PORT=8080", "BITPLOY_APP=" + app.Slug, "BITPLOY_URL=https://" + app.Slug + ".bitploy.test", "DATABASE_URL=postgres://user:hunter2@db/x"} {
		if !strings.Contains(joined, want) {
			t.Errorf("container env lacks %s:\n%s", want, joined)
		}
	}

	vars, err = e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"API_KEY": "new"}, []string{"DATABASE_URL"})
	if err != nil || len(vars) != 1 {
		t.Fatalf("%v %+v", err, vars)
	}
	for name, in := range map[string]map[string]string{
		"env_name_invalid":  {"1BAD": "x"},
		"env_name_reserved": {"PORT": "1"},
	} {
		if _, err := e.s.SetEnv(ctx, e.user, app.Slug, in, nil); depCode(err) != name {
			t.Errorf("want %s, got %v", name, err)
		}
	}
	if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"bitploy_x": "1"}, nil); depCode(err) != "env_name_reserved" {
		t.Errorf("BITPLOY_ prefix: %v", err)
	}
	if _, err := e.s.SetEnv(ctx, e.user, app.Slug, map[string]string{"A": "1"}, []string{"A"}); depCode(err) != "env_conflict" {
		t.Errorf("conflict: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.SetEnv(ctx, ro, app.Slug, map[string]string{"A": "1"}, nil); depCode(err) != "token_read_only" {
		t.Errorf("read token: %v", err)
	}
	if list, err := e.s.ListEnv(ctx, ro, app.Slug); err != nil || len(list) != 1 {
		t.Errorf("read token lists names: %v %v", err, list)
	}
	// Audit never contains values.
	var details string
	_ = e.s.Store.DB.QueryRow(`SELECT group_concat(details) FROM audit_log WHERE action = 'env.set'`).Scan(&details)
	if strings.Contains(details, "hunter2") || strings.Contains(details, "s3cret") {
		t.Errorf("secret in audit log: %s", details)
	}
}

func TestDeployFilesQueuesAndCancelsOlder(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	d1, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	if err != nil {
		t.Fatal(err)
	}
	if d1.State != "queued" || d1.Seq != 1 || d1.SourceKind != "files" || d1.Channel != "api" || d1.URL != app.URL || len(d1.NextSteps) == 0 {
		t.Fatalf("%+v", d1)
	}
	if !strings.Contains(strings.Join(d1.NextSteps, " "), "get_deployment") {
		t.Errorf("next_steps should point at get_deployment: %v", d1.NextSteps)
	}
	if _, err := os.Stat(e.s.sourceZip(d1.ID)); err != nil {
		t.Fatalf("source snapshot missing: %v", err)
	}
	select {
	case <-e.s.WakeCh():
	default:
		t.Error("the worker was not woken")
	}

	d2, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	if err != nil {
		t.Fatal(err)
	}
	if d2.Seq != 2 {
		t.Fatalf("seq %d", d2.Seq)
	}
	old, err := e.s.GetDeployment(ctx, e.user, d1.ID, 0)
	if err != nil || old.State != "cancelled" || old.FinishedAt == nil {
		t.Fatalf("older queued deployment must be cancelled: %+v %v", old, err)
	}
	if _, err := os.Stat(e.s.sourceZip(d1.ID)); !os.IsNotExist(err) {
		t.Error("cancelled deployment's snapshot should be gone")
	}
	fresh, _ := e.s.GetApp(ctx, e.user, app.Slug)
	if fresh.Status != "deploying" {
		t.Errorf("app status %q", fresh.Status)
	}
	page, err := e.s.ListDeployments(ctx, e.user, app.Slug, 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Seq != 2 || page.NextCursor == "" {
		t.Fatalf("page 1: %+v %v", page, err)
	}
	page, err = e.s.ListDeployments(ctx, e.user, app.Slug, 1, page.NextCursor)
	if err != nil || len(page.Items) != 1 || page.Items[0].Seq != 1 || page.NextCursor != "" {
		t.Fatalf("page 2: %+v %v", page, err)
	}
	if _, err := e.s.ListDeployments(ctx, e.user, app.Slug, 5, "zzz"); depCode(err) != "cursor_invalid" {
		t.Errorf("cursor: %v", err)
	}
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.GetDeployment(ctx, bob, d1.ID, 0); depCode(err) != "deployment_not_found" {
		t.Errorf("foreign deployment: %v", err)
	}
}

func TestDeployFilesErrors(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	tests := map[string][]FileInput{
		"dockerfile_missing":    {{Path: "index.html", Content: "hi"}},
		"files_empty":           nil,
		"files_invalid_path":    {{Path: "../x", Content: "x"}},
		"files_invalid_content": {{Path: "a", Content: "!!", Encoding: "base64"}},
		"port_ambiguous":        {{Path: "Dockerfile", Content: "FROM alpine:3.20\nEXPOSE 80 81\nCMD [\"sleep\",\"1\"]\n"}},
		"files_too_large":       {{Path: "Dockerfile", Content: depStaticDockerfile}, {Path: "big.bin", Content: strings.Repeat("a", 2<<20)}},
	}
	for want, files := range tests {
		_, err := e.s.DeployFiles(ctx, e.user, app.Slug, files)
		if depCode(err) != want {
			t.Errorf("want %s, got %v", want, err)
		}
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&n)
	if n != 0 {
		t.Errorf("failed validation must not queue a deployment, got %d", n)
	}
	// dockerfile_missing carries the template for the detected stack.
	_, err := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "index.html", Content: "hi"}})
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Hint == "" || ae.Next == "" {
		t.Errorf("dockerfile_missing needs a hint and next tool: %+v", ae)
	}
	// A worker without EXPOSE is fine.
	w := e.mustApp(t, CreateAppInput{Type: "worker"})
	if _, err := e.s.DeployFiles(ctx, e.user, w.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM alpine:3.20\nCMD [\"sleep\",\"1000\"]\n"}}); err != nil {
		t.Errorf("worker: %v", err)
	}
}

func TestDeployRateLimit(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 2 WHERE id = ?`, e.user.UserID)
	app := e.mustApp(t, CreateAppInput{})
	for i := 0; i < 2; i++ {
		if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles()); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "deploy_rate_limited" || ae.HTTPStatus != 429 {
		t.Fatalf("%v", err)
	}
	if secs, ok := ae.Details["retry_after_seconds"].(int64); !ok || secs < 1 || secs > 3600 {
		t.Errorf("retry_after_seconds: %v", ae.Details)
	}
	// Deployments older than an hour do not count.
	e.exec(t, `UPDATE deployments SET queued_at = ?`, time.Now().Add(-2*time.Hour).Unix())
	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles()); err != nil {
		t.Errorf("after the hour: %v", err)
	}
}

func makeZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestUploadFlow(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})

	up, err := e.s.CreateUpload(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(up.ID, "upl_") || up.URL != "https://bitploy.test/u/"+up.ID {
		t.Fatalf("%+v", up)
	}
	if up.Curl != "curl -sS --fail-with-body --data-binary @app.zip -H 'Content-Type: application/zip' "+up.URL {
		t.Errorf("curl: %s", up.Curl)
	}
	if d := time.Until(up.ExpiresAt); d < 14*time.Minute || d > 15*time.Minute+time.Second {
		t.Errorf("expires in %v", d)
	}
	if up.MaxBytes != 95<<20 || len(up.NextSteps) == 0 {
		t.Errorf("%+v", up)
	}
	// The zip command leaves out .env and .git files in every folder.
	if !strings.Contains(up.NextSteps[0], "zip -r app.zip . -x 'node_modules/*' '*/node_modules/*' '.git/*' '*/.git/*' '.env' '.env.*' '*/.env' '*/.env.*' '.bitploy'") {
		t.Errorf("zip command: %s", up.NextSteps[0])
	}

	// Deploying before the upload arrived explains what to do.
	if _, err := e.s.Deploy(ctx, e.user, app.Slug, up.ID); depCode(err) != "upload_not_received" {
		t.Errorf("not received: %v", err)
	}
	// An invalid zip is rejected and the URL stays usable.
	if _, err := e.s.ReceiveUpload(ctx, up.ID, strings.NewReader("not a zip")); depCode(err) != "source_zip_invalid" {
		t.Errorf("invalid zip: %v", err)
	}
	zipped := makeZipBytes(t, map[string]string{"site/Dockerfile": depStaticDockerfile, "site/index.html": "hi", "__MACOSX/site/._index.html": "junk"})
	res, err := e.s.ReceiveUpload(ctx, up.ID, bytes.NewReader(zipped))
	if err != nil {
		t.Fatal(err)
	}
	if res["upload_id"] != up.ID || res["size_bytes"] != int64(len(zipped)) {
		t.Errorf("%+v", res)
	}
	// Single use.
	if _, err := e.s.ReceiveUpload(ctx, up.ID, bytes.NewReader(zipped)); depCode(err) != "upload_used" {
		t.Errorf("second receive: %v", err)
	}
	if _, err := e.s.ReceiveUpload(ctx, "upl_unknown", bytes.NewReader(zipped)); depCode(err) != "upload_not_found" {
		t.Errorf("unknown: %v", err)
	}

	// Another app cannot deploy it.
	other := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.Deploy(ctx, e.user, other.Slug, up.ID); depCode(err) != "upload_not_found" {
		t.Errorf("wrong app: %v", err)
	}
	d, err := e.s.Deploy(ctx, e.user, app.Slug, up.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceKind != "upload" || d.SourceRef != up.ID || d.State != "queued" {
		t.Fatalf("%+v", d)
	}
	if _, err := os.Stat(e.s.sourceZip(d.ID)); err != nil {
		t.Errorf("snapshot: %v", err)
	}
	if _, err := e.s.Deploy(ctx, e.user, app.Slug, up.ID); depCode(err) != "upload_used" {
		t.Errorf("reuse: %v", err)
	}
	// Redeploy rebuilds from the stored snapshot.
	r, err := e.s.Redeploy(ctx, e.user, app.Slug)
	if err != nil || r.SourceKind != "upload" || r.Seq != 2 {
		t.Fatalf("redeploy: %+v %v", r, err)
	}
	if _, err := os.Stat(e.s.sourceZip(r.ID)); err != nil {
		t.Errorf("redeploy snapshot: %v", err)
	}
}

func TestUploadExpiry(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	up, _ := e.s.CreateUpload(ctx, e.user, app.Slug)
	e.exec(t, `UPDATE uploads SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).Unix(), up.ID)
	_, err := e.s.ReceiveUpload(ctx, up.ID, bytes.NewReader(makeZipBytes(t, map[string]string{"a": "b"})))
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "upload_expired" || ae.HTTPStatus != 410 {
		t.Errorf("%v", err)
	}
	// The reconciler's cleanup removes expired uploads.
	e.s.cleanupMisc(ctx)
	if _, err := e.s.Q.GetUpload(ctx, up.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expired upload row should be deleted: %v", err)
	}
}

func TestUploadTooLarge(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	up, _ := e.s.CreateUpload(ctx, e.user, app.Slug)
	_, err := e.s.ReceiveUpload(ctx, up.ID, io.LimitReader(zeroReader{}, 96<<20))
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "upload_too_large" || ae.HTTPStatus != 413 {
		t.Fatalf("%v", err)
	}
	if entries, _ := os.ReadDir(e.s.Cfg.UploadsDir()); len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}

// Uploads that wait to be deployed take disk space nobody counts, so one
// user can only hold a few, and only while they could still deploy.
func TestUploadLimits(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	zipped := makeZipBytes(t, map[string]string{"Dockerfile": depStaticDockerfile, "index.html": "hi"})

	var ups []Upload
	for i := 0; i < maxParkedUploads; i++ {
		up, err := e.s.CreateUpload(ctx, e.user, app.Slug)
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		ups = append(ups, up)
	}
	if _, err := e.s.ReceiveUpload(ctx, ups[0].ID, bytes.NewReader(zipped)); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.CreateUpload(ctx, e.user, app.Slug)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "too_many_uploads" || ae.HTTPStatus != 429 || ae.Hint == "" {
		t.Fatalf("one upload too many: %v", err)
	}
	// Another user is not affected.
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	bobApp, err := e.s.CreateApp(ctx, bob, CreateAppInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.CreateUpload(ctx, bob, bobApp.Slug); err != nil {
		t.Errorf("other user: %v", err)
	}
	// A deployed upload, an expired URL and an upload too old to deploy free a place each.
	if _, err := e.s.Deploy(ctx, e.user, app.Slug, ups[0].ID); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE uploads SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Minute).Unix(), ups[1].ID)
	if _, err := e.s.ReceiveUpload(ctx, ups[2].ID, bytes.NewReader(zipped)); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE uploads SET received_at = ? WHERE id = ?`, time.Now().Add(-25*time.Hour).Unix(), ups[2].ID)
	for i := 0; i < 3; i++ {
		if _, err := e.s.CreateUpload(ctx, e.user, app.Slug); err != nil {
			t.Fatalf("after freeing places, upload %d: %v", i, err)
		}
	}
	if _, err := e.s.CreateUpload(ctx, e.user, app.Slug); depCode(err) != "too_many_uploads" {
		t.Errorf("full again: %v", err)
	}
	e.exec(t, `DELETE FROM uploads`)

	// The deploy rate and the storage allowance are checked before an upload, as for deploy.
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 1 WHERE id = ?`, e.user.UserID)
	if _, err := e.s.CreateUpload(ctx, e.user, app.Slug); depCode(err) != "deploy_rate_limited" {
		t.Errorf("rate: %v", err)
	}
	e.exec(t, `UPDATE users SET quota_deploys_per_hour = 100, quota_volume_mb = 1 WHERE id = ?`, e.user.UserID)
	e.exec(t, `INSERT INTO app_disk (app_id, image_bytes, volume_bytes, measured_at) VALUES (?, ?, 0, 1)`, app.ID, int64(10)<<20)
	if _, err := e.s.CreateUpload(ctx, e.user, app.Slug); depCode(err) != "storage_full" {
		t.Errorf("storage: %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestDeployWithoutSource(t *testing.T) {
	e := depTestSvc(t)
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.Deploy(context.Background(), e.user, app.Slug, ""); depCode(err) != "no_source" {
		t.Errorf("%v", err)
	}
	// Never deployed: say so, do not claim a stored source went missing.
	_, err := e.s.Redeploy(context.Background(), e.user, app.Slug)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != "app_not_deployed" || ae.HTTPStatus != 409 || ae.Next != "deploy_files" ||
		!strings.Contains(ae.Message, "never been deployed") {
		t.Errorf("redeploy of a new app: %v", err)
	}
	// A deployment exists but its stored source is gone: that is source_gone.
	d0, err := e.s.DeployFiles(context.Background(), e.user, app.Slug, staticFiles())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.s.sourceZip(d0.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Redeploy(context.Background(), e.user, app.Slug); depCode(err) != "source_gone" {
		t.Errorf("redeploy without a stored source: %v", err)
	}
	// With a git source set, deploy queues a git deployment.
	e.exec(t, `UPDATE apps SET git_repo_url = 'https://github.com/o/r', git_branch = 'main' WHERE id = ?`, app.ID)
	d, err := e.s.Deploy(context.Background(), e.user, app.Slug, "")
	if err != nil || d.SourceKind != "git" || d.SourceRef != "main" {
		t.Errorf("git deploy: %+v %v", d, err)
	}
	got, _ := e.s.GetApp(context.Background(), e.user, app.Slug)
	if got.GitSource == nil || got.GitSource.RepoURL != "https://github.com/o/r" {
		t.Errorf("git source: %+v", got.GitSource)
	}
}

func TestGetDeploymentLongPoll(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	d, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())

	// Without wait it returns at once.
	start := time.Now()
	got, err := e.s.GetDeployment(ctx, e.user, d.ID, 0)
	if err != nil || got.State != "queued" || time.Since(start) > time.Second {
		t.Fatalf("%+v %v", got, err)
	}
	// With wait it returns as soon as the state turns terminal.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = e.s.Store.DB.Exec(`UPDATE deployments SET state = 'failed', error_code = 'build_failed', error_message = 'boom', error_hint = 'fix it', log_tail = 'l1' || char(10) || 'l2', finished_at = ? WHERE id = ?`, time.Now().Unix(), d.ID)
		e.s.NotifyDeployment(d.ID)
	}()
	start = time.Now()
	got, err = e.s.GetDeployment(ctx, e.user, d.ID, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "failed" || time.Since(start) > 3*time.Second {
		t.Fatalf("long poll: %+v after %v", got, time.Since(start))
	}
	if got.Error == nil || got.Error.Code != "build_failed" || got.Error.Hint != "fix it" || len(got.LogTail) != 2 || len(got.NextSteps) == 0 {
		t.Errorf("failed deployment view: %+v", got)
	}
	if !got.Terminal() {
		t.Error("failed is terminal")
	}
	// A wait that runs out returns the current state.
	d2, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	start = time.Now()
	got, _ = e.s.GetDeployment(ctx, e.user, d2.ID, 300*time.Millisecond)
	if got.State != "queued" || time.Since(start) < 250*time.Millisecond {
		t.Errorf("wait ran out: %+v after %v", got.State, time.Since(start))
	}
}

func TestBuildLogReadingAndFollow(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	d, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	path := e.s.Cfg.BuildLog(d.ID)
	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line " + itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o640); err != nil {
		t.Fatal(err)
	}
	lines, err := e.s.BuildLog(ctx, e.user, d.ID, 3)
	if err != nil || len(lines) != 3 || lines[2].Text != "line 300" || lines[0].Stream != "build" {
		t.Fatalf("%v %+v", err, lines)
	}
	if got, _ := e.s.Logs(ctx, e.user, app.Slug, LogOptions{Kind: "build", Tail: 2}); len(got) != 2 || got[1].Text != "line 300" {
		t.Errorf("Logs build: %+v", got)
	}
	if got, _ := e.s.BuildLog(ctx, e.user, d.ID, 0); len(got) != 200 {
		t.Errorf("default tail: %d", len(got))
	}
	if _, err := e.s.Logs(ctx, e.user, app.Slug, LogOptions{Kind: "nope"}); depCode(err) != "log_kind_invalid" {
		t.Errorf("%v", err)
	}
	if _, err := e.s.Logs(ctx, e.user, app.Slug, LogOptions{Kind: "runtime"}); depCode(err) == "" {
		t.Error("runtime logs without a container must fail clearly")
	}

	// Follow: existing lines, then new ones, ends when the deployment is terminal.
	var got []string
	done := make(chan error, 1)
	go func() {
		done <- e.s.FollowBuildLog(ctx, e.user, d.ID, func(l LogLine) error { got = append(got, l.Text); return nil })
	}()
	time.Sleep(300 * time.Millisecond)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o640)
	_, _ = f.WriteString("late line\nlast partial")
	f.Close()
	time.Sleep(100 * time.Millisecond)
	e.exec(t, `UPDATE deployments SET state = 'running', finished_at = ? WHERE id = ?`, time.Now().Unix(), d.ID)
	e.s.NotifyDeployment(d.ID)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not end after the deployment finished")
	}
	if len(got) != 302 || got[0] != "line 1" || got[300] != "late line" || got[301] != "last partial" {
		t.Fatalf("followed %d lines, first %q, tail %q", len(got), got[0], got[len(got)-2:])
	}
	// Follow stops when the client goes away.
	d2, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	cctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if err := e.s.FollowBuildLog(cctx, e.user, d2.ID, func(LogLine) error { return nil }); err != nil {
		t.Errorf("cancelled follow: %v", err)
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(appendInt(nil, n))) }

func appendInt(b []byte, n int) []byte {
	if n >= 10 {
		b = appendInt(b, n/10)
	}
	return append(b, byte('0'+n%10))
}

func TestStopStartLifecycleWithoutDocker(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.StartApp(ctx, e.user, app.Slug); depCode(err) != "app_not_deployed" {
		t.Errorf("start undeployed: %v", err)
	}
	if _, err := e.s.RestartApp(ctx, e.user, app.Slug); depCode(err) != "app_not_deployed" {
		t.Errorf("restart undeployed: %v", err)
	}
	e.seedRunning(t, app, 1)
	_ = e.s.SyncRoute(ctx, app.ID)
	// A queued deployment is cancelled by stop, and the route goes away.
	queued, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	stopped, err := e.s.StopApp(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "stopped" || stopped.DesiredState != "stopped" {
		t.Errorf("%+v", stopped)
	}
	if b, _ := e.s.Routes.Read(app.Slug); b != nil {
		t.Error("stopped app must have no route file (the catch-all shows 'This app is stopped')")
	}
	if got, _ := e.s.GetDeployment(ctx, e.user, queued.ID, 0); got.State != "cancelled" {
		t.Errorf("queued deployment after stop: %s", got.State)
	}
	// Start without Docker cannot work, and must say so clearly.
	if _, err := e.s.StartApp(ctx, e.user, app.Slug); depCode(err) != "docker_unavailable" {
		t.Errorf("start without docker: %v", err)
	}
}

func TestStopAllForUser(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	a := e.mustApp(t, CreateAppInput{})
	b := e.mustApp(t, CreateAppInput{})
	e.seedRunning(t, a, 1)
	_ = e.s.SyncRoute(ctx, a.ID)
	if err := e.s.StopAllForUser(ctx, e.user.UserID); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{a.Slug, b.Slug} {
		got, _ := e.s.GetApp(ctx, e.user, slug)
		if got.DesiredState != "stopped" {
			t.Errorf("%s: %+v", slug, got)
		}
	}
	if x, _ := e.s.Routes.Read(a.Slug); x != nil {
		t.Error("route still there")
	}
}

func TestRollbackRules(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	d1 := e.seedRunning(t, app, 1)
	e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at, error_code) VALUES ('dep_failed', ?, 2, 'failed', 'files', 'api', ?, 'build_failed')`, app.ID, time.Now().Unix())
	e.exec(t, `UPDATE apps SET deploy_seq = 2 WHERE id = ?`, app.ID)
	if _, err := e.s.Rollback(ctx, e.user, app.Slug, "dep_failed"); depCode(err) != "rollback_unavailable" {
		t.Errorf("failed target: %v", err)
	}
	if _, err := e.s.Rollback(ctx, e.user, app.Slug, "dep_nope"); depCode(err) != "deployment_not_found" {
		t.Errorf("unknown: %v", err)
	}
	d, err := e.s.Rollback(ctx, e.user, app.Slug, d1)
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceKind != "rollback" || d.SourceRef != d1 || d.Image == nil || *d.Image != "bitploy/"+app.Slug+":1" || d.Seq != 3 {
		t.Errorf("%+v", d)
	}
	other := e.mustApp(t, CreateAppInput{})
	if _, err := e.s.Rollback(ctx, e.user, other.Slug, d1); depCode(err) != "deployment_not_found" {
		t.Errorf("another app's deployment: %v", err)
	}
}

func TestDeleteApp(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	dep, _ := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles())
	_ = os.WriteFile(e.s.Cfg.BuildLog(dep.ID), []byte("x"), 0o640)
	e.seedRunning(t, app, 5)
	_ = e.s.SyncRoute(ctx, app.ID)

	if err := e.s.DeleteApp(ctx, e.user, app.Slug, "wrong"); depCode(err) != "confirm_mismatch" {
		t.Errorf("mismatch: %v", err)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, ""); depCode(err) != "confirm_mismatch" {
		t.Errorf("empty: %v", err)
	}
	if _, err := e.s.GetApp(ctx, e.user, app.Slug); err != nil {
		t.Fatalf("nothing may be deleted on mismatch: %v", err)
	}
	if err := e.s.DeleteApp(ctx, e.user, app.Slug, app.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.GetApp(ctx, e.user, app.Slug); depCode(err) != "app_not_found" {
		t.Errorf("after delete: %v", err)
	}
	for _, p := range []string{e.s.sourceZip(dep.ID), e.s.Cfg.BuildLog(dep.ID), filepath.Join(e.s.Cfg.RoutesDir(), app.Slug+".yml")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be gone", p)
		}
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&n)
	if n != 0 {
		t.Errorf("deployments left: %d", n)
	}
}

func TestSyncRouteAndReconcileRoutes(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	// No deployment yet: no file.
	if err := e.s.SyncRoute(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := e.s.Routes.Read(app.Slug); b != nil {
		t.Fatal("route file before first deploy")
	}
	e.seedRunning(t, app, 1)
	if err := e.s.SyncRoute(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := e.s.Routes.Read(app.Slug)
	if !strings.Contains(string(b), "http://bp-"+app.Slug+"-1:8080") || !strings.Contains(string(b), app.Slug+".bitploy.test") {
		t.Fatalf("route file:\n%s", b)
	}

	// Reconciler: rewrites a tampered file, removes unknown and stopped ones.
	path := filepath.Join(e.s.Cfg.RoutesDir(), app.Slug+".yml")
	_ = os.WriteFile(path, []byte("garbage"), 0o644)
	_ = os.WriteFile(filepath.Join(e.s.Cfg.RoutesDir(), "ghost-app.yml"), []byte("x"), 0o644)
	_ = e.s.Routes.WriteCatchAll("bitploy.test", "http://bitploy:8080")
	e.s.reconcileRoutes(ctx)
	if got, _ := e.s.Routes.Read(app.Slug); string(got) != string(b) {
		t.Errorf("tampered route not repaired:\n%s", got)
	}
	if g, _ := e.s.Routes.Read("ghost-app"); g != nil {
		t.Error("unknown route file not removed")
	}
	if c, _ := os.ReadFile(filepath.Join(e.s.Cfg.RoutesDir(), "_catchall.yml")); len(c) == 0 {
		t.Error("catch-all must survive the reconciler")
	}
	e.exec(t, `UPDATE apps SET desired_state = 'stopped' WHERE id = ?`, app.ID)
	e.s.reconcileRoutes(ctx)
	if got, _ := e.s.Routes.Read(app.Slug); got != nil {
		t.Error("route of a stopped app not removed")
	}
	// An in-flight candidate is part of the expected file.
	e.exec(t, `UPDATE apps SET desired_state = 'running' WHERE id = ?`, app.ID)
	e.s.setCandidate(app.ID, "http://bp-"+app.Slug+"-2:8080")
	e.s.reconcileRoutes(ctx)
	got, _ := e.s.Routes.Read(app.Slug)
	if !strings.Contains(string(got), "bp-"+app.Slug+"-2:8080") || !strings.Contains(string(got), "bp-"+app.Slug+"-1:8080") {
		t.Errorf("candidate route:\n%s", got)
	}
	// Workers never get a route.
	w := e.mustApp(t, CreateAppInput{Type: "worker"})
	e.seedRunning(t, w, 1)
	_ = e.s.SyncRoute(ctx, w.ID)
	if x, _ := e.s.Routes.Read(w.Slug); x != nil {
		t.Error("worker has a route file")
	}
}

func TestReconcileInterruptedDeployments(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	now := time.Now().Unix()
	for i, st := range []string{"fetching", "validating", "building", "starting", "health_check", "swapping", "queued", "running"} {
		e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES (?, ?, ?, ?, 'files', 'api', ?)`,
			"dep_"+st, app.ID, i+1, st, now)
	}
	e.exec(t, `UPDATE apps SET status = 'deploying' WHERE id = ?`, app.ID)
	_ = os.MkdirAll(e.s.Cfg.BuildDir("dep_building"), 0o750)
	_ = os.WriteFile(filepath.Join(e.s.Cfg.BuildDir("dep_building"), "f"), []byte("x"), 0o640)
	// A pipeline that is really running in this process is left alone.
	e.s.tryBusy("app_other", "dep_swapping", func() {})

	e.s.Reconcile(ctx)

	states := map[string]string{}
	rows, _ := e.s.Store.DB.Query(`SELECT id, state, COALESCE(error_code, '') FROM deployments`)
	defer rows.Close()
	for rows.Next() {
		var id, st, code string
		_ = rows.Scan(&id, &st, &code)
		states[id] = st + "/" + code
	}
	want := map[string]string{
		"dep_fetching": "failed/interrupted", "dep_validating": "failed/interrupted", "dep_building": "failed/interrupted",
		"dep_starting": "failed/interrupted", "dep_health_check": "failed/interrupted",
		"dep_swapping": "swapping/", // active in this process
		"dep_queued":   "queued/",   // the worker will pick it up
		"dep_running":  "running/",
	}
	for id, w := range want {
		if states[id] != w {
			t.Errorf("%s = %s, want %s", id, states[id], w)
		}
	}
	if _, err := os.Stat(e.s.Cfg.BuildDir("dep_building")); !os.IsNotExist(err) {
		t.Error("build dir of an interrupted build must be removed")
	}
	got, _ := e.s.GetApp(ctx, e.user, app.Slug)
	if got.Status != "failed" || got.StatusMessage == "" {
		t.Errorf("first deployment interrupted should fail the app: %+v", got)
	}
	d, _ := e.s.GetDeployment(ctx, e.user, "dep_building", 0)
	if d.Error == nil || d.Error.Hint == "" || d.Error.Message == "" {
		t.Errorf("interrupted error: %+v", d.Error)
	}
	// Idempotent.
	e.s.Reconcile(ctx)
}

func TestPruneRetention(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	now := time.Now().Unix()
	// 25 deployments: logs for the newest 20 stay.
	for i := 1; i <= 25; i++ {
		id := "dep_" + itoa(i)
		e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES (?, ?, ?, 'superseded', 'files', 'api', ?)`, id, app.ID, i, now)
		_ = os.WriteFile(e.s.Cfg.BuildLog(id), []byte("x"), 0o640)
		_ = os.MkdirAll(e.s.sourcesDir(), 0o750)
		_ = os.WriteFile(e.s.sourceZip(id), []byte("x"), 0o640)
	}
	e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_3' WHERE id = ?`, app.ID)
	e.s.pruneApp(ctx, app.ID)
	for i := 1; i <= 25; i++ {
		_, err := os.Stat(e.s.Cfg.BuildLog("dep_" + itoa(i)))
		if (i > 5) != (err == nil) {
			t.Errorf("log of deployment %d: exists=%v", i, err == nil)
		}
	}
	// Source snapshots: only the newest and the current deployment's stay.
	kept := 0
	for i := 1; i <= 25; i++ {
		if _, err := os.Stat(e.s.sourceZip("dep_" + itoa(i))); err == nil {
			kept++
			if i != 3 && i != 25 {
				t.Errorf("snapshot of %d kept", i)
			}
		}
	}
	if kept != 2 {
		t.Errorf("kept %d snapshots", kept)
	}
}

func TestDeploymentViewShapes(t *testing.T) {
	e := depTestSvc(t)
	for state, wantStep := range map[string]string{
		"queued": "get_deployment", "building": "get_deployment", "running": "link", "failed": "after 3 failures",
		"superseded": "newer deployment", "cancelled": "get_app",
	} {
		d := e.s.deploymentView(dbDep(state), "my-app", "web")
		if len(d.NextSteps) == 0 || !strings.Contains(strings.ToLower(strings.Join(d.NextSteps, " ")), wantStep) {
			t.Errorf("%s: next_steps %v", state, d.NextSteps)
		}
		if d.Warnings == nil {
			t.Errorf("%s: warnings must be an empty list, not null", state)
		}
		if d.URL != "https://my-app.bitploy.test" {
			t.Errorf("url %q", d.URL)
		}
	}
	if d := e.s.deploymentView(dbDep("running"), "w", "worker"); d.URL != "" {
		t.Errorf("worker deployment url %q", d.URL)
	}
}

func dbDep(state string) db.Deployment {
	return db.Deployment{ID: "dep_x", AppID: "app_x", Seq: 1, State: state, SourceKind: "files", Channel: "api", QueuedAt: time.Now().Unix()}
}

// A Dockerfile that is only FROM <image> has no EXPOSE: the check passes with
// an info finding and the port is read from the image after the build.
func TestDeployFilesPortFromImage(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	d, err := e.s.DeployFiles(ctx, e.user, app.Slug, []FileInput{{Path: "Dockerfile", Content: "FROM ghcr.io/foo/bar:1.2\n"}})
	if err != nil {
		t.Fatalf("one-line Dockerfile: %v", err)
	}
	found := false
	for _, w := range d.Warnings {
		if w.Code == "port_from_image" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a port_from_image finding in %+v", d.Warnings)
	}
}

func TestPortFromImageConfig(t *testing.T) {
	tests := []struct {
		name    string
		exposed []int
		want    int
		code    string
	}{
		{"one port", []int{8090}, 8090, ""},
		{"none", nil, 0, "port_unknown"},
		{"several", []int{80, 443}, 0, "port_ambiguous"},
		{"reserved", []int{8081}, 0, "port_reserved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ae := portFromImageConfig(tt.exposed, "ghcr.io/foo/bar:1.2")
			if tt.code == "" {
				if ae != nil || got != tt.want {
					t.Fatalf("got %d, %v", got, ae)
				}
				return
			}
			if ae == nil || ae.Code != tt.code || ae.Hint == "" {
				t.Fatalf("want %s, got %v", tt.code, ae)
			}
		})
	}
}
