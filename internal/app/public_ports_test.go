package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AVMG20/bitploy/internal/dockerx"
)

// fakeProxies stands in for the Docker client: it remembers the forwarders.
type fakeProxies struct {
	mu      sync.Mutex
	running map[string]dockerx.PortProxySpec
	taken   map[int]bool // host ports something else holds
	fail    error        // returned by the next EnsurePortProxy
	ensures int
}

func newFakeProxies() *fakeProxies {
	return &fakeProxies{running: map[string]dockerx.PortProxySpec{}, taken: map[int]bool{}}
}

func (f *fakeProxies) EnsurePortProxy(_ context.Context, s dockerx.PortProxySpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensures++
	if f.fail != nil {
		err := f.fail
		f.fail = nil
		return err
	}
	if f.taken[s.HostPort] {
		return fmt.Errorf("%w: Bind for 0.0.0.0:%d failed: port is already allocated", dockerx.ErrPortInUse, s.HostPort)
	}
	f.running[s.AppID] = s
	return nil
}

func (f *fakeProxies) RemovePortProxy(_ context.Context, appID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, appID)
	return nil
}

func (f *fakeProxies) ListPortProxies(context.Context) ([]dockerx.PortProxyInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []dockerx.PortProxyInfo{}
	for id := range f.running {
		out = append(out, dockerx.PortProxyInfo{Name: dockerx.PortProxyName(id), AppID: id, Running: true})
	}
	return out, nil
}

func (f *fakeProxies) PortProxyRunning(_ context.Context, appID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.running[appID]
	return ok, nil
}

func (f *fakeProxies) spec(appID string) (dockerx.PortProxySpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.running[appID]
	return s, ok
}

// pubEnv is a service with public ports 30000-30002 and a postgres app.
func pubEnv(t *testing.T) (*depEnv, *fakeProxies, App) {
	t.Helper()
	e := depTestSvc(t)
	e.s.Cfg.PublicPortsFrom, e.s.Cfg.PublicPortsTo = 30000, 30002
	e.s.Cfg.PublicPortsPerUser = 2
	e.s.Cfg.PublicPortsHost = "203.0.113.10"
	fp := newFakeProxies()
	e.s.usePortProxies(fp)
	ctx := context.Background()
	p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	return e, fp, pg
}

func auditDetails(t *testing.T, e *depEnv, action string) []string {
	t.Helper()
	rows, err := e.s.Store.DB.Query(`SELECT details FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func TestPublicPortOpenRevealClose(t *testing.T) {
	e, fp, pg := pubEnv(t)
	ctx := context.Background()
	if pg.PublicPort.Enabled || !pg.PublicPort.Available {
		t.Fatalf("new postgres: %+v", pg.PublicPort)
	}

	got, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	pp := got.PublicPort
	if !pp.Enabled || pp.Port != 30000 || pp.Address != "203.0.113.10:30000" || pp.TargetPort != 5432 || !pp.Running || pp.ExpiresAt == nil {
		t.Fatalf("open: %+v", pp)
	}
	if d := time.Until(*pp.ExpiresAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("default close after = %v, want 1h", d)
	}
	if pp.ConnectionURL != "postgresql://app:********@203.0.113.10:30000/app" {
		t.Errorf("masked url = %q", pp.ConnectionURL)
	}
	spec, ok := fp.spec(pg.ID)
	if !ok || spec.Target != "app:5432" || spec.Network != "bp-"+pg.Slug || spec.HostPort != 30000 || spec.Until != pp.ExpiresAt.Unix() || len(spec.Allow) != 0 {
		t.Fatalf("forwarder: %+v", spec)
	}

	// The password is in no app JSON and no audit row.
	vars, _ := e.s.sharedVars(ctx, pg.ID)
	pass := vars["POSTGRES_PASSWORD"]
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), pass) {
		t.Fatal("the app JSON holds the password")
	}

	rev, err := e.s.RevealPublicConnection(ctx, e.user, pg.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if rev.ConnectionURL != "postgresql://app:"+pass+"@203.0.113.10:30000/app" || rev.Secrets["POSTGRES_PASSWORD"] != pass {
		t.Fatalf("reveal = %+v", rev)
	}
	for _, action := range []string{"app.public_port_opened", "app.public_port_revealed"} {
		ds := auditDetails(t, e, action)
		if len(ds) != 1 || strings.Contains(ds[0], pass) {
			t.Fatalf("%s audit = %v", action, ds)
		}
	}

	// Change while open: the port stays, the allow list and expiry change.
	got, err = e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true, AllowFrom: []string{"198.51.100.7"}, CloseAfter: strp("never")})
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicPort.Port != 30000 || got.PublicPort.ExpiresAt != nil || len(got.PublicPort.AllowFrom) != 1 || got.PublicPort.AllowFrom[0] != "198.51.100.7/32" {
		t.Fatalf("changed: %+v", got.PublicPort)
	}
	if spec, _ := fp.spec(pg.ID); spec.Until != 0 || len(spec.Allow) != 1 {
		t.Fatalf("forwarder after change: %+v", spec)
	}
	// Leaving allow_from out keeps it.
	got, _ = e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true})
	if len(got.PublicPort.AllowFrom) != 1 || got.PublicPort.ExpiresAt != nil {
		t.Fatalf("kept: %+v", got.PublicPort)
	}
	if n := len(auditDetails(t, e, "app.public_port_changed")); n != 2 {
		t.Errorf("changed audit rows = %d", n)
	}

	// Close.
	got, err = e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if got.PublicPort.Enabled || got.PublicPort.Port != 0 {
		t.Fatalf("closed: %+v", got.PublicPort)
	}
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("the forwarder survived the close")
	}
	if ds := auditDetails(t, e, "app.public_port_closed"); len(ds) != 1 || !strings.Contains(ds[0], `"reason":"closed"`) {
		t.Fatalf("closed audit = %v", ds)
	}
	if _, err := e.s.RevealPublicConnection(ctx, e.user, pg.Slug); depCode(err) != "public_port_closed" {
		t.Errorf("reveal while closed: %v", err)
	}
	// Closing again is fine.
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: false}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPortRefusals(t *testing.T) {
	e, _, pg := pubEnv(t)
	ctx := context.Background()

	web := e.mustApp(t, CreateAppInput{Name: "Site"})
	if !strings.Contains(fmt.Sprint(web.PublicPort.Reason), "web_app") || web.PublicPort.Available {
		t.Errorf("web app view: %+v", web.PublicPort)
	}
	if _, err := e.s.SetPublicPort(ctx, e.user, web.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_port_web_app" {
		t.Errorf("web app: %v", err)
	}
	worker := e.mustApp(t, CreateAppInput{Name: "Jobs", Type: "worker"})
	if _, err := e.s.SetPublicPort(ctx, e.user, worker.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_port_no_port" {
		t.Errorf("worker without a port: %v", err)
	}
	for _, c := range []struct {
		in   PublicPortInput
		code string
	}{
		{PublicPortInput{Enabled: true, CloseAfter: strp("2h")}, "close_after_invalid"},
		{PublicPortInput{Enabled: true, AllowFrom: []string{"2001:db8::1"}}, "allow_from_invalid"},
		{PublicPortInput{Enabled: true, AllowFrom: []string{"example.com"}}, "allow_from_invalid"},
		{PublicPortInput{Enabled: true, Service: strp("db")}, "service_not_for_single_app"},
		{PublicPortInput{Enabled: true, OnlyMyIP: true}, "my_ip_unknown"},
	} {
		if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, c.in); depCode(err) != c.code {
			t.Errorf("%+v: %v, want %s", c.in, err, c.code)
		}
	}
	v6 := e.user
	v6.IP = "2001:db8::5"
	if _, err := e.s.SetPublicPort(ctx, v6, pg.Slug, PublicPortInput{Enabled: true, OnlyMyIP: true}); depCode(err) != "my_ip_not_ipv4" {
		t.Errorf("only my ip from IPv6: %v", err)
	}
	mine := e.user
	mine.IP = "198.51.100.23"
	got, err := e.s.SetPublicPort(ctx, mine, pg.Slug, PublicPortInput{Enabled: true, OnlyMyIP: true})
	if err != nil || len(got.PublicPort.AllowFrom) != 1 || got.PublicPort.AllowFrom[0] != "198.51.100.23/32" {
		t.Fatalf("only my ip: %+v %v", got.PublicPort, err)
	}

	// Another user's app: not found. A read-only token: refused.
	bob := e.addUser(t, "usr_bob", "bob@example.com", "user")
	if _, err := e.s.SetPublicPort(ctx, bob, pg.Slug, PublicPortInput{Enabled: true}); depCode(err) != "app_not_found" {
		t.Errorf("other user: %v", err)
	}
	if _, err := e.s.RevealPublicConnection(ctx, bob, pg.Slug); depCode(err) != "app_not_found" {
		t.Errorf("other user reveal: %v", err)
	}
	ro := e.user
	ro.Scope = ScopeRead
	if _, err := e.s.RevealPublicConnection(ctx, ro, pg.Slug); err == nil {
		t.Error("a read-only token revealed the password")
	}
	// An admin with a session reads the app but cannot reveal it.
	admin := e.addUser(t, "usr_root", "root@example.com", "admin")
	admin.Scope = ScopeSession
	if _, err := e.s.RevealPublicConnection(ctx, admin, pg.Slug); depCode(err) != "app_not_found" {
		t.Errorf("admin reveal: %v", err)
	}
	if v, err := e.s.GetApp(ctx, admin, pg.Slug); err != nil || strings.Contains(v.PublicPort.ConnectionURL, "postgresql://app:"+"x") {
		t.Errorf("admin view: %v", err)
	}

	// Off: opening is refused, closing still works.
	e.s.Cfg.PublicPortsFrom, e.s.Cfg.PublicPortsTo = 0, 0
	other, _ := e.s.AddService(ctx, e.user, got.Project.ID, AddServiceInput{Template: "valkey"})
	if _, err := e.s.SetPublicPort(ctx, e.user, other.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_ports_off" {
		t.Errorf("off: %v", err)
	}
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: false}); err != nil {
		t.Errorf("close while off: %v", err)
	}
}

func TestPublicPortLimitAndAllocation(t *testing.T) {
	e, fp, pg := pubEnv(t)
	ctx := context.Background()
	vk, err := e.s.AddService(ctx, e.user, pg.Project.ID, AddServiceInput{Template: "valkey"})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := e.s.AddService(ctx, e.user, pg.Project.ID, AddServiceInput{Template: "meilisearch"})
	if err != nil {
		t.Fatal(err)
	}

	// Something else holds 30000 on the host: the next port is taken.
	fp.taken[30000] = true
	a, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true})
	if err != nil || a.PublicPort.Port != 30001 {
		t.Fatalf("taken host port: %+v %v", a.PublicPort, err)
	}
	b, err := e.s.SetPublicPort(ctx, e.user, vk.Slug, PublicPortInput{Enabled: true})
	if err != nil || b.PublicPort.Port != 30002 || b.PublicPort.TargetPort != 6379 {
		t.Fatalf("second: %+v %v", b.PublicPort, err)
	}
	if _, err := e.s.SetPublicPort(ctx, e.user, ms.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_port_limit" {
		t.Errorf("over the per-user limit: %v", err)
	}
	// Another user hits the full range.
	e.s.Cfg.PublicPortsPerUser = 5
	if _, err := e.s.SetPublicPort(ctx, e.user, ms.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_ports_full" {
		t.Errorf("full range: %v", err)
	}

	// A failing forwarder: the port is saved with the error, and the
	// reconciler starts it later.
	_, _ = e.s.SetPublicPort(ctx, e.user, vk.Slug, PublicPortInput{Enabled: false})
	fp.fail = errors.New("no such image")
	if _, err := e.s.SetPublicPort(ctx, e.user, ms.Slug, PublicPortInput{Enabled: true}); depCode(err) != "public_port_failed" {
		t.Fatalf("failing forwarder: %v", err)
	}
	v, _ := e.s.GetApp(ctx, e.user, ms.Slug)
	if !v.PublicPort.Enabled || v.PublicPort.Running || !strings.Contains(v.PublicPort.Error, "no such image") {
		t.Fatalf("after the failure: %+v", v.PublicPort)
	}
	e.s.reconcilePublicPorts(ctx)
	v, _ = e.s.GetApp(ctx, e.user, ms.Slug)
	if !v.PublicPort.Running || v.PublicPort.Error != "" || v.PublicPort.ConnectionURL == "" {
		t.Fatalf("after the reconciler: %+v", v.PublicPort)
	}
}

func TestPublicPortReconcile(t *testing.T) {
	e, fp, pg := pubEnv(t)
	ctx := context.Background()
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true, CloseAfter: strp("24h")}); err != nil {
		t.Fatal(err)
	}

	// A restart lost the forwarder: the reconciler starts it again on the same port.
	_ = fp.RemovePortProxy(ctx, pg.ID)
	e.s.reconcilePublicPorts(ctx)
	if spec, ok := fp.spec(pg.ID); !ok || spec.HostPort != 30000 {
		t.Fatalf("restarted forwarder: %+v %v", spec, ok)
	}
	// A forwarder without a row goes.
	fp.running["app_gone"] = dockerx.PortProxySpec{AppID: "app_gone"}
	e.s.reconcilePublicPorts(ctx)
	if _, ok := fp.spec("app_gone"); ok {
		t.Fatal("the orphan forwarder stayed")
	}

	// The admin moved the range: the port moves into it.
	e.s.Cfg.PublicPortsFrom, e.s.Cfg.PublicPortsTo = 31000, 31009
	e.s.reconcilePublicPorts(ctx)
	if spec, _ := fp.spec(pg.ID); spec.HostPort != 31000 {
		t.Fatalf("after the range moved: %+v", spec)
	}

	// Expired: closed with the reason in the audit log.
	e.exec(t, `UPDATE public_ports SET expires_at = ? WHERE app_id = ?`, time.Now().Unix()-1, pg.ID)
	e.s.reconcilePublicPorts(ctx)
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("the expired forwarder stayed")
	}
	if ds := auditDetails(t, e, "app.public_port_closed"); len(ds) != 1 || !strings.Contains(ds[0], `"reason":"expired"`) {
		t.Fatalf("expired audit = %v", ds)
	}

	// The range is turned off: every port closes.
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.s.Cfg.PublicPortsFrom, e.s.Cfg.PublicPortsTo = 0, 0
	e.s.reconcilePublicPorts(ctx)
	if v, _ := e.s.GetApp(ctx, e.user, pg.Slug); v.PublicPort.Enabled || v.PublicPort.Reason != "public_ports_off" {
		t.Fatalf("after turning it off: %+v", v.PublicPort)
	}
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("the forwarder stayed after the range was turned off")
	}
}

func TestPublicPortDeleteAndAdmin(t *testing.T) {
	e, fp, pg := pubEnv(t)
	ctx := context.Background()
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	admin := e.addUser(t, "usr_root", "root@example.com", "admin")
	if _, err := e.s.ListPublicPorts(ctx, admin); depCode(err) != "admin_only" {
		t.Errorf("admin token: %v", err)
	}
	admin.Scope = ScopeSession
	ov, err := e.s.ListPublicPorts(ctx, admin)
	if err != nil || !ov.Enabled || ov.From != 30000 || ov.To != 30002 || ov.PerUser != 2 || len(ov.Open) != 1 ||
		ov.Open[0].App != pg.Slug || ov.Open[0].OwnerEmail != "alice@example.com" || ov.Open[0].Port != 30000 {
		t.Fatalf("overview: %+v %v", ov, err)
	}
	if err := e.s.AdminClosePublicPort(ctx, admin, pg.Slug); err != nil {
		t.Fatal(err)
	}
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("admin close left the forwarder")
	}
	if ds := auditDetails(t, e, "app.public_port_closed"); len(ds) != 1 || !strings.Contains(ds[0], `"reason":"admin"`) {
		t.Fatalf("admin close audit = %v", ds)
	}

	// Deleting the app closes its port and frees the number.
	if _, err := e.s.SetPublicPort(ctx, e.user, pg.Slug, PublicPortInput{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeleteApp(ctx, e.user, pg.Slug, pg.Slug); err != nil {
		t.Fatal(err)
	}
	if _, ok := fp.spec(pg.ID); ok {
		t.Fatal("delete left the forwarder")
	}
	var n int
	_ = e.s.Store.DB.QueryRow(`SELECT count(*) FROM public_ports`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d public port rows after the delete", n)
	}
	if ds := auditDetails(t, e, "app.public_port_closed"); len(ds) != 2 || !strings.Contains(ds[1], `"reason":"app_deleted"`) {
		t.Fatalf("delete audit = %v", ds)
	}
}
