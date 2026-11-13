package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
)

// The dashboard and the edge host can live below the apps domain. An app must
// not be able to take that label, or it would get a router for the same host.
func TestSlugCannotTakeBitployOwnHosts(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.s.Cfg.PublicURL = "https://console.bitploy.test"
	e.s.Cfg.EdgeHost = "gate.bitploy.test"
	for slug, want := range map[string]string{
		"console":  "slug_reserved", // dashboard host
		"CONSOLE":  "slug_reserved",
		"gate":     "slug_reserved", // custom BITPLOY_EDGE_HOST
		"consoles": "",
		"gate2":    "",
	} {
		_, err := e.s.CreateApp(ctx, e.user, CreateAppInput{Slug: slug})
		if got := depCode(err); got != want {
			t.Errorf("create %q: code %q, want %q", slug, got, want)
		}
	}
	// A rename is checked the same way.
	a := e.mustApp(t, CreateAppInput{})
	for _, slug := range []string{"console", "gate"} {
		if _, err := e.s.UpdateApp(ctx, e.user, a.Slug, UpdateAppInput{Slug: &slug}); depCode(err) != "slug_reserved" {
			t.Errorf("rename to %q: %v", slug, err)
		}
	}
}

// Parallel create_app calls must not get past the app quota between the check and the insert.
func TestCreateAppQuotaHoldsUnderParallelCalls(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET quota_apps = 3 WHERE id = ?`, e.user.UserID)
	var ok, quota atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{}); depCode(err) {
			case "":
				ok.Add(1)
			case "apps_quota_exceeded":
				quota.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 3 || quota.Load() != 9 {
		t.Fatalf("created %d apps (quota 3), %d refused", ok.Load(), quota.Load())
	}
	if apps, _ := e.s.ListApps(ctx, e.user); len(apps) != 3 {
		t.Fatalf("%d apps stored", len(apps))
	}
}

func TestServerAppLimitHoldsUnderParallelCalls(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	e.s.Cfg.MaxApps = 2
	e.exec(t, `UPDATE users SET quota_apps = 100`)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.s.CreateApp(ctx, e.user, CreateAppInput{}); err == nil {
				ok.Add(1)
			} else if depCode(err) != "server_app_limit" {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 2 {
		t.Fatalf("created %d apps with BITPLOY_MAX_APPS=2", ok.Load())
	}
	// A user hears that the server is full, not its numbers or setting, and
	// is not told to delete their own app.
	_, err := e.s.CreateApp(ctx, e.user, CreateAppInput{})
	ae := apperr.As(err)
	if ae.Code != "server_app_limit" || strings.Contains(ae.Message+ae.Hint, "BITPLOY_MAX_APPS") || strings.Contains(ae.Message, "2") ||
		strings.Contains(ae.Hint, "delete") || ae.Next != "" || !strings.Contains(ae.Hint, "admin") {
		t.Errorf("user sees %q / %q (next %q)", ae.Message, ae.Hint, ae.Next)
	}
}

// With the disk almost full no new upload is written and no deployment is queued.
func TestUploadsAndDeploysRefusedWhenDiskIsFull(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	up, err := e.s.CreateUpload(ctx, e.user, app.Slug)
	if err != nil {
		t.Fatal(err)
	}
	full := true
	e.s.ops.diskHasRoom = func() (bool, error) { return !full, nil }

	zipped := makeZipBytes(t, map[string]string{"Dockerfile": depStaticDockerfile, "index.html": "hi"})
	_, err = e.s.ReceiveUpload(ctx, up.ID, bytes.NewReader(zipped))
	if depCode(err) != "server_disk_full" {
		t.Fatalf("receive with a full disk: %v", err)
	}
	if _, err := e.s.DeployFiles(ctx, e.user, app.Slug, staticFiles()); depCode(err) != "server_disk_full" {
		t.Fatalf("deploy with a full disk: %v", err)
	}
	// The refused upload was not consumed: once there is room it works.
	full = false
	if _, err := e.s.ReceiveUpload(ctx, up.ID, bytes.NewReader(zipped)); err != nil {
		t.Fatalf("receive after space was freed: %v", err)
	}
}

// An email or password that can never be valid is refused before it reaches
// the limiter keys and the audit log (a megabyte per attempt adds up).
func TestLoginRefusesOversizedCredentialsWithoutStoringThem(t *testing.T) {
	s, _ := acctTestSvc(t)
	ctx := context.Background()
	mustUser(t, s, "me@example.nl", false)
	for name, c := range map[string]struct{ email, pw string }{
		"huge email":    {strings.Repeat("a", 1<<20) + "@example.nl", "password-1234"},
		"huge password": {"me@example.nl", strings.Repeat("p", 1<<20)},
	} {
		_, _, err := s.Login(ctx, c.email, c.pw, "198.51.100.9", "ua")
		if code(err) != "invalid_credentials" {
			t.Errorf("%s: code %q", name, code(err))
		}
	}
	lim := s.limiter()
	lim.mu.Lock()
	for k := range lim.fails {
		if len(k) > 512 {
			t.Errorf("limiter stores a %d byte key", len(k))
		}
	}
	lim.mu.Unlock()
	entries, err := s.AdminListAudit(ctx, Actor{Role: "admin", Scope: ScopeSession}, 100, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if len(mustJSON(en.Details)) > 1024 {
			t.Errorf("audit row %s holds %d bytes of details", en.Action, len(mustJSON(en.Details)))
		}
	}
	// A normal login still works.
	if _, _, err := s.Login(ctx, "me@example.nl", "password-1234", "198.51.100.9", "ua"); err != nil {
		t.Fatal(err)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// A body that hits the HTTP layer's size cap (chunked upload, no Content-Length)
// is "too large", not "cut off": retrying the same curl would never work.
func TestUploadOverHTTPCapIsTooLargeNotInterrupted(t *testing.T) {
	e := depTestSvc(t)
	ctx := context.Background()
	app := e.mustApp(t, CreateAppInput{})
	up, _ := e.s.CreateUpload(ctx, e.user, app.Slug)
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"size cap":    {&http.MaxBytesError{Limit: 95 << 20}, "upload_too_large"},
		"cut off":     {io.ErrUnexpectedEOF, "upload_interrupted"},
		"wrapped cap": {fmt.Errorf("read body: %w", &http.MaxBytesError{Limit: 1}), "upload_too_large"},
	} {
		_, err := e.s.ReceiveUpload(ctx, up.ID, failingReader{c.err})
		if got := depCode(err); got != c.want {
			t.Errorf("%s: code %q, want %q", name, got, c.want)
		}
	}
	if entries, _ := os.ReadDir(e.s.Cfg.UploadsDir()); len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}
