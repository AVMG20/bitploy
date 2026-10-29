package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/apperr"
	"github.com/AVMG20/bitploy/internal/config"
	"github.com/AVMG20/bitploy/internal/domains"
	"github.com/AVMG20/bitploy/internal/metrics"
	"github.com/AVMG20/bitploy/internal/store"
)

// opsFakeDNS answers from maps; unknown names are NXDOMAIN.
type opsFakeDNS struct {
	cname map[string]string
	hosts map[string][]string
	txt   map[string][]string
}

func nx(n string) error { return &net.DNSError{Err: "no such host", Name: n, IsNotFound: true} }

func (f *opsFakeDNS) LookupCNAME(_ context.Context, h string) (string, error) {
	if c, ok := f.cname[h]; ok {
		return c + ".", nil
	}
	if _, ok := f.hosts[h]; ok {
		return h + ".", nil
	}
	return "", nx(h)
}
func (f *opsFakeDNS) LookupHost(_ context.Context, h string) ([]string, error) {
	if v, ok := f.hosts[h]; ok {
		return v, nil
	}
	return nil, nx(h)
}
func (f *opsFakeDNS) LookupTXT(_ context.Context, n string) ([]string, error) {
	if v, ok := f.txt[n]; ok {
		return v, nil
	}
	return nil, nx(n)
}

type opsEnv struct {
	svc     *Service
	dns     *opsFakeDNS
	synced  []string
	syncErr error
	now     time.Time
	owner   Actor
	other   Actor
	admin   Actor
}

func newOpsEnv(t *testing.T) *opsEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		PublicURL: "https://bitploy.test", AppsDomain: "bitploy.test", AppsScheme: "https", EdgeHost: "edge.bitploy.test",
		ServerIPv4: "203.0.113.10", DataDir: dir, Signup: "invite",
	}
	st, err := store.Open(filepath.Join(dir, "test.db"))
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
	e := &opsEnv{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), dns: &opsFakeDNS{}}
	e.svc = New(cfg, st, crypto, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.svc.Now = func() time.Time { return e.now }
	e.svc.ops.resolvers = []domains.Named{{Name: "test", R: e.dns}}
	e.svc.ops.syncRoute = func(_ context.Context, appID string) error {
		e.synced = append(e.synced, appID)
		return e.syncErr
	}
	for _, u := range []struct{ id, role string }{{"usr_owner", "user"}, {"usr_other", "user"}, {"usr_admin", "admin"}} {
		e.exec(t, `INSERT INTO users (id, email, name, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, 'x', ?, 1, 1)`,
			u.id, u.id+"@example.nl", u.id, u.role)
	}
	e.owner = Actor{UserID: "usr_owner", Role: "user", Channel: ChannelAPI, Scope: ScopeDeploy}
	e.other = Actor{UserID: "usr_other", Role: "user", Channel: ChannelAPI, Scope: ScopeDeploy}
	e.admin = Actor{UserID: "usr_admin", Role: "admin", Channel: ChannelDashboard, Scope: ScopeSession}
	return e
}

func (e *opsEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.svc.Store.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (e *opsEnv) addApp(t *testing.T, id, slug, user, typ string) {
	t.Helper()
	e.exec(t, `INSERT INTO apps (id, user_id, slug, name, type, status, memory_mb, cpu_millis, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'running', 512, 500, 1, 1)`,
		id, user, slug, slug, typ)
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
	if ae.Hint == "" {
		t.Errorf("error %s has no hint", code)
	}
}

func recordOf(d Domain, typ string) (DNSRecord, bool) {
	for _, r := range d.Records {
		if r.Type == typ {
			return r, true
		}
	}
	return DNSRecord{}, false
}

func TestAddDomain(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	e.addApp(t, "app_2", "calm-fox-1234", "usr_other", "web")
	e.addApp(t, "app_w", "bot-worker", "usr_owner", "worker")

	d, err := e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "WWW.Example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hostname != "www.example.com" || d.Status != "pending_dns" || d.ID == "" {
		t.Errorf("domain = %+v", d)
	}
	cname, ok := recordOf(d, "CNAME")
	if !ok || cname.Name != "www.example.com" || cname.Value != "edge.bitploy.test" {
		t.Errorf("CNAME record = %+v", cname)
	}
	txt, ok := recordOf(d, "TXT")
	if !ok || txt.Name != "_bitploy-challenge.www.example.com" || !strings.HasPrefix(txt.Value, "bitploy-") || len(txt.Value) < 30 {
		t.Errorf("TXT record = %+v", txt)
	}
	if len(d.Notes) == 0 || !strings.Contains(d.Notes[len(d.Notes)-1], "DNS only (grey cloud)") {
		t.Errorf("notes = %v", d.Notes)
	}

	// Same hostname, same app: idempotent, same token.
	again, err := e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "www.example.com")
	if err != nil || again.ID != d.ID {
		t.Errorf("repeat add: %+v, %v", again, err)
	}
	if t2, _ := recordOf(again, "TXT"); t2.Value != txt.Value {
		t.Error("token changed on repeat add")
	}

	// Apex domain: A record to the server.
	apex, err := e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "example.nl")
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := recordOf(apex, "A"); !ok || a.Value != "203.0.113.10" {
		t.Errorf("apex records = %+v", apex.Records)
	}

	// One hostname belongs to one app.
	_, err = e.svc.AddDomain(ctx, e.other, "calm-fox-1234", "www.example.com")
	wantCode(t, err, "domain_taken")

	// Validation and reserved names.
	_, err = e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "https://x.example.com/")
	wantCode(t, err, "domain_invalid")
	_, err = e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "other.bitploy.test")
	wantCode(t, err, "domain_reserved")
	_, err = e.svc.AddDomain(ctx, e.owner, "bot-worker", "bot.example.com")
	wantCode(t, err, "domain_worker_app")

	// Other people's apps do not exist for you; read-only tokens cannot add.
	_, err = e.svc.AddDomain(ctx, e.other, "quiet-otter-3f9a", "mine.example.com")
	wantCode(t, err, "app_not_found")
	ro := e.owner
	ro.Scope = ScopeRead
	_, err = e.svc.AddDomain(ctx, ro, "quiet-otter-3f9a", "ro.example.com")
	wantCode(t, err, "token_read_only")

	// Quota: users.quota_domains_per_app.
	e.exec(t, `UPDATE users SET quota_domains_per_app = 2 WHERE id = 'usr_owner'`)
	_, err = e.svc.AddDomain(ctx, e.owner, "quiet-otter-3f9a", "third.example.com")
	wantCode(t, err, "domain_limit")

	list, err := e.svc.ListDomains(ctx, e.owner, "quiet-otter-3f9a")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if _, err := e.svc.ListDomains(ctx, e.other, "quiet-otter-3f9a"); err == nil {
		t.Error("another user could list the domains")
	}
	var audits int
	e.svc.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'domain.add'`).Scan(&audits)
	if audits != 2 {
		t.Errorf("%d domain.add audit rows, want 2", audits)
	}
}

func TestMaxFiveDomainsByDefault(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	for _, h := range []string{"a", "b", "c", "d", "e"} {
		if _, err := e.svc.AddDomain(ctx, e.owner, "app_1", h+".example.com"); err != nil {
			t.Fatalf("domain %s: %v", h, err)
		}
	}
	_, err := e.svc.AddDomain(ctx, e.owner, "app_1", "f.example.com")
	wantCode(t, err, "domain_limit")
}

func TestCheckDomainLifecycle(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	d, err := e.svc.AddDomain(ctx, e.owner, "app_1", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := recordOf(d, "TXT")
	edgeIPs := map[string][]string{"edge.bitploy.test": {"203.0.113.10"}}

	// 1. Nothing in DNS yet.
	e.dns.hosts = edgeIPs
	d, err = e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "pending_dns" || !strings.Contains(d.LastError, "No DNS record found for www.example.com") || d.LastCheckedAt == nil {
		t.Errorf("empty DNS: %+v", d)
	}

	// 2. Wrong CNAME (a parking page) and no TXT.
	e.dns.cname = map[string]string{"www.example.com": "parking.godaddy.com"}
	e.dns.hosts = map[string][]string{"edge.bitploy.test": {"203.0.113.10"}, "www.example.com": {"198.51.100.7"}}
	d, _ = e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if d.Status != "pending_dns" || !strings.Contains(d.LastError, "CNAME points to parking.godaddy.com, expected edge.bitploy.test") {
		t.Errorf("wrong CNAME: %+v", d)
	}
	if len(e.synced) != 0 {
		t.Error("route written before DNS passed")
	}
	if hosts, _ := e.svc.ActiveHostnames(ctx, "app_1"); len(hosts) != 0 {
		t.Errorf("hostnames before DNS: %v", hosts)
	}

	// 3. Correct CNAME and TXT.
	e.dns.cname = map[string]string{"www.example.com": "edge.bitploy.test"}
	e.dns.txt = map[string][]string{"_bitploy-challenge.www.example.com": {token.Value}}
	e.now = e.now.Add(time.Minute)
	d, err = e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "active" || d.LastError != "" || d.ActivatedAt == nil {
		t.Fatalf("after fixing DNS: %+v", d)
	}
	if len(e.synced) != 1 || e.synced[0] != "app_1" {
		t.Errorf("route syncs = %v", e.synced)
	}
	hosts, _ := e.svc.ActiveHostnames(ctx, "app_1")
	if len(hosts) != 1 || hosts[0] != "www.example.com" {
		t.Errorf("active hostnames = %v", hosts)
	}

	// 4. DNS breaks later: the domain stays active, the error is reported.
	e.dns.cname = map[string]string{"www.example.com": "parking.godaddy.com"}
	d, _ = e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if d.Status != "active" || !strings.Contains(d.LastError, "DNS no longer matches") {
		t.Errorf("broken active domain: %+v", d)
	}

	// 5. Remove: the route is rewritten and the hostname is free again.
	e.synced = nil
	if err := e.svc.RemoveDomain(ctx, e.owner, "app_1", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	if len(e.synced) != 1 {
		t.Errorf("route syncs after remove = %v", e.synced)
	}
	if hosts, _ := e.svc.ActiveHostnames(ctx, "app_1"); len(hosts) != 0 {
		t.Errorf("hostnames after remove: %v", hosts)
	}
	wantCode(t, e.svc.RemoveDomain(ctx, e.owner, "app_1", "www.example.com"), "domain_not_found")
	_, err = e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	wantCode(t, err, "domain_not_found")

	var actions []string
	rows, _ := e.svc.Store.DB.Query(`SELECT action FROM audit_log WHERE action LIKE 'domain.%' ORDER BY id`)
	defer rows.Close()
	for rows.Next() {
		var a string
		rows.Scan(&a)
		actions = append(actions, a)
	}
	if got := strings.Join(actions, ","); got != "domain.add,domain.issuing_cert,domain.active,domain.remove" {
		t.Errorf("audit trail = %s", got)
	}
}

func TestCheckDomainOfAnotherAppIsNotFound(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	e.addApp(t, "app_2", "calm-fox-1234", "usr_owner", "web")
	if _, err := e.svc.AddDomain(ctx, e.owner, "app_1", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CheckDomain(ctx, e.owner, "app_2", "www.example.com")
	wantCode(t, err, "domain_not_found")
	wantCode(t, e.svc.RemoveDomain(ctx, e.owner, "app_2", "www.example.com"), "domain_not_found")
}

func TestRouteWriteFailureKeepsIssuingCertAndRetries(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	d, _ := e.svc.AddDomain(ctx, e.owner, "app_1", "www.example.com")
	tok, _ := recordOf(d, "TXT")
	e.dns.cname = map[string]string{"www.example.com": "edge.bitploy.test"}
	e.dns.txt = map[string][]string{"_bitploy-challenge.www.example.com": {tok.Value}}
	e.syncErr = errors.New("disk full")

	d, err := e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "issuing_cert" || d.LastError == "" || d.ActivatedAt != nil {
		t.Fatalf("after failed route write: %+v", d)
	}
	// The hostname is already listed so a successful SyncRoute includes it.
	if hosts, _ := e.svc.ActiveHostnames(ctx, "app_1"); len(hosts) != 1 {
		t.Errorf("hostnames while issuing = %v", hosts)
	}

	e.syncErr = nil
	e.svc.checkPendingDomains(ctx)
	list, _ := e.svc.ListDomains(ctx, e.owner, "app_1")
	if len(list) != 1 || list[0].Status != "active" || list[0].LastError != "" {
		t.Errorf("after the background retry: %+v", list)
	}
}

func TestPendingDomainGivesUpAfter48Hours(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	if _, err := e.svc.AddDomain(ctx, e.owner, "app_1", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	status := func() Domain {
		l, _ := e.svc.ListDomains(ctx, e.owner, "app_1")
		return l[0]
	}

	e.now = e.now.Add(47 * time.Hour)
	e.svc.checkPendingDomains(ctx)
	if d := status(); d.Status != "pending_dns" {
		t.Fatalf("after 47 h: %+v", d)
	}
	e.now = e.now.Add(2 * time.Hour)
	e.svc.checkPendingDomains(ctx)
	d := status()
	if d.Status != "failed" || !strings.Contains(d.LastError, "stopped checking after 48 hours") {
		t.Fatalf("after 49 h: %+v", d)
	}
	// Failed domains are not polled any more.
	e.svc.checkPendingDomains(ctx)
	if d2 := status(); d2.LastCheckedAt == nil || !d2.LastCheckedAt.Equal(*d.LastCheckedAt) {
		t.Error("a failed domain was checked again in the background")
	}
	// Check now restarts the 48 hour watch.
	d, err := e.svc.CheckDomain(ctx, e.owner, "app_1", "www.example.com")
	if err != nil || d.Status != "pending_dns" {
		t.Errorf("manual check of a failed domain: %+v, %v", d, err)
	}
	e.now = e.now.Add(time.Hour)
	e.svc.checkPendingDomains(ctx)
	if d := status(); d.Status != "pending_dns" {
		t.Errorf("one hour after restart: %+v", d)
	}
}

func TestUsage(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	e.addApp(t, "app_2", "calm-fox-1234", "usr_owner", "web")
	e.exec(t, `UPDATE apps SET status = 'stopped' WHERE id = 'app_2'`)
	e.addApp(t, "app_x", "other-app", "usr_other", "web")

	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	dep := func(id, app string, seq int, queued, started, built, finished int64) {
		e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at, started_at, built_at, finished_at)
			VALUES (?, ?, ?, 'running', 'files', 'api', ?, ?, ?, ?)`, id, app, seq, queued, started, built, finished)
	}
	now := e.now.Unix()
	dep("dep_1", "app_1", 1, now-3600, now-3600, now-3600+120, now-3500)                 // today, 2 build minutes
	dep("dep_2", "app_1", 2, now-3*86400, now-3*86400, now-3*86400+60, now-3*86400+90)   // 3 days ago, 1 build minute
	dep("dep_3", "app_2", 1, month-86400, month-86400, month-86400+600, month-86400+700) // September: not counted
	dep("dep_x", "app_x", 1, now-100, now-100, now-50, now-40)                           // someone else
	e.exec(t, `INSERT INTO usage_daily VALUES ('app_1','usr_owner','2026-10-28', 86400), ('app_1','usr_owner','2026-10-29', 3600),
		('app_1','usr_owner','2026-09-30', 86400), ('app_x','usr_other','2026-10-29', 999999)`)
	e.exec(t, `INSERT INTO app_disk VALUES ('app_1', ?, ?, 1), ('app_2', ?, 0, 1)`, 100<<20, 20<<20, 50<<20)

	// A live sample for app_1 only.
	e.svc.metricsMgr().SetLatestForTest("app_1", metrics.Point{TS: e.now, CPU: 12.34, MemBytes: 64 << 20})

	u, err := e.svc.Usage(ctx, "usr_owner")
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{
		Apps: 2, AppsRunning: 1, MemoryReservedMB: 1024, CPUReservedMillis: 1000,
		MemoryUsedMB: 64, CPUNowPercent: 12.3, BuildMinutesMonth: 3, RunningHoursMonth: 25,
		DeploysMonth: 2, DeploysWeek: 2, DiskUsedMB: 170,
	}
	if u != want {
		t.Errorf("usage =\n%+v\nwant\n%+v", u, want)
	}
	if z, err := e.svc.Usage(ctx, "usr_nobody"); err != nil || z != (Usage{}) {
		t.Errorf("unknown user: %+v, %v", z, err)
	}
}

func TestGetMetricsAndLatestSample(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	for i := 0; i < 4; i++ {
		e.exec(t, `INSERT INTO metric_samples VALUES ('app_1', ?, ?, ?, 10, 20)`, e.now.Unix()-int64(i)*15, 25.0, 128<<20)
	}
	m, err := e.svc.GetMetrics(ctx, e.owner, "quiet-otter-3f9a", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if m.App != "quiet-otter-3f9a" || m.Window != "1h" || m.MemoryLimitMB != 512 || len(m.Points) != 4 || m.Latest != nil {
		t.Errorf("metrics = %+v", m)
	}
	if p := m.Points[0]; p.CPU != 25 || p.MemoryMB != 128 || p.RxBytes != 10 || p.TxBytes != 20 {
		t.Errorf("point = %+v", p)
	}
	if m, err = e.svc.GetMetrics(ctx, e.owner, "app_1", ""); err != nil || m.Window != "24h" || len(m.Points) == 0 {
		t.Errorf("default window: %+v, %v", m, err)
	}
	_, err = e.svc.GetMetrics(ctx, e.owner, "app_1", "90d")
	wantCode(t, err, "invalid_window")
	_, err = e.svc.GetMetrics(ctx, e.other, "app_1", "1h")
	wantCode(t, err, "app_not_found")
	if _, err = e.svc.GetMetrics(ctx, e.admin, "app_1", "1h"); err != nil {
		t.Errorf("admins can read every app: %v", err)
	}
	// Read-only tokens may read metrics.
	ro := e.owner
	ro.Scope = ScopeRead
	if _, err = e.svc.GetMetrics(ctx, ro, "app_1", "1h"); err != nil {
		t.Errorf("read scope: %v", err)
	}

	e.svc.metricsMgr().SetLatestForTest("app_1", metrics.Point{TS: e.now, CPU: 3.04, MemBytes: 256 << 20})
	if p := e.svc.LatestSample("app_1"); p == nil || p.CPU != 3 || p.MemoryMB != 256 {
		t.Errorf("LatestSample = %+v", p)
	}
	if e.svc.LatestSample("app_none") != nil {
		t.Error("sample for an unknown app")
	}
}

func TestAdminServerStats(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	e.addApp(t, "app_1", "quiet-otter-3f9a", "usr_owner", "web")
	e.addApp(t, "app_2", "calm-fox-1234", "usr_owner", "web")
	e.exec(t, `UPDATE apps SET status = 'failed' WHERE id = 'app_2'`)
	for i, st := range []string{"queued", "queued", "building", "fetching", "running", "failed"} {
		e.exec(t, `INSERT INTO deployments (id, app_id, seq, state, source_kind, channel, queued_at) VALUES (?, 'app_1', ?, ?, 'files', 'api', 1)`,
			"dep_"+st+string(rune('a'+i)), i+1, st)
	}
	_, err := e.svc.AdminServerStats(ctx, e.owner)
	wantCode(t, err, "admin_only")
	st, err := e.svc.AdminServerStats(ctx, e.admin)
	if err != nil {
		t.Fatal(err)
	}
	if st.AppsTotal != 2 || st.AppsRunning != 1 || st.MemReservedMB != 1024 || st.BuildsQueued != 2 || st.BuildsRunning != 2 {
		t.Errorf("stats = %+v", st)
	}
	if st.DiskTotalGB <= 0 || st.DiskUsedGB <= 0 {
		t.Errorf("disk = %v / %v GB", st.DiskUsedGB, st.DiskTotalGB)
	}
}

func TestCapacityGuardsDoNotFailLocally(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	if ok, err := e.svc.DiskHasRoom(); err != nil {
		t.Errorf("DiskHasRoom: %v", err)
	} else {
		_ = ok
	}
	ok, err := e.svc.ServerHasRoom(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.HostSupported && !ok {
		t.Error("without host stats there must always be room")
	}
	if metrics.HostSupported {
		// An absurd request never fits.
		if ok, _ := e.svc.ServerHasRoom(ctx, 1<<40); ok {
			t.Error("a petabyte of app memory fits?")
		}
	}
}

func TestRunBackgroundWithoutDockerStopsOnCancel(t *testing.T) {
	e := newOpsEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.RunBackground(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunBackground did not stop")
	}
}
