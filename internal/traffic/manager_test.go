package traffic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/store"
)

type mgrEnv struct {
	t   *testing.T
	m   *Manager
	st  *store.Store
	log string
	now time.Time
}

func newMgrEnv(t *testing.T) *mgrEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &mgrEnv{t: t, st: st, log: filepath.Join(dir, "access.log"), now: time.Date(2026, 10, 30, 14, 5, 0, 0, time.UTC)}
	e.m = New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), e.log, nil)
	e.m.Now = func() time.Time { return e.now }
	e.exec(`INSERT INTO users (id, email, name, password_hash, created_at, updated_at) VALUES ('usr_1', 'a@example.nl', 'a', 'x', 1, 1)`)
	e.addApp("app_1", "quiet-otter-3f9a")
	e.addApp("app_2", "other-app-1234")
	e.exec(`INSERT INTO domains (id, app_id, hostname, status, txt_token, check_until, created_at) VALUES ('dom_1', 'app_1', 'www.example.com', 'active', 't', 1, 1)`)
	return e
}

func (e *mgrEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.DB.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *mgrEnv) addApp(id, slug string) {
	e.exec(`INSERT INTO apps (id, user_id, slug, name, created_at, updated_at) VALUES (?, 'usr_1', ?, ?, 1, 1)`, id, slug, slug)
}

func (e *mgrEnv) count(q string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.st.DB.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// logLine builds a Traefik access log line.
func logLine(router, entry, path, ip string, status int) string {
	return fmt.Sprintf(`{"ClientHost":%q,"DownstreamStatus":%d,"Duration":1000,"EntryPointName":%q,"RequestHost":"h","RequestMethod":"GET","RequestPath":%q,"RouterName":%q,"StartUTC":"2026-10-30T14:04:00Z","request_Cf-Ipcountry":"NL","request_User-Agent":"Mozilla/5.0 Firefox/130 UNIQUE-UA"}`,
		ip, status, entry, path, router) + "\n"
}

func (e *mgrEnv) write(lines ...string) {
	e.t.Helper()
	if err := os.WriteFile(e.log, []byte(strings.Join(lines, "")), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *mgrEnv) run() {
	ctx := context.Background()
	e.m.Poll(ctx)
	e.m.Flush(ctx)
}

func TestRoutersMapToApps(t *testing.T) {
	e := newMgrEnv(t)
	e.write(
		logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200),
		logLine("app-quiet-otter-3f9a-www-example-com@file", "websecure", "/custom", "203.0.113.2", 200), // custom domain
		logLine("app-other-app-1234@file", "websecure", "/", "203.0.113.1", 200),
		logLine("bitploy-dashboard@file", "websecure", "/api/v1/session", "203.0.113.1", 200), // bitploy's own routers
		logLine("bitploy-catchall@file", "websecure", "/", "203.0.113.1", 404),
		logLine("acme-http@internal", "web", "/.well-known/acme-challenge/x", "203.0.113.1", 200),
		logLine("web-to-websecure@internal", "web", "/", "203.0.113.1", 301),
		logLine("app-quiet-otter-3f9a-live@file", "internal", "/", "10.201.0.2", 200), // bitploy's own probe
		logLine("app-quiet-otter-3f9a@docker", "websecure", "/", "203.0.113.1", 200),  // other provider
		logLine("app-deleted-app@file", "websecure", "/", "203.0.113.1", 200),         // no such app
		"this is not json\n",
	)
	e.run()
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_hourly WHERE app_id = 'app_1'`); got != 2 {
		t.Errorf("app_1 requests = %d, want 2 (own host and custom domain)", got)
	}
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_hourly WHERE app_id = 'app_2'`); got != 1 {
		t.Errorf("app_2 requests = %d, want 1", got)
	}
	if got := e.count(`SELECT COUNT(*) FROM traffic_hourly`); got != 2 {
		t.Errorf("%d hourly rows, want 2", got)
	}
	if got := e.count(`SELECT views FROM traffic_paths WHERE app_id = 'app_1' AND path = '/custom'`); got != 1 {
		t.Errorf("custom domain page views = %d", got)
	}
}

func TestNewAppIsFoundWithoutWaiting(t *testing.T) {
	e := newMgrEnv(t)
	e.write(logLine("app-fresh-1@file", "websecure", "/", "203.0.113.1", 200))
	e.run() // not an app yet
	e.addApp("app_3", "fresh-1")
	e.now = e.now.Add(15 * time.Second) // past the 10 second retry guard, inside the minute
	e.write(logLine("app-fresh-1@file", "websecure", "/", "203.0.113.1", 200))
	e.m.tail.reset()
	e.run()
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_hourly WHERE app_id = 'app_3'`); got != 1 {
		t.Errorf("new app requests = %d", got)
	}
}

// The database must hold counts only: no IP address, no user agent, no query string.
func TestDatabaseHoldsNoIPOrUserAgent(t *testing.T) {
	e := newMgrEnv(t)
	e.write(
		logLine("app-quiet-otter-3f9a@file", "websecure", "/page?email=me@example.com", "203.0.113.99", 200),
		logLine("app-quiet-otter-3f9a@file", "websecure", "/page", "2001:db8::1234", 200),
	)
	e.run()
	var dump strings.Builder
	for _, table := range []string{"traffic_hourly", "traffic_daily", "traffic_paths", "traffic_countries", "traffic_referrers"} {
		rows, err := e.st.DB.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&dump, "%s %v\n", table, vals)
		}
		rows.Close()
	}
	if !strings.Contains(dump.String(), "/page") {
		t.Fatalf("nothing stored: %s", dump.String())
	}
	for _, secret := range []string{"203.0.113.99", "2001:db8", "UNIQUE-UA", "Firefox", "email=", "example.com"} {
		if strings.Contains(dump.String(), secret) {
			t.Errorf("database contains %q:\n%s", secret, dump.String())
		}
	}
}

func TestFlushAddsUpAndSurvivesDeletedApp(t *testing.T) {
	e := newMgrEnv(t)
	ctx := context.Background()
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200))
	e.m.Poll(ctx)
	e.m.Flush(ctx)
	// More traffic from the same visitor in the same hour: requests add up, the visitor stays one.
	e.write(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 500))
	e.m.tail.reset()
	e.m.Poll(ctx)
	e.m.Flush(ctx)
	var req, e5, vis int64
	if err := e.st.DB.QueryRow(`SELECT requests, errors_5xx, visitors FROM traffic_daily WHERE app_id = 'app_1'`).Scan(&req, &e5, &vis); err != nil {
		t.Fatal(err)
	}
	if req != 2 || e5 != 1 || vis != 1 {
		t.Errorf("daily = %d requests, %d errors, %d visitors", req, e5, vis)
	}

	// An app deleted between reading and saving: its counts go, the others are saved.
	e.write(
		logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200),
		logLine("app-other-app-1234@file", "websecure", "/", "203.0.113.1", 200),
	)
	e.m.tail.reset()
	e.m.Poll(ctx)
	e.exec(`DELETE FROM apps WHERE id = 'app_1'`)
	e.m.Flush(ctx)
	if got := e.count(`SELECT COUNT(*) FROM traffic_daily WHERE app_id = 'app_1'`); got != 0 {
		t.Errorf("stats of the deleted app remain: %d rows", got)
	}
	if got := e.count(`SELECT COUNT(*) FROM traffic_paths WHERE app_id = 'app_1'`); got != 0 {
		t.Errorf("paths of the deleted app remain: %d rows", got)
	}
	if got := e.count(`SELECT COALESCE(SUM(requests), 0) FROM traffic_daily WHERE app_id = 'app_2'`); got != 1 {
		t.Errorf("other app requests = %d", got)
	}
}

func TestPrune(t *testing.T) {
	e := newMgrEnv(t)
	old := e.now.Add(-Retention - 48*time.Hour)
	recent := e.now.Add(-24 * time.Hour)
	for _, ts := range []time.Time{old, recent} {
		day := ts.Format("2006-01-02")
		e.exec(`INSERT INTO traffic_hourly (app_id, hour, requests) VALUES ('app_1', ?, 1)`, ts.Truncate(time.Hour).Unix())
		e.exec(`INSERT INTO traffic_daily (app_id, day, requests) VALUES ('app_1', ?, 1)`, day)
		e.exec(`INSERT INTO traffic_paths (app_id, day, path, views) VALUES ('app_1', ?, '/', 1)`, day)
		e.exec(`INSERT INTO traffic_countries (app_id, day, country, requests) VALUES ('app_1', ?, 'NL', 1)`, day)
		e.exec(`INSERT INTO traffic_referrers (app_id, day, host, visits) VALUES ('app_1', ?, 'x.com', 1)`, day)
	}
	e.m.Prune(context.Background())
	for _, table := range []string{"traffic_hourly", "traffic_daily", "traffic_paths", "traffic_countries", "traffic_referrers"} {
		if got := e.count("SELECT COUNT(*) FROM " + table); got != 1 {
			t.Errorf("%s: %d rows left, want 1", table, got)
		}
	}
}

func TestCountryFromHeaderOrGeo(t *testing.T) {
	e := newMgrEnv(t)
	line := func(country string) string {
		return strings.Replace(logLine("app-quiet-otter-3f9a@file", "websecure", "/", "203.0.113.1", 200), `"request_Cf-Ipcountry":"NL"`, `"request_Cf-Ipcountry":"`+country+`"`, 1)
	}
	e.write(line("DE"), line("XX"))
	e.run()
	for code, want := range map[string]int64{"DE": 1, UnknownCountry: 1} {
		if got := e.count(`SELECT requests FROM traffic_countries WHERE app_id = 'app_1' AND country = ?`, code); got != want {
			t.Errorf("%s = %d requests, want %d", code, got, want)
		}
	}
}
